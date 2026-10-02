/*
Package pluginmanager handles the communication with the model and
decision plugins
*/
package pluginmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"plugin"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"

	"github.com/nats-io/nats.go"
)

// ModelTransmitionResults is the struct that contains the results of the model plugin
type ModelTransmitionResults struct {
	TransactionId        string `json:"transactionId"`
	waceapi.ModelResults `json:",inline"`
	Error                string `json:"error,omitempty"`
}

// modelPluginData is the struct that stores the model plugin and its type
type modelPluginData struct {
	pluginType      configstore.ModelPluginType
	modelPlugin     waceapi.ModelPlugin
	trainingChannel chan any
	trainingCtx     context.Context
	trainingCancel  context.CancelFunc
}

// decisionPluginData is the struct that stores the decision plugin
type decisionPluginData struct {
	decisionPlugin  waceapi.DecisionPlugin
	trainingChannel chan any
	trainingCtx     context.Context
	trainingCancel  context.CancelFunc
}

// ModelStatus stores whether there was an error while processing a
// request (response) by the modelID model plugin
type ModelStatus struct {
	ModelID    string
	ProbAttack float64
	Err        error
}

// PluginManager is the main plugin struct storing information of
// every plugin execution.
type PluginManager struct {
	reloadMutex         sync.Mutex
	modelPlugins        map[string]modelPluginData
	modelMutex          sync.RWMutex
	decisionPlugins     map[string]decisionPluginData
	decisionMutex       sync.RWMutex
	results             sync.Map
	channelsMutex       sync.Mutex
	syncModelsChannels  sync.Map
	asyncModelsChannels sync.Map
	natConn             *nats.Conn
	// logger carries component=pluginmanager
	logger atomic.Pointer[slog.Logger]
	// baseLogger is the host logger without a component attribute
	baseLogger atomic.Pointer[slog.Logger]
}

const (
	// model plugin constructor name
	modelInitFunctionName = "NewPlugin"

	// decision plugin constructor name
	decisionInitFunctionName = "NewPlugin"
)

// New creates a new PluginManager instance. The given logger must not
// already carry a component attribute. If it is nil, slog.Default() is used.
func New(meter metric.Meter, logger *slog.Logger) (*PluginManager, error) {
	pm := new(PluginManager)
	pm.setLogger(logger)
	conf, err := configstore.Get()
	if err != nil {
		return nil, err
	}

	if conf.NatsURL != "" {
		pm.getLogger().Debug("connecting to NATS server", "nats.url", conf.NatsURL)
		nc, err := nats.Connect(conf.NatsURL)

		if err != nil {
			pm.getLogger().Error("failed to connect to NATS server", "nats.url", conf.NatsURL, "error", err)
			return nil, err
		}

		pm.natConn = nc
	}

	pm.modelPlugins = make(map[string]modelPluginData)
	pm.loadModelPlugins(meter)

	pm.decisionPlugins = make(map[string]decisionPluginData)
	pm.loadDecisionPlugins(meter)

	return pm, nil
}

// setLogger replaces the logger of the plugin manager. The given logger
// must not already carry a component attribute. If it is nil,
// slog.Default() is used.
func (pm *PluginManager) setLogger(logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	pm.baseLogger.Store(logger)
	pm.logger.Store(logger.With(waceapi.LogKeyComponent, "pluginmanager"))
}

// getLogger returns the current logger of the plugin manager.
func (pm *PluginManager) getLogger() *slog.Logger {
	return pm.logger.Load()
}

// pluginLogger returns the current plugin manager logger with the
// attributes of the given plugin. Long-lived goroutines call it on
// every record so that they see the logger set by Reload.
func (pm *PluginManager) pluginLogger(id string, kind pluginKind) *slog.Logger {
	return pm.getLogger().With(waceapi.LogKeyPluginType, kind.logValue(), waceapi.LogKeyPlugin, id)
}

// Reload reloads the configuration for all already-loaded plugins and loads any
// newly added plugins from the current configstore state.
func (pm *PluginManager) Reload(meter metric.Meter, l *slog.Logger) error {
	pm.reloadMutex.Lock()
	defer pm.reloadMutex.Unlock()
	pm.setLogger(l)
	if err := pm.loadModelPlugins(meter); err != nil {
		return err
	}
	return pm.loadDecisionPlugins(meter)
}

// pluginConfig builds the configuration passed to NewPlugin and Reload
// of the plugin with the given id.
func (pm *PluginManager) pluginConfig(id string, kind pluginKind, params map[string]string, meter metric.Meter) waceapi.PluginConfig {
	return waceapi.PluginConfig{
		Params: params,
		Meter:  meter,
		Logger: pm.baseLogger.Load().With(waceapi.LogKeyComponent, "plugin",
			waceapi.LogKeyPluginType, kind.logValue(),
			waceapi.LogKeyPlugin, id),
	}
}

func (pm *PluginManager) startTraining(id string, td configstore.TrainingData, kind pluginKind) (chan any, context.Context, context.CancelFunc) {
	ch := make(chan any)
	ctx, cancel := context.WithCancel(context.Background())
	go pm.handleTraining(id, td, ctx, cancel, ch, kind)
	return ch, ctx, cancel
}

// loadModelPlugins load new Plugins and reload their configuration if they previously existed
func (pm *PluginManager) loadModelPlugins(meter metric.Meter) error {
	conf, err := configstore.Get()
	if err != nil {
		return err
	}
	// Load plugin models
	// TODO: change plugin on path updates
	for _, data := range conf.ModelPlugins {
		logger := pm.pluginLogger(data.ID, modelKind)
		mpData, found := pm.modelPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Error("cannot open plugin", "plugin.path", data.Path, "error", err)
				continue
			}
			f, err := p.Lookup(modelInitFunctionName)
			if err != nil {
				logger.Error("cannot load plugin: init function not found", "function", modelInitFunctionName, "error", err)
				continue
			}

			newPluginFunc, ok := f.(func(waceapi.PluginConfig) (waceapi.ModelPlugin, error))
			if !ok {
				logger.Error("cannot load plugin: invalid init function type", "function", modelInitFunctionName)
				continue
			}

			// plugin initialization
			mp, err := newPluginFunc(pm.pluginConfig(data.ID, modelKind, data.Params, meter))
			if err != nil {
				logger.Error("cannot initialize plugin", "error", err)
				continue
			}
			if mp == nil {
				logger.Error("cannot initialize plugin: init function returned a nil plugin", "function", modelInitFunctionName)
				continue
			}
			if conf.IsAsync(data.ID) || conf.IsRemote(data.ID) {
				err := pm.ModelProcessHandler(data.ID, mp.Process)
				if err != nil {
					logger.Error("cannot start process handler", "error", err)
					if err := mp.Clean(); err != nil {
						logger.Warn("cannot clean plugin", "error", err)
					}
					continue
				}
				go pm.ModelResultsHandler(data.ID)
			}
			var trainingChannel chan any
			var trainingCtx context.Context
			var trainingCancel context.CancelFunc
			if conf.IsInTraining(data.ID) {
				trainingChannel, trainingCtx, trainingCancel = pm.startTraining(data.ID, data.TrainingData, modelKind)
			}
			pm.modelMutex.Lock()
			pm.modelPlugins[data.ID] = modelPluginData{
				pluginType:      data.PluginType,
				modelPlugin:     mp,
				trainingChannel: trainingChannel,
				trainingCtx:     trainingCtx,
				trainingCancel:  trainingCancel,
			}
			pm.modelMutex.Unlock()
			logger.Info("plugin loaded")
		} else {
			err = mpData.modelPlugin.Reload(pm.pluginConfig(data.ID, modelKind, data.Params, meter))
			if err != nil {
				logger.Warn("cannot reload plugin", "error", err)
				continue
			}
			if !conf.IsInTraining(data.ID) && mpData.trainingChannel != nil {
				mpData.trainingCancel()
				mpData.trainingChannel, mpData.trainingCtx, mpData.trainingCancel = nil, nil, nil
			} else if conf.IsInTraining(data.ID) && mpData.trainingChannel == nil {
				mpData.trainingChannel, mpData.trainingCtx, mpData.trainingCancel = pm.startTraining(data.ID, data.TrainingData, modelKind)
			} else {
				continue
			}
			pm.modelMutex.Lock()
			pm.modelPlugins[data.ID] = mpData
			pm.modelMutex.Unlock()
		}
	}

	for id, mp := range pm.modelPlugins {
		if _, ok := conf.ModelPlugins[id]; ok {
			continue
		}
		pm.modelMutex.Lock()
		delete(pm.modelPlugins, id)
		pm.modelMutex.Unlock()
		if mp.trainingCancel != nil {
			mp.trainingCancel()
		}
		logger := pm.pluginLogger(id, modelKind)
		if err := mp.modelPlugin.Clean(); err != nil {
			logger.Warn("cannot clean plugin", "error", err)
		}
		logger.Info("plugin unloaded")
	}
	return nil
}

// loadDecisionPlugins load new Plugins and reload their configuration if they previously existed
func (pm *PluginManager) loadDecisionPlugins(meter metric.Meter) error {
	conf, err := configstore.Get()
	if err != nil {
		return err
	}
	// Load decision plugins
	for _, data := range conf.DecisionPlugins {
		logger := pm.pluginLogger(data.ID, decisionKind)
		dpData, found := pm.decisionPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Error("cannot open plugin", "plugin.path", data.Path, "error", err)
				continue
			}
			f, err := p.Lookup(decisionInitFunctionName)
			if err != nil {
				logger.Error("cannot load plugin: init function not found", "function", decisionInitFunctionName, "error", err)
				continue
			}
			newPluginFunc, ok := f.(func(waceapi.PluginConfig) (waceapi.DecisionPlugin, error))
			if !ok {
				logger.Error("cannot load plugin: invalid init function type", "function", decisionInitFunctionName)
				continue
			}

			// plugin initialization
			dp, err := newPluginFunc(pm.pluginConfig(data.ID, decisionKind, data.Params, meter))
			if err != nil {
				logger.Error("cannot initialize plugin", "error", err)
				continue
			}
			if dp == nil {
				logger.Error("cannot initialize plugin: init function returned a nil plugin", "function", decisionInitFunctionName)
				continue
			}

			var trainingChannel chan any
			var trainingCtx context.Context
			var trainingCancel context.CancelFunc
			if conf.IsDecisionInTraining(data.ID) {
				trainingChannel, trainingCtx, trainingCancel = pm.startTraining(data.ID, data.TrainingData, decisionKind)
			}
			pm.decisionMutex.Lock()
			pm.decisionPlugins[data.ID] = decisionPluginData{
				decisionPlugin:  dp,
				trainingChannel: trainingChannel,
				trainingCtx:     trainingCtx,
				trainingCancel:  trainingCancel,
			}
			pm.decisionMutex.Unlock()
			logger.Info("plugin loaded")
		} else {
			err = dpData.decisionPlugin.Reload(pm.pluginConfig(data.ID, decisionKind, data.Params, meter))
			if err != nil {
				logger.Warn("cannot reload plugin", "error", err)
				continue
			}
			if !conf.IsDecisionInTraining(data.ID) && dpData.trainingChannel != nil {
				dpData.trainingCancel()
				dpData.trainingChannel, dpData.trainingCtx, dpData.trainingCancel = nil, nil, nil
			} else if conf.IsDecisionInTraining(data.ID) && dpData.trainingChannel == nil {
				dpData.trainingChannel, dpData.trainingCtx, dpData.trainingCancel = pm.startTraining(data.ID, data.TrainingData, decisionKind)
			} else {
				continue
			}
			pm.decisionMutex.Lock()
			pm.decisionPlugins[data.ID] = dpData
			pm.decisionMutex.Unlock()
		}
	}

	for id, dp := range pm.decisionPlugins {
		if _, ok := conf.DecisionPlugins[id]; ok {
			continue
		}
		pm.decisionMutex.Lock()
		delete(pm.decisionPlugins, id)
		pm.decisionMutex.Unlock()
		if dp.trainingCancel != nil {
			dp.trainingCancel()
		}
		logger := pm.pluginLogger(id, decisionKind)
		if err := dp.decisionPlugin.Clean(); err != nil {
			logger.Warn("cannot clean plugin", "error", err)
		}
		logger.Info("plugin unloaded")
	}

	return nil
}

// InitTransaction initializes the transaction with the given ID
func (p *PluginManager) InitTransaction(transactionId string) {
	p.results.Store(transactionId, new(sync.Map))
}

// CloseTransaction closes the transaction with the given ID
// removing all sync model data
func (p *PluginManager) CloseTransaction(transactionId string) {
	logger := p.getLogger().With(waceapi.LogKeyTxID, transactionId)

	_, ok := p.results.LoadAndDelete(transactionId)
	if !ok {
		logger.Error("results for transaction not found")
	}

	_, ok = p.syncModelsChannels.LoadAndDelete(transactionId)
	if !ok {
		logger.Debug("transaction not found")
		return
	}
}

// AddModelChannel adds a channel to result channel map
func (p *PluginManager) AddModelChannel(transactionId string, t configstore.ModelPluginType, modelPlugStatus chan ModelStatus, modelType string) {
	typeModel := new(sync.Map)
	var value interface{}
	if modelType == "sync" {
		value, _ = p.syncModelsChannels.LoadOrStore(transactionId, typeModel)
	} else {
		value, _ = p.asyncModelsChannels.LoadOrStore(transactionId, typeModel)
	}
	value.(*sync.Map).Store(t.String(), modelPlugStatus)
}

// RemoveModelChannel removes a channel from the result channel map
func (p *PluginManager) RemoveAsyncModelChannel(transactionId string, t configstore.ModelPluginType) {
	typeModel, ok := p.asyncModelsChannels.Load(transactionId)
	if ok {
		channelMap := typeModel.(*sync.Map)
		channelMap.Delete(t.String())

		remainChannels := 0
		typeModel.(*sync.Map).Range(func(key, value interface{}) bool {
			remainChannels++
			return true
		})
		if remainChannels == 0 {
			p.asyncModelsChannels.Delete(transactionId)
		}
	} else {
		p.getLogger().Error("transaction not found when trying to remove async model channel", waceapi.LogKeyTxID, transactionId)
	}
}

// AddToQueue adds a payload to the model queue
func (p *PluginManager) AddToQueue(modelID, transactionID string, payload waceapi.HTTPPayload) error {
	payloadToSend := &waceapi.ModelInput{
		TransactionId: transactionID,
		Payload:       payload,
	}

	jsonPayload, err := json.Marshal(payloadToSend)

	if err != nil {
		return err
	}

	return p.natConn.Publish(modelID, jsonPayload)
}

// pluginContext returns the context passed to a plugin call: parent
// bounded by timeout when it is positive, or parent itself otherwise.
func pluginContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return parent, func() {}
}

func (p *PluginManager) modelProcess(ctx context.Context, modelID string, mp modelPluginData, payload waceapi.ModelInput, t configstore.ModelPluginType) (waceapi.ModelResults, error) {
	// check if the plugin is capable of analyzing the indicated part of the transaction
	if mp.pluginType != t {
		return waceapi.ModelResults{}, fmt.Errorf("plugin type %v cannot process a request with incompatible type %v", mp.pluginType, t)
	}

	conf, err := configstore.Get()
	if err != nil {
		return waceapi.ModelResults{}, err
	}

	if conf.IsAsync(modelID) {
		return waceapi.ModelResults{}, fmt.Errorf("model plugin is async")
	}
	ctx, cancel := pluginContext(ctx, conf.ModelPlugins[modelID].Timeout)
	defer cancel()
	return mp.modelPlugin.Process(ctx, payload)
}

// Process is in charge of calling the model plugin with id modelID. The
// plugin gets a context derived from ctx, bounded by its own timeout if
// one is configured.
func (p *PluginManager) Process(ctx context.Context, modelID, transactionID string, payload waceapi.HTTPPayload, t configstore.ModelPluginType, modelPlugStatus chan ModelStatus) {
	p.modelMutex.RLock()
	mp, exists := p.modelPlugins[modelID]
	p.modelMutex.RUnlock()
	if !exists {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("Model plugin %s not found", modelID)}
		return
	}

	res, err := p.modelProcess(ctx, modelID, mp, waceapi.ModelInput{TransactionId: transactionID, Payload: payload}, t)

	if err != nil {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: err}
		return
	}
	// store the results
	resultSyncMap, ok := p.results.Load(transactionID)
	if !ok {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("transaction results not found")}
		return
	}
	resultSyncMap.(*sync.Map).Store(modelID, res)
	modelPlugStatus <- ModelStatus{ModelID: modelID, ProbAttack: res.ProbAttack, Err: nil}
}

// CheckResult is in charge of calling the decision plugin with id decisionID over the
// transaction with id transactionID
func (p *PluginManager) CheckResult(transactionID string, decisionIds []string, wafData waceapi.WAFData) (bool, bool, error) {
	logger := p.getLogger().With(waceapi.LogKeyTxID, transactionID)

	transactionResults, ok := p.results.Load(transactionID)
	if !ok {
		return false, false, fmt.Errorf("transaction results not found")
	}

	cs, err := configstore.Get()
	if err != nil {
		return false, false, nil
	}

	modelResultMap := make(map[string]waceapi.ModelResults)
	transactionResults.(*sync.Map).Range(func(key, value interface{}) bool {
		modelResultMap[key.(string)] = value.(waceapi.ModelResults)
		return true
	})

	enabledPluginFound := false
	result := false

	for _, id := range decisionIds {
		p.decisionMutex.RLock()
		dp, ok := p.decisionPlugins[id]
		p.decisionMutex.RUnlock()
		if !ok {
			return false, false, fmt.Errorf("decision plugin not found")
		}

		logger := logger.With(waceapi.LogKeyPluginType, waceapi.LogValueDecisionPluginType, waceapi.LogKeyPlugin, id)
		dpConf := cs.DecisionPlugins[id]
		input := waceapi.DecisionInput{
			TransactionId: transactionID,
			Results:       modelResultMap,
			ModelWeight:   dpConf.ModelWeights,
			WAFWeight:     dpConf.WAFWeight,
			WAFdata:       wafData,
			Training:      dpConf.Training,
		}

		if dpConf.Training {
			// Shadow plugin: collect a training sample off the request path,
			// without affecting the blocking decision.
			if dp.trainingCtx == nil {
				continue
			}
			go func() {
				// The sample is collected off the request path, so it is only
				// bounded by the plugin timeout and stopped with the training.
				ctx, cancel := pluginContext(dp.trainingCtx, dpConf.Timeout)
				defer cancel()
				res, err := dp.decisionPlugin.CheckResults(ctx, input)
				if err != nil {
					logger.Warn("training check failed, dropping sample", "error", err)
					return
				}
				select {
				case dp.trainingChannel <- res:
				case <-dp.trainingCtx.Done():
					logger.Debug("training cancelled, dropping result")
				}
			}()
			continue
		}

		// Production plugin: at most one is allowed, and it decides the block.
		if enabledPluginFound {
			return false, true, fmt.Errorf("multiple enabled decision plugins found")
		}
		enabledPluginFound = true

		ctx, cancel := pluginContext(context.Background(), dpConf.Timeout)
		defer cancel()
		res, err := dp.decisionPlugin.CheckResults(ctx, input)
		if err != nil {
			return false, true, err
		}
		logger.Info("transaction checked", "block", res.Block)
		result = res.Block
	}

	return result, enabledPluginFound, nil
}

// notifyStatus sends status on ch without blocking. The channel is
// buffered with room for one status per model, so it is only full when
// a result is received more than once (e.g. a duplicated message);
// that extra result is dropped instead of blocking the goroutine forever.
func (p *PluginManager) notifyStatus(ch chan ModelStatus, status ModelStatus, transactionID string) {
	select {
	case ch <- status:
	default:
		p.pluginLogger(status.ModelID, modelKind).Warn("result dropped, nobody is waiting for it", waceapi.LogKeyTxID, transactionID)
	}
}

// ModelResultsHandler listens for messages on the model results queue
func (p *PluginManager) ModelResultsHandler(modelID string) error {
	cs, err := configstore.Get()
	if err != nil {
		return err
	}

	sub, err := p.natConn.Subscribe(modelID+"/results", func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &ModelTransmitionResults{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				p.pluginLogger(modelID, modelKind).Error("failed to parse JSON results payload", "error", err)
			} else {
				var channel interface{}
				var ok bool
				if cs.IsAsync(modelID) {
					channel, ok = p.asyncModelsChannels.Load(data.TransactionId)
				} else {
					channel, ok = p.syncModelsChannels.Load(data.TransactionId)
				}
				if !ok {
					p.pluginLogger(modelID, modelKind).Error("transaction not found", waceapi.LogKeyTxID, data.TransactionId)
				} else {
					modelChannel, ok := channel.(*sync.Map).Load(cs.ModelPlugins[modelID].PluginType.String())
					if !ok {
						p.pluginLogger(modelID, modelKind).Error("model channel not found", waceapi.LogKeyTxID, data.TransactionId)
					} else {
						if data.Error != "" {
							p.notifyStatus(modelChannel.(chan ModelStatus), ModelStatus{ModelID: modelID, Err: fmt.Errorf("%s", data.Error)}, data.TransactionId)
						} else {
							if !cs.IsAsync(modelID) {
								// store the results
								resultSyncMap, ok := p.results.Load(data.TransactionId)
								if !ok {
									p.notifyStatus(modelChannel.(chan ModelStatus), ModelStatus{ModelID: modelID, Err: fmt.Errorf("transaction results not found")}, data.TransactionId)
									return
								}
								modelResult := waceapi.ModelResults{ProbAttack: data.ProbAttack, Data: data.Data}
								resultSyncMap.(*sync.Map).Store(modelID, modelResult)
							}
							p.notifyStatus(modelChannel.(chan ModelStatus), ModelStatus{ModelID: modelID, ProbAttack: data.ProbAttack, Err: nil}, data.TransactionId)
						}
					}
				}
			}
		}(*msg)
	})

	if err != nil {
		p.pluginLogger(modelID, modelKind).Error("failed to subscribe to model results queue", "error", err)
		return err
	}

	p.pluginLogger(modelID, modelKind).Info("listening for messages on model results queue")

	defer sub.Unsubscribe()
	defer p.natConn.Drain()

	select {}

}

// ModelProcessHandler listens for messages on the model queue
func (p *PluginManager) ModelProcessHandler(modelId string, modelProcess func(context.Context, waceapi.ModelInput) (waceapi.ModelResults, error)) error {
	p.pluginLogger(modelId, modelKind).Info("starting model process handler")
	cs, err := configstore.Get()
	if err != nil {
		return err
	}

	nc, err := nats.Connect(cs.NatsURL)

	if err != nil {
		p.pluginLogger(modelId, modelKind).Error("failed to connect to NATS server", "nats.url", cs.NatsURL, "error", err)
		return err
	}

	_, err = nc.Subscribe(modelId, func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &waceapi.ModelInput{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				p.pluginLogger(modelId, modelKind).Error("failed to parse JSON input payload", "error", err)
			} else {
				// TODO: propagate the context of the request through NATS
				ctx, cancel := pluginContext(context.Background(), cs.ModelPlugins[modelId].Timeout)
				res, err := modelProcess(ctx, *data)
				cancel()
				modelResult := waceapi.ModelResults{ProbAttack: res.ProbAttack, Data: res.Data}
				payloadToSend := &ModelTransmitionResults{
					TransactionId: data.TransactionId,
					ModelResults:  modelResult,
				}
				if err != nil {
					payloadToSend.Error = err.Error()
				}

				jsonPayload, err := json.Marshal(payloadToSend)

				if err != nil {
					p.pluginLogger(modelId, modelKind).Error("failed to encode JSON results payload", waceapi.LogKeyTxID, data.TransactionId, "error", err)
				}

				nc.Publish(modelId+"/results", jsonPayload)
			}
		}(*msg)
	})

	if err != nil {
		p.pluginLogger(modelId, modelKind).Error("failed to subscribe to model queue", "error", err)
		return err
	}

	p.pluginLogger(modelId, modelKind).Info("listening for messages on model queue")
	return nil
}

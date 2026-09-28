/*
Package pluginmanager handles the communication with the model and
decision plugins
*/
package pluginmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"plugin"
	"sync"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"

	"github.com/nats-io/nats.go"
	"github.com/tilsor/ModSecIntl_logging/logging"
)

// ModelTransmitionResults is the struct that contains the results of the model plugin
type ModelTransmitionResults struct {
	TransactionId        string `json:"transactionId"`
	waceapi.ModelResults `json:",inline"`
	Error                error `json:"error"`
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
}

const (
	// model plugin constructor name
	modelInitFunctionName = "NewPlugin"

	// decision plugin constructor name
	decisionInitFunctionName = "NewPlugin"
)

// New creates a new PluginManager instance.
func New(meter metric.Meter) (*PluginManager, error) {
	pm := new(PluginManager)
	conf, err := configstore.Get()
	if err != nil {
		return nil, err
	}
	logger := logging.Get()
	logger.Printf(logging.DEBUG, "Connecting to NATS server at %s", conf.NatsURL)

	if conf.NatsURL != "" {
		nc, err := nats.Connect(conf.NatsURL)

		if err != nil {
			logger.Printf(logging.ERROR, "Failed to connect to NATS server")
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

// Reload reloads the configuration for all already-loaded plugins and loads any
// newly added plugins from the current configstore state.
func (pm *PluginManager) Reload(meter metric.Meter) error {
	pm.reloadMutex.Lock()
	defer pm.reloadMutex.Unlock()
	if err := pm.loadModelPlugins(meter); err != nil {
		return err
	}
	return pm.loadDecisionPlugins(meter)
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
	logger := logging.Get()

	// Load plugin models
	// TODO: change plugin on path updates
	for _, data := range conf.ModelPlugins {
		mpData, found := pm.modelPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot open plugin: %v", data.ID, err)
				continue
			}
			f, err := p.Lookup(modelInitFunctionName)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot load plugin: %s function not found: %v", data.ID, modelInitFunctionName, err)
				continue
			}

			newPluginFunc, ok := f.(func(map[string]string, metric.Meter) (waceapi.ModelPlugin, error))
			if !ok {
				logger.Printf(logging.ERROR, "| %s | cannot load plugin: invalid %s function type", data.ID, modelInitFunctionName)
				continue
			}

			// plugin initialization
			mp, err := newPluginFunc(data.Params, meter)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot initialize plugin: %v", data.ID, err)
				continue
			}
			if mp == nil {
				logger.Printf(logging.ERROR, "| %s | cannot initialize plugin: %s returned a nil plugin", data.ID, modelInitFunctionName)
				continue
			}
			if conf.IsAsync(data.ID) || conf.IsRemote(data.ID) {
				err := ModelProcessHandler(data.ID, mp.Process)
				if err != nil {
					logger.Printf(logging.ERROR, "| %s | cannot start process handler: %v", data.ID, err)
					if err := mp.Clean(); err != nil {
						logger.Printf(logging.WARN, "| %s | cannot clean plugin: %v", data.ID, err)
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
			logger.Printf(logging.INFO, "| %s | plugin loaded", data.ID)
		} else {
			err = mpData.modelPlugin.Reload(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot reload plugin: %v", data.ID, err)
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
		if err := mp.modelPlugin.Clean(); err != nil {
			logger.Printf(logging.WARN, "| %s | cannot clean plugin: %v", id, err)
		}
		logger.Printf(logging.INFO, "| %s | plugin unloaded", id)
	}
	return nil
}

// loadDecisionPlugins load new Plugins and reload their configuration if they previously existed
func (pm *PluginManager) loadDecisionPlugins(meter metric.Meter) error {
	conf, err := configstore.Get()
	if err != nil {
		return err
	}
	logger := logging.Get()

	// Load decision plugins
	for _, data := range conf.DecisionPlugins {
		dpData, found := pm.decisionPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot open plugin: %v", data.ID, err)
				continue
			}
			f, err := p.Lookup(decisionInitFunctionName)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot load plugin: %s function not found: %v", data.ID, decisionInitFunctionName, err)
				continue
			}
			newPluginFunc, ok := f.(func(map[string]string, metric.Meter) (waceapi.DecisionPlugin, error))
			if !ok {
				logger.Printf(logging.ERROR, "| %s | cannot load plugin: invalid %s function type", data.ID, decisionInitFunctionName)
				continue
			}

			// plugin initialization
			dp, err := newPluginFunc(data.Params, meter)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot initialize plugin: %v", data.ID, err)
				continue
			}
			if dp == nil {
				logger.Printf(logging.ERROR, "| %s | cannot initialize plugin: %s returned a nil plugin", data.ID, decisionInitFunctionName)
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
			logger.Printf(logging.INFO, "| %s | plugin loaded", data.ID)
		} else {
			err = dpData.decisionPlugin.Reload(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot reload plugin: %v", data.ID, err)
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
		if err := dp.decisionPlugin.Clean(); err != nil {
			logger.Printf(logging.WARN, "| %s | cannot clean plugin: %v", id, err)
		}
		logger.Printf(logging.INFO, "| %s | plugin unloaded", id)
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
	logger := logging.Get()
	transactionMap, ok := p.syncModelsChannels.Load(transactionId)
	if !ok {
		logger.TPrintf(logging.ERROR, transactionId, "Transaction %s not found", transactionId)
	} else {
		transactionMap.(*sync.Map).Range(func(key, value interface{}) bool {
			ch := value.(chan ModelStatus)
			close(ch)
			for range ch {
			}
			transactionMap.(*sync.Map).Delete(key)
			return true
		})
		p.syncModelsChannels.Delete(transactionId)
		resultsMap, ok := p.results.Load(transactionId)
		if !ok {
			logger.TPrintf(logging.ERROR, transactionId, "Results for transaction %s not found", transactionId)
		} else {
			resultsMap.(*sync.Map).Range(func(key, value interface{}) bool {
				resultsMap.(*sync.Map).Delete(key)
				return true
			})
		}
		p.results.Delete(transactionId)
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
		ch, channelOk := channelMap.Load(t.String())

		if channelOk {
			close(ch.(chan ModelStatus))
			for range ch.(chan ModelStatus) {
			}
			channelMap.Delete(t.String())
		}

		remainChannels := 0
		typeModel.(*sync.Map).Range(func(key, value interface{}) bool {
			remainChannels++
			return true
		})
		if remainChannels == 0 {
			p.asyncModelsChannels.Delete(transactionId)
		}
	} else {
		logger := logging.Get()
		logger.TPrintf(logging.ERROR, transactionId, "Transaction %s not found when trying to remove async model channel", transactionId)
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

func (p *PluginManager) modelProcess(modelID string, mp modelPluginData, payload waceapi.ModelInput, t configstore.ModelPluginType) (waceapi.ModelResults, error) {
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
	return mp.modelPlugin.Process(payload)
}

// Process is in charge of calling the model plugin with id modelID
func (p *PluginManager) Process(modelID, transactionID string, payload waceapi.HTTPPayload, t configstore.ModelPluginType, modelPlugStatus chan ModelStatus) {
	p.modelMutex.RLock()
	mp, exists := p.modelPlugins[modelID]
	p.modelMutex.RUnlock()
	if !exists {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("Model plugin %s not found", modelID)}
		return
	}

	res, err := p.modelProcess(modelID, mp, waceapi.ModelInput{TransactionId: transactionID, Payload: payload}, t)

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
	logger := logging.Get()

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
				res, err := dp.decisionPlugin.CheckResults(input)
				if err != nil {
					logger.TPrintf(logging.WARN, transactionID, "%s | training check failed, dropping sample: %v", id, err)
					return
				}
				select {
				case dp.trainingChannel <- res:
				case <-dp.trainingCtx.Done():
					logger.TPrintf(logging.DEBUG, transactionID, "training cancelled for decision %s, dropping result", id)
				}
			}()
			continue
		}

		// Production plugin: at most one is allowed, and it decides the block.
		if enabledPluginFound {
			return false, true, fmt.Errorf("multiple enabled decision plugins found")
		}
		enabledPluginFound = true

		res, err := dp.decisionPlugin.CheckResults(input)
		if err != nil {
			return false, true, err
		}
		logger.TPrintf(logging.INFO, transactionID, "%s | transaction checked. Block: %t ", id, res.Block)
		result = res.Block
	}

	return result, enabledPluginFound, nil
}

// ModelResultsHandler listens for messages on the model results queue
func (p *PluginManager) ModelResultsHandler(modelID string) error {
	logger := logging.Get()
	cs, err := configstore.Get()
	if err != nil {
		return err
	}

	sub, err := p.natConn.Subscribe(modelID+"/results", func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &ModelTransmitionResults{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				logger.Printf(logging.ERROR, "Model: %s | Failed to parse JSON payload", modelID)
			} else {
				var channel interface{}
				var ok bool
				if cs.IsAsync(modelID) {
					channel, ok = p.asyncModelsChannels.Load(data.TransactionId)
				} else {
					channel, ok = p.syncModelsChannels.Load(data.TransactionId)
				}
				if !ok {
					logger.TPrintf(logging.ERROR, data.TransactionId, " Model %s | Transaction not found", modelID)
				} else {
					modelChannel, ok := channel.(*sync.Map).Load(cs.ModelPlugins[modelID].PluginType.String())
					if !ok {
						logger.Printf(logging.ERROR, "Model %s not found", modelID)
					} else {
						if data.Error != nil {
							modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelID, Err: data.Error}
						} else {
							if !cs.IsAsync(modelID) {
								// store the results
								resultSyncMap, ok := p.results.Load(data.TransactionId)
								if !ok {
									modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("transaction results not found")}
									return
								}
								modelResult := waceapi.ModelResults{ProbAttack: data.ProbAttack, Data: data.Data}
								resultSyncMap.(*sync.Map).Store(modelID, modelResult)
							}
							modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelID, ProbAttack: data.ProbAttack, Err: nil}
						}
					}
				}
			}
		}(*msg)
	})

	if err != nil {
		logger.Printf(logging.ERROR, "Model: %s | Failed to subscribe to model queue | %s", modelID, err.Error())
		return err
	}

	logger.Printf(logging.INFO, "Model: %s | Listening for messages on model results queue", modelID)

	defer sub.Unsubscribe()
	defer p.natConn.Drain()

	select {}

}

// ModelProcessHandler listens for messages on the model queue
func ModelProcessHandler(modelId string, modelProcess func(waceapi.ModelInput) (waceapi.ModelResults, error)) error {
	logger := logging.Get()
	logger.Printf(logging.INFO, "Model: %s | Starting model process handler", modelId)
	cs, err := configstore.Get()
	if err != nil {
		return err
	}

	nc, err := nats.Connect(cs.NatsURL)

	if err != nil {
		logger.Printf(logging.ERROR, "Model: %s | Failed to connect to NATS server", modelId)
		return err
	}

	_, err = nc.Subscribe(modelId, func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &waceapi.ModelInput{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				logger.Printf(logging.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
			} else {
				res, err := modelProcess(*data)
				modelResult := waceapi.ModelResults{ProbAttack: res.ProbAttack, Data: res.Data}
				payloadToSend := &ModelTransmitionResults{
					TransactionId: data.TransactionId,
					ModelResults:  modelResult,
					Error:         err,
				}

				jsonPayload, err := json.Marshal(payloadToSend)

				if err != nil {
					logger.Printf(logging.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
				}

				nc.Publish(modelId+"/results", jsonPayload)
			}
		}(*msg)
	})

	if err != nil {
		logger.Printf(logging.ERROR, "Model: %s | Failed to subscribe to model queue | %s", modelId, err.Error())
		return err
	}

	logger.Printf(logging.INFO, "Model: %s | Listening for messages on model queue", modelId)
	return nil
}

/*
Package pluginmanager handles the communication with the model and
decision plugins
*/
package pluginmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"plugin"
	"runtime"
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
	// processHandler and resultsHandler are the NATS handlers of an
	// async or remote model plugin, nil otherwise.
	processHandler *NatsHandler
	resultsHandler *NatsHandler
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

// numPluginTypes is the number of model plugin types, used to index
// the per-transaction channels by type.
const numPluginTypes = configstore.Everything + 1

// syncTx is the state of a transaction that lives from InitTransaction
// to CloseTransaction: the results of its sync model plugins and the
// channels its remote sync model plugins report on, one per plugin
// type.
type syncTx struct {
	mu       sync.Mutex
	results  map[string]waceapi.ModelResults
	channels [numPluginTypes]chan ModelStatus
}

// asyncTx holds the channels the async model plugins of a transaction
// report on, one per plugin type. It outlives CloseTransaction and is
// removed together with its last channel.
type asyncTx struct {
	mu       sync.Mutex
	channels [numPluginTypes]chan ModelStatus
	count    int
	// removed is set once the asyncTx is taken out of asyncTxs, so
	// that AddModelChannel does not add a channel nobody can find.
	removed bool
}

// PluginManager is the main plugin struct storing information of
// every plugin execution.
type PluginManager struct {
	reloadMutex     sync.Mutex
	modelPlugins    map[string]modelPluginData
	modelMutex      sync.RWMutex
	decisionPlugins map[string]decisionPluginData
	decisionMutex   sync.RWMutex
	syncTxs         sync.Map
	asyncTxs        sync.Map
	natConn         *nats.Conn
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
			mpData = modelPluginData{
				pluginType:  data.PluginType,
				modelPlugin: mp,
			}
			if conf.IsAsync(data.ID) || conf.IsRemote(data.ID) {
				if err := pm.startHandlers(data.ID, &mpData); err != nil {
					logger.Error("cannot start NATS handlers", "error", err)
					if err := mp.Clean(); err != nil {
						logger.Warn("cannot clean plugin", "error", err)
					}
					continue
				}
			}
			if conf.IsInTraining(data.ID) {
				mpData.trainingChannel, mpData.trainingCtx, mpData.trainingCancel = pm.startTraining(data.ID, data.TrainingData, modelKind)
			}
			pm.modelMutex.Lock()
			pm.modelPlugins[data.ID] = mpData
			pm.modelMutex.Unlock()
			logger.Info("plugin loaded")
		} else {
			err = mpData.modelPlugin.Reload(pm.pluginConfig(data.ID, modelKind, data.Params, meter))
			if err != nil {
				logger.Warn("cannot reload plugin", "error", err)
				continue
			}
			changed := mpData.pluginType != data.PluginType
			mpData.pluginType = data.PluginType
			// start or stop the NATS handlers when the plugin changes
			// between local and async or remote
			usesNats := conf.IsAsync(data.ID) || conf.IsRemote(data.ID)
			if usesNats && mpData.processHandler == nil {
				if err := pm.startHandlers(data.ID, &mpData); err != nil {
					logger.Error("cannot start NATS handlers", "error", err)
				}
				changed = true
			} else if !usesNats && mpData.processHandler != nil {
				pm.stopHandlers(data.ID, &mpData)
				changed = true
			}
			if !conf.IsInTraining(data.ID) && mpData.trainingChannel != nil {
				mpData.trainingCancel()
				mpData.trainingChannel, mpData.trainingCtx, mpData.trainingCancel = nil, nil, nil
				changed = true
			} else if conf.IsInTraining(data.ID) && mpData.trainingChannel == nil {
				mpData.trainingChannel, mpData.trainingCtx, mpData.trainingCancel = pm.startTraining(data.ID, data.TrainingData, modelKind)
				changed = true
			}
			if !changed {
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
		// no message may reach the plugin once it is cleaned
		pm.stopHandlers(id, &mp)
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

// loadSyncTx returns the sync state of the transaction with the given ID
func (p *PluginManager) loadSyncTx(transactionId string) (*syncTx, bool) {
	v, ok := p.syncTxs.Load(transactionId)
	if !ok {
		return nil, false
	}
	return v.(*syncTx), true
}

// loadAsyncTx returns the async channels of the transaction with the given ID
func (p *PluginManager) loadAsyncTx(transactionId string) (*asyncTx, bool) {
	v, ok := p.asyncTxs.Load(transactionId)
	if !ok {
		return nil, false
	}
	return v.(*asyncTx), true
}

// storeResult stores the results of the model plugin with id modelID
func (tx *syncTx) storeResult(modelID string, res waceapi.ModelResults) {
	tx.mu.Lock()
	tx.results[modelID] = res
	tx.mu.Unlock()
}

// channel returns the channel of the plugin type t, or nil if there is none
func (tx *syncTx) channel(t configstore.ModelPluginType) chan ModelStatus {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.channels[t]
}

// channel returns the channel of the plugin type t, or nil if there is none
func (tx *asyncTx) channel(t configstore.ModelPluginType) chan ModelStatus {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.channels[t]
}

// InitTransaction initializes the transaction with the given ID
func (p *PluginManager) InitTransaction(transactionId string) {
	p.syncTxs.Store(transactionId, &syncTx{results: make(map[string]waceapi.ModelResults)})
}

// CloseTransaction closes the transaction with the given ID
// removing all sync model data. The async model channels are kept
// until RemoveAsyncModelChannel removes the last one.
func (p *PluginManager) CloseTransaction(transactionId string) {
	if _, ok := p.syncTxs.LoadAndDelete(transactionId); !ok {
		p.getLogger().Error("transaction not found", waceapi.LogKeyTxID, transactionId)
	}
}

// AddModelChannel adds the channel the model plugins of type t report
// on. modelType is "sync" for sync model plugins, which needs the
// transaction to be initialized, and anything else for async ones.
func (p *PluginManager) AddModelChannel(transactionId string, t configstore.ModelPluginType, modelPlugStatus chan ModelStatus, modelType string) {
	if t < 0 || t >= numPluginTypes {
		p.getLogger().Error("invalid model plugin type", waceapi.LogKeyTxID, transactionId, "type", int(t))
		return
	}
	if modelType == "sync" {
		tx, ok := p.loadSyncTx(transactionId)
		if !ok {
			p.getLogger().Error("transaction not found when trying to add sync model channel", waceapi.LogKeyTxID, transactionId)
			return
		}
		tx.mu.Lock()
		tx.channels[t] = modelPlugStatus
		tx.mu.Unlock()
		return
	}
	for {
		// TODO: find a better flow for this
		v, _ := p.asyncTxs.LoadOrStore(transactionId, &asyncTx{})
		tx := v.(*asyncTx)
		tx.mu.Lock()
		if !tx.removed {
			if tx.channels[t] == nil {
				tx.count++
			}
			tx.channels[t] = modelPlugStatus
			tx.mu.Unlock()
			return
		}
		// RemoveAsyncModelChannel took it out of asyncTxs in the
		// meantime: retry with a new one.
		tx.mu.Unlock()
	}
}

// RemoveAsyncModelChannel removes the async channel of plugin type t,
// and the async state of the transaction along with its last channel.
func (p *PluginManager) RemoveAsyncModelChannel(transactionId string, t configstore.ModelPluginType) {
	tx, ok := p.loadAsyncTx(transactionId)
	if !ok {
		p.getLogger().Error("transaction not found when trying to remove async model channel", waceapi.LogKeyTxID, transactionId)
		return
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if t >= 0 && t < numPluginTypes && tx.channels[t] != nil {
		tx.channels[t] = nil
		tx.count--
	}
	if tx.count == 0 && !tx.removed {
		tx.removed = true
		p.asyncTxs.CompareAndDelete(transactionId, tx)
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
	tx, ok := p.loadSyncTx(transactionID)
	if !ok {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("transaction results not found")}
		return
	}
	tx.storeResult(modelID, res)
	modelPlugStatus <- ModelStatus{ModelID: modelID, ProbAttack: res.ProbAttack, Err: nil}
}

// CheckResult is in charge of calling the decision plugin with id decisionID over the
// transaction with id transactionID
func (p *PluginManager) CheckResult(transactionID string, decisionIds []string, wafData waceapi.WAFData) (bool, bool, error) {
	logger := p.getLogger().With(waceapi.LogKeyTxID, transactionID)

	tx, ok := p.loadSyncTx(transactionID)
	if !ok {
		return false, false, fmt.Errorf("transaction results not found")
	}

	cs, err := configstore.Get()
	if err != nil {
		return false, false, nil
	}

	// The decision plugins get a copy: model plugins may still store
	// results while they run.
	tx.mu.Lock()
	modelResultMap := maps.Clone(tx.results)
	tx.mu.Unlock()

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

// processWorkers is the number of messages each model process handler
// handles in parallel.
var processWorkers = runtime.GOMAXPROCS(0)

// NatsHandler is a set of NATS subscriptions that handle their messages
// inline, so that a finished drain means no message is being handled.
type NatsHandler struct {
	subs []*nats.Subscription
	// conn is closed by Stop. It is nil when the subscriptions use a
	// connection shared with other handlers.
	conn *nats.Conn
}

// Stop drains the subscriptions, which handles the messages already
// received, waits for the drain to finish and closes the handler's own
// connection, if any.
func (h *NatsHandler) Stop() error {
	var errs []error
	var closed []<-chan nats.SubStatus
	for _, sub := range h.subs {
		// registered before Drain so the closed status is not missed
		ch := sub.StatusChanged(nats.SubscriptionClosed)
		if err := sub.Drain(); err != nil {
			errs = append(errs, err)
			continue
		}
		closed = append(closed, ch)
	}
	// the channel gets the closed status, or is closed, once the last
	// message callback has returned
	for _, ch := range closed {
		<-ch
	}
	if h.conn != nil {
		// send the results of the drained messages before closing
		if err := h.conn.Flush(); err != nil {
			errs = append(errs, err)
		}
		h.conn.Close()
	}
	return errors.Join(errs...)
}

// startHandlers starts the NATS handlers of the async or remote model
// plugin with id modelID.
func (pm *PluginManager) startHandlers(modelID string, mpData *modelPluginData) error {
	ph, err := pm.ModelProcessHandler(modelID, mpData.modelPlugin.Process)
	if err != nil {
		return err
	}
	rh, err := pm.ModelResultsHandler(modelID)
	if err != nil {
		if err := ph.Stop(); err != nil {
			pm.pluginLogger(modelID, modelKind).Warn("cannot stop process handler", "error", err)
		}
		return err
	}
	mpData.processHandler, mpData.resultsHandler = ph, rh
	return nil
}

// stopHandlers stops the NATS handlers of the model plugin with id
// modelID, if it has any.
func (pm *PluginManager) stopHandlers(modelID string, mpData *modelPluginData) {
	logger := pm.pluginLogger(modelID, modelKind)
	if mpData.processHandler != nil {
		if err := mpData.processHandler.Stop(); err != nil {
			logger.Warn("cannot stop process handler", "error", err)
		}
		mpData.processHandler = nil
	}
	if mpData.resultsHandler != nil {
		if err := mpData.resultsHandler.Stop(); err != nil {
			logger.Warn("cannot stop results handler", "error", err)
		}
		mpData.resultsHandler = nil
	}
}

// ModelResultsHandler subscribes to the results queue of the model
// plugin with id modelID.
func (p *PluginManager) ModelResultsHandler(modelID string) (*NatsHandler, error) {
	// Not a queue group: every wacecore instance must get every result,
	// as only the one that sent the request has the transaction. The
	// messages are handled inline, the work is short and never blocks.
	sub, err := p.natConn.Subscribe(modelID+"/results", func(msg *nats.Msg) {
		p.handleModelResult(modelID, msg)
	})
	if err != nil {
		p.pluginLogger(modelID, modelKind).Error("failed to subscribe to model results queue", "error", err)
		return nil, err
	}
	h := &NatsHandler{subs: []*nats.Subscription{sub}}
	// make sure the server has the subscription before any request is sent
	if err := p.natConn.Flush(); err != nil {
		p.pluginLogger(modelID, modelKind).Error("failed to subscribe to model results queue", "error", err)
		h.Stop()
		return nil, err
	}

	p.pluginLogger(modelID, modelKind).Info("listening for messages on model results queue")
	return h, nil
}

// handleModelResult notifies the result in msg to the transaction
// waiting for it, using the configuration current at the time it
// arrives.
func (p *PluginManager) handleModelResult(modelID string, msg *nats.Msg) {
	logger := p.pluginLogger(modelID, modelKind)
	data := &ModelTransmitionResults{}
	if err := json.Unmarshal(msg.Data, data); err != nil {
		logger.Error("failed to parse JSON results payload", "error", err)
		return
	}
	cs, err := configstore.Get()
	if err != nil {
		logger.Error("cannot get configuration", waceapi.LogKeyTxID, data.TransactionId, "error", err)
		return
	}
	modelConf, found := cs.ModelPlugins[modelID]
	if !found {
		logger.Error("model plugin no longer configured", waceapi.LogKeyTxID, data.TransactionId)
		return
	}
	pluginType := modelConf.PluginType
	isAsync := cs.IsAsync(modelID)
	var sTx *syncTx
	var modelChannel chan ModelStatus
	var ok bool
	if isAsync {
		var aTx *asyncTx
		if aTx, ok = p.loadAsyncTx(data.TransactionId); ok {
			modelChannel = aTx.channel(pluginType)
		}
	} else {
		if sTx, ok = p.loadSyncTx(data.TransactionId); ok {
			modelChannel = sTx.channel(pluginType)
		}
	}
	if !ok {
		logger.Error("transaction not found", waceapi.LogKeyTxID, data.TransactionId)
		return
	}
	if modelChannel == nil {
		logger.Error("model channel not found", waceapi.LogKeyTxID, data.TransactionId)
		return
	}
	if data.Error != "" {
		p.notifyStatus(modelChannel, ModelStatus{ModelID: modelID, Err: fmt.Errorf("%s", data.Error)}, data.TransactionId)
		return
	}
	if !isAsync {
		// store the results
		sTx.storeResult(modelID, waceapi.ModelResults{ProbAttack: data.ProbAttack, Data: data.Data})
	}
	p.notifyStatus(modelChannel, ModelStatus{ModelID: modelID, ProbAttack: data.ProbAttack, Err: nil}, data.TransactionId)
}

// ModelProcessHandler subscribes to the queue of the model plugin with
// id modelId, on its own connection, and publishes the results of
// modelProcess on its results queue. It handles up to processWorkers
// messages in parallel, one per subscription of the modelId queue
// group, which also spreads the messages among every instance of the
// model instead of each instance handling all of them.
func (p *PluginManager) ModelProcessHandler(modelId string, modelProcess func(context.Context, waceapi.ModelInput) (waceapi.ModelResults, error)) (*NatsHandler, error) {
	p.pluginLogger(modelId, modelKind).Info("starting model process handler")
	cs, err := configstore.Get()
	if err != nil {
		return nil, err
	}

	nc, err := nats.Connect(cs.NatsURL)

	if err != nil {
		p.pluginLogger(modelId, modelKind).Error("failed to connect to NATS server", "nats.url", cs.NatsURL, "error", err)
		return nil, err
	}

	h := &NatsHandler{conn: nc}
	for range processWorkers {
		sub, err := nc.QueueSubscribe(modelId, modelId, func(msg *nats.Msg) {
			p.handleModelInput(modelId, nc, msg, modelProcess)
		})
		if err != nil {
			p.pluginLogger(modelId, modelKind).Error("failed to subscribe to model queue", "error", err)
			nc.Close()
			return nil, err
		}
		h.subs = append(h.subs, sub)
	}
	// make sure the server has the subscriptions before returning
	if err := nc.Flush(); err != nil {
		p.pluginLogger(modelId, modelKind).Error("failed to subscribe to model queue", "error", err)
		nc.Close()
		return nil, err
	}

	p.pluginLogger(modelId, modelKind).Info("listening for messages on model queue", "workers", processWorkers)
	return h, nil
}

// handleModelInput processes the input in msg with modelProcess and
// publishes the results on nc, using the configuration current at the
// time it arrives.
func (p *PluginManager) handleModelInput(modelId string, nc *nats.Conn, msg *nats.Msg, modelProcess func(context.Context, waceapi.ModelInput) (waceapi.ModelResults, error)) {
	logger := p.pluginLogger(modelId, modelKind)
	data := &waceapi.ModelInput{}
	if err := json.Unmarshal(msg.Data, data); err != nil {
		logger.Error("failed to parse JSON input payload", "error", err)
		return
	}
	var timeout time.Duration
	if cs, err := configstore.Get(); err == nil {
		timeout = cs.ModelPlugins[modelId].Timeout
	} else {
		logger.Warn("cannot get configuration, processing without timeout", waceapi.LogKeyTxID, data.TransactionId, "error", err)
	}
	// TODO: propagate the context of the request through NATS
	ctx, cancel := pluginContext(context.Background(), timeout)
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
		logger.Error("failed to encode JSON results payload", waceapi.LogKeyTxID, data.TransactionId, "error", err)
	}

	nc.Publish(modelId+"/results", jsonPayload)
}

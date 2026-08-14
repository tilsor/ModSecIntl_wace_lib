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

// modelPlugin is the struct that stores the model plugin and its type
type modelPlugin struct {
	p               *plugin.Plugin
	pluginType      configstore.ModelPluginType
	process         func(waceapi.ModelInput) (waceapi.ModelResults, error)
	reload          func(map[string]string, metric.Meter) error
	trainingChannel chan any
	trainingCtx     context.Context
	trainingCancel  context.CancelFunc
}

// decisionPlugin is the struct that stores the decision plugin
type decisionPlugin struct {
	p               *plugin.Plugin
	checkResults    func(waceapi.DecisionInput) (waceapi.DecisionResult, error)
	reload          func(map[string]string, metric.Meter) error
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
	modelPlugins        map[string]modelPlugin
	decisionPlugins     map[string]decisionPlugin
	results             sync.Map
	channelsMutex       sync.Mutex
	syncModelsChannels  sync.Map
	asyncModelsChannels sync.Map
	natConn             *nats.Conn
}

const (
	// model plugin function names
	modelInitFunctionName      = "InitPlugin"
	modelInitAsyncFunctionName = "InitPluginAsync"
	modelProcessFunctionName   = "Process"
	modelReloadFunction        = "ReloadPlugin"

	// decision plugin function names
	decisionInitFunctionName   = "InitPlugin"
	decisionCheckFuncionName   = "CheckResults"
	decisionReloadFunctionName = "ReloadPlugin"
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

	pm.modelPlugins = make(map[string]modelPlugin)
	pm.loadModelPlugins(meter)

	pm.decisionPlugins = make(map[string]decisionPlugin)
	pm.loadDecisionPlugins(meter)

	return pm, nil
}

// Reload reloads the configuration for all already-loaded plugins and loads any
// newly added plugins from the current configstore state.
func (pm *PluginManager) Reload(meter metric.Meter) error {
	if err := pm.loadModelPlugins(meter); err != nil {
		return err
	}
	return pm.loadDecisionPlugins(meter)
}

// loadModelPlugins load new Plugins and reload their configuration if they previously existed
func (pm *PluginManager) loadModelPlugins(meter metric.Meter) error {
	conf, err := configstore.Get()
	if err != nil {
		return err
	}
	logger := logging.Get()

	// Load plugin models
	// TODO: remove data from old plugins in a best effort approach
	for _, data := range conf.ModelPlugins {
		mp, found := pm.modelPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot open plugin: %v", data.ID, err)
				continue
			}
			var processFunc func(waceapi.ModelInput) (waceapi.ModelResults, error)
			if conf.IsAsync(data.ID) || conf.IsRemote(data.ID) {
				f, err := p.Lookup(modelInitAsyncFunctionName)
				if err != nil {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
					continue
				}
				initPlugin, ok := f.(func(map[string]string, metric.Meter, func(func(waceapi.ModelInput) (waceapi.ModelResults, error))) error)
				if !ok {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: invalid %s function type", data.ID, modelInitAsyncFunctionName)
					continue
				}

				// plugin initialization
				err = initPlugin(data.Params, meter, func(modelProcess func(waceapi.ModelInput) (waceapi.ModelResults, error)) {
					ModelProcessHandler(data.ID, modelProcess)
				})
				if err != nil {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
					continue
				}
				go pm.ModelResultsHandler(data.ID)
			} else {
				f, err := p.Lookup(modelInitFunctionName)
				if err != nil {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
					continue
				}
				initPlugin, ok := f.(func(map[string]string, metric.Meter) error)
				if !ok {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: invalid %s function type", data.ID, modelInitFunctionName)
					continue
				}

				// plugin initialization
				err = initPlugin(data.Params, meter)
				procFunc, err := p.Lookup(modelProcessFunctionName)
				if err != nil {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: cannot load %s function", data.ID, modelProcessFunctionName)
					continue
				}
				processFunc, ok = procFunc.(func(waceapi.ModelInput) (waceapi.ModelResults, error))
				if !ok {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: invalid %s function type", data.ID, modelProcessFunctionName)
					continue
				}
			}
			rFun, err := p.Lookup(modelReloadFunction)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot load plugin: cannot load %s function", data.ID, modelReloadFunction)
				continue
			}
			reload, ok := rFun.(func(map[string]string, metric.Meter) error)
			if !ok {
				logger.Printf(logging.WARN, "| %s | cannot load plugin: invalid %s function type", data.ID, modelReloadFunction)
				continue
			}
			var trainingChannel chan any
			var trainingCtx context.Context
			var trainingCancel context.CancelFunc
			if conf.IsInTraining(data.ID) {
				trainingChannel = make(chan any)
				trainingCtx, trainingCancel = context.WithCancel(context.Background())
				go pm.handleTraining(data.ID, data.TrainingData, trainingCtx, trainingCancel, trainingChannel, "Model")
			}
			modelPluginLoaded := modelPlugin{p, data.PluginType, processFunc, reload, trainingChannel, trainingCtx, trainingCancel}
			pm.modelPlugins[data.ID] = modelPluginLoaded
			logger.Printf(logging.INFO, "| %s | plugin loaded", data.ID)
		} else {
			err = mp.reload(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot reload plugin: %s", data.ID, err.Error())
				continue
			}
			if !conf.IsInTraining(data.ID) && mp.trainingCancel != nil {
				mp.trainingCancel()
			}
		}
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
		dp, found := pm.decisionPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
				continue
			}
			f, err := p.Lookup(decisionInitFunctionName)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
				continue
			}
			initPlugin, ok := f.(func(map[string]string, metric.Meter) error)
			if !ok {
				logger.Printf(logging.WARN, "| %s | cannot load plugin: invalid %s function type", data.ID, decisionInitFunctionName)
				continue
			}

			// plugin initialization
			err = initPlugin(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
				continue
			}
			checkFunc, err := p.Lookup(decisionCheckFuncionName)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot load plugin %s function: %v", data.ID, decisionCheckFuncionName, err)
				continue
			}
			checkResults, ok := checkFunc.(func(waceapi.DecisionInput) (waceapi.DecisionResult, error))
			if !ok {
				logger.Printf(logging.ERROR, "| %s | %s lookup failed for plugin: invalid function type", data.ID, decisionCheckFuncionName)
				continue
			}

			reloadFunc, err := p.Lookup(decisionReloadFunctionName)
			if err != nil {
				logger.Printf(logging.ERROR, "| %s | cannot load plugin %s function: %v", data.ID, decisionReloadFunctionName, err)
				continue
			}
			reload, ok := reloadFunc.(func(map[string]string, metric.Meter) error)
			if !ok {
				logger.Printf(logging.ERROR, "| %s | %s lookup failed for plugin: invalid function type", data.ID, decisionReloadFunctionName)
				continue
			}
			var trainingChannel chan any
			var trainingCtx context.Context
			var trainingCancel context.CancelFunc
			if conf.IsDecisionInTraining(data.ID) {
				trainingChannel = make(chan any)
				trainingCtx, trainingCancel = context.WithCancel(context.Background())
				go pm.handleTraining(data.ID, data.TrainingData, trainingCtx, trainingCancel, trainingChannel, "Decision")
			}

			decisionPluginLoaded := decisionPlugin{p, checkResults, reload, trainingChannel, trainingCtx, trainingCancel}
			pm.decisionPlugins[data.ID] = decisionPluginLoaded
		} else {
			err = dp.reload(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot reload plugin: %s", data.ID, err.Error())
				continue
			}
			if !conf.IsDecisionInTraining(data.ID) && dp.trainingCancel != nil {
				dp.trainingCancel()
			}
		}
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

func (p *PluginManager) modelProcess(modelID string, mp modelPlugin, payload waceapi.ModelInput, t configstore.ModelPluginType) (waceapi.ModelResults, error) {
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
	return mp.process(payload)
}

// Process is in charge of calling the model plugin with id modelID
func (p *PluginManager) Process(modelID, transactionID string, payload waceapi.HTTPPayload, t configstore.ModelPluginType, modelPlugStatus chan ModelStatus) {
	mp, exists := p.modelPlugins[modelID]
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
		dp, ok := p.decisionPlugins[id]
		if !ok {
			return false, false, fmt.Errorf("decision plugin not found")
		}

		dpConf := cs.DecisionPlugins[id]
		input := waceapi.DecisionInput{
			TransactionId: transactionID,
			Results:       modelResultMap,
			ModelWeight:   dpConf.ModelWeight,
			WAFWeight:     dpConf.WAFWeight,
			WAFdata:       wafData,
		}

		if dpConf.Training {
			// Shadow plugin: collect a training sample off the request path,
			// without affecting the blocking decision.
			go func() {
				res, err := dp.checkResults(input)
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

		res, err := dp.checkResults(input)
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

/*
Package pluginmanager handles the communication with the model and
decision plugins
*/
package pluginmanager

import (
	"encoding/json"
	"fmt"
	"plugin"
	"sync"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"go.opentelemetry.io/otel/metric"

	"github.com/nats-io/nats.go"
	"github.com/tilsor/ModSecIntl_logging/logging"
)

// ResultData maps the model plugin ID with the corresponding analysis result.
type ModelResults struct {
	ProbAttack float64                `json:"probattack"`
	Data       map[string]interface{} `json:"data"`
}

type HTTPHeader struct {
	Key   string
	Value string
}

type HTTPPayload struct {
	URI              string
	Method           string
	HTTPVersion      string
	RequestHeaders   []HTTPHeader
	RequestBody      string
	ResponseProtocol string
	ResponseCode     int
	ResponseHeaders  []HTTPHeader
	ResponseBody     string
}

// ModelInput is the struct that contains the input data for the model plugin
type ModelInput struct {
	TransactionId string      `json:"transactionId"`
	Payload       HTTPPayload `json:"payload"`
}

// DecisionInput is the struct that contains the input data for the decision plugin
type DecisionInput struct {
	TransactionId string
	Results       map[string]ModelResults
	ModelWeight   map[string]float64
	WAFdata       map[string]string
}

// ModelTransmitionResults is the struct that contains the results of the model plugin
type ModelTransmitionResults struct {
	TransactionId string `json:"transactionId"`
	ModelResults  `json:",inline"`
	Error         error `json:"error"`
}

// modelPlugin is the struct that stores the model plugin and its type
type modelPlugin struct {
	p          *plugin.Plugin
	pluginType configstore.ModelPluginType
	process    func(ModelInput) (ModelResults, error)
	reload     func(map[string]string, metric.Meter) error
}

// decisionPlugin is the struct that stores the decision plugin
type decisionPlugin struct {
	p            *plugin.Plugin
	checkResults func(DecisionInput) (bool, error)
	reload       func(map[string]string, metric.Meter) error
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

	nc, err := nats.Connect(conf.NatsURL)

	if err != nil {
		logger.Printf(logging.ERROR, "Failed to connect to NATS server")
	}

	pm.natConn = nc

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
	for _, data := range conf.ModelPlugins {
		mp, found := pm.modelPlugins[data.ID]
		if !found {
			p, err := plugin.Open(data.Path)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot open plugin: %v", data.ID, err)
				continue
			}
			var processFunc func(ModelInput) (ModelResults, error)
			// TODO: change mode to bool
			if data.Mode == "async" || conf.ModelPlugins[data.ID].Remote {
				f, err := p.Lookup(modelInitAsyncFunctionName)
				if err != nil {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: %v", data.ID, err)
					continue
				}
				initPlugin, ok := f.(func(map[string]string, metric.Meter, func(func(ModelInput) (ModelResults, error))) error)
				if !ok {
					logger.Printf(logging.WARN, "| %s | cannot load plugin: invalid %s function type", data.ID, modelInitAsyncFunctionName)
					continue
				}

				// plugin initialization
				err = initPlugin(data.Params, meter, func(modelProcess func(ModelInput) (ModelResults, error)) {
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
				processFunc, ok = procFunc.(func(ModelInput) (ModelResults, error))
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
			modelPluginLoaded := modelPlugin{p, data.PluginType, processFunc, reload}
			pm.modelPlugins[data.ID] = modelPluginLoaded
			logger.Printf(logging.INFO, "| %s | plugin loaded", data.ID)
		} else {
			err = mp.reload(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot reload plugin: %s", data.ID, err.Error())
				continue
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
			checkResults, ok := checkFunc.(func(DecisionInput) (bool, error))
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

			decisionPluginLoaded := decisionPlugin{p, checkResults, reload}
			pm.decisionPlugins[data.ID] = decisionPluginLoaded
		} else {
			err = dp.reload(data.Params, meter)
			if err != nil {
				logger.Printf(logging.WARN, "| %s | cannot reload plugin: %s", data.ID, err.Error())
				continue
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
func (p *PluginManager) AddToQueue(modelID, transactionID string, payload HTTPPayload) error {
	payloadToSend := &ModelInput{
		TransactionId: transactionID,
		Payload:       payload,
	}

	jsonPayload, err := json.Marshal(payloadToSend)

	if err != nil {
		return err
	}

	return p.natConn.Publish(modelID, jsonPayload)
}

// Process is in charge of calling the model plugin with id modelID
func (p *PluginManager) Process(modelID, transactionId string, payload HTTPPayload, t configstore.ModelPluginType, modelPlugStatus chan ModelStatus) error {
	conf, err := configstore.Get()
	if err != nil {
		return err
	}

	mp, exists := p.modelPlugins[modelID]
	if !exists {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("model plugin not found")}
		return nil
	}

	// check if the plugin is capable of analyzing the indicated part of the transaction
	if mp.pluginType != t {
		modelPlugStatus <- ModelStatus{ModelID: modelID,
			Err: fmt.Errorf("plugin type %v cannot process a request with incompatible type %v", mp.pluginType, t)}
		return nil
	}

	mp, ok := p.modelPlugins[modelID]
	if !ok {
		return fmt.Errorf("Model plugin %s not found", modelID)
	}

	if conf.ModelPlugins[modelID].Mode == "async" {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("model plugin is async")}
		return nil
	} else {
		res, err := mp.process(ModelInput{TransactionId: transactionId, Payload: payload})

		if err != nil {
			modelPlugStatus <- ModelStatus{ModelID: modelID, Err: err}
			return nil
		}
		// store the results
		resultSyncMap, ok := p.results.Load(transactionId)
		if !ok {
			modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("transaction results not found")}
			return nil
		}
		resultSyncMap.(*sync.Map).Store(modelID, res)
		modelPlugStatus <- ModelStatus{ModelID: modelID, ProbAttack: res.ProbAttack, Err: nil}
	}
	return nil
}

// CheckResult is in charge of calling the decision plugin with id decisionID over the
// transaction with id transactID
func (p *PluginManager) CheckResult(transactionId, decisionId string, wafParams map[string]string) (bool, error) {
	logger := logging.Get()

	dp, ok := p.decisionPlugins[decisionId]
	if !ok {
		return false, fmt.Errorf("decision plugin not found")
	}

	transactionResults, ok := p.results.Load(transactionId)
	if !ok {
		return false, fmt.Errorf("transaction results not found")
	}

	cs, err := configstore.Get()
	if err != nil {
		return false, nil
	}

	modelResultMap := make(map[string]ModelResults)
	modelWeightMap := make(map[string]float64)
	transactionResults.(*sync.Map).Range(func(key, value interface{}) bool {
		modelResultMap[key.(string)] = value.(ModelResults)
		modelWeightMap[key.(string)] = cs.ModelPlugins[key.(string)].Weight
		return true
	})

	res, err := dp.checkResults(DecisionInput{TransactionId: transactionId, Results: modelResultMap, ModelWeight: modelWeightMap, WAFdata: wafParams})
	logger.TPrintf(logging.INFO, transactionId, "%s | transaction checked. Block: %t ", decisionId, res)

	return res, err
}

// ModelResultsHandler listens for messages on the model results queue
func (p *PluginManager) ModelResultsHandler(modelId string) error {
	logger := logging.Get()
	cs, err := configstore.Get()
	if err != nil {
		return err
	}

	sub, err := p.natConn.Subscribe(modelId+"/results", func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &ModelTransmitionResults{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				logger.Printf(logging.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
			} else {
				var channel interface{}
				var ok bool
				if cs.ModelPlugins[modelId].Mode == "async" {
					channel, ok = p.asyncModelsChannels.Load(data.TransactionId)
				} else {
					channel, ok = p.syncModelsChannels.Load(data.TransactionId)
				}
				if !ok {
					logger.TPrintf(logging.ERROR, data.TransactionId, " Model %s | Transaction not found", modelId)
				} else {
					modelChannel, ok := channel.(*sync.Map).Load(cs.ModelPlugins[modelId].PluginType.String())
					if !ok {
						logger.Printf(logging.ERROR, "Model %s not found", modelId)
					} else {
						if data.Error != nil {
							modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelId, Err: data.Error}
						} else {
							if cs.ModelPlugins[modelId].Mode != "async" {
								// store the results
								resultSyncMap, ok := p.results.Load(data.TransactionId)
								if !ok {
									modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelId, Err: fmt.Errorf("transaction results not found")}
									return
								}
								modelResult := ModelResults{ProbAttack: data.ProbAttack, Data: data.Data}
								resultSyncMap.(*sync.Map).Store(modelId, modelResult)
							}
							modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelId, ProbAttack: data.ProbAttack, Err: nil}
						}
					}
				}
			}
		}(*msg)
	})

	if err != nil {
		logger.Printf(logging.ERROR, "Model: %s | Failed to subscribe to model queue | %s", modelId, err.Error())
		return err
	}

	logger.Printf(logging.INFO, "Model: %s | Listening for messages on model results queue", modelId)

	defer sub.Unsubscribe()
	defer p.natConn.Drain()

	select {}

}

// ModelProcessHandler listens for messages on the model queue
func ModelProcessHandler(modelId string, modelProcess func(ModelInput) (ModelResults, error)) error {
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
			data := &ModelInput{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				logger.Printf(logging.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
			} else {
				res, err := modelProcess(*data)
				modelResult := ModelResults{ProbAttack: res.ProbAttack, Data: res.Data}
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

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

	cf "github.com/tiroa-tilsor/wacelib/configstore"
	"go.opentelemetry.io/otel/metric"

	"github.com/nats-io/nats.go"
	lg "github.com/tilsor/ModSecIntl_logging/logging"
)

// ResultData maps the model plugin ID with the corresponding analysis result.
type ModelResults struct {
	ProbAttack float64                `json:"probattack"`
	Data       map[string]interface{} `json:"data"`
}

// ModelInput is the struct that contains the input data for the model plugin
type ModelInput struct {
	TransactionId string `json:"transactionId"`
	Payload       string `json:"payload"`
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
	pluginType cf.ModelPluginType
}

// decisionPlugin is the struct that stores the decision plugin
type decisionPlugin struct {
	p *plugin.Plugin
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
	modelProcessFunc    map[string]func(ModelInput) (ModelResults, error)
	decisionCheckFunc   map[string]func(DecisionInput) (bool, error)
	decisionPlugins     map[string]decisionPlugin
	results             sync.Map
	channelsMutex       sync.Mutex
	syncModelsChannels  sync.Map
	asyncModelsChannels sync.Map
	natConn             *nats.Conn
}

// New creates a new PluginManager instance.
func New(meter metric.Meter) *PluginManager {
	pm := new(PluginManager)
	conf := cf.Get()
	logger := lg.Get()
	logger.Printf(lg.DEBUG, "Connecting to NATS server at %s", conf.NatsURL)

	nc, err := nats.Connect(conf.NatsURL)

	if err != nil {
		logger.Printf(lg.ERROR, "Failed to connect to NATS server")
	}

	pm.natConn = nc

	// Loading of model plugins
	pm.modelPlugins = make(map[string]modelPlugin)
	pm.modelProcessFunc = make(map[string]func(ModelInput) (ModelResults, error))
	for _, data := range conf.ModelPlugins {
		tp, err := plugin.Open(data.Path)
		if err != nil {
			logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
			continue
		}
		if data.Mode == "async" || conf.ModelPlugins[data.ID].Remote {
			f, err := tp.Lookup("InitPluginAsync")
			if err != nil {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
				continue
			}
			initPlugin, ok := f.(func(map[string]string, metric.Meter, func(func(ModelInput) (ModelResults, error))) error)
			if !ok {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: invalid InitPluginAsync function type", data.ID)
				continue
			}
			err = initPlugin(data.Params, meter, func(modelProcess func(ModelInput) (ModelResults, error)) {
				ModelProcessHandler(data.ID, modelProcess)
			})
			if err != nil {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
				continue
			}
			go pm.ModelResultsHandler(data.ID)
		} else {
			f, err := tp.Lookup("InitPlugin")
			if err != nil {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
				continue
			}
			initPlugin, ok := f.(func(map[string]string, metric.Meter) error)
			if !ok {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: invalid InitPlugin function type", data.ID)
				continue
			}
			err = initPlugin(data.Params, meter)
			procFunc, err := tp.Lookup("Process")
			if err != nil {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: cannot load Process function", data.ID)
				continue
			}
			process, ok := procFunc.(func(ModelInput) (ModelResults, error))
			if !ok {
				logger.Printf(lg.WARN, "| %s | cannot load plugin: invalid Process function type", data.ID)
				continue
			}
			pm.modelProcessFunc[data.ID] = process
		}
		modelPluginLoaded := modelPlugin{tp, data.PluginType}
		pm.modelPlugins[data.ID] = modelPluginLoaded
		logger.Printf(lg.INFO, "| %s | plugin loaded", data.ID)
	}

	pm.decisionPlugins = make(map[string]decisionPlugin)
	pm.decisionCheckFunc = make(map[string]func(DecisionInput) (bool, error))
	// Loading of decision plugins
	for _, data := range conf.DecisionPlugins {
		tp, err := plugin.Open(data.Path)
		if err != nil {
			logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
			continue
		}
		f, err := tp.Lookup("InitPlugin")
		if err != nil {
			logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
			continue
		}
		initPlugin, ok := f.(func(map[string]string, metric.Meter) error)
		if !ok {
			logger.Printf(lg.WARN, "| %s | cannot load plugin: invalid InitPlugin function type", data.ID)
			continue
		}
		err = initPlugin(data.Params, meter)
		if err != nil {
			logger.Printf(lg.WARN, "| %s | cannot load plugin: %v", data.ID, err)
			continue
		}
		cR, err := tp.Lookup("CheckResults")
		if err != nil {
			logger.Printf(lg.ERROR, "| %s | cannot load plugin check results function: %v", data.ID, err)
			continue
		}
		checkResults, ok := cR.(func(DecisionInput) (bool, error))
		if !ok {
			logger.Printf(lg.ERROR, "| %s | CheckResults lookup failed for plugin: invalid function type", data.ID)
			continue
		}
		pm.decisionCheckFunc[data.ID] = checkResults
		decisionPluginLoaded := decisionPlugin{tp}
		pm.decisionPlugins[data.ID] = decisionPluginLoaded
	}
	return pm
}

// InitTransaction initializes the transaction with the given ID
func (p *PluginManager) InitTransaction(transactionId string) {
	p.results.Store(transactionId, new(sync.Map))
}

// CloseTransaction closes the transaction with the given ID
// removing all sync model data
func (p *PluginManager) CloseTransaction(transactionId string) {
	logger := lg.Get()
	transactionMap, ok := p.syncModelsChannels.Load(transactionId)
	if !ok {
		logger.TPrintf(lg.ERROR, transactionId, "Transaction %s not found", transactionId)
	} else {
		transactionMap.(*sync.Map).Range(func(key, value interface{}) bool {
			ch := value.(chan ModelStatus)
            close(ch)
            for range ch {}
			transactionMap.(*sync.Map).Delete(key)
			return true
		})
		p.syncModelsChannels.Delete(transactionId)
		resultsMap, ok := p.results.Load(transactionId)
		if !ok {
			logger.TPrintf(lg.ERROR, transactionId, "Results for transaction %s not found", transactionId)
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
func (p *PluginManager) AddModelChannel(transactionId string, t cf.ModelPluginType, modelPlugStatus chan ModelStatus, modelType string) {
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
func (p *PluginManager) RemoveAsyncModelChannel(transactionId string, t cf.ModelPluginType) {
	typeModel, ok := p.asyncModelsChannels.Load(transactionId)
	if ok {
		channelMap := typeModel.(*sync.Map)
        ch, channelOk := channelMap.Load(t.String())

        if channelOk {
			close(ch.(chan ModelStatus))
			for range ch.(chan ModelStatus) {}
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
		logger := lg.Get()
		logger.TPrintf(lg.ERROR, transactionId, "Transaction %s not found when trying to remove async model channel", transactionId)
	}
}

// AddToQueue adds a payload to the model queue
func (p *PluginManager) AddToQueue(modelId, transactionId, payload string) error {
	payloadToSend := &ModelInput{
		TransactionId: transactionId,
		Payload:       payload,
	}

	jsonPayload, err := json.Marshal(payloadToSend)

	if err != nil {
		return err
	}

	return p.natConn.Publish(modelId, jsonPayload)
}

// Process is in charge of calling the model plugin with id modelID
func (p *PluginManager) Process(modelID, transactionId, payload string, t cf.ModelPluginType, modelPlugStatus chan ModelStatus) {
	conf := cf.Get()

	mp, exists := p.modelPlugins[modelID]
	if !exists {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("model plugin not found")}
		return
	}

	// check if the plugin is capable of analyzing the indicated part of the transaction
	if mp.pluginType != t {
		modelPlugStatus <- ModelStatus{ModelID: modelID,
			Err: fmt.Errorf("plugin type %v cannot process a request with incompatible type %v", mp.pluginType, t)}
		return
	}

	process := p.modelProcessFunc[modelID]

	if conf.ModelPlugins[modelID].Mode == "async" {
		modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("model plugin is async")}
		return
	} else {
		res, err := process(ModelInput{TransactionId: transactionId, Payload: payload})
		// res, err := process(transactionId, payload)

		if err != nil {
			modelPlugStatus <- ModelStatus{ModelID: modelID, Err: err}
			return
		}
		// store the results
		resultSyncMap, ok := p.results.Load(transactionId)
		if !ok {
			modelPlugStatus <- ModelStatus{ModelID: modelID, Err: fmt.Errorf("transaction results not found")}
			return
		}
		resultSyncMap.(*sync.Map).Store(modelID, res)
		modelPlugStatus <- ModelStatus{ModelID: modelID, ProbAttack: res.ProbAttack, Err: nil}
	}
}

// CheckResult is in charge of calling the decision plugin with id decisionID over the
// transaction with id transactID
func (p *PluginManager) CheckResult(transactionId, decisionId string, wafParams map[string]string) (bool, error) {
	logger := lg.Get()

	checkResults, ok := p.decisionCheckFunc[decisionId]
	if !ok {
		return false, fmt.Errorf("decision plugin not found")
	}

	transactionResults, ok := p.results.Load(transactionId)
	if !ok {
		return false, fmt.Errorf("transaction results not found")
	}

	configStore := cf.Get()

	modelResultMap := make(map[string]ModelResults)
	modelWeightMap := make(map[string]float64)
	transactionResults.(*sync.Map).Range(func(key, value interface{}) bool {
		modelResultMap[key.(string)] = value.(ModelResults)
		modelWeightMap[key.(string)] = configStore.ModelPlugins[key.(string)].Weight
		return true
	})

	res, err := checkResults(DecisionInput{TransactionId: transactionId, Results: modelResultMap, ModelWeight: modelWeightMap, WAFdata: wafParams})
	logger.TPrintf(lg.INFO, transactionId, "%s | transaction checked. Block: %t ", decisionId, res)

	return res, err
}

// ModelResultsHandler listens for messages on the model results queue
func (p *PluginManager) ModelResultsHandler(modelId string) {
	logger := lg.Get()
	conf := cf.Get()

	sub, err := p.natConn.Subscribe(modelId+"/results", func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &ModelTransmitionResults{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				logger.Printf(lg.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
			} else {
				var channel interface{}
				var ok bool
				if conf.ModelPlugins[modelId].Mode == "async" {
					channel, ok = p.asyncModelsChannels.Load(data.TransactionId)
				} else {
					channel, ok = p.syncModelsChannels.Load(data.TransactionId)
				}
				if !ok {
					logger.TPrintf(lg.ERROR, data.TransactionId, " Model %s | Transaction not found", modelId)
				} else {
					modelChannel, ok := channel.(*sync.Map).Load(conf.ModelPlugins[modelId].PluginType.String())
					if !ok {
						logger.Printf(lg.ERROR, "Model %s not found", modelId)
					} else {
						if data.Error != nil {
							modelChannel.(chan ModelStatus) <- ModelStatus{ModelID: modelId, Err: data.Error}
						} else {
							if conf.ModelPlugins[modelId].Mode != "async" {
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
		logger.Printf(lg.ERROR, "Model: %s | Failed to subscribe to model queue | %s", modelId, err.Error())
		return
	}

	logger.Printf(lg.INFO, "Model: %s | Listening for messages on model results queue", modelId)

	defer sub.Unsubscribe()
	defer p.natConn.Drain()

	select {}
}

// ModelProcessHandler listens for messages on the model queue
func ModelProcessHandler(modelId string, modelProcess func(ModelInput) (ModelResults, error)) {
	logger := lg.Get()
	logger.Printf(lg.INFO, "Model: %s | Starting model process handler", modelId)
	conf := cf.Get()

	nc, err := nats.Connect(conf.NatsURL)

	if err != nil {
		logger.Printf(lg.ERROR, "Model: %s | Failed to connect to NATS server", modelId)
		return
	}

	_, err = nc.Subscribe(modelId, func(msg *nats.Msg) {
		go func(msg nats.Msg) {
			data := &ModelInput{}
			err := json.Unmarshal(msg.Data, data)
			if err != nil {
				logger.Printf(lg.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
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
					logger.Printf(lg.ERROR, "Model: %s | Failed to parse JSON payload", modelId)
				}

				nc.Publish(modelId+"/results", jsonPayload)
			}
		}(*msg)
	})

	if err != nil {
		logger.Printf(lg.ERROR, "Model: %s | Failed to subscribe to model queue | %s", modelId, err.Error())
		return
	}

	logger.Printf(lg.INFO, "Model: %s | Listening for messages on model queue", modelId)
}

/*
Package configstore handles the configuration of WACE. The
configuration file is parsed, checked for errors and loaded into
memory
*/
package configstore

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sync/atomic"
	"time"
)

// ModelPluginType is an enum listing the parts of a request or
// response that a model plugin can handle.
type ModelPluginType int

const (
	RequestHeaders ModelPluginType = iota
	RequestBody
	AllRequest
	ResponseHeaders
	ResponseBody
	AllResponse
	Everything
)

// String returns the string representation of a model plugin type
func (t ModelPluginType) String() string {
	switch t {
	case RequestHeaders:
		return "RequestHeaders"
	case RequestBody:
		return "RequestBody"
	case AllRequest:
		return "AllRequest"
	case ResponseHeaders:
		return "ResponseHeaders"
	case ResponseBody:
		return "ResponseBody"
	case AllResponse:
		return "AllResponse"
	default:
		return "Everything"
	}
}

// StringToPluginType converts a string to the corresponding model plugin type
func StringToPluginType(textType string) (ModelPluginType, error) {
	switch textType {
	case "RequestHeaders":
		return RequestHeaders, nil
	case "RequestBody":
		return RequestBody, nil
	case "AllRequest":
		return AllRequest, nil
	case "ResponseHeaders":
		return ResponseHeaders, nil
	case "ResponseBody":
		return ResponseBody, nil
	case "AllResponse":
		return AllResponse, nil
	case "Everything":
		return Everything, nil
	}
	return -1, fmt.Errorf("invalid plugin type %s", textType)
}

type TrainingData struct {
	MinSamples           int    `yaml:"min_samples"`
	MaxSamples           int    `yaml:"max_samples"`
	ResultFilePath       string `yaml:"result_file_path"`
	StatusFilePath       string `yaml:"status_file_path"`
	StatusUpdateInterval int    `yaml:"status_update_interval"`
}

// ModelPluginConfig stores the configuration of a model plugin
type modelPluginConfig struct {
	ID           string
	Path         string
	Params       map[string]string
	PluginType   ModelPluginType
	async        bool
	remote       bool
	Training     bool
	TrainingData TrainingData
	sanitize     bool
	Timeout      time.Duration
}

// DecisionPluginConfig stores the configuration of a decision plugin
type decisionPluginConfig struct {
	ID           string
	Path         string
	Params       map[string]string
	Training     bool
	TrainingData TrainingData
	ModelWeights map[string]float64
	WAFWeight    float64
	// Timeout bounds each call to the plugin. Zero means no timeout.
	Timeout time.Duration
}

// ConfigStore stores all wacecore configuration from the config file.
type ConfigStore struct {
	ModelPlugins      map[string]modelPluginConfig
	DecisionPlugins   map[string]decisionPluginConfig
	NatsURL           string
	ApplicationId     string
	CredentialHeaders []string
	ModelTimeout      time.Duration
	AsyncModelTimeout time.Duration
}

// DefaultModelTimeout is used when model_timeout is not set in the
// config file
const DefaultModelTimeout = 200 * time.Millisecond

// DefaultAsyncModelTimeout is used when async_model_timeout is not set
// in the config file
const DefaultAsyncModelTimeout = 60 * time.Second

var config atomic.Pointer[ConfigStore]

// Get returns the unique instance of configstore
func Get() (*ConfigStore, error) {
	if config.Load() == nil {
		return nil, fmt.Errorf("ConfigStore: Configuration was not loaded")
	}
	return config.Load(), nil
}

// Clean remove the references to the stored instance of configstore
func Clean() {
	config.Store(nil)
}

type configFileModelPlugin struct {
	ID           string
	Path         string
	Params       map[string]string
	PluginType   string `yaml:"plugin_type"`
	Async        bool
	Remote       bool
	Training     bool
	TrainingData TrainingData `yaml:"training_data"`
	Sanitize     bool
	Timeout      time.Duration
}

type configFileDecisionPlugin struct {
	ID           string
	Path         string
	Params       map[string]string
	ModelWeights map[string]float64 `yaml:"model_weights"`
	WAFWeight    float64            `yaml:"waf_weight"`
	Training     bool
	TrainingData TrainingData `yaml:"training_data"`
	Timeout      time.Duration
}

type ConfigFileData struct {
	ModelPlugins      []configFileModelPlugin    `yaml:"model_plugins"`
	DecisionPlugins   []configFileDecisionPlugin `yaml:"decision_plugins"`
	NatsURL           string
	CredentialHeaders []string `yaml:"credential_headers"`
	// nil value indicates that the default value must be used
	// a 0 value indicates no timeout must be used
	ModelTimeout      *time.Duration `yaml:"model_timeout"`
	AsyncModelTimeout *time.Duration `yaml:"async_model_timeout"`
}

// IsAsync returns true if the model plugin is async
func (c *ConfigStore) IsAsync(modelID string) bool {
	return c.ModelPlugins[modelID].async
}

// IsRemote returns true if the model plugin is remote
func (c *ConfigStore) IsRemote(modelID string) bool {
	return c.ModelPlugins[modelID].remote
}

// IsInTraining returns true if the model plugin is in training mode (collecting data)
func (c *ConfigStore) IsInTraining(modelID string) bool {
	return c.ModelPlugins[modelID].Training
}

// IsDecisionInTraining returns true if the decision plugin is in training mode (collecting data)
func (c *ConfigStore) IsDecisionInTraining(decisionID string) bool {
	return c.DecisionPlugins[decisionID].Training
}

func (c *ConfigStore) ShouldSanitize(modelID string) bool {
	return c.ModelPlugins[modelID].sanitize
}

// CheckConfig verifies if the configuration read from the config file
// is correct.
func checkConfig(inConf ConfigFileData) error {
	if inConf.ModelTimeout != nil && *inConf.ModelTimeout < 0 {
		return fmt.Errorf("model timeout cannot be negative: %v", *inConf.ModelTimeout)
	}
	if inConf.AsyncModelTimeout != nil && *inConf.AsyncModelTimeout < 0 {
		return fmt.Errorf("async model timeout cannot be negative: %v", *inConf.AsyncModelTimeout)
	}

	// check modelplugins
	for _, modelP := range inConf.ModelPlugins {
		if modelP.Path != "" {
			if _, err := os.Stat(modelP.Path); err != nil {
				return fmt.Errorf("%s plugin path %s: %v", modelP.ID, modelP.Path, err)
			}
		} else {
			return fmt.Errorf("%s plugin path is empty, please provide a valid path", modelP.ID)
		}
		if modelP.PluginType == "" {
			return fmt.Errorf("%s plugin type cannot be empty, please provide a valid type", modelP.ID)
		}
		if modelP.Timeout < 0 {
			return fmt.Errorf("model %s: timeout cannot be negative: %v", modelP.ID, modelP.Timeout)
		}
		if modelP.Training && modelP.Async {
			return fmt.Errorf("model %s plugin cannot be in training mode and async mode at the same time", modelP.ID)
		}
		if modelP.Training && modelP.Remote {
			return fmt.Errorf("model %s: remote training mode is not supported", modelP.ID)
		}
		if modelP.Training && (modelP.TrainingData.MaxSamples <= 0 || modelP.TrainingData.MinSamples < 0 ||
			modelP.TrainingData.MaxSamples < modelP.TrainingData.MinSamples || modelP.TrainingData.MaxSamples < modelP.TrainingData.StatusUpdateInterval) {
			return fmt.Errorf("model %s: Max sample count should be greater than 0. Min sample count should be greater than or equal 0. Max sample count should be greater than or equal min sample count. Max sample count should be greater than or equal status update interval", modelP.ID)
		}
	}
	// check decisionplugins
	for _, decisionP := range inConf.DecisionPlugins {

		if decisionP.Path != "" {
			if _, err := os.Stat(decisionP.Path); err != nil {
				return fmt.Errorf("%s plugin path %s cannot be opened: %v", decisionP.ID, decisionP.Path, err)
			}
		} else {
			return fmt.Errorf("%s plugin path is empty, please provide a valid path", decisionP.ID)
		}
		if decisionP.Timeout < 0 {
			return fmt.Errorf("decision %s: timeout cannot be negative: %v", decisionP.ID, decisionP.Timeout)
		}
		if decisionP.Training && (decisionP.TrainingData.MaxSamples <= 0 || decisionP.TrainingData.MinSamples < 0 ||
			decisionP.TrainingData.MaxSamples < decisionP.TrainingData.MinSamples || decisionP.TrainingData.MaxSamples < decisionP.TrainingData.StatusUpdateInterval) {
			return fmt.Errorf("decision %s: Max sample count should be greater than 0. Min sample count should be greater than or equal 0. Max sample count should be greater than or equal min sample count. Max sample count should be greater than or equal status update interval", decisionP.ID)
		}
	}

	return nil
}

// SetConfig sets the configuration of WACE from the configuration file
func SetConfig(inConf ConfigFileData) (*ConfigStore, error) {
	err := checkConfig(inConf)
	if err != nil {
		return nil, err
	}

	cs := new(ConfigStore)

	cs.ModelPlugins = make(map[string]modelPluginConfig)
	for _, modelP := range inConf.ModelPlugins {
		var modelConfig modelPluginConfig
		modelConfig.ID = modelP.ID
		modelConfig.Path = modelP.Path
		modelConfig.Params = maps.Clone(modelP.Params)
		modelConfig.PluginType, err = StringToPluginType(modelP.PluginType)
		modelConfig.async = modelP.Async
		modelConfig.remote = modelP.Remote
		modelConfig.Training = modelP.Training
		modelConfig.TrainingData = modelP.TrainingData
		if modelConfig.TrainingData.StatusUpdateInterval == 0 {
			modelConfig.TrainingData.StatusUpdateInterval = max(1, modelConfig.TrainingData.MaxSamples/10)
		}
		modelConfig.sanitize = modelP.Sanitize
		modelConfig.Timeout = modelP.Timeout
		if err != nil {
			return nil, err
		}
		cs.ModelPlugins[modelConfig.ID] = modelConfig
	}

	cs.DecisionPlugins = make(map[string]decisionPluginConfig)
	for _, decisionP := range inConf.DecisionPlugins {
		var decisionConfig decisionPluginConfig
		decisionConfig.ID = decisionP.ID
		decisionConfig.Path = decisionP.Path
		decisionConfig.Params = maps.Clone(decisionP.Params)
		decisionConfig.Training = decisionP.Training
		decisionConfig.TrainingData = decisionP.TrainingData
		if decisionConfig.TrainingData.StatusUpdateInterval == 0 {
			decisionConfig.TrainingData.StatusUpdateInterval = max(1, decisionConfig.TrainingData.MaxSamples/10)
		}
		decisionConfig.ModelWeights = maps.Clone(decisionP.ModelWeights)
		decisionConfig.WAFWeight = decisionP.WAFWeight
		decisionConfig.Timeout = decisionP.Timeout
		cs.DecisionPlugins[decisionConfig.ID] = decisionConfig
	}

	cs.NatsURL = inConf.NatsURL

	cs.CredentialHeaders = slices.Clone(inConf.CredentialHeaders)

	cs.ModelTimeout = DefaultModelTimeout
	if inConf.ModelTimeout != nil {
		cs.ModelTimeout = *inConf.ModelTimeout
	}
	cs.AsyncModelTimeout = DefaultAsyncModelTimeout
	if inConf.AsyncModelTimeout != nil {
		cs.AsyncModelTimeout = *inConf.AsyncModelTimeout
	}

	config.Store(cs)

	return cs, nil
}

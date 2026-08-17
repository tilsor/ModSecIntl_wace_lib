/*
Package configstore handles the configuration of WACE. The
configuration file is parsed, checked for errors and loaded into
memory
*/
package configstore

import (
	"fmt"
	"os"

	"github.com/tilsor/ModSecIntl_logging/logging"
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
	Weight       float64
	Threshold    float64
	Params       map[string]string
	PluginType   ModelPluginType
	async        bool
	remote       bool
	Training     bool
	TrainingData TrainingData
	sanitize     bool
}

// DecisionPluginConfig stores the configuration of a decision plugin
type decisionPluginConfig struct {
	ID           string
	Path         string
	Params       map[string]string
	Training     bool
	TrainingData TrainingData
	ModelWeight  map[string]float64
	WAFWeight    float64
}

// ConfigStore stores all wacecore configuration from the config file.
type ConfigStore struct {
	ModelPlugins      map[string]modelPluginConfig
	DecisionPlugins   map[string]decisionPluginConfig
	LogPath           string
	LogLevel          logging.LogLevel
	NatsURL           string
	ApplicationId     string
	CredentialHeaders []string
}

var config *ConfigStore

// Create and returns the unique instance of configstore if it does not exist previously, in other case returns error
func New() (*ConfigStore, error) {
	if config != nil {
		return nil, fmt.Errorf("ConfigStore: an instance already exists")
	}
	config = new(ConfigStore)
	return config, nil
}

// Get returns the unique instance of configstore
func Get() (*ConfigStore, error) {
	if config == nil {
		return nil, fmt.Errorf("ConfigStore: Configuration was not loaded")
	}
	return config, nil
}

// Clean remove the references to the stored instance of configstore
func Clean() {
	config = nil
}

type configFileModelPlugin struct {
	ID           string
	Path         string
	Weight       float64
	Threshold    float64
	Params       map[string]string
	PluginType   string `yaml:"plugintype"`
	Async        bool
	Remote       bool
	Training     bool
	TrainingData TrainingData `yaml:"training_data"`
	Sanitize     bool
}

type configFileDecisionPlugin struct {
	ID           string
	Path         string
	Params       map[string]string
	ModelWeight  map[string]float64
	WAFWeight    float64
	Training     bool
	TrainingData TrainingData `yaml:"training_data"`
}

type ConfigFileData struct {
	Logpath           string
	Loglevel          string
	Modelplugins      []configFileModelPlugin
	Decisionplugins   []configFileDecisionPlugin
	NatsURL           string
	CredentialHeaders []string `yaml:"credential_headers"`
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

// CheckLogging verifies if the log path is valid
func checkLogging(inConf ConfigFileData) error {
	// check logpath
	if inConf.Logpath == "" {
		return fmt.Errorf("log path empty")
	}
	_, err := os.Stat(inConf.Logpath)
	if err != nil { // check if log file does not exists already
		// Attempt to create dummy file
		var d []byte
		err = os.WriteFile(inConf.Logpath, d, 0644)
		if err == nil {
			err = os.Remove(inConf.Logpath) // delete it
		}
	}
	return err
}

// CheckConfig verifies if the configuration read from the config file
// is correct.
func checkConfig(inConf ConfigFileData) error {
	err := checkLogging(inConf)
	if err != nil {
		return fmt.Errorf("invalid log path %s: %v", inConf.Logpath, err)
	}

	// check modelplugins
	for _, modelP := range inConf.Modelplugins {
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
	for _, decisionP := range inConf.Decisionplugins {

		if decisionP.Path != "" {
			if _, err := os.Stat(decisionP.Path); err != nil {
				return fmt.Errorf("%s plugin path %s cannot be opened: %v", decisionP.ID, decisionP.Path, err)
			}
		} else {
			return fmt.Errorf("%s plugin path is empty, please provide a valid path", decisionP.ID)
		}
		if decisionP.Training && (decisionP.TrainingData.MaxSamples <= 0 || decisionP.TrainingData.MinSamples < 0 ||
			decisionP.TrainingData.MaxSamples < decisionP.TrainingData.MinSamples || decisionP.TrainingData.MaxSamples < decisionP.TrainingData.StatusUpdateInterval) {
			return fmt.Errorf("decision %s: Max sample count should be greater than 0. Min sample count should be greater than or equal 0. Max sample count should be greater than or equal min sample count. Max sample count should be greater than or equal status update interval", decisionP.ID)
		}
	}

	return nil
}

// SetConfig sets the configuration of WACE from the configuration file
func (cs *ConfigStore) SetConfig(inConf ConfigFileData) error {
	err := checkConfig(inConf)
	if err != nil {
		return err
	}

	cs.LogPath = inConf.Logpath
	cs.LogLevel, err = logging.StringToLogLevel(inConf.Loglevel)
	if err != nil {
		return err
	}

	cs.ModelPlugins = make(map[string]modelPluginConfig)
	for _, modelP := range inConf.Modelplugins {
		var modelConfig modelPluginConfig
		modelConfig.ID = modelP.ID
		modelConfig.Path = modelP.Path
		modelConfig.Weight = modelP.Weight
		modelConfig.Threshold = modelP.Threshold
		modelConfig.Params = modelP.Params
		modelConfig.PluginType, err = StringToPluginType(modelP.PluginType)
		modelConfig.async = modelP.Async
		modelConfig.remote = modelP.Remote
		modelConfig.Training = modelP.Training
		modelConfig.TrainingData = modelP.TrainingData
		if modelConfig.TrainingData.StatusUpdateInterval == 0 {
			modelConfig.TrainingData.StatusUpdateInterval = max(1, modelConfig.TrainingData.MaxSamples/10)
		}
		modelConfig.sanitize = modelP.Sanitize
		if err != nil {
			return err
		}
		cs.ModelPlugins[modelConfig.ID] = modelConfig
	}

	cs.DecisionPlugins = make(map[string]decisionPluginConfig)
	for _, decisionP := range inConf.Decisionplugins {
		var decisionConfig decisionPluginConfig
		decisionConfig.ID = decisionP.ID
		decisionConfig.Path = decisionP.Path
		decisionConfig.Params = decisionP.Params
		decisionConfig.Training = decisionP.Training
		decisionConfig.TrainingData = decisionP.TrainingData
		if decisionConfig.TrainingData.StatusUpdateInterval == 0 {
			decisionConfig.TrainingData.StatusUpdateInterval = max(1, decisionConfig.TrainingData.MaxSamples/10)
		}
		decisionConfig.ModelWeight = decisionP.ModelWeight
		decisionConfig.WAFWeight = decisionP.WAFWeight
		cs.DecisionPlugins[decisionConfig.ID] = decisionConfig
	}

	cs.NatsURL = inConf.NatsURL

	cs.CredentialHeaders = inConf.CredentialHeaders

	return nil
}

package configstore

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var validConfig = []byte(`---
model_plugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    params:
      d: "sds"
      b: "dnid"
      e: "dofnno"
    plugin_type: "RequestHeaders"
    mode: "sync"
  - id: "trivial2"
    path: "../testdata/plugins/model/trivial2.so"
    params:
      a: "sdsds"
      b: "sdfjdnid"
      c: "kfoskdofnno"
    plugin_type: "RequestHeaders"
decision_plugins:
  - id: "test"
    path: "../testdata/plugins/decision/test.so"
    waf_weight: 0.5
    decisionbalance: 0.5
    params:
      ssdaf: "sdsds"
      dsfb: "sdfjdnid"
      csfd: "kfoskdofnno"
`)

// initialize publishes the given configuration and returns the
// resulting snapshot.
func initialize(configuration []byte) (*ConfigStore, error) {
	var aux ConfigFileData
	if err := yaml.Unmarshal(configuration, &aux); err != nil {
		return nil, err
	}
	return SetConfig(aux)
}

func TestLoadConfigYamlEmpty(t *testing.T) {
	defer Clean()

	// a configuration without plugins is valid
	cs, err := initialize([]byte(`---`))
	if err != nil {
		t.Fatalf("empty config returned error: %v", err)
	}
	if len(cs.ModelPlugins) != 0 || len(cs.DecisionPlugins) != 0 {
		t.Errorf("empty config loaded plugins: %v %v", cs.ModelPlugins, cs.DecisionPlugins)
	}
}

func TestLoadConfigYamlValid(t *testing.T) {
	defer Clean()

	_, err := initialize(validConfig)
	if err != nil {
		t.Errorf("valid config returned error: %v", err)
	}
}

func TestLoadConfigYamlInvalid(t *testing.T) {
	defer Clean()

	_, err := initialize([]byte(`()=)(/&/()~@#~½¬{[{½¬½---sfdjlskjfs#@~sjdfa`))

	if err == nil {
		t.Error("invalid config does not return error")
	}
}

// TestLoadConfigYamlIgnoresLegacyLogKeys checks that configuration files
// written for the old logging package still load: logpath and loglevel
// are ignored, whatever their value.
func TestLoadConfigYamlIgnoresLegacyLogKeys(t *testing.T) {
	defer Clean()

	config := "---\nlogpath: /usr/not_writable.log\nloglevel: INVALIDLOGLEVEL\n"
	_, err := initialize([]byte(config))
	if err != nil {
		t.Errorf("config with legacy log keys returned error: %v", err)
	}
}

func TestLoadConfigYamlPluginType(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		wantErr  bool
		wantType string
	}{
		{
			name: "invalid plugin type",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: InvalidPluginType
`,
			wantErr: true,
		},
		{
			name: "empty plugin type",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: ""
`,
			wantErr: true,
		},
		{
			name: "nonexistent model plugin path",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/nonexistent.so"
    plugin_type: "RequestHeaders"
`,
			wantErr: true,
		},
		{
			name: "empty model plugin path",
			config: `---
model_plugins:
  - id: "testplugin"
    path: ""
    plugin_type: "RequestHeaders"
`,
			wantErr: true,
		},
		{
			name: "empty decision plugin path",
			config: `---
decision_plugins:
  - id: "test"
    path: ""
`,
			wantErr: true,
		},
		{
			name: "nonexistent decision plugin path",
			config: `---
decision_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/decision/nonexistent.so"
`,
			wantErr: true,
		},
		{
			name: "valid RequestHeaders",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
`,
			wantType: "RequestHeaders",
		},
		{
			name: "valid RequestBody",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestBody"
`,
			wantType: "RequestBody",
		},
		{
			name: "valid AllRequest",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "AllRequest"
`,
			wantType: "AllRequest",
		},
		{
			name: "valid ResponseHeaders",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "ResponseHeaders"
`,
			wantType: "ResponseHeaders",
		},
		{
			name: "valid ResponseBody",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "ResponseBody"
`,
			wantType: "ResponseBody",
		},
		{
			name: "valid AllResponse",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "AllResponse"
`,
			wantType: "AllResponse",
		},
		{
			name: "valid Everything",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "Everything"
`,
			wantType: "Everything",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			cs, err := initialize([]byte(tt.config))
			if (err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("expected error but got none")
				} else {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if tt.wantType != "" {
				if got := fmt.Sprint(cs.ModelPlugins["testplugin"].PluginType); got != tt.wantType {
					t.Errorf("plugin type = %q, want %q", got, tt.wantType)
				}
			}
		})
	}
}

func TestSetConfigGetCleanLifecycle(t *testing.T) {
	cs1, err := initialize(validConfig)
	if err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	cs2, err := Get()
	if err != nil {
		t.Fatalf("Get() after SetConfig() failed: %v", err)
	}
	if cs1 != cs2 {
		t.Errorf("Get() returned a different instance than SetConfig()")
	}

	Clean()

	_, err = Get()
	if err == nil {
		t.Errorf("Get() after Clean() should return error")
	}
}

// TestSetConfigPublishesNewSnapshot checks that SetConfig publishes a new
// ConfigStore instead of modifying the current one, so whoever holds the
// previous snapshot keeps a consistent view.
func TestSetConfigPublishesNewSnapshot(t *testing.T) {
	defer Clean()
	old, err := initialize(validConfig)
	if err != nil {
		t.Fatalf("first SetConfig: %v", err)
	}
	oldModels := len(old.ModelPlugins)

	cur, err := initialize([]byte(`---`))
	if err != nil {
		t.Fatalf("second SetConfig: %v", err)
	}
	if cur == old {
		t.Fatal("SetConfig reused the previous snapshot")
	}
	if len(old.ModelPlugins) != oldModels {
		t.Errorf("previous snapshot changed: %d model plugins, want %d", len(old.ModelPlugins), oldModels)
	}
	if got, _ := Get(); got != cur {
		t.Error("Get() does not return the last published snapshot")
	}
}

// TestSetConfigInvalidKeepsPrevious checks that a rejected configuration
// does not replace the published one.
func TestSetConfigInvalidKeepsPrevious(t *testing.T) {
	defer Clean()
	prev, err := initialize(validConfig)
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	for name, conf := range map[string]string{
		"invalid path": "---\nmodel_plugins:\n  - id: \"missing\"\n    path: \"../testdata/plugins/model/does_not_exist.so\"\n    plugin_type: \"Everything\"\n",
		"invalid type": "---\nmodel_plugins:\n  - id: \"bad\"\n    path: \"../testdata/plugins/model/trivial.so\"\n    plugin_type: \"NotAType\"\n",
	} {
		if _, err := initialize([]byte(conf)); err == nil {
			t.Errorf("%s: SetConfig should return error", name)
		}
		if got, _ := Get(); got != prev {
			t.Errorf("%s: rejected SetConfig replaced the published config", name)
		}
	}
}

// TestSetConfigDoesNotAliasInput checks that the published snapshot does
// not share maps or slices with the ConfigFileData passed to SetConfig.
func TestSetConfigDoesNotAliasInput(t *testing.T) {
	defer Clean()
	in := ConfigFileData{
		ModelPlugins: []configFileModelPlugin{{
			ID: "m", Path: "../testdata/plugins/model/trivial.so", PluginType: "Everything",
			Params: map[string]string{"k": "v"},
		}},
		DecisionPlugins: []configFileDecisionPlugin{{
			ID: "d", Path: "../testdata/plugins/decision/simple.so",
			Params:       map[string]string{"k": "v"},
			ModelWeights: map[string]float64{"m": 1},
		}},
		CredentialHeaders: []string{"x-token"},
	}
	cs, err := SetConfig(in)
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	in.ModelPlugins[0].Params["k"] = "changed"
	in.DecisionPlugins[0].Params["k"] = "changed"
	in.DecisionPlugins[0].ModelWeights["m"] = 2
	in.CredentialHeaders[0] = "changed"

	if got := cs.ModelPlugins["m"].Params["k"]; got != "v" {
		t.Errorf("model Params aliased: got %q", got)
	}
	if got := cs.DecisionPlugins["d"].Params["k"]; got != "v" {
		t.Errorf("decision Params aliased: got %q", got)
	}
	if got := cs.DecisionPlugins["d"].ModelWeights["m"]; got != 1 {
		t.Errorf("ModelWeights aliased: got %v", got)
	}
	if got := cs.CredentialHeaders[0]; got != "x-token" {
		t.Errorf("CredentialHeaders aliased: got %q", got)
	}
}

func TestStringToPluginType(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ModelPluginType
		wantErr bool
	}{
		{"RequestHeaders", "RequestHeaders", RequestHeaders, false},
		{"RequestBody", "RequestBody", RequestBody, false},
		{"AllRequest", "AllRequest", AllRequest, false},
		{"ResponseHeaders", "ResponseHeaders", ResponseHeaders, false},
		{"ResponseBody", "ResponseBody", ResponseBody, false},
		{"AllResponse", "AllResponse", AllResponse, false},
		{"Everything", "Everything", Everything, false},
		{"invalid value", "invalid", 0, true},
		{"empty string", "", 0, true},
		{"wrong case", "requestheaders", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StringToPluginType(tt.input)
			if (err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("StringToPluginType(%q) should return error but did not", tt.input)
				} else {
					t.Errorf("StringToPluginType(%q) returned unexpected error: %v", tt.input, err)
				}
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("StringToPluginType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestModelPluginTypeString(t *testing.T) {
	tests := []struct {
		pluginType ModelPluginType
		want       string
	}{
		{RequestHeaders, "RequestHeaders"},
		{RequestBody, "RequestBody"},
		{AllRequest, "AllRequest"},
		{ResponseHeaders, "ResponseHeaders"},
		{ResponseBody, "ResponseBody"},
		{AllResponse, "AllResponse"},
		{Everything, "Everything"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.pluginType.String(); got != tt.want {
				t.Errorf("ModelPluginType(%d).String() = %q, want %q", int(tt.pluginType), got, tt.want)
			}
		})
	}
}

func TestIsAsync(t *testing.T) {
	tests := []struct {
		name      string
		async     bool
		wantAsync bool
	}{
		{"no async field defaults to sync", false, false},
		{"async: true is async", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			config := fmt.Sprintf(`---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    async: %v
`, tt.async)
			cs, err := initialize([]byte(config))
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if got := cs.IsAsync("testplugin"); got != tt.wantAsync {
				t.Errorf("IsAsync with async=%v = %v, want %v", tt.async, got, tt.wantAsync)
			}
		})
	}
}

func TestGetBeforeSetConfig(t *testing.T) {
	// ensure clean state
	Clean()

	_, err := Get()
	if err == nil {
		t.Error("Get() before SetConfig() should return error")
	}
}

func TestIsInTraining(t *testing.T) {
	tests := []struct {
		name         string
		training     bool
		wantTraining bool
	}{
		{"no training field defaults to false", false, false},
		{"training: true enables training mode", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			trainingSection := ""
			if tt.training {
				trainingSection = "\n    training_data:\n      max_samples: 10"
			}
			config := fmt.Sprintf(`---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    training: %v%s
`, tt.training, trainingSection)
			cs, err := initialize([]byte(config))
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if got := cs.IsInTraining("testplugin"); got != tt.wantTraining {
				t.Errorf("IsInTraining with training=%v = %v, want %v", tt.training, got, tt.wantTraining)
			}
		})
	}
}

func TestTrainingDataConfig(t *testing.T) {
	tests := []struct {
		name           string
		config         string
		wantErr        bool
		wantMaxSamples int
		wantPath       string
	}{
		{
			name: "training with zero max_samples returns error",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    training: true
`,
			wantErr: true,
		},
		{
			name: "training and async are mutually exclusive",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    training: true
    async: true
    training_data:
      max_samples: 10
`,
			wantErr: true,
		},
		{
			name: "training and remote are mutually exclusive",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    training: true
    remote: true
    training_data:
      max_samples: 10
`,
			wantErr: true,
		},
		{
			name: "valid training config stores TrainingData correctly",
			config: `---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    training: true
    training_data:
      max_samples: 42
      result_file_path: "/dev/null"
`,
			wantErr:        false,
			wantMaxSamples: 42,
			wantPath:       "/dev/null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			cs, err := initialize([]byte(tt.config))
			if (err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("expected error but got none")
				} else {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if !tt.wantErr {
				got := cs.ModelPlugins["testplugin"].TrainingData
				if got.MaxSamples != tt.wantMaxSamples {
					t.Errorf("MaxSamples = %d, want %d", got.MaxSamples, tt.wantMaxSamples)
				}
				if got.ResultFilePath != tt.wantPath {
					t.Errorf("ResultsFilePath = %q, want %q", got.ResultFilePath, tt.wantPath)
				}
			}
		})
	}
}

func TestTrainingDataStatusUpdateInterval(t *testing.T) {
	cases := []struct {
		name             string
		maxSamples       int
		explicitInterval int // 0 means omitted from YAML
		wantInterval     int
	}{
		{"omitted with MaxSamples=100 defaults to 10", 100, 0, 10},
		{"omitted with MaxSamples=5 defaults to 1 (floor protection)", 5, 0, 1},
		{"omitted with MaxSamples=10 defaults to 1", 10, 0, 1},
		{"explicit value is preserved", 100, 7, 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer Clean()

			intervalLine := ""
			if tc.explicitInterval != 0 {
				intervalLine = fmt.Sprintf("\n      status_update_interval: %d", tc.explicitInterval)
			}
			config := fmt.Sprintf(`---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    training: true
    training_data:
      max_samples: %d%s
`, tc.maxSamples, intervalLine)

			cs, err := initialize([]byte(config))
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}

			got := cs.ModelPlugins["testplugin"].TrainingData.StatusUpdateInterval
			if got != tc.wantInterval {
				t.Errorf("StatusUpdateInterval = %d, want %d", got, tc.wantInterval)
			}
		})
	}
}

func TestShouldSanitize(t *testing.T) {
	tests := []struct {
		name         string
		sanitize     bool
		wantSanitize bool
	}{
		{"sanitize omitted defaults to false", false, false},
		{"sanitize: true propagates correctly", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			config := fmt.Sprintf(`---
model_plugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
    sanitize: %v
`, tt.sanitize)
			cs, err := initialize([]byte(config))
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}

			if got := cs.ShouldSanitize("testplugin"); got != tt.wantSanitize {
				t.Errorf("ShouldSanitize = %v, want %v", got, tt.wantSanitize)
			}
		})
	}
}

func TestShouldSanitizeUnknownModel(t *testing.T) {
	defer Clean()

	cs, err := initialize(validConfig)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if cs.ShouldSanitize("nonexistent") {
		t.Error("ShouldSanitize for unknown model ID should return false")
	}
}

func TestCredentialHeaders(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		wantHeaders []string
	}{
		{
			name: "no credential_headers field uses the defaults",
			config: `---
`,
			wantHeaders: defaultCredentialHeaders,
		},
		{
			name: "empty credential_headers field uses the defaults",
			config: `---
credential_headers:
`,
			wantHeaders: defaultCredentialHeaders,
		},
		{
			name: "empty credential_headers list disables header sanitization",
			config: `---
credential_headers: []
`,
			wantHeaders: []string{},
		},
		{
			name: "credential_headers values are stored",
			config: `---
credential_headers:
  - x-api-key
  - x-secret-token
`,
			wantHeaders: []string{"x-api-key", "x-secret-token"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			cs, err := initialize([]byte(tt.config))
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}

			if !reflect.DeepEqual(cs.CredentialHeaders, tt.wantHeaders) {
				t.Errorf("CredentialHeaders = %v, want %v", cs.CredentialHeaders, tt.wantHeaders)
			}
		})
	}
}

// TestCredentialHeadersDefaultsNotAliased checks that changing the stored
// headers does not change the defaults used by later configurations.
func TestCredentialHeadersDefaultsNotAliased(t *testing.T) {
	defer Clean()

	cs, err := initialize([]byte("---\n"))
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	cs.CredentialHeaders[0] = "changed"

	if got := defaultCredentialHeaders[0]; got != "authorization" {
		t.Errorf("defaultCredentialHeaders aliased: got %q", got)
	}
}

func TestNatsURL(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name: "empty string when natsurl not set",
			config: `---
`,
			wantURL: "",
		},
		{
			name: "stores custom URL",
			config: `---
natsurl: "nats.example.com:4222"
`,
			wantURL: "nats.example.com:4222",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			cs, err := initialize([]byte(tt.config))
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if cs.NatsURL != tt.wantURL {
				t.Errorf("NatsURL = %q, want %q", cs.NatsURL, tt.wantURL)
			}
		})
	}
}

func TestModelTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout string
		want    time.Duration
		wantErr bool
	}{
		{name: "default when model_timeout not set", timeout: "", want: DefaultModelTimeout},
		{name: "explicit 0s disables the timeout", timeout: "model_timeout: 0s\n", want: 0},
		{name: "stores custom timeout", timeout: "model_timeout: 750ms\n", want: 750 * time.Millisecond},
		{name: "negative timeout is rejected", timeout: "model_timeout: -1s\n", wantErr: true},
		{name: "timeout without unit is rejected", timeout: "model_timeout: 150\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			config := "---\n" + tt.timeout
			cs, err := initialize([]byte(config))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ModelTimeout = %v", cs.ModelTimeout)
				}
				return
			}
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if cs.ModelTimeout != tt.want {
				t.Errorf("ModelTimeout = %v, want %v", cs.ModelTimeout, tt.want)
			}
		})
	}
}

func TestAsyncModelTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout string
		want    time.Duration
		wantErr bool
	}{
		{name: "default when async_model_timeout not set", timeout: "", want: DefaultAsyncModelTimeout},
		{name: "explicit 0s disables the timeout", timeout: "async_model_timeout: 0s\n", want: 0},
		{name: "stores custom timeout", timeout: "async_model_timeout: 90s\n", want: 90 * time.Second},
		{name: "negative timeout is rejected", timeout: "async_model_timeout: -1s\n", wantErr: true},
		{name: "timeout without unit is rejected", timeout: "async_model_timeout: 60\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer Clean()

			config := "---\n" + tt.timeout
			cs, err := initialize([]byte(config))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got AsyncModelTimeout = %v", cs.AsyncModelTimeout)
				}
				return
			}
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if cs.AsyncModelTimeout != tt.want {
				t.Errorf("AsyncModelTimeout = %v, want %v", cs.AsyncModelTimeout, tt.want)
			}
		})
	}
}

func TestPluginTimeout(t *testing.T) {
	tests := []struct {
		name    string
		timeout string
		want    time.Duration
		wantErr bool
	}{
		{name: "no timeout when not set", timeout: "", want: 0},
		{name: "explicit 0s means no timeout", timeout: "    timeout: 0s\n", want: 0},
		{name: "stores custom timeout", timeout: "    timeout: 750ms\n", want: 750 * time.Millisecond},
		{name: "negative timeout is rejected", timeout: "    timeout: -1s\n", wantErr: true},
		{name: "timeout without unit is rejected", timeout: "    timeout: 150\n", wantErr: true},
	}

	for _, tt := range tests {
		for _, kind := range []string{"model", "decision"} {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				defer Clean()

				config := "---\nmodel_plugins:\n  - id: \"trivial\"\n    path: \"../testdata/plugins/model/trivial.so\"\n    plugin_type: \"Everything\"\n"
				if kind == "model" {
					config += tt.timeout
				}
				config += "decision_plugins:\n  - id: \"test\"\n    path: \"../testdata/plugins/decision/test.so\"\n"
				if kind == "decision" {
					config += tt.timeout
				}

				cs, err := initialize([]byte(config))
				if tt.wantErr {
					if err == nil {
						t.Fatal("expected error, got nil")
					}
					return
				}
				if err != nil {
					t.Fatalf("initialize failed: %v", err)
				}

				got := cs.ModelPlugins["trivial"].Timeout
				if kind == "decision" {
					got = cs.DecisionPlugins["test"].Timeout
				}
				if got != tt.want {
					t.Errorf("Timeout = %v, want %v", got, tt.want)
				}
			})
		}
	}
}

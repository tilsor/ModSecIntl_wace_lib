package configstore

import (
	"fmt"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

var validConfig = []byte(`---
logpath: "/dev/stderr"
loglevel: "DEBUG"
modelplugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    weight: 1
    threshold: 0.5
    params:
      d: "sds"
      b: "dnid"
      e: "dofnno"
    plugintype: "RequestHeaders"
    mode: "sync"
  - id: "trivial2"
    path: "../testdata/plugins/model/trivial2.so"
    weight: 2
    threshold: 0.1
    params:
      a: "sdsds"
      b: "sdfjdnid"
      c: "kfoskdofnno"
    plugintype: "RequestHeaders"
decisionplugins:
  - id: "test"
    path: "../testdata/plugins/decision/test.so"
    wafweight: 0.5
    decisionbalance: 0.5
    params:
      ssdaf: "sdsds"
      dsfb: "sdfjdnid"
      csfd: "kfoskdofnno"
`)

func initialize(configuration []byte) error {
	cs, err := Get()
	if err != nil {
		return err
	}
	var aux ConfigFileData
	err = yaml.Unmarshal(configuration, &aux)
	if err != nil {
		return err
	}
	err = cs.SetConfig(aux)
	if err != nil {
		return err
	}
	return nil
}

func TestLoadConfigYamlEmpty(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize([]byte(`---`))
	if err == nil {
		t.Error("empty config does not return error")
	}
}

func TestLoadConfigYamlValid(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize(validConfig)
	if err != nil {
		t.Errorf("valid config returned error: %v", err)
	}
}

func TestLoadConfigYamlInvalid(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Error(err)
	}

	defer Clean()

	err = initialize([]byte(`()=)(/&/()~@#~½¬{[{½¬½---sfdjlskjfs#@~sjdfa`))

	if err == nil {
		t.Error("invalid config does not return error")
	}
}

func TestLoadConfigYamlLogLevel(t *testing.T) {
	tests := []struct {
		level   string
		wantErr bool
	}{
		{"a", true},
		{"4", true},
		{"0", true},
		{"DEBUG", false},
		{"INFO", false},
		{"WARN", false},
		{"ERROR", false},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			_, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer Clean()

			config := "---\nlogpath: \"/dev/null\"\nloglevel: " + tt.level
			err = initialize([]byte(config))
			if (err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("log level %q should return error but did not", tt.level)
				} else {
					t.Errorf("log level %q returned unexpected error: %v", tt.level, err)
				}
			}
		})
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
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: InvalidPluginType
`,
			wantErr: true,
		},
		{
			name: "empty plugin type",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: ""
`,
			wantErr: true,
		},
		{
			name: "nonexistent model plugin path",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/nonexistent.so"
    plugintype: "RequestHeaders"
`,
			wantErr: true,
		},
		{
			name: "empty model plugin path",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: ""
    plugintype: "RequestHeaders"
`,
			wantErr: true,
		},
		{
			name: "empty decision plugin path",
			config: `---
loglevel: ERROR
logpath: /dev/null
decisionplugins:
  - id: "test"
    path: ""
`,
			wantErr: true,
		},
		{
			name: "nonexistent decision plugin path",
			config: `---
loglevel: ERROR
logpath: /dev/null
decisionplugins:
  - id: "testplugin"
    path: "../testdata/plugins/decision/nonexistent.so"
`,
			wantErr: true,
		},
		{
			name: "valid RequestHeaders",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "RequestHeaders"
`,
			wantType: "RequestHeaders",
		},
		{
			name: "valid RequestBody",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "RequestBody"
`,
			wantType: "RequestBody",
		},
		{
			name: "valid AllRequest",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "AllRequest"
`,
			wantType: "AllRequest",
		},
		{
			name: "valid ResponseHeaders",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "ResponseHeaders"
`,
			wantType: "ResponseHeaders",
		},
		{
			name: "valid ResponseBody",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "ResponseBody"
`,
			wantType: "ResponseBody",
		},
		{
			name: "valid AllResponse",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "AllResponse"
`,
			wantType: "AllResponse",
		},
		{
			name: "valid Everything",
			config: `---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "Everything"
`,
			wantType: "Everything",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer Clean()

			err = initialize([]byte(tt.config))
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

func TestInvalidLogging(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		cleanup func(t *testing.T)
		wantErr bool
	}{
		{
			name: "invalid log level",
			config: `---
loglevel: INVALIDLOGLEVEL
logpath: /dev/null
`,
			wantErr: true,
		},
		{
			name: "writable log path",
			config: `---
loglevel: ERROR
logpath: ./configstore_test.log`,
			cleanup: func(t *testing.T) {
				if _, err := os.Stat("./configstore_test.log"); err == nil {
					if err := os.Remove("./configstore_test.log"); err != nil {
						t.Errorf("could not remove ./configstore_test.log")
					}
				}
			},
			wantErr: false,
		},
		{
			name: "inaccessible log path",
			config: `---
loglevel: ERROR
logpath: /usr/configstore_test.log`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer Clean()
			if tt.cleanup != nil {
				defer tt.cleanup(t)
			}

			err = initialize([]byte(tt.config))
			if (err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("expected error but got none")
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestNewGetCleanLifecycle(t *testing.T) {
	cs1, err := New()
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	cs2, err := Get()
	if err != nil {
		t.Fatalf("Get() after New() failed: %v", err)
	}
	if cs1 != cs2 {
		t.Errorf("Get() returned a different instance than New()")
	}

	Clean()

	_, err = Get()
	if err == nil {
		t.Errorf("Get() after Clean() should return error")
	}
}

func TestNewDuplicate(t *testing.T) {
	_, err := New()
	if err != nil {
		t.Fatalf("first New() failed: %v", err)
	}
	defer Clean()

	_, err = New()
	if err == nil {
		t.Errorf("second New() should return error when instance already exists")
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
		mode      string
		wantAsync bool
	}{
		{"sync mode", "sync", false},
		{"async mode", "async", true},
		{"empty mode defaults to sync", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer Clean()

			config := fmt.Sprintf(`---
loglevel: ERROR
logpath: /dev/null
modelplugins:
  - id: "testplugin"
    path: "../testdata/plugins/model/trivial.so"
    plugintype: "RequestHeaders"
    mode: "%s"
`, tt.mode)
			if err := initialize([]byte(config)); err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if got := cs.IsAsync("testplugin"); got != tt.wantAsync {
				t.Errorf("IsAsync with mode %q = %v, want %v", tt.mode, got, tt.wantAsync)
			}
		})
	}
}

func TestGetBeforeNew(t *testing.T) {
	// ensure clean state
	Clean()

	_, err := Get()
	if err == nil {
		t.Error("Get() before New() should return error")
	}
}


func TestNatsURL(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name: "defaults to localhost:4222",
			config: `---
loglevel: ERROR
logpath: /dev/null
`,
			wantURL: "localhost:4222",
		},
		{
			name: "stores custom URL",
			config: `---
loglevel: ERROR
logpath: /dev/null
natsurl: "nats.example.com:4222"
`,
			wantURL: "nats.example.com:4222",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs, err := New()
			if err != nil {
				t.Fatal(err)
			}
			defer Clean()

			if err := initialize([]byte(tt.config)); err != nil {
				t.Fatalf("initialize failed: %v", err)
			}

			if cs.NatsURL != tt.wantURL {
				t.Errorf("NatsURL = %q, want %q", cs.NatsURL, tt.wantURL)
			}
		})
	}
}

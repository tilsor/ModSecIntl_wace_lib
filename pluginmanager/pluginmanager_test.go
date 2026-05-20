package pluginmanager

import (
	"math/rand"
	"testing"
	"time"

	"github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/sdk/metric"
	"gopkg.in/yaml.v3"
)

var baseConfig = `---
logpath: "/dev/null"
loglevel: "WARN"
`

var trivialPlugin = `  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var trivial2Plugin = `  - id: "trivial2"
    path: "../testdata/plugins/model/trivial2.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var errorReqPlugin = `  - id: "error_req"
    path: "../testdata/plugins/model/error_req.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var testPlugin = `  - id: "test"
    path: "../testdata/plugins/decision/test.so"
    wafweight: 0.5
    decisionbalance: 0.5
`

var simplePlugin = `  - id: "simple"
    path: "../testdata/plugins/decision/simple.so"
    decisionbalance: 0.5
`

// Model plugins that fail to load (silently dropped by pluginmanager)
var noInitPlugin = `  - id: "no_init"
    path: "../testdata/plugins/model/no_init.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var wrongInitPlugin = `  - id: "wrong_init"
    path: "../testdata/plugins/model/wrong_init.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var errorInitPlugin = `  - id: "error_init"
    path: "../testdata/plugins/model/error_init.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var noReqPlugin = `  - id: "no_req"
    path: "../testdata/plugins/model/no_req.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

var wrongReqPlugin = `  - id: "wrong_req"
    path: "../testdata/plugins/model/wrong_req.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
`

// paramPlugin returns whatever float64 is stored in params["result"].
// Its ReloadPlugin updates that value, so the output changes after a Reload.
var paramPlugin = `  - id: "param"
    path: "../testdata/plugins/model/param.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
    params:
      result: "0.3"
`

// Decision plugins that fail to load (wrong InitPlugin signature)
var noCheckPlugin = `  - id: "no_check"
    path: "../testdata/plugins/decision/no_check.so"
    decisionbalance: 0.5
`

var wrongCheckPlugin = `  - id: "wrong_check"
    path: "../testdata/plugins/decision/wrong_check.so"
    decisionbalance: 0.5
`

var provider = metric.NewMeterProvider()
var testMeter = provider.Meter("pluginmanager-test-meter")

func init() {
	rand.Seed(time.Now().UnixNano())
	logger := logging.Get()
	if err := logger.LoadLogger("/dev/null", logging.ERROR); err != nil {
		panic("Error loading logger: " + err.Error())
	}
}

func generateRandomID() string {
	letters := "1234567890ABCDEF"
	id := make([]byte, 16)
	for i := range id {
		id[i] = letters[rand.Intn(len(letters))]
	}
	return string(id)
}

// setupPluginManager creates a fresh ConfigStore from the given YAML config,
// returns an initialised PluginManager, and registers configstore.Clean as a
// test cleanup function.
func setupPluginManager(t *testing.T, configuration []byte) *PluginManager {
	t.Helper()
	configstore.Clean()
	cs, err := configstore.New()
	if err != nil {
		t.Fatalf("configstore.New() failed: %v", err)
	}
	t.Cleanup(configstore.Clean)

	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal(configuration, &aux); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v", err)
	}
	if err := cs.SetConfig(aux); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	logger := logging.Get()
	if err := logger.LoadLogger(cs.LogPath, cs.LogLevel); err != nil {
		t.Fatalf("LoadLogger failed: %v", err)
	}
	pm, err := New(testMeter)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	return pm
}

func TestPluginManagerNew(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin + "decisionplugins:\n" + testPlugin)
	pm := setupPluginManager(t, config)
	if pm == nil {
		t.Fatal("New() returned nil plugin manager")
	}
}

func TestPluginManagerProcessSync(t *testing.T) {
	tests := []struct {
		name      string
		modelConf string
		modelID   string
		wantProb  float64
		wantErr   bool
	}{
		{
			name:      "trivial returns zero probability",
			modelConf: trivialPlugin,
			modelID:   "trivial",
			wantProb:  0.0,
		},
		{
			name:      "trivial2 returns full attack probability",
			modelConf: trivial2Plugin,
			modelID:   "trivial2",
			wantProb:  1.0,
		},
		{
			name:      "error_req plugin reports error via channel",
			modelConf: errorReqPlugin,
			modelID:   "error_req",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := []byte(baseConfig + "modelplugins:\n" + tt.modelConf)
			pm := setupPluginManager(t, config)

			txID := generateRandomID()
			pm.InitTransaction(txID)
			defer pm.CloseTransaction(txID)

			ch := make(chan ModelStatus, 1)
			go pm.Process(tt.modelID, txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
			status := <-ch

			if (status.Err != nil) != tt.wantErr {
				if tt.wantErr {
					t.Errorf("Process(%q) expected error but got none", tt.modelID)
				} else {
					t.Errorf("Process(%q) unexpected error: %v", tt.modelID, status.Err)
				}
			}
			if !tt.wantErr && status.ProbAttack != tt.wantProb {
				t.Errorf("Process(%q) ProbAttack = %f, want %f", tt.modelID, status.ProbAttack, tt.wantProb)
			}
		})
	}
}

func TestPluginManagerProcessNonexistentPlugin(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin)
	pm := setupPluginManager(t, config)

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	ch := make(chan ModelStatus, 1)
	go pm.Process("nonexistent", txID, waceapi.HTTPPayload{}, configstore.Everything, ch)
	status := <-ch
	if status.Err == nil {
		t.Error("Process with nonexistent plugin ID should return error via channel")
	}
}

func TestPluginManagerCheckResult(t *testing.T) {
	tests := []struct {
		name      string
		modelConf string
		modelID   string
		wafParams map[string]string
		wantBlock bool
	}{
		{
			name:      "trivial (prob=0.0) does not block even with alerting WAF",
			modelConf: trivialPlugin,
			modelID:   "trivial",
			wafParams: map[string]string{"inbound_blocking": "20", "inbound_threshold": "5"},
			wantBlock: false,
		},
		{
			name:      "trivial2 (prob=1.0) blocks when WAF also alerts",
			modelConf: trivial2Plugin,
			modelID:   "trivial2",
			wafParams: map[string]string{"inbound_blocking": "20", "inbound_threshold": "5"},
			wantBlock: true,
		},
		{
			name:      "trivial2 does not block with empty WAF data",
			modelConf: trivial2Plugin,
			modelID:   "trivial2",
			wafParams: make(map[string]string),
			wantBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := []byte(baseConfig + "modelplugins:\n" + tt.modelConf + "decisionplugins:\n" + simplePlugin)
			pm := setupPluginManager(t, config)

			txID := generateRandomID()
			pm.InitTransaction(txID)
			defer pm.CloseTransaction(txID)

			ch := make(chan ModelStatus, 1)
			go pm.Process(tt.modelID, txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
			<-ch

			result, err := pm.CheckResult(txID, "simple", tt.wafParams)
			if err != nil {
				t.Fatalf("CheckResult error: %v", err)
			}
			if result != tt.wantBlock {
				t.Errorf("CheckResult = %v, want %v", result, tt.wantBlock)
			}
		})
	}
}

func TestPluginManagerCheckResultNonexistentDecision(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin + "decisionplugins:\n" + testPlugin)
	pm := setupPluginManager(t, config)

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	_, err := pm.CheckResult(txID, "nonexistent", make(map[string]string))
	if err == nil {
		t.Error("CheckResult with nonexistent decision plugin should return error")
	}
}

func TestPluginManagerTransactionLifecycle(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin + "decisionplugins:\n" + testPlugin)
	pm := setupPluginManager(t, config)

	txID := generateRandomID()
	pm.InitTransaction(txID)

	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	status := <-ch
	if status.Err != nil {
		t.Fatalf("Process error: %v", status.Err)
	}

	// test plugin blocks when anomalyscore >= inboundthreshold
	result, err := pm.CheckResult(txID, "test", map[string]string{"anomalyscore": "100", "inboundthreshold": "10"})
	if err != nil {
		t.Fatalf("CheckResult error: %v", err)
	}
	if !result {
		t.Error("expected transaction to be blocked (anomalyscore 100 >= inboundthreshold 10)")
	}

	pm.CloseTransaction(txID)
}

// TestPluginManagerLoadModelFailures checks that New() succeeds even when model
// plugins fail to load (due to missing/wrong Init or Process symbols), and that
// those plugins are not available for processing.
func TestPluginManagerLoadModelFailures(t *testing.T) {
	tests := []struct {
		name      string
		modelConf string
		modelID   string
	}{
		{"no InitPlugin symbol", noInitPlugin, "no_init"},
		{"wrong InitPlugin signature", wrongInitPlugin, "wrong_init"},
		{"InitPlugin returns error", errorInitPlugin, "error_init"},
		{"no Process symbol", noReqPlugin, "no_req"},
		{"wrong Process signature", wrongReqPlugin, "wrong_req"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := []byte(baseConfig + "modelplugins:\n" + tt.modelConf)
			pm := setupPluginManager(t, config)
			if pm == nil {
				t.Fatal("New() returned nil — expected success even with bad plugin")
			}

			// The bad plugin must have been dropped: Process should return an error.
			txID := generateRandomID()
			pm.InitTransaction(txID)
			defer pm.CloseTransaction(txID)

			ch := make(chan ModelStatus, 1)
			go pm.Process(tt.modelID, txID, waceapi.HTTPPayload{}, configstore.Everything, ch)
			status := <-ch
			if status.Err == nil {
				t.Errorf("Process(%q): expected error (plugin should not have been loaded)", tt.modelID)
			}
		})
	}
}

// TestPluginManagerLoadDecisionFailures checks that New() succeeds even when
// decision plugins fail to load (wrong InitPlugin signature), and that those
// plugins are not available for CheckResult.
func TestPluginManagerLoadDecisionFailures(t *testing.T) {
	tests := []struct {
		name       string
		decConf    string
		decisionID string
	}{
		{"no CheckResults symbol", noCheckPlugin, "no_check"},
		{"wrong CheckResults signature", wrongCheckPlugin, "wrong_check"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin + "decisionplugins:\n" + tt.decConf)
			pm := setupPluginManager(t, config)
			if pm == nil {
				t.Fatal("New() returned nil — expected success even with bad decision plugin")
			}

			txID := generateRandomID()
			pm.InitTransaction(txID)
			defer pm.CloseTransaction(txID)

			_, err := pm.CheckResult(txID, tt.decisionID, make(map[string]string))
			if err == nil {
				t.Errorf("CheckResult(%q): expected error (plugin should not have been loaded)", tt.decisionID)
			}
		})
	}
}

// TestPluginManagerReload exercises the already-loaded-plugin branch in
// loadModelPlugins / loadDecisionPlugins (the `found == true` path).
func TestPluginManagerReload(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin + "decisionplugins:\n" + simplePlugin)
	pm := setupPluginManager(t, config)

	if err := pm.Reload(testMeter); err != nil {
		t.Fatalf("Reload() returned error: %v", err)
	}

	// Plugin must still be functional after reload.
	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	status := <-ch
	if status.Err != nil {
		t.Errorf("Process after Reload: unexpected error: %v", status.Err)
	}
	if status.ProbAttack != 0.0 {
		t.Errorf("Process after Reload: ProbAttack = %f, want 0.0", status.ProbAttack)
	}
}

// TestPluginManagerProcessTypeMismatch verifies that Process sends an error
// when the plugin's registered type does not match the requested type.
func TestPluginManagerProcessTypeMismatch(t *testing.T) {
	// Configure trivial as RequestHeaders type.
	conf := baseConfig + `modelplugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    weight: 1
    plugintype: "RequestHeaders"
    mode: sync
`
	pm := setupPluginManager(t, []byte(conf))

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	// Pass Everything — does not match the registered RequestHeaders type.
	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	status := <-ch
	if status.Err == nil {
		t.Error("Process with mismatched plugin type should return error via channel")
	}
}

// TestPluginManagerReloadChangesOutput verifies that after Reload the plugin
// picks up new params and returns a different ProbAttack value.
// param.so is configured with result=0.3; after updating the configstore to
// result=0.8 and calling Reload, Process must return 0.8.
func TestPluginManagerReloadChangesOutput(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + paramPlugin)
	pm := setupPluginManager(t, config)

	runProcess := func(wantProb float64) {
		t.Helper()
		txID := generateRandomID()
		pm.InitTransaction(txID)
		defer pm.CloseTransaction(txID)

		ch := make(chan ModelStatus, 1)
		go pm.Process("param", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
		status := <-ch
		if status.Err != nil {
			t.Errorf("Process: unexpected error: %v", status.Err)
			return
		}
		if status.ProbAttack != wantProb {
			t.Errorf("ProbAttack = %f, want %f", status.ProbAttack, wantProb)
		}
	}

	runProcess(0.3)

	// Update configstore so Reload picks up the new params.
	updatedConfig := baseConfig + `modelplugins:
  - id: "param"
    path: "../testdata/plugins/model/param.so"
    weight: 1
    plugintype: "Everything"
    mode: sync
    params:
      result: "0.8"
`
	cs, err := configstore.Get()
	if err != nil {
		t.Fatalf("configstore.Get: %v", err)
	}
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal([]byte(updatedConfig), &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := cs.SetConfig(aux); err != nil {
		t.Fatalf("SetConfig with updated params: %v", err)
	}

	if err := pm.Reload(testMeter); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	runProcess(0.8)
}

// TestPluginManagerAddModelChannelAndClose exercises AddModelChannel (sync path)
// and the CloseTransaction sync-cleanup branch, which only runs when
// syncModelsChannels has an entry for the transaction.
func TestPluginManagerAddModelChannelAndClose(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin)
	pm := setupPluginManager(t, config)

	txID := generateRandomID()
	pm.InitTransaction(txID)

	ch := make(chan ModelStatus)
	pm.AddModelChannel(txID, configstore.Everything, ch, "sync")

	// CloseTransaction must close the registered channel and clean up maps.
	pm.CloseTransaction(txID)

	// A closed channel returns immediately with ok=false.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed by CloseTransaction")
		}
	default:
		t.Error("CloseTransaction should have closed the registered channel")
	}
}

// TestPluginManagerProcessAsyncPlugin verifies that Process sends an error when
// the configstore marks the plugin as async, even though it is present in the
// in-process plugin map.
func TestPluginManagerProcessAsyncPlugin(t *testing.T) {
	// Load trivial as sync so it ends up in pm.modelPlugins.
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin)
	pm := setupPluginManager(t, config)

	// Update configstore to mark the plugin as async without reloading pm.
	asyncConf := baseConfig + `modelplugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    weight: 1
    plugintype: "Everything"
    mode: async
`
	cs, err := configstore.Get()
	if err != nil {
		t.Fatalf("configstore.Get: %v", err)
	}
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal([]byte(asyncConf), &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := cs.SetConfig(aux); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	status := <-ch
	if status.Err == nil {
		t.Error("Process on async-configured plugin should return error via channel")
	}
}

// TestPluginManagerCheckResultWithoutTransaction verifies that CheckResult
// returns an error when InitTransaction was never called (results map absent).
func TestPluginManagerCheckResultWithoutTransaction(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin + "decisionplugins:\n" + simplePlugin)
	pm := setupPluginManager(t, config)

	// Deliberately skip pm.InitTransaction — no results entry exists.
	txID := generateRandomID()

	_, err := pm.CheckResult(txID, "simple", make(map[string]string))
	if err == nil {
		t.Error("CheckResult without InitTransaction should return error")
	}
}

// TestPluginManagerProcessWithoutTransaction verifies that Process sends an
// error when the transaction was never initialised (results map is absent).
func TestPluginManagerProcessWithoutTransaction(t *testing.T) {
	config := []byte(baseConfig + "modelplugins:\n" + trivialPlugin)
	pm := setupPluginManager(t, config)

	// Deliberately skip pm.InitTransaction so there is no results entry.
	txID := generateRandomID()

	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	status := <-ch
	if status.Err == nil {
		t.Error("Process without InitTransaction should return error via channel")
	}
}

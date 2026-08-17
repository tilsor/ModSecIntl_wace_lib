package pluginmanager

import (
	"fmt"
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
    plugin_type: "Everything"
    mode: sync
`

var trivial2Plugin = `  - id: "trivial2"
    path: "../testdata/plugins/model/trivial2.so"
    plugin_type: "Everything"
    mode: sync
`

var errorReqPlugin = `  - id: "error_req"
    path: "../testdata/plugins/model/error_req.so"
    plugin_type: "Everything"
    mode: sync
`

var testPlugin = `  - id: "test"
    path: "../testdata/plugins/decision/test.so"
    waf_weight: 0.5
    decisionbalance: 0.5
`

var simplePlugin = `  - id: "simple"
    path: "../testdata/plugins/decision/simple.so"
    model_weights:
      trivial: 1
      trivial2: 1
    decisionbalance: 0.5
`

// Model plugins that fail to load (silently dropped by pluginmanager)
var noInitPlugin = `  - id: "no_init"
    path: "../testdata/plugins/model/no_init.so"
    plugin_type: "Everything"
    mode: sync
`

var wrongInitPlugin = `  - id: "wrong_init"
    path: "../testdata/plugins/model/wrong_init.so"
    plugin_type: "Everything"
    mode: sync
`

var errorInitPlugin = `  - id: "error_init"
    path: "../testdata/plugins/model/error_init.so"
    plugin_type: "Everything"
    mode: sync
`

var noReqPlugin = `  - id: "no_req"
    path: "../testdata/plugins/model/no_req.so"
    plugin_type: "Everything"
    mode: sync
`

var wrongReqPlugin = `  - id: "wrong_req"
    path: "../testdata/plugins/model/wrong_req.so"
    plugin_type: "Everything"
    mode: sync
`

// paramPlugin returns whatever float64 is stored in params["result"].
// Its ReloadPlugin updates that value, so the output changes after a Reload.
var paramPlugin = `  - id: "param"
    path: "../testdata/plugins/model/param.so"
    plugin_type: "Everything"
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
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" + testPlugin)
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
			config := []byte(baseConfig + "model_plugins:\n" + tt.modelConf)
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
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin)
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
		wafParams waceapi.WAFData
		wantBlock bool
	}{
		{
			name:      "trivial (prob=0.0) does not block even with alerting WAF",
			modelConf: trivialPlugin,
			modelID:   "trivial",
			wafParams: waceapi.WAFData{Scores: map[string]float64{"inbound_blocking": 20, "inbound_threshold": 5}},
			wantBlock: false,
		},
		{
			name:      "trivial2 (prob=1.0) blocks when WAF also alerts",
			modelConf: trivial2Plugin,
			modelID:   "trivial2",
			wafParams: waceapi.WAFData{Scores: map[string]float64{"inbound_blocking": 20, "inbound_threshold": 5}},
			wantBlock: true,
		},
		{
			name:      "trivial2 does not block with empty WAF data",
			modelConf: trivial2Plugin,
			modelID:   "trivial2",
			wafParams: waceapi.WAFData{},
			wantBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := []byte(baseConfig + "model_plugins:\n" + tt.modelConf + "decision_plugins:\n" + simplePlugin)
			pm := setupPluginManager(t, config)

			txID := generateRandomID()
			pm.InitTransaction(txID)
			defer pm.CloseTransaction(txID)

			ch := make(chan ModelStatus, 1)
			go pm.Process(tt.modelID, txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
			<-ch

			result, _, err := pm.CheckResult(txID, []string{"simple"}, tt.wafParams)
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
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" + testPlugin)
	pm := setupPluginManager(t, config)

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	_, _, err := pm.CheckResult(txID, []string{"nonexistent"}, waceapi.WAFData{})
	if err == nil {
		t.Error("CheckResult with nonexistent decision plugin should return error")
	}
}

func TestPluginManagerTransactionLifecycle(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" + testPlugin)
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
	result, _, err := pm.CheckResult(txID, []string{"test"}, waceapi.WAFData{Scores: map[string]float64{"anomalyscore": 100, "inboundthreshold": 10}})
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
			config := []byte(baseConfig + "model_plugins:\n" + tt.modelConf)
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
			config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" + tt.decConf)
			pm := setupPluginManager(t, config)
			if pm == nil {
				t.Fatal("New() returned nil — expected success even with bad decision plugin")
			}

			txID := generateRandomID()
			pm.InitTransaction(txID)
			defer pm.CloseTransaction(txID)

			_, _, err := pm.CheckResult(txID, []string{tt.decisionID}, waceapi.WAFData{})
			if err == nil {
				t.Errorf("CheckResult(%q): expected error (plugin should not have been loaded)", tt.decisionID)
			}
		})
	}
}

// TestPluginManagerReload exercises the already-loaded-plugin branch in
// loadModelPlugins / loadDecisionPlugins (the `found == true` path).
func TestPluginManagerReload(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" + simplePlugin)
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
	conf := baseConfig + `model_plugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "RequestHeaders"
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
	config := []byte(baseConfig + "model_plugins:\n" + paramPlugin)
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
	updatedConfig := baseConfig + `model_plugins:
  - id: "param"
    path: "../testdata/plugins/model/param.so"
    plugin_type: "Everything"
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
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin)
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
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin)
	pm := setupPluginManager(t, config)

	// Update configstore to mark the plugin as async without reloading pm.
	asyncConf := baseConfig + `model_plugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "Everything"
    async: true
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
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" + simplePlugin)
	pm := setupPluginManager(t, config)

	// Deliberately skip pm.InitTransaction — no results entry exists.
	txID := generateRandomID()

	_, _, err := pm.CheckResult(txID, []string{"simple"}, waceapi.WAFData{})
	if err == nil {
		t.Error("CheckResult without InitTransaction should return error")
	}
}

var trivialTrainingPlugin = `  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "Everything"
    training: true
    training_data:
      max_samples: 3
      result_file_path: "/dev/null"
`

// TestPluginManagerTrainingPluginLoaded verifies that a training plugin is
// loaded with its channel, context, and cancel function all initialised and
// that the context is live immediately after load.
func TestPluginManagerTrainingPluginLoaded(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialTrainingPlugin)
	pm := setupPluginManager(t, config)

	mp, ok := pm.modelPlugins["trivial"]
	if !ok {
		t.Fatal("training plugin was not loaded into modelPlugins")
	}
	if mp.trainingChannel == nil {
		t.Error("trainingChannel should not be nil for a training plugin")
	}
	if mp.trainingCtx == nil {
		t.Error("trainingCtx should not be nil for a training plugin")
	}
	if mp.trainingCancel == nil {
		t.Error("trainingCancel should not be nil for a training plugin")
	}
	select {
	case <-mp.trainingCtx.Done():
		t.Error("trainingCtx should not be done immediately after load")
	default:
	}
}

// TestPluginManagerProcessTrainingExhaustsMaxSamples sends maxSamples results
// through ProcessTraining and verifies that the goroutine exits (calling
// defer cancel()) once it has consumed all of them.
func TestPluginManagerProcessTrainingExhaustsMaxSamples(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialTrainingPlugin)
	pm := setupPluginManager(t, config)
	mp := pm.modelPlugins["trivial"]

	txID := generateRandomID()

	for i := 0; i < 3; i++ {
		go pm.ProcessTraining("trivial", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything)
	}

	select {
	case <-mp.trainingCtx.Done():
		// goroutine exited after maxSamples and called defer cancel()
	case <-time.After(2 * time.Second):
		t.Error("training goroutine did not exit after exhausting maxSamples")
	}
}

// TestPluginManagerProcessTrainingNonexistent verifies that ProcessTraining
// returns immediately when the model ID does not exist, without blocking.
func TestPluginManagerProcessTrainingNonexistent(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialTrainingPlugin)
	pm := setupPluginManager(t, config)

	done := make(chan struct{})
	go func() {
		pm.ProcessTraining("nonexistent", generateRandomID(), waceapi.HTTPPayload{URI: "/test"}, configstore.Everything)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("ProcessTraining with nonexistent plugin should return immediately")
	}
}

// TestPluginManagerProcessTrainingAfterCancel verifies that ProcessTraining
// does not block when the training context has already been cancelled.
func TestPluginManagerProcessTrainingAfterCancel(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialTrainingPlugin)
	pm := setupPluginManager(t, config)
	mp := pm.modelPlugins["trivial"]

	mp.trainingCancel()

	done := make(chan struct{})
	go func() {
		pm.ProcessTraining("trivial", generateRandomID(), waceapi.HTTPPayload{URI: "/test"}, configstore.Everything)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("ProcessTraining should not block after context cancellation")
	}
}

// TestPluginManagerTrainingReloadDisablesTraining verifies that Reload cancels
// the training goroutine when the new config has training disabled for the plugin.
func TestPluginManagerTrainingReloadDisablesTraining(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialTrainingPlugin)
	pm := setupPluginManager(t, config)
	mp := pm.modelPlugins["trivial"]

	select {
	case <-mp.trainingCtx.Done():
		t.Fatal("trainingCtx should be alive before Reload")
	default:
	}

	disabledConfig := baseConfig + `model_plugins:
  - id: "trivial"
    path: "../testdata/plugins/model/trivial.so"
    plugin_type: "Everything"
`
	cs, err := configstore.Get()
	if err != nil {
		t.Fatalf("configstore.Get: %v", err)
	}
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal([]byte(disabledConfig), &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := cs.SetConfig(aux); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := pm.Reload(testMeter); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	select {
	case <-mp.trainingCtx.Done():
		// goroutine was cancelled by Reload
	case <-time.After(time.Second):
		t.Error("trainingCtx should be cancelled after Reload with training disabled")
	}
}

// TestPluginManagerTrainingResultNotUsedInDecision verifies that a result
// produced by ProcessTraining is stored in the training channel only and never
// reaches p.results, so CheckResult cannot use it to influence a decision.
//
// trivial2 always returns ProbAttack=1.0. If its training result leaked into
// p.results, CheckResult would block the transaction; it must not.
func TestPluginManagerTrainingResultNotUsedInDecision(t *testing.T) {
	conf := baseConfig + `model_plugins:
  - id: "trivial2"
    path: "../testdata/plugins/model/trivial2.so"
    plugin_type: "Everything"
    training: true
    training_data:
      max_samples: 5
      result_file_path: "/dev/null"
decision_plugins:
` + simplePlugin
	pm := setupPluginManager(t, []byte(conf))

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	done := make(chan struct{})
	go func() {
		pm.ProcessTraining("trivial2", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything)
		close(done)
	}()
	<-done // wait until the result has been handed off to the training goroutine

	// WAF params that would cause a block if trivial2's prob=1.0 reached the decision.
	result, _, err := pm.CheckResult(txID, []string{"simple"}, waceapi.WAFData{Scores: map[string]float64{
		"inbound_blocking":  20,
		"inbound_threshold": 5,
	}})
	if err != nil {
		t.Fatalf("CheckResult error: %v", err)
	}
	if result {
		t.Error("CheckResult blocked — training model result must not feed into the decision plugin")
	}
}

// TestPluginManagerProcessWithoutTransaction verifies that Process sends an
// error when the transaction was never initialised (results map is absent).
func TestPluginManagerProcessWithoutTransaction(t *testing.T) {
	config := []byte(baseConfig + "model_plugins:\n" + trivialPlugin)
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

// ── Decision plugin training ─────────────────────────────────────────────────

// decisionTrainingConf builds a training decision plugin config entry using
// simple.so, collecting samples to resultPath (and status to statusPath). It
// weighs trivial2 so the shadow plugin would compute Block=true on its own.
func decisionTrainingConf(id, resultPath, statusPath string, maxSamples int) string {
	return fmt.Sprintf(`  - id: %q
    path: "../testdata/plugins/decision/simple.so"
    model_weights:
      trivial2: 1
    training: true
    training_data:
      max_samples: %d
      result_file_path: %q
      status_file_path: %q
`, id, maxSamples, resultPath, statusPath)
}

// TestPluginManagerDecisionTrainingPluginLoaded verifies that a decision plugin
// marked training:true is loaded with its channel, context, and cancel function
// initialised, and that the context is live right after load.
func TestPluginManagerDecisionTrainingPluginLoaded(t *testing.T) {
	conf := baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" +
		decisionTrainingConf("simple_training", "/dev/null", "", 3)
	pm := setupPluginManager(t, []byte(conf))

	dp, ok := pm.decisionPlugins["simple_training"]
	if !ok {
		t.Fatal("training decision plugin was not loaded into decisionPlugins")
	}
	if dp.trainingChannel == nil {
		t.Error("trainingChannel should not be nil for a training decision plugin")
	}
	if dp.trainingCtx == nil {
		t.Error("trainingCtx should not be nil for a training decision plugin")
	}
	if dp.trainingCancel == nil {
		t.Error("trainingCancel should not be nil for a training decision plugin")
	}
	select {
	case <-dp.trainingCtx.Done():
		t.Error("trainingCtx should not be done immediately after load")
	default:
	}
}

// TestPluginManagerDecisionTrainingCollectsAlongsideProduction verifies that
// with one production plugin and one training plugin in the list, the block
// decision comes from the production plugin while the training plugin collects
// a sample off the request path.
func TestPluginManagerDecisionTrainingCollectsAlongsideProduction(t *testing.T) {
	dir := t.TempDir()
	resultPath := dir + "/decision.ndjson"
	statusPath := dir + "/decision.status"

	conf := baseConfig + "model_plugins:\n" + trivial2Plugin + "decision_plugins:\n" +
		simplePlugin + decisionTrainingConf("simple_training", resultPath, statusPath, 1)
	pm := setupPluginManager(t, []byte(conf))
	dp := pm.decisionPlugins["simple_training"]

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial2", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	<-ch

	waf := waceapi.WAFData{Scores: map[string]float64{"inbound_blocking": 20, "inbound_threshold": 5}}
	block, enabled, err := pm.CheckResult(txID, []string{"simple", "simple_training"}, waf)
	if err != nil {
		t.Fatalf("CheckResult error: %v", err)
	}
	if !enabled {
		t.Error("enabledPluginFound should be true when a production plugin is present")
	}
	if !block {
		t.Error("production plugin should have blocked (trivial2 prob=1.0 + alerting WAF)")
	}

	// The training goroutine exits (defer cancel) once it collects max_samples=1.
	select {
	case <-dp.trainingCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("training collection did not complete")
	}
	if n := countFileLines(t, resultPath); n != 1 {
		t.Errorf("collected %d samples, want 1", n)
	}
}

// TestPluginManagerDecisionTrainingOnlyDoesNotBlock verifies that a call with
// only training plugins (no production plugin) never blocks — even though the
// shadow plugin's own CheckResults would return Block=true — and still collects
// a sample.
func TestPluginManagerDecisionTrainingOnlyDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	resultPath := dir + "/decision.ndjson"
	statusPath := dir + "/decision.status"

	conf := baseConfig + "model_plugins:\n" + trivial2Plugin + "decision_plugins:\n" +
		decisionTrainingConf("simple_training", resultPath, statusPath, 1)
	pm := setupPluginManager(t, []byte(conf))
	dp := pm.decisionPlugins["simple_training"]

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	ch := make(chan ModelStatus, 1)
	go pm.Process("trivial2", txID, waceapi.HTTPPayload{URI: "/test"}, configstore.Everything, ch)
	<-ch

	waf := waceapi.WAFData{Scores: map[string]float64{"inbound_blocking": 20, "inbound_threshold": 5}}
	block, enabled, err := pm.CheckResult(txID, []string{"simple_training"}, waf)
	if err != nil {
		t.Fatalf("CheckResult error: %v", err)
	}
	if enabled {
		t.Error("enabledPluginFound should be false for a training-only call")
	}
	if block {
		t.Error("training-only call must never block, regardless of the shadow plugin's verdict")
	}

	select {
	case <-dp.trainingCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("training collection did not complete")
	}
	if n := countFileLines(t, resultPath); n != 1 {
		t.Errorf("collected %d samples, want 1", n)
	}
}

// TestPluginManagerMultipleProductionDecisionPlugins verifies that a list with
// more than one production (non-training) decision plugin is rejected.
func TestPluginManagerMultipleProductionDecisionPlugins(t *testing.T) {
	conf := baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" +
		simplePlugin + testPlugin
	pm := setupPluginManager(t, []byte(conf))

	txID := generateRandomID()
	pm.InitTransaction(txID)
	defer pm.CloseTransaction(txID)

	_, _, err := pm.CheckResult(txID, []string{"simple", "test"}, waceapi.WAFData{})
	if err == nil {
		t.Error("CheckResult with two production decision plugins should return an error")
	}
}

// TestPluginManagerDecisionTrainingReloadDisables verifies that Reload cancels
// the decision training goroutine when the new config disables training.
func TestPluginManagerDecisionTrainingReloadDisables(t *testing.T) {
	conf := baseConfig + "model_plugins:\n" + trivialPlugin + "decision_plugins:\n" +
		decisionTrainingConf("simple_training", "/dev/null", "", 3)
	pm := setupPluginManager(t, []byte(conf))
	dp := pm.decisionPlugins["simple_training"]

	select {
	case <-dp.trainingCtx.Done():
		t.Fatal("trainingCtx should be alive before Reload")
	default:
	}

	disabledConfig := baseConfig + "model_plugins:\n" + trivialPlugin + `decision_plugins:
  - id: "simple_training"
    path: "../testdata/plugins/decision/simple.so"
`
	cs, err := configstore.Get()
	if err != nil {
		t.Fatalf("configstore.Get: %v", err)
	}
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal([]byte(disabledConfig), &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := cs.SetConfig(aux); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := pm.Reload(testMeter); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	select {
	case <-dp.trainingCtx.Done():
	case <-time.After(time.Second):
		t.Error("trainingCtx should be cancelled after Reload with training disabled")
	}
}

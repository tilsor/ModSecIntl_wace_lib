package wace

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/sdk/metric"

	"gopkg.in/yaml.v3"
)

var requestURI = "/cgi-bin/process.cgi"
var requestMethod = "POST"
var requestVersion = "HTTP/1.1"

// var requestLine = "POST /cgi-bin/process.cgi HTTP/1.1\n"

var requestHeaders = []waceapi.HTTPHeader{
	{Key: "User-Agent", Value: "Mozilla/4.0 (compatible; MSIE5.01; Windows NT)"},
	{Key: "Host", Value: "www.tutorialspoint.com"},
	{Key: "Content-Type", Value: "application/x-www-form-urlencoded"},
	{Key: "Content-Length", Value: "length"},
	{Key: "Accept-Language", Value: "en-us"},
	{Key: "Accept-Encoding", Value: "gzip, deflate"},
	{Key: "Connection", Value: "Keep-Alive"},
}

var requestHeadersPayload = waceapi.HTTPPayload{
	URI:            requestURI,
	Method:         requestMethod,
	HTTPVersion:    requestVersion,
	RequestHeaders: requestHeaders,
}

var requestBody = "licenseID=string&content=string&/paramsXML=string\n"
var wholeRequest = waceapi.HTTPPayload{
	URI:         requestURI,
	Method:      requestMethod,
	HTTPVersion: requestVersion,
	RequestBody: requestBody,
}

// var wholeRequest = requestLine + requestHeaders + "\n" + requestBody
var responseCode = 200
var responseProto = "HTTP/1.1"
var responseHeaders = []waceapi.HTTPHeader{
	{Key: "Date", Value: "Mon, 27 Jul 2009 12:28:53 GMT"},
	{Key: "Server", Value: "Apache/2.2.14 (Win32)"},
	{Key: "Last-Modified", Value: "Wed, 22 Jul 2009 19:15:56 GMT"},
	{Key: "Content-Length", Value: "88"},
	{Key: "Content-Type", Value: "text/html"},
	{Key: "Connection", Value: "Closed"},
}

var responseHeadersPayload = waceapi.HTTPPayload{
	ResponseProtocol: responseProto,
	ResponseCode:     responseCode,
	ResponseHeaders:  responseHeaders,
}

var responseBody = `<html>
<body>
<h1>Hello, World!</h1>
</body>
</html>
`

var wholeResponse = waceapi.HTTPPayload{
	ResponseProtocol: responseProto,
	ResponseCode:     responseCode,
	ResponseHeaders:  responseHeaders,
	ResponseBody:     responseBody,
}

var config = []byte(`---
model_plugins:
  - id: "trivial"
    path: "testdata/plugins/model/trivial.so"
    params:
      d: "sds"
      b: "dnid"
      e: "dofnno"
    # plugin_type: "RequestHeaders"
    plugin_type: "Everything"
  - id: "trivial2"
    path: "testdata/plugins/model/trivial2.so"
    params:
      a: "sdsds"
      b: "sdfjdnid"
      c: "kfoskdofnno"
    plugin_type: "Everything"
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
    waf_weight: 0.5
    decisionbalance: 0.5
`)

var configAllModels = []byte(`---
#The level of debug, the valid options are - ERRO, WARN, INFO, DEBUG

#The model plugins configuration
model_plugins:
  - id: "trivialRequestHeaders"
    plugin_type: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    mode: sync
  - id: "trivialRequestBody"
    plugin_type: RequestBody
    path: "testdata/plugins/model/trivial.so"
    mode: sync
  - id: "trivialAllRequest"
    plugin_type: AllRequest
    path: "testdata/plugins/model/trivial.so"
    mode: sync
  - id: "trivialResponseHeaders"
    plugin_type: ResponseHeaders
    path: "testdata/plugins/model/trivial.so"
    mode: sync
  - id: "trivialResponseBody"
    plugin_type: ResponseBody
    path: "testdata/plugins/model/trivial.so"
    mode: sync
  - id: "trivialAllResponse"
    plugin_type: AllResponse
    path: "testdata/plugins/model/trivial.so"
    mode: sync

#The decision plugin configuration
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    waf_weight: 0.5
    decisionbalance: 0.1
`)

var configSyncNoRemote = []byte(`---
#The level of debug, the valid options are - ERRO, WARN, INFO, DEBUG

#The model plugins configuration
model_plugins:
  - id: "trivial"
    plugin_type: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    mode: sync
  - id: "trivial2"
    plugin_type: RequestHeaders
    path: "testdata/plugins/model/trivial2.so"
    mode: sync

#The decision plugin configuration
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    waf_weight: 0.5
    model_weights:
      trivial: 1
      trivial2: 1
    decisionbalance: 0.1
`)

// configAsync is completed with the nats_url line by asyncConfig.
var configAsync = `
#The model plugins configuration
model_plugins:
  - id: "trivial"
    plugin_type: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    async: true
  - id: "trivial2"
    plugin_type: RequestHeaders
    path: "testdata/plugins/model/trivial2.so"
    async: true
#The decision plugin configuration
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    waf_weight: 0.5
    decisionbalance: 0.1
`

var provider = metric.NewMeterProvider()
var testMeter = provider.Meter("example-meter")
var discardLogger = slog.New(slog.DiscardHandler)

func initialize(configuration []byte) error {
	var aux configstore.ConfigFileData
	err := yaml.Unmarshal(configuration, &aux)
	if err != nil {
		return err
	}
	err = Init(testMeter, aux, discardLogger)
	if err != nil {
		return err
	}
	return nil
}

func generateRandomID() string {
	letters := "1234567890ABCDEF"
	id := ""
	for i := 0; i < 16; i++ {
		id += string(letters[rand.Intn(len(letters))])
	}

	return id
}

func TestAnalyze(t *testing.T) {
	type step struct {
		payloadType string
		payload     waceapi.HTTPPayload
		plugins     []string
	}
	tests := []struct {
		name   string
		config []byte
		steps  []step
	}{
		{
			name:   "request in parts",
			config: configAllModels,
			steps: []step{
				{"RequestHeaders", requestHeadersPayload, []string{"trivialRequestHeaders"}},
				{"RequestBody", waceapi.HTTPPayload{RequestBody: requestBody}, []string{"trivialRequestBody"}},
			},
		},
		{
			name:   "whole request",
			config: configAllModels,
			steps: []step{
				{"AllRequest", wholeRequest, []string{"trivialAllRequest"}},
			},
		},
		{
			name:   "response in parts",
			config: configAllModels,
			steps: []step{
				{"ResponseHeaders", responseHeadersPayload, []string{"trivialResponseHeaders"}},
				{"ResponseBody", waceapi.HTTPPayload{ResponseBody: responseBody}, []string{"trivialResponseBody"}},
			},
		},
		{
			name:   "whole response",
			config: configAllModels,
			steps: []step{
				{"AllResponse", wholeResponse, []string{"trivialAllResponse"}},
			},
		},
		{
			name:   "empty models list is a no-op",
			config: configAllModels,
			steps:  []step{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := initialize(tt.config)
			defer configstore.Clean()
			if err != nil {
				t.Fatalf("Error initing test: %v", err)
			}

			transactionID := generateRandomID()
			InitTransaction(transactionID)

			for _, s := range tt.steps {
				if err := Analyze(s.payloadType, transactionID, s.payload, s.plugins); err != nil {
					t.Errorf("Analyze %s: %v", s.payloadType, err)
				}
			}

			_, _, err = CheckTransaction(transactionID, []string{"simple"}, waceapi.WAFData{})
			if err != nil {
				t.Errorf("CheckTransaction: %v", err)
			}

			CloseTransaction(transactionID)
		})
	}
}

func TestCheckInvalidTransaction(t *testing.T) {
	_, _, err := CheckTransaction("INEXISTENT", []string{"simple"}, waceapi.WAFData{})
	if err == nil {
		t.Errorf("Error: CheckTransaction with inexistent transaction does not rise an error")
	}
}

func TestCheckAttackTransaction(t *testing.T) {
	err := initialize(configSyncNoRemote)
	defer configstore.Clean()
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()

	InitTransaction(transactionID)

	wafParams := parseWAFParams("COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=20,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2")

	err = Analyze("RequestHeaders", transactionID, requestHeadersPayload, []string{"trivial", "trivial2", "trivial3"})
	if err != nil {
		t.Errorf("Error: Analyze RequestHeaders: %s", err.Error())
	}

	res, _, err := CheckTransaction(transactionID, []string{"simple"}, wafParams)
	if err != nil {
		t.Errorf("Error: CheckTransaction: %s", err.Error())
	}
	if !res {
		t.Errorf("Error: CheckTransaction: transaction should be blocked")
	}

	CloseTransaction(transactionID)
}

func TestAnalyzeInvalidType(t *testing.T) {
	err := initialize(configAllModels)
	defer configstore.Clean()
	if err != nil {
		t.Fatalf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()
	InitTransaction(transactionID)
	defer CloseTransaction(transactionID)

	err = Analyze("InvalidType", transactionID, requestHeadersPayload, []string{"trivialRequestHeaders"})
	if err == nil {
		t.Errorf("Analyze with invalid type should return error")
	}
}

// TestInitDuplicate covers the already-initialized branch in Init: calling
// Init a second time without Clean in between must return an error.
func TestInitDuplicate(t *testing.T) {
	err := initialize(config)
	if err != nil {
		t.Fatalf("first initialize: %v", err)
	}
	defer configstore.Clean()

	err = initialize(config)
	if err == nil {
		t.Error("second Init without Clean should return error")
	}
}

// TestReloadBeforeInit checks that Reload without a loaded configuration
// returns an error instead of reloading a plugin manager that may not
// exist, and does not publish the given configuration.
func TestReloadBeforeInit(t *testing.T) {
	configstore.Clean()
	defer configstore.Clean()
	var conf configstore.ConfigFileData
	if err := yaml.Unmarshal(config, &conf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}

	if err := Reload(testMeter, conf, discardLogger); err == nil {
		t.Error("Reload before Init should return error")
	}
	if _, err := configstore.Get(); err == nil {
		t.Error("Reload before Init published a configuration")
	}
}

// TestInitInvalidConfig covers the SetConfig error branch in Init: a config
// referencing a nonexistent plugin path must cause Init to return an error.
func TestInitInvalidConfig(t *testing.T) {
	badConfig := []byte(`---
model_plugins:
  - id: "missing"
    path: "testdata/plugins/model/does_not_exist.so"
    plugin_type: "Everything"
`)
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal(badConfig, &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	err := Init(testMeter, aux, discardLogger)
	configstore.Clean()
	if err == nil {
		t.Error("Init with nonexistent plugin path should return error")
	}
}

func TestCloseNonexistentTransaction(t *testing.T) {
	err := initialize(configAllModels)
	defer configstore.Clean()
	if err != nil {
		t.Fatalf("Error initing test: %v", err)
	}

	// should log an error but not panic
	CloseTransaction("NONEXISTENT")
}

func TestCheckNonexistentDecisionPlugin(t *testing.T) {
	err := initialize(configAllModels)
	defer configstore.Clean()
	if err != nil {
		t.Fatalf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()
	InitTransaction(transactionID)
	defer CloseTransaction(transactionID)

	_, _, err = CheckTransaction(transactionID, []string{"nonexistent_plugin"}, waceapi.WAFData{})
	if err == nil {
		t.Errorf("CheckTransaction with nonexistent decision plugin should return error")
	}
}

// parseWAFParams parses a comma-separated "key=value" string into WAFData,
// keeping only entries whose value parses as a float64 score.
func parseWAFParams(s string) waceapi.WAFData {
	scores := make(map[string]float64)
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
				scores[parts[0]] = v
			}
		}
	}
	return waceapi.WAFData{Scores: scores}
}

func TestCheckTransactionResult(t *testing.T) {
	blockingWAF := parseWAFParams("inbound_blocking=20,inbound_threshold=5")
	noAlertWAF := parseWAFParams("inbound_blocking=0,inbound_threshold=5")

	tests := []struct {
		name      string
		config    []byte
		models    []string
		wafParams waceapi.WAFData
		wantBlock bool
	}{
		{
			name:      "trivial2 (prob=1.0) with alerting WAF blocks",
			config:    configSyncNoRemote,
			models:    []string{"trivial2"},
			wafParams: blockingWAF,
			wantBlock: true,
		},
		{
			name:      "trivial (prob=0.0) with alerting WAF does not block",
			config:    configSyncNoRemote,
			models:    []string{"trivial"},
			wafParams: blockingWAF,
			wantBlock: false,
		},
		{
			name:      "trivial2 with non-alerting WAF does not block",
			config:    configSyncNoRemote,
			models:    []string{"trivial2"},
			wafParams: noAlertWAF,
			wantBlock: false,
		},
		{
			name:      "empty models list never blocks",
			config:    configSyncNoRemote,
			models:    []string{},
			wafParams: blockingWAF,
			wantBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := initialize(tt.config)
			defer configstore.Clean()
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}

			txID := generateRandomID()
			InitTransaction(txID)
			defer CloseTransaction(txID)

			if len(tt.models) > 0 {
				if err := Analyze("RequestHeaders", txID, requestHeadersPayload, tt.models); err != nil {
					t.Fatalf("Analyze: %v", err)
				}
			}

			blocked, _, err := CheckTransaction(txID, []string{"simple"}, tt.wafParams)
			if err != nil {
				t.Fatalf("CheckTransaction: %v", err)
			}
			if blocked != tt.wantBlock {
				t.Errorf("blocked = %v, want %v", blocked, tt.wantBlock)
			}
		})
	}
}

func TestAnalyzeMultiPhase(t *testing.T) {
	err := initialize(configAllModels)
	defer configstore.Clean()
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	txID := generateRandomID()
	InitTransaction(txID)
	defer CloseTransaction(txID)

	phases := []struct {
		payloadType string
		payload     waceapi.HTTPPayload
		models      []string
	}{
		{"RequestHeaders", requestHeadersPayload, []string{"trivialRequestHeaders"}},
		{"RequestBody", waceapi.HTTPPayload{RequestBody: requestBody}, []string{"trivialRequestBody"}},
		{"ResponseHeaders", responseHeadersPayload, []string{"trivialResponseHeaders"}},
		{"ResponseBody", waceapi.HTTPPayload{ResponseBody: responseBody}, []string{"trivialResponseBody"}},
	}

	for _, p := range phases {
		if err := Analyze(p.payloadType, txID, p.payload, p.models); err != nil {
			t.Errorf("Analyze(%s): %v", p.payloadType, err)
		}
	}

	_, _, err = CheckTransaction(txID, []string{"simple"}, waceapi.WAFData{})
	if err != nil {
		t.Errorf("CheckTransaction after multi-phase analysis: %v", err)
	}
}

func TestConcurrentTransactions(t *testing.T) {
	err := initialize(configSyncNoRemote)
	defer configstore.Clean()
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	wafParams := parseWAFParams("inbound_blocking=20,inbound_threshold=5")

	const goroutines = 20
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			txID := generateRandomID()
			InitTransaction(txID)

			if err := Analyze("RequestHeaders", txID, requestHeadersPayload, []string{"trivial", "trivial2"}); err != nil {
				errs <- fmt.Errorf("Analyze: %w", err)
				CloseTransaction(txID)
				return
			}

			if _, _, err := CheckTransaction(txID, []string{"simple"}, wafParams); err != nil {
				errs <- fmt.Errorf("CheckTransaction: %w", err)
				CloseTransaction(txID)
				return
			}

			CloseTransaction(txID)
			errs <- nil
		}()
	}

	for i := 0; i < goroutines; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent transaction error: %v", err)
		}
	}
}

// configParamWith returns a YAML config using param.so with the given result value.
func configParamWith(result string) []byte {
	return []byte(`---
model_plugins:
  - id: "param"
    path: "testdata/plugins/model/param.so"
    plugin_type: "Everything"
    mode: sync
    params:
      result: "` + result + `"
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
    decisionbalance: 0.5
`)
}

// TestReload verifies that Reload succeeds and that transactions still work
// correctly after it.
// TestReloadInvalidConfigKeepsLogger checks that a rejected Reload does
// not replace the logger.
func TestReloadInvalidConfigKeepsLogger(t *testing.T) {
	if err := initialize(configParamWith("0.3")); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer configstore.Clean()

	var badConf configstore.ConfigFileData
	if err := yaml.Unmarshal([]byte(`---
model_plugins:
  - id: "missing"
    path: "testdata/plugins/model/does_not_exist.so"
    plugin_type: "Everything"
`), &badConf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	before := coreLogger.Load()
	var out strings.Builder
	if err := Reload(testMeter, badConf, slog.New(slog.NewJSONHandler(&out, nil))); err == nil {
		t.Fatal("Reload with nonexistent plugin path should return error")
	}
	if coreLogger.Load() != before {
		t.Error("rejected Reload replaced the core logger")
	}
	// logged by the core: the transaction does not exist
	CloseTransaction(generateRandomID())
	if out.Len() != 0 {
		t.Errorf("new logger received records after a rejected Reload:\n%s", out.String())
	}
}

func TestReload(t *testing.T) {
	if err := initialize(configParamWith("0.3")); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer configstore.Clean()

	var newConf configstore.ConfigFileData
	if err := yaml.Unmarshal(configParamWith("0.8"), &newConf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := Reload(testMeter, newConf, discardLogger); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Transactions must still complete successfully after a reload.
	txID := generateRandomID()
	InitTransaction(txID)
	defer CloseTransaction(txID)
	if err := Analyze("Everything", txID, waceapi.HTTPPayload{URI: "/test"}, []string{"param"}); err != nil {
		t.Fatalf("Analyze after Reload: %v", err)
	}
	if _, _, err := CheckTransaction(txID, []string{"simple"}, waceapi.WAFData{}); err != nil {
		t.Fatalf("CheckTransaction after Reload: %v", err)
	}
}

func BenchmarkTrivial(b *testing.B) {
	err := initialize(configSyncNoRemote)
	defer configstore.Clean()
	if err != nil {
		b.Errorf("Error initing test: %v", err)
	}

	wafParams := parseWAFParams("COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=0,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2")
	for i := 0; i < b.N; i++ {
		transactionId := strconv.Itoa(i)
		InitTransaction(transactionId)

		Analyze("RequestHeaders", transactionId, waceapi.HTTPPayload{URI: "Request line and headers\n"}, []string{"trivial", "trivial2"})

		_, _, err := CheckTransaction(transactionId, []string{"simple"}, wafParams)
		if err != nil {
			b.Errorf("Error checking transaction: %v", err)
		}
		CloseTransaction(transactionId)
	}
}

// TestCloseTransactionWaitsForPendingAnalysis drives the same internal sequence callPlugins follows: load the
// transactionSync from analysisMap, then (after doing its work) signal
// completion on it. The load happens up front and completion is signalled
// from a delayed goroutine, reproducing the race window between those two
// steps where a concurrent CloseTransaction used to tear things down early.
// With the fix, CloseTransaction must block until that signal arrives
// instead of racing ahead.
func TestCloseTransactionWaitsForPendingAnalysis(t *testing.T) {
	err := initialize(configAllModels)
	defer configstore.Clean()
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}

	txID := generateRandomID()
	InitTransaction(txID)
	addTransactionAnalysis(txID)

	value, ok := analysisMap.Load(txID)
	if !ok {
		t.Fatal("transaction missing right after InitTransaction/addTransactionAnalysis")
	}
	tSync := value.(*transactionSync)

	finished := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		tSync.wg.Done()
		close(finished)
	}()

	// Must block until the goroutine above calls wg.Done(), not race ahead
	// and tear down pluginmanager's channels while it could still be sending.
	CloseTransaction(txID)

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("pending analysis goroutine never completed")
	}
}

// TestReloadRejectsNatsURLChange checks that Reload rejects a nats_url
// change and keeps the configuration.
func TestReloadRejectsNatsURLChange(t *testing.T) {
	if err := initialize(configParamWith("0.3")); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	defer configstore.Clean()

	var newConf configstore.ConfigFileData
	withNats := strings.Replace(string(configParamWith("0.8")), "---\n", "---\nnats_url: \"nats://localhost:4222\"\n", 1)
	if err := yaml.Unmarshal([]byte(withNats), &newConf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := Reload(testMeter, newConf, discardLogger); err == nil {
		t.Fatal("Reload changing nats_url should return error")
	}
	cs, err := configstore.Get()
	if err != nil {
		t.Fatalf("configstore.Get: %v", err)
	}
	if cs.NatsURL != "" {
		t.Errorf("rejected Reload changed nats_url to %q", cs.NatsURL)
	}
}

// testNatsURL returns the NATS server the NATS tests run against, given
// in WACE_TEST_NATS_URL (e.g. nats://localhost:4222), and skips the test
// when it is not set.
func testNatsURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("WACE_TEST_NATS_URL")
	if url == "" {
		t.Skip("WACE_TEST_NATS_URL not set")
	}
	return url
}

// countHandler is a slog handler that counts the records match accepts.
type countHandler struct {
	match func(slog.Record) bool
	count *atomic.Int64
}

func (h countHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h countHandler) Handle(_ context.Context, r slog.Record) error {
	if h.match(r) {
		h.count.Add(1)
	}
	return nil
}

func (h countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h countHandler) WithGroup(string) slog.Handler      { return h }

// recordAttr returns the value of the attribute key of r, or "".
func recordAttr(r slog.Record, key string) string {
	var value string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			value = a.Value.String()
			return false
		}
		return true
	})
	return value
}

// initializeWithLogger is initialize with the given logger.
func initializeWithLogger(t *testing.T, configuration string, l *slog.Logger) {
	t.Helper()
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal([]byte(configuration), &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if err := Init(testMeter, aux, l); err != nil {
		t.Fatalf("Init: %v", err)
	}
}

// TestAnalyzeAsync checks that the results of the async model plugins
// come back through NATS.
func TestAnalyzeAsync(t *testing.T) {
	url := testNatsURL(t)
	var succeeded atomic.Int64
	logger := slog.New(countHandler{
		match: func(r slog.Record) bool {
			return r.Message == "model plugin succeeded" && recordAttr(r, "plugin.mode") == "async"
		},
		count: &succeeded,
	})
	initializeWithLogger(t, fmt.Sprintf("---\nnats_url: %q\n", url)+configAsync, logger)
	defer configstore.Clean()

	txID := generateRandomID()
	InitTransaction(txID)
	if err := Analyze("RequestHeaders", txID, requestHeadersPayload, []string{"trivial", "trivial2"}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, _, err := CheckTransaction(txID, []string{"simple"}, waceapi.WAFData{}); err != nil {
		t.Fatalf("CheckTransaction: %v", err)
	}
	CloseTransaction(txID)

	deadline := time.Now().Add(5 * time.Second)
	for succeeded.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("got %d async results, want 2", succeeded.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAnalyzeRemoteSendFailure checks that a request that cannot be
// sent to a remote model plugin fails right away instead of waiting
// for the model timeout.
func TestAnalyzeRemoteSendFailure(t *testing.T) {
	url := testNatsURL(t)
	var failed atomic.Int64
	logger := slog.New(countHandler{
		match: func(r slog.Record) bool {
			return r.Message == "model plugin failed" && recordAttr(r, waceapi.LogKeyPlugin) == "remote"
		},
		count: &failed,
	})
	initializeWithLogger(t, fmt.Sprintf(`---
nats_url: %q
model_timeout: 30s
model_plugins:
  - id: "remote"
    plugin_type: RequestBody
    path: "testdata/plugins/model/trivial.so"
    remote: true
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
`, url), logger)
	defer configstore.Clean()

	txID := generateRandomID()
	InitTransaction(txID)
	defer CloseTransaction(txID)
	// larger than the default max_payload of the server (1MB)
	payload := waceapi.HTTPPayload{RequestBody: strings.Repeat("a", 2<<20)}
	if err := Analyze("RequestBody", txID, payload, []string{"remote"}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// Analyze waits for the model plugins in the background: the
	// failure must show up well before the model timeout
	deadline := time.Now().Add(5 * time.Second)
	for failed.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the send failure was not reported before the model timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

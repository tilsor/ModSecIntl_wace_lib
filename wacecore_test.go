package wace

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"
	"go.opentelemetry.io/otel/sdk/metric"

	"gopkg.in/yaml.v3"
)

var requestURI = "/cgi-bin/process.cgi"
var requestMethod = "POST"
var requestVersion = "HTTP/1.1"

// var requestLine = "POST /cgi-bin/process.cgi HTTP/1.1\n"

var requestHeaders = []pluginmanager.HTTPHeader{
	{Key: "User-Agent", Value: "Mozilla/4.0 (compatible; MSIE5.01; Windows NT)"},
	{Key: "Host", Value: "www.tutorialspoint.com"},
	{Key: "Content-Type", Value: "application/x-www-form-urlencoded"},
	{Key: "Content-Length", Value: "length"},
	{Key: "Accept-Language", Value: "en-us"},
	{Key: "Accept-Encoding", Value: "gzip, deflate"},
	{Key: "Connection", Value: "Keep-Alive"},
}

var requestHeadersPayload = pluginmanager.HTTPPayload{
	URI:            requestURI,
	Method:         requestMethod,
	HTTPVersion:    requestVersion,
	RequestHeaders: requestHeaders,
}

var requestBody = "licenseID=string&content=string&/paramsXML=string\n"
var wholeRequest = pluginmanager.HTTPPayload{
	URI:         requestURI,
	Method:      requestMethod,
	HTTPVersion: requestVersion,
	RequestBody: requestBody,
}

// var wholeRequest = requestLine + requestHeaders + "\n" + requestBody
var responseCode = 200
var responseProto = "HTTP/1.1"
var responseHeaders = []pluginmanager.HTTPHeader{
	{Key: "Date", Value: "Mon, 27 Jul 2009 12:28:53 GMT"},
	{Key: "Server", Value: "Apache/2.2.14 (Win32)"},
	{Key: "Last-Modified", Value: "Wed, 22 Jul 2009 19:15:56 GMT"},
	{Key: "Content-Length", Value: "88"},
	{Key: "Content-Type", Value: "text/html"},
	{Key: "Connection", Value: "Closed"},
}

var responseHeadersPayload = pluginmanager.HTTPPayload{
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

var wholeResponse = pluginmanager.HTTPPayload{
	ResponseProtocol: responseProto,
	ResponseCode:     responseCode,
	ResponseHeaders:  responseHeaders,
	ResponseBody:     responseBody,
}

var config = []byte(`---
logpath: "/dev/null"
loglevel: DEBUG
modelplugins:
  - id: "trivial"
    path: "testdata/plugins/model/trivial.so"
    weight: 1
    params:
      d: "sds"
      b: "dnid"
      e: "dofnno"
    # plugintype: "RequestHeaders"
    plugintype: "Everything"
  - id: "trivial2"
    path: "testdata/plugins/model/trivial2.so"
    weight: 2
    params:
      a: "sdsds"
      b: "sdfjdnid"
      c: "kfoskdofnno"
    plugintype: "Everything"
decisionplugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
    wafweight: 0.5
    decisionbalance: 0.5
`)

var configAllModels = []byte(`---
logpath: "/dev/null"
#The level of debug, the valid options are - ERRO, WARN, INFO, DEBUG
loglevel: "WARN"

#The model plugins configuration
modelplugins:
  - id: "trivialRequestHeaders"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialRequestBody"
    plugintype: RequestBody
    path: "testdata/plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialAllRequest"
    plugintype: AllRequest
    path: "testdata/plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialResponseHeaders"
    plugintype: ResponseHeaders
    path: "testdata/plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialResponseBody"
    plugintype: ResponseBody
    path: "testdata/plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialAllResponse"
    plugintype: AllResponse
    path: "testdata/plugins/model/trivial.so"
    weight: 0.1
    mode: sync

#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    wafweight: 0.5
    decisionbalance: 0.1
`)

var configSyncNoRemote = []byte(`---
logpath: "/dev/null"
#The level of debug, the valid options are - ERRO, WARN, INFO, DEBUG
loglevel: "WARN"

#The model plugins configuration
modelplugins:
  - id: "trivial"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    weight: 1
    mode: sync
  - id: "trivial2"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial2.so"
    weight: 2
    mode: sync

#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    wafweight: 0.5
    decisionbalance: 0.1
`)

var configSyncRemote = []byte(`---
logpath: "/dev/null"
#The level of debug, the valid options are - ERRO, WARN, INFO, DEBUG
loglevel: "WARN"

#The model plugins configuration
modelplugins:
  - id: "trivial"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    weight: 1
    mode: sync
    remote: true
  - id: "trivial2"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial2.so"
    weight: 2
    mode: sync
    remote: true
#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    wafweight: 0.5
    decisionbalance: 0.1
`)

var configAsync = []byte(`---
logpath: "/dev/null"
#The level of debug, the valid options are - ERRO, WARN, INFO, DEBUG
loglevel: "WARN"

#The model plugins configuration
modelplugins:
  - id: "trivial"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial.so"
    weight: 1
    mode: async
  - id: "trivial2"
    plugintype: RequestHeaders
    path: "testdata/plugins/model/trivial2.so"
    weight: 2
    mode: async
#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
#    wafweight: 0.5
    decisionbalance: 0.1
`)

var provider = metric.NewMeterProvider()
var testMeter = provider.Meter("example-meter")

func initialize(configuration []byte) error {
	var aux configstore.ConfigFileData
	err := yaml.Unmarshal(configuration, &aux)
	if err != nil {
		return err
	}
	err = Init(testMeter, aux)
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
		payload     pluginmanager.HTTPPayload
		plugins     []string
	}
	tests := []struct {
		name      string
		config    []byte
		steps     []step
		postDelay time.Duration
	}{
		{
			name:   "request in parts",
			config: configAllModels,
			steps: []step{
				{"RequestHeaders", requestHeadersPayload, []string{"trivialRequestHeaders"}},
				{"RequestBody", pluginmanager.HTTPPayload{RequestBody: requestBody}, []string{"trivialRequestBody"}},
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
				{"ResponseBody", pluginmanager.HTTPPayload{ResponseBody: responseBody}, []string{"trivialResponseBody"}},
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
			name:      "request in parts async",
			config:    configAsync,
			steps:     []step{{"RequestHeaders", requestHeadersPayload, []string{"trivial", "trivial2"}}},
			postDelay: 10 * time.Millisecond,
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

			_, err = CheckTransaction(transactionID, "simple", make(map[string]string))
			if err != nil {
				t.Errorf("CheckTransaction: %v", err)
			}

			CloseTransaction(transactionID)

			if tt.postDelay > 0 {
				time.Sleep(tt.postDelay)
			}
		})
	}
}

func TestCheckInvalidTransaction(t *testing.T) {
	_, err := CheckTransaction("INEXISTENT", "simple", make(map[string]string))
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

	wafParams := make(map[string]string)
	auxString := "COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=20,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2"
	for _, score := range strings.Split(auxString, ",") {
		scoreParts := strings.Split(score, "=")
		wafParams[scoreParts[0]] = scoreParts[1]
	}

	err = Analyze("RequestHeaders", transactionID, requestHeadersPayload, []string{"trivial", "trivial2", "trivial3"})
	if err != nil {
		t.Errorf("Error: Analyze RequestHeaders: %s", err.Error())
	}

	res, err := CheckTransaction(transactionID, "simple", wafParams)
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

// TestInitDuplicate covers the configstore.New() error branch in Init: calling
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

// TestInitInvalidConfig covers the SetConfig error branch in Init: a config
// referencing a nonexistent plugin path must cause Init to return an error.
func TestInitInvalidConfig(t *testing.T) {
	badConfig := []byte(`---
logpath: "/dev/null"
loglevel: "ERROR"
modelplugins:
  - id: "missing"
    path: "testdata/plugins/model/does_not_exist.so"
    plugintype: "Everything"
`)
	var aux configstore.ConfigFileData
	if err := yaml.Unmarshal(badConfig, &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	err := Init(testMeter, aux)
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

	_, err = CheckTransaction(transactionID, "nonexistent_plugin", make(map[string]string))
	if err == nil {
		t.Errorf("CheckTransaction with nonexistent decision plugin should return error")
	}
}

// parseWAFParams parses a comma-separated "key=value" string into a map.
func parseWAFParams(s string) map[string]string {
	params := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			params[parts[0]] = parts[1]
		}
	}
	return params
}

func TestCheckTransactionResult(t *testing.T) {
	blockingWAF := parseWAFParams("inbound_blocking=20,inbound_threshold=5")
	noAlertWAF := parseWAFParams("inbound_blocking=0,inbound_threshold=5")

	tests := []struct {
		name      string
		config    []byte
		models    []string
		wafParams map[string]string
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

			blocked, err := CheckTransaction(txID, "simple", tt.wafParams)
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
		payload     pluginmanager.HTTPPayload
		models      []string
	}{
		{"RequestHeaders", requestHeadersPayload, []string{"trivialRequestHeaders"}},
		{"RequestBody", pluginmanager.HTTPPayload{RequestBody: requestBody}, []string{"trivialRequestBody"}},
		{"ResponseHeaders", responseHeadersPayload, []string{"trivialResponseHeaders"}},
		{"ResponseBody", pluginmanager.HTTPPayload{ResponseBody: responseBody}, []string{"trivialResponseBody"}},
	}

	for _, p := range phases {
		if err := Analyze(p.payloadType, txID, p.payload, p.models); err != nil {
			t.Errorf("Analyze(%s): %v", p.payloadType, err)
		}
	}

	_, err = CheckTransaction(txID, "simple", make(map[string]string))
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

			if _, err := CheckTransaction(txID, "simple", wafParams); err != nil {
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

func BenchmarkTrivial(b *testing.B) {
	err := initialize(configSyncNoRemote)
	defer configstore.Clean()
	if err != nil {
		b.Errorf("Error initing test: %v", err)
	}

	wafParams := make(map[string]string)
	auxString := "COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=0,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2"
	for _, score := range strings.Split(auxString, ",") {
		scoreParts := strings.Split(score, "=")
		wafParams[scoreParts[0]] = scoreParts[1]
	}
	for i := 0; i < b.N; i++ {
		transactionId := strconv.Itoa(i)
		InitTransaction(transactionId)

		Analyze("RequestHeaders", transactionId, pluginmanager.HTTPPayload{URI: "Request line and headers\n"}, []string{"trivial", "trivial2"})

		_, err := CheckTransaction(transactionId, "simple", wafParams)
		if err != nil {
			b.Errorf("Error checking transaction: %v", err)
		}
		CloseTransaction(transactionId)
	}
}

func BenchmarkTrivialFullNATS(b *testing.B) {
	err := initialize(configSyncRemote)
	defer configstore.Clean()
	if err != nil {
		b.Errorf("Error initing test: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	wafParams := make(map[string]string)
	auxString := "COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=0,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2"
	for _, score := range strings.Split(auxString, ",") {
		scoreParts := strings.Split(score, "=")
		wafParams[scoreParts[0]] = scoreParts[1]
	}
	for i := 0; i < b.N; i++ {
		transactionId := generateRandomID()
		InitTransaction(transactionId)

		Analyze("RequestHeaders", transactionId, pluginmanager.HTTPPayload{URI: "Request line and headers\n"}, []string{"trivial", "trivial2"})

		_, err := CheckTransaction(transactionId, "simple", wafParams)
		if err != nil {
			b.Errorf("Error checking transaction: %v", err)
		}
		CloseTransaction(transactionId)
	}
}

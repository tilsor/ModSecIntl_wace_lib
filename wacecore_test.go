package wace

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	cf "github.com/tiroa-tilsor/wacelib/configstore"
	"go.opentelemetry.io/otel/sdk/metric"

	"gopkg.in/yaml.v3"
)

var requestLine = "POST /cgi-bin/process.cgi HTTP/1.1\n"
var requestHeaders = `User-Agent: Mozilla/4.0 (compatible; MSIE5.01; Windows NT)
Host: www.tutorialspoint.com
Content-Type: application/x-www-form-urlencoded
Content-Length: length
Accept-Language: en-us
Accept-Encoding: gzip, deflate
Connection: Keep-Alive
`

var requestBody = "licenseID=string&content=string&/paramsXML=string\n"
var wholeRequest = requestLine + requestHeaders + "\n" + requestBody

var responseLine = "HTTP/1.1 200 OK\n"
var responseHeaders = `Date: Mon, 27 Jul 2009 12:28:53 GMT
Server: Apache/2.2.14 (Win32)
Last-Modified: Wed, 22 Jul 2009 19:15:56 GMT
Content-Length: 88
Content-Type: text/html
Connection: Closed
`
var responseBody = `<html>
<body>
<h1>Hello, World!</h1>
</body>
</html>
`
var wholeResponse = responseLine + responseHeaders + "\n" + responseBody

var config = []byte(`---
logpath: "/dev/null"
loglevel: DEBUG
modelplugins:
  - id: "trivial"
    path: "_plugins/model/trivial.so"
    weight: 1
    params:
      d: "sds"
      b: "dnid"
      e: "dofnno"
    # plugintype: "RequestHeaders"
    plugintype: "Everything"
  - id: "trivial2"
    path: "_plugins/model/trivial2.so"
    weight: 2
    params:
      a: "sdsds"
      b: "sdfjdnid"
      c: "kfoskdofnno"
    plugintype: "Everything"
decisionplugins:
  - id: "simple"
    path: "_plugins/decision/simple.so"
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
    path: "_plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialRequestBody"
    plugintype: RequestBody
    path: "_plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialAllRequest"
    plugintype: AllRequest
    path: "_plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialResponseHeaders"
    plugintype: ResponseHeaders
    path: "_plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialResponseBody"
    plugintype: ResponseBody
    path: "_plugins/model/trivial.so"
    weight: 0.1
    mode: sync
  - id: "trivialAllResponse"
    plugintype: AllResponse
    path: "_plugins/model/trivial.so"
    weight: 0.1
    mode: sync

#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "_plugins/decision/simple.so"
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
    path: "_plugins/model/trivial.so"
    weight: 1
    mode: sync
  - id: "trivial2"
    plugintype: RequestHeaders
    path: "_plugins/model/trivial2.so"
    weight: 2
    mode: sync

#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "_plugins/decision/simple.so"
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
    path: "_plugins/model/trivial.so"
    weight: 1
    mode: sync
    remote: true
  - id: "trivial2"
    plugintype: RequestHeaders
    path: "_plugins/model/trivial2.so"
    weight: 2
    mode: sync
    remote: true
#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "_plugins/decision/simple.so"
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
    path: "_plugins/model/trivial.so"
    weight: 1
    mode: async
  - id: "trivial2"
    plugintype: RequestHeaders
    path: "_plugins/model/trivial2.so"
    weight: 2
    mode: async
#The decision plugin configuration
decisionplugins:
  - id: "simple"
    path: "_plugins/decision/simple.so"
#    wafweight: 0.5
    decisionbalance: 0.1
`)

// var configRoberta = []byte(`---
// logpath: "/dev/null"
// loglevel: DEBUG
// listenport: "50051"
// modelplugins:
//   - id: "trivial"
//     path: "_plugins/model/trivial.so"
//     weight: 1
//     threshold: 0.5
//     params:
//       d: "sds"
//       b: "dnid"
//       e: "dofnno"
//     # plugintype: "RequestHeaders"
//     plugintype: "Everything"
//   - id: "trivial2"
//     path: "_plugins/model/trivial2.so"
//     weight: 2
//     threshold: 0.1
//     params:
//       a: "sdsds"
//       b: "sdfjdnid"
//       c: "kfoskdofnno"
//     plugintype: "Everything"
//   - id: "roberta"
//     path: "_plugins/model/roberta.so"
//     weight: 1
//     threshold: 0.5
//     params:
//       url: "localhost:9999"
//       distance_threshold: -0.02
//     plugintype: "AllRequest"
// decisionplugins:
//   - id: "simple"
//     path: "_plugins/decision/simple.so"
//     wafweight: 0.5
//     decisionbalance: 0.5
// `)

var provider = metric.NewMeterProvider()
var testMeter = provider.Meter("example-meter")

func initilize(configuration []byte) error {
	var aux cf.ConfigFileData
	err := yaml.Unmarshal(configuration, &aux)
	if err != nil {
		return err
	}
	err = cf.Get().SetConfig(aux)
	if err != nil {
		return err
	}
	Init(testMeter)
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

func TestAnalyzeRequestInParts(t *testing.T) {
	err := initilize(configAllModels)
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()

	InitTransaction(transactionID)

	res := Analyze("RequestHeaders", transactionID, requestLine+"\n"+requestHeaders, []string{"trivialRequestHeaders"})
	if res != nil {
		t.Errorf("Error: Analyze RequestHeaders: %s", res.Error())
	}
	res = Analyze("RequestBody", transactionID, requestBody, []string{"trivialRequestBody"})
	if res != nil {
		t.Errorf("Error: Analyze RequestBody: %s", res.Error())
	}

	_, err = CheckTransaction(transactionID, "simple", make(map[string]string))
	if err != nil {
		t.Errorf("Error: CheckTransaction: %s", err.Error())
	}

	CloseTransaction(transactionID)
}

func TestAnalyzeWholeRequest(t *testing.T) {
	err := initilize(configAllModels)
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()

	InitTransaction(transactionID)

	res := Analyze("AllRequest", transactionID, wholeRequest, []string{"trivialAllRequest"})
	if res != nil {
		t.Errorf("Error: Analyze AllRequest: %s", res.Error())
	}

	_, err = CheckTransaction(transactionID, "simple", make(map[string]string))
	if err != nil {
		t.Errorf("Error: CheckTransaction: %s", err.Error())
	}

	CloseTransaction(transactionID)
}

func TestAnalyzeResponseInParts(t *testing.T) {
	err := initilize(configAllModels)
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()

	InitTransaction(transactionID)

	res := Analyze("ResponseHeaders", transactionID, responseLine+"\n"+responseHeaders, []string{"trivialResponseHeaders"})
	if res != nil {
		t.Errorf("Error: Analyze ResponseHeaders: %s", res.Error())
	}
	res = Analyze("ResponseBody", transactionID, responseBody, []string{"trivialResponseBody"})
	if res != nil {
		t.Errorf("Error: Analyze ResponseBody: %s", res.Error())
	}

	_, err = CheckTransaction(transactionID, "simple", make(map[string]string))
	if err != nil {
		t.Errorf("Error: CheckTransaction: %s", err.Error())
	}

	CloseTransaction(transactionID)
}

func TestAnalyzeWholeResponse(t *testing.T) {
	err := initilize(configAllModels)
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}

	transactionID := generateRandomID()

	InitTransaction(transactionID)

	res := Analyze("AllResponse", transactionID, wholeResponse, []string{"trivialAllResponse"})
	if res != nil {
		t.Errorf("Error: Analyze AllResponse: %s", res.Error())
	}

	_, err = CheckTransaction(transactionID, "simple", make(map[string]string))
	if err != nil {
		t.Errorf("Error: CheckTransaction: %s", err.Error())
	}

	CloseTransaction(transactionID)
}

func TestAnalyzeRequestInPartsAsync(t *testing.T) {
	var aux cf.ConfigFileData
	err := yaml.Unmarshal(configAsync, &aux)
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}
	err = cf.Get().SetConfig(aux)
	Init(testMeter)
	transactionID := generateRandomID()

	InitTransaction(transactionID)

	res := Analyze("RequestHeaders", transactionID, requestLine+"\n"+requestHeaders, []string{"trivial", "trivial2"})
	if res != nil {
		t.Errorf("Error: Analyze RequestHeaders: %s", res.Error())
	}

	_, err = CheckTransaction(transactionID, "simple", make(map[string]string))
	if err != nil {
		t.Errorf("Error: CheckTransaction: %s", err.Error())
	}

	CloseTransaction(transactionID)

	time.Sleep(10 * time.Millisecond)
}

func TestCheckInvalidTransaction(t *testing.T) {
	_, err := CheckTransaction("INEXISTENT", "simple", make(map[string]string))
	if err == nil {
		t.Errorf("Error: CheckTransaction with inexistent transaction does not rise an error")
	}
}

func TestCheckAttackTransaction(t *testing.T) {
	var aux cf.ConfigFileData
	err := yaml.Unmarshal(configSyncNoRemote, &aux)
	if err != nil {
		t.Errorf("Error initing test: %v", err)
	}
	err = cf.Get().SetConfig(aux)
	Init(testMeter)
	transactionID := generateRandomID()

	InitTransaction(transactionID)

	wafParams := make(map[string]string)
	auxString := "COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=20,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2"
	for _, score := range strings.Split(auxString, ",") {
		scoreParts := strings.Split(score, "=")
		wafParams[scoreParts[0]] = scoreParts[1]
	}

	err = Analyze("RequestHeaders", transactionID, requestLine+"\n"+requestHeaders, []string{"trivial", "trivial2", "trivial3"})
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

// func TestAnalyzeStress(t *testing.T) {
// 	for i := 0; i < 1000; i++ {
// 		transactionID := generateRandomID()
// 		AnalyzeRequest(transactionID, wholeRequest, []string{"trivial", "trivial2"})
// 		_, err := CheckTransaction(transactionID, "simple", make(map[string]string))
// 		if err != nil {
// 			t.Errorf("checkTransaction error: %v", err)
// 		}
// 	}

// }

// func processRequest(models []string) error {
// 	transactionID := generateRandomID()

// 	res := AnalyzeRequest(transactionID, wholeRequest, models)
// 	if res != 0 {
// 		return errors.New("analyzeRequest returned non-zero")
// 	}

// 	_, err := CheckTransaction(transactionID, "simple",
// 		map[string]string{"anomalyscore": "1",
// 			"inboundthreshold": "100"})
// 	return err
// }

// func TestRoberta(t *testing.T) {
// 	conf := cf.Get()
// 	err := conf.LoadConfigYaml(configRoberta)
// 	if err != nil {
// 		panic("Error loading config: " + err.Error())
// 	}

// 	err = processRequest([]string{"roberta"})
// 	if err != nil {
// 		t.Errorf("callRoberta error: %v", err)
// 	}
// }

// func BenchmarkRoberta(b *testing.B) {
// 	for i := 0; i < b.N; i++ {
// 		processRequest([]string{"roberta"})
// 	}
// }

func BenchmarkTrivial(b *testing.B) {

	var aux cf.ConfigFileData
	err := yaml.Unmarshal(configSyncNoRemote, &aux)
	if err != nil {
		b.Errorf("Error initing test: %v", err)
	}
	err = cf.Get().SetConfig(aux)
	Init(testMeter)
	wafParams := make(map[string]string)
	auxString := "COMBINED_SCORE=0,HTTP=0,LFI=0,PHPI=0,RCE=0,RFI=0,SESS=0,SQLI=0,XSS=0,inbound_blocking=0,inbound_detection=0,inbound_per_pl=0-0-0-0,inbound_threshold=5,outbound_blocking=0,outbound_detection=0,outbound_per_pl=0-0-0-0,outbound_threshold=4,phase=2"
	for _, score := range strings.Split(auxString, ",") {
		scoreParts := strings.Split(score, "=")
		wafParams[scoreParts[0]] = scoreParts[1]
	}
	for i := 0; i < b.N; i++ {
		transactionId := strconv.Itoa(i)
		InitTransaction(transactionId)

		Analyze("RequestHeaders", transactionId, "Request line and headers\n", []string{"trivial", "trivial2"})

		_, err := CheckTransaction(transactionId, "simple", wafParams)
		if err != nil {
			b.Errorf("Error checking transaction: %v", err)
		}
		CloseTransaction(transactionId)
	}
}

func BenchmarkTrivialFullNATS(b *testing.B) {
	var aux cf.ConfigFileData
	err := yaml.Unmarshal(configSyncRemote, &aux)
	if err != nil {
		b.Errorf("Error initing test: %v", err)
	}
	err = cf.Get().SetConfig(aux)
	Init(testMeter)
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

		Analyze("RequestHeaders", transactionId, "Request line and headers\n", []string{"trivial", "trivial2"})

		_, err := CheckTransaction(transactionId, "simple", wafParams)
		if err != nil {
			b.Errorf("Error checking transaction: %v", err)
		}
		CloseTransaction(transactionId)
	}
}

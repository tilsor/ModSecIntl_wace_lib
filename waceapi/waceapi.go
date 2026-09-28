package waceapi

import "go.opentelemetry.io/otel/metric"

type HTTPHeader struct {
	Key   string
	Value string
}

type HTTPPayload struct {
	URI              string
	Method           string
	HTTPVersion      string
	RequestHeaders   []HTTPHeader
	RequestBody      string
	ResponseProtocol string
	ResponseCode     int
	ResponseHeaders  []HTTPHeader
	ResponseBody     string
}

// ModelInput is the struct that contains the input data for the model plugin
type ModelInput struct {
	TransactionId string      `json:"transactionId"`
	Payload       HTTPPayload `json:"payload"`
	Training      bool        `json:"training"`
}

type ModelResults struct {
	ProbAttack float64 `json:"probattack"`
	Data       any     `json:"data"`
}

// DecisionInput is the struct that contains the input data for the decision plugin
type DecisionInput struct {
	TransactionId string
	Results       map[string]ModelResults
	ModelWeight   map[string]float64
	WAFWeight     float64
	WAFdata       WAFData
	Training      bool
}

type DecisionResult struct {
	Block bool `json:"block"`
	Data  any  `json:"data"`
}

type WAFData struct {
	Scores map[string]float64
	Rules  map[int]int
}

type ModelPlugin interface {
	Process(ModelInput) (ModelResults, error)
	Reload(params map[string]string, meter metric.Meter) error
	Clean() error
}

type DecisionPlugin interface {
	CheckResults(DecisionInput) (DecisionResult, error)
	Reload(params map[string]string, meter metric.Meter) error
	Clean() error
}

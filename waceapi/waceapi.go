package waceapi

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
)

const (
	LogKeyTxID                 = "tx_id"
	LogKeyComponent            = "component"
	LogKeyPlugin               = "plugin.id"
	LogKeyPluginType           = "plugin.type"
	LogValueModelPluginType    = "model"
	LogValueDecisionPluginType = "decision"
)

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

// PluginConfig is what the plugin manager passes to NewPlugin and
// Reload.
type PluginConfig struct {
	Params map[string]string
	Meter  metric.Meter
	// Logger already carries the component, plugin.type and plugin.id
	// attributes; plugins must not add them again. It is never nil.
	Logger *slog.Logger
}

// ModelPlugin is the instance returned by the NewPlugin function of a
// model plugin.
type ModelPlugin interface {
	Process(context.Context, ModelInput) (ModelResults, error)
	Reload(PluginConfig) error
	Clean() error
}

// DecisionPlugin is the instance returned by the NewPlugin function of
// a decision plugin.
type DecisionPlugin interface {
	CheckResults(context.Context, DecisionInput) (DecisionResult, error)
	Reload(PluginConfig) error
	Clean() error
}

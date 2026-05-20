package waceapi

type ModelResults struct {
	ProbAttack float64                `json:"probattack"`
	Data       map[string]interface{} `json:"data"`
}

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
}

// DecisionInput is the struct that contains the input data for the decision plugin
type DecisionInput struct {
	TransactionId string
	Results       map[string]ModelResults
	ModelWeight   map[string]float64
	WAFdata       map[string]string
}

/* Trivial Test Decision Plugin that blocks only if the WAF says so
 */

package main

import (
	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type testDecision struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error) {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[test:NewPlugin] %v\n", params)
	return &testDecision{}, nil
}

// CheckResults returns true (block traffic) if WAF says so, and false
// in other case.
func (d *testDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	logger := lg.Get()

	modelRes := decisionInput.Results
	modelWeight := decisionInput.ModelWeight
	wafData := decisionInput.WAFdata
	transactionID := decisionInput.TransactionId

	logger.TPrintf(lg.WARN, transactionID, "[test:CheckResults]\n  modelRes: %v\n  modelWeight: %v\n  wafData: %v\n", modelRes, modelWeight, wafData)

	if len(wafData.Scores) != 0 {
		as := wafData.Scores["anomalyscore"]
		it := wafData.Scores["inboundthreshold"]
		if as >= it { // modsec wants to block
			return waceapi.DecisionResult{Block: true}, nil
		}
	}
	return waceapi.DecisionResult{Block: false}, nil
}

// Reload reloads the plugin (does nothing in this case)
func (d *testDecision) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (d *testDecision) Clean() error {
	return nil
}

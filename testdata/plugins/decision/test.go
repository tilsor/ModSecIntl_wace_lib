/* Trivial Test Decision Plugin that always returns no attack
 */

package main

import (
	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

// InitPlugin intitalizes the plugins (does nothing in this case)
func InitPlugin(params map[string]string, meter metric.Meter) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[test:InitPlugin] %v\n", params)

	return nil
}

// CheckResults returns true (block traffic) if WAF says so, and false
// in other case.
// func CheckResults(transactionID string, modelRes map[string]float64, modelWeight map[string]float64, modelThres map[string]float64, wafData map[string]string) (bool, error) {
func CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	logger := lg.Get()

	modelRes := decisionInput.Results
	modelWeight := decisionInput.ModelWeight
	wafData := decisionInput.WAFdata
	transactionID := decisionInput.TransactionId

	logger.TPrintf(lg.WARN, transactionID, "[test:CheckResults]\n  modelRes: %v\n  modelWeight: %v\n  modelThres: %v\n  wafData: %v\n", modelRes, modelWeight, wafData)

	if len(wafData.Scores) != 0 {
		as := wafData.Scores["anomalyscore"]
		it := wafData.Scores["inboundthreshold"]
		if as >= it { // modsec wants to block
			return waceapi.DecisionResult{Block: true}, nil
		}
	}
	return waceapi.DecisionResult{Block: false}, nil
}

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

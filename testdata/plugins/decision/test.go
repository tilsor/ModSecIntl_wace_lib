/* Trivial Test Decision Plugin that always returns no attack
 */

package main

import (
	"strconv"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	pm "github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"
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
func CheckResults(decisionInput pm.DecisionInput) (bool, error) {
	logger := lg.Get()

	modelRes := decisionInput.Results
	modelWeight := decisionInput.ModelWeight
	wafData := decisionInput.WAFdata
	transactionID := decisionInput.TransactionId

	logger.TPrintf(lg.WARN, transactionID, "[test:CheckResults]\n  modelRes: %v\n  modelWeight: %v\n  modelThres: %v\n  wafData: %v\n", modelRes, modelWeight, wafData)

	if len(wafData) != 0 {
		as, err := strconv.Atoi(wafData["anomalyscore"])
		if err != nil {
			return false, err
		}
		it, err := strconv.Atoi(wafData["inboundthreshold"])
		if err != nil {
			return false, err
		}
		if as >= it { // modsec wants to block
			return true, nil
		}
	}
	return false, nil
}

/* Trivial Decision Plugin that always returns no attack
 */

package main

import (
	"context"
	"strconv"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	pm "github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func InitPlugin(params map[string]string, meter metric.Meter) error {
	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "simple"), attribute.String("plugin_type", "decision")))
	return nil
}

func CheckResults(decisionInput pm.DecisionInput) (bool, error) {
	logger := lg.Get()
	var totalModelW float64 = 0
	var modelDetectionCount int = 0
	var totalModelProb float64 = 0
	for key, value := range decisionInput.Results {
		logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "simple | model_id: %v result: %v", key, value)
		if value.ProbAttack >= 0.5 {
			modelDetectionCount++
			totalModelW += decisionInput.ModelWeight[key]
		}
	}

	for key, value := range decisionInput.WAFdata {
		logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "simple | WAF data: %v: %v", key, value)
	}

	// if we have some model results
	if modelDetectionCount > 0 {
		totalModelProb = totalModelW / float64(modelDetectionCount)
	}
	if len(decisionInput.WAFdata) != 0 {
		as, _ := strconv.Atoi(decisionInput.WAFdata["inbound_blocking"])
		it, _ := strconv.Atoi(decisionInput.WAFdata["inbound_threshold"])
		logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "Coraza | Anomaly score: %v Anomaly score threshold: %v ", as, it)

		if as >= it && totalModelProb > 0.5 { // coraza wants to block and models agree
			return true, nil
		}
	}
	return false, nil
}

// func CheckResults(transactionID string, modelRes map[string]float64, modelWeight map[string]float64, modelThres map[string]float64, WAFdata map[string]string) (bool, error) {
// 	logger := lg.Get()
// 	var totalModelW float64 = 0
// 	var modelDetectionCount int = 0
// 	var totalModelProb float64 = 0
// 	for key, value := range modelRes {
// 		logger.TPrintf(lg.DEBUG, transactionID, "simple | model_id: %v result: %v threshold: %v", key, value, modelThres[key])
// 		if value >= modelThres[key] {
// 			modelDetectionCount++
// 			totalModelW += modelWeight[key]
// 		}
// 	}

// 	// DEBUG: print WAF data
// 	for key, value := range WAFdata {
// 		logger.TPrintf(lg.DEBUG, transactionID, "simple | WAF data: %v: %v", key, value)
// 	}

// 	// if we have some model results
// 	if modelDetectionCount > 0 {
// 		totalModelProb = totalModelW / float64(modelDetectionCount)
// 	}
// 	if len(WAFdata) != 0 {
// 		as, _ := strconv.Atoi(WAFdata["inbound_blocking"])
// 		it, _ := strconv.Atoi(WAFdata["inbound_threshold"])
// 		logger.TPrintf(lg.DEBUG, transactionID, "Coraza | Anomaly score: %v Anomaly score threshold: %v ", as, it)

// 		if as >= it && totalModelProb > 0.5 { // modsec wants to block
// 			return true, nil
// 		}
// 	}
// 	return false, nil
// }

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

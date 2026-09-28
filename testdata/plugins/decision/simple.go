/* Simple Decision Plugin that blocks when the WAF alerts and the models agree
 */

package main

import (
	"context"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type simpleDecision struct{}

func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error) {
	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "simple"), attribute.String("plugin_type", "decision")))
	return &simpleDecision{}, nil
}

func (d *simpleDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
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

	for key, value := range decisionInput.WAFdata.Scores {
		logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "simple | WAF score: %v: %v", key, value)
	}

	// if we have some model results
	if modelDetectionCount > 0 {
		totalModelProb = totalModelW / float64(modelDetectionCount)
	}
	if len(decisionInput.WAFdata.Scores) != 0 {
		as := decisionInput.WAFdata.Scores["inbound_blocking"]
		it := decisionInput.WAFdata.Scores["inbound_threshold"]
		logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "Coraza | Anomaly score: %v Anomaly score threshold: %v ", as, it)

		if as >= it && totalModelProb > 0.5 { // coraza wants to block and models agree
			return waceapi.DecisionResult{Block: true}, nil
		}
	}
	return waceapi.DecisionResult{Block: false}, nil
}

// Reload reloads the plugin (does nothing in this case)
func (d *simpleDecision) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (d *simpleDecision) Clean() error {
	return nil
}

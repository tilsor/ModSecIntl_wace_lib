/* Simple Decision Plugin that blocks when the WAF alerts and the models agree
 */

package main

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type simpleDecision struct {
	logger atomic.Pointer[slog.Logger]
}

func NewPlugin(cfg waceapi.PluginConfig) (waceapi.DecisionPlugin, error) {
	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := cfg.Meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "simple"), attribute.String("plugin_type", "decision")))
	d := &simpleDecision{}
	d.logger.Store(cfg.Logger)
	return d, nil
}

func (d *simpleDecision) CheckResults(ctx context.Context, decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	logger := d.logger.Load().With(waceapi.LogKeyTxID, decisionInput.TransactionId)
	var totalModelW float64 = 0
	var modelDetectionCount int = 0
	var totalModelProb float64 = 0
	for key, value := range decisionInput.Results {
		logger.Debug("model result", "model.id", key, "model.result", value)
		if value.ProbAttack >= 0.5 {
			modelDetectionCount++
			totalModelW += decisionInput.ModelWeight[key]
		}
	}

	for key, value := range decisionInput.WAFdata.Scores {
		logger.Debug("WAF score", "score.name", key, "score.value", value)
	}

	// if we have some model results
	if modelDetectionCount > 0 {
		totalModelProb = totalModelW / float64(modelDetectionCount)
	}
	if len(decisionInput.WAFdata.Scores) != 0 {
		as := decisionInput.WAFdata.Scores["inbound_blocking"]
		it := decisionInput.WAFdata.Scores["inbound_threshold"]
		logger.Debug("WAF anomaly score", "anomaly_score", as, "anomaly_score.threshold", it)

		if as >= it && totalModelProb > 0.5 { // coraza wants to block and models agree
			return waceapi.DecisionResult{Block: true}, nil
		}
	}
	return waceapi.DecisionResult{Block: false}, nil
}

// Reload reloads the plugin (does nothing in this case)
func (d *simpleDecision) Reload(cfg waceapi.PluginConfig) error {
	d.logger.Store(cfg.Logger)
	return nil
}

func (d *simpleDecision) Clean() error {
	return nil
}

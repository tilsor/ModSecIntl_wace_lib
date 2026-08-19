// Decision Plugin that uses weighted sum algorithm to decide if a transaction should be blocked

package main

import (
	"context"
	"fmt"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const PLUGIN_NAME = "waf_only"

var threshold float64

func InitPlugin(params map[string]string, meter metric.Meter) error {
	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", PLUGIN_NAME), attribute.String("plugin_type", "decision")))
	return nil
}

func CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	as, ok := decisionInput.WAFdata.Scores["inbound_blocking"]
	if !ok {
		return waceapi.DecisionResult{}, fmt.Errorf("inbound_blocking score not found")
	}
	it, ok := decisionInput.WAFdata.Scores["inbound_threshold"]
	if !ok {
		return waceapi.DecisionResult{}, fmt.Errorf("inbound_threshold score not found")
	}

	logger := lg.Get()
	logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "%s | anomaly score: %v anomaly score threshold: %v", PLUGIN_NAME, as, it)
	return waceapi.DecisionResult{Block: as >= it}, nil
}

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

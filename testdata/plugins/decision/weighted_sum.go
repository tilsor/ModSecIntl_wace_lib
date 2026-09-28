// Decision Plugin that uses weighted sum algorithm to decide if a transaction should be blocked

package main

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type weightedSumDecision struct {
	mu        sync.RWMutex
	threshold float64
}

func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error) {
	d := &weightedSumDecision{}
	if err := d.Reload(params, meter); err != nil {
		return nil, err
	}

	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "weighted_sum"), attribute.String("plugin_type", "decision")))
	return d, nil
}

func (d *weightedSumDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	var weightedSum float64 = 0
	var weightsSum float64 = 0
	for key, value := range decisionInput.Results {
		weightedSum += value.ProbAttack * decisionInput.ModelWeight[key]
		weightsSum += decisionInput.ModelWeight[key]
	}

	as, ok := decisionInput.WAFdata.Scores["inbound_blocking"]
	if !ok {
		return waceapi.DecisionResult{}, fmt.Errorf("inbound_blocking score not found")
	}
	it, ok := decisionInput.WAFdata.Scores["inbound_threshold"]
	if !ok {
		return waceapi.DecisionResult{}, fmt.Errorf("inbound_threshold score not found")
	}

	wafWeight := decisionInput.WAFWeight

	logger := lg.Get()
	logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "weighted_sum | anomaly score: %v anomaly score threshold: %v", as, it)

	if as >= it {
		weightedSum += wafWeight
	} else {
		weightedSum += (as / it) * wafWeight
	}
	weightsSum += wafWeight

	weightedSum /= weightsSum

	d.mu.RLock()
	threshold := d.threshold
	d.mu.RUnlock()

	logger.TPrintf(lg.DEBUG, decisionInput.TransactionId, "weighted_sum | weighted sum: %v threshold: %v", weightedSum, threshold)
	return waceapi.DecisionResult{Block: weightedSum > threshold}, nil
}

// Reload reads the optional "threshold" param (default 0.5)
func (d *weightedSumDecision) Reload(params map[string]string, meter metric.Meter) error {
	threshold := 0.5
	if stringThreshold, ok := params["threshold"]; ok {
		var err error
		threshold, err = strconv.ParseFloat(stringThreshold, 64)
		if err != nil {
			return fmt.Errorf("error parsing threshold parameter: %v", err)
		}
	}
	d.mu.Lock()
	d.threshold = threshold
	d.mu.Unlock()
	return nil
}

func (d *weightedSumDecision) Clean() error {
	return nil
}

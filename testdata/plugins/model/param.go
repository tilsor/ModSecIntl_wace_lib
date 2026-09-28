/* Param Model Plugin that returns the probability of attack given in its "result" param
 */

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

type paramModel struct {
	mu     sync.RWMutex
	result float64
}

// NewPlugin reads the "result" param and sets the probability that Process will return.
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[param:NewPlugin] %v\n", params)
	m := &paramModel{}
	if err := m.Reload(params, meter); err != nil {
		return nil, err
	}
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "param"), attribute.String("plugin_type", "model")))
	return m, nil
}

func (m *paramModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	logger := lg.Get()
	logger.TPrintf(lg.WARN, input.TransactionId, "[param:Process] \"%v\"\n", input.Payload)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return waceapi.ModelResults{
		ProbAttack: m.result,
		// Data:       input,
	}, nil
}

// Reload updates the probability returned by Process from the new params.
func (m *paramModel) Reload(params map[string]string, meter metric.Meter) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[param:Reload] %v\n", params)
	resultString, ok := params["result"]
	if !ok {
		return fmt.Errorf("result parameter not found")
	}
	result, err := strconv.ParseFloat(resultString, 64)
	if err != nil {
		return fmt.Errorf("error parsing result parameter: %v", err)
	}
	m.mu.Lock()
	m.result = result
	m.mu.Unlock()
	return nil
}

func (m *paramModel) Clean() error {
	return nil
}

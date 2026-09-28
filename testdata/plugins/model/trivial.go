/* Trivial Model Plugin that always returns 0 probability of attack
 */

package main

import (
	"context"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type trivialModel struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[trivial:NewPlugin] %v\n", params)
	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "trivial"), attribute.String("plugin_type", "model")))
	return &trivialModel{}, nil
}

func (m *trivialModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	logger := lg.Get()
	logger.TPrintf(lg.WARN, input.TransactionId, "[trivial:Process] \"%v\"\n", input.Payload)
	result := waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       input,
	}
	return result, nil
}

// Reload reloads the plugin (does nothing in this case)
func (m *trivialModel) Reload(params map[string]string, meter metric.Meter) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[trivial:Reload] %v\n", params)
	return nil
}

func (m *trivialModel) Clean() error {
	return nil
}

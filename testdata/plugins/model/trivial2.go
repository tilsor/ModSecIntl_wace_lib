/* Trivial Model Plugin that always returns 1 probability of attack
 */

package main

import (
	"context"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type trivial2Model struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[trivial2:NewPlugin] %v\n", params)
	// Create counter for plugin register
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "trivial2"), attribute.String("plugin_type", "model")))
	return &trivial2Model{}, nil
}

func (m *trivial2Model) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	logger := lg.Get()
	logger.TPrintf(lg.WARN, input.TransactionId, "[trivial2:Process] \"%v\"\n", input.Payload)
	result := waceapi.ModelResults{
		ProbAttack: 1.0,
		Data:       make(map[string]interface{}),
	}
	return result, nil
}

// Reload reloads the plugin (does nothing in this case)
func (m *trivial2Model) Reload(params map[string]string, meter metric.Meter) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[trivial2:Reload] %v\n", params)
	return nil
}

func (m *trivial2Model) Clean() error {
	return nil
}

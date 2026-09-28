/* Wrong Init model plugin whose NewPlugin returns the concrete type instead of
 * waceapi.ModelPlugin, so the function type assertion in the plugin manager fails
 */

package main

import (
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type wrongInitModel struct{}

// NewPlugin has the wrong return type
func NewPlugin(params map[string]string, meter metric.Meter) (*wrongInitModel, error) {
	return &wrongInitModel{}, nil
}

func (m *wrongInitModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	return waceapi.ModelResults{ProbAttack: 0.0}, nil
}

func (m *wrongInitModel) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (m *wrongInitModel) Clean() error {
	return nil
}

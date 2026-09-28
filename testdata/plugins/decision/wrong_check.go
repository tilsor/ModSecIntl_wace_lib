/* Wrong Check Decision Plugin whose NewPlugin returns the concrete type instead of
 * waceapi.DecisionPlugin, so the function type assertion in the plugin manager fails
 */

package main

import (
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type wrongCheckDecision struct{}

// NewPlugin has the wrong return type
func NewPlugin(params map[string]string, meter metric.Meter) (*wrongCheckDecision, error) {
	return &wrongCheckDecision{}, nil
}

func (d *wrongCheckDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{Block: false}, nil
}

func (d *wrongCheckDecision) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (d *wrongCheckDecision) Clean() error {
	return nil
}

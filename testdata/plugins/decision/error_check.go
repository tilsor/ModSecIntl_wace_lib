/* Error Check Decision Plugin that always returns an error from CheckResults
 */

package main

import (
	"errors"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type errorCheckDecision struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error) {
	return &errorCheckDecision{}, nil
}

// CheckResults always returns an error
func (d *errorCheckDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{}, errors.New("Some error")
}

func (d *errorCheckDecision) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (d *errorCheckDecision) Clean() error {
	return nil
}

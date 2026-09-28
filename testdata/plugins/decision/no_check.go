/* No Check Decision Plugin that implements waceapi.DecisionPlugin but exports no NewPlugin function
 */

package main

import (
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type noCheckDecision struct{}

var _ waceapi.DecisionPlugin = (*noCheckDecision)(nil)

func (d *noCheckDecision) CheckResults(decisionInput waceapi.DecisionInput) (waceapi.DecisionResult, error) {
	return waceapi.DecisionResult{Block: false}, nil
}

func (d *noCheckDecision) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (d *noCheckDecision) Clean() error {
	return nil
}

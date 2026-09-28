/* No Init model plugin that implements waceapi.ModelPlugin but exports no NewPlugin function
 */

package main

import (
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type noInitModel struct{}

var _ waceapi.ModelPlugin = (*noInitModel)(nil)

func (m *noInitModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	return waceapi.ModelResults{ProbAttack: 0.0}, nil
}

func (m *noInitModel) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (m *noInitModel) Clean() error {
	return nil
}

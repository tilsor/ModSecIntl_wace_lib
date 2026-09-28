/* Error Req model plugin that raises an error in Process
 */

package main

import (
	"errors"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type errorReqModel struct{}

// NewPlugin intitalizes the plugin (does nothing in this case)
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	return &errorReqModel{}, nil
}

// Process always returns an error
func (m *errorReqModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	result := waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       make(map[string]interface{}),
	}
	return result, errors.New("Some error")
}

func (m *errorReqModel) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (m *errorReqModel) Clean() error {
	return nil
}

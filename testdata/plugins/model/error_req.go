/* Error Init model plugin that raises an error in Process
 */

package main

import (
	"errors"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

// InitPlugin intitalizes the plugins (does nothing in this case)
func InitPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

// Process always returns 0 probability of attack
func Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	result := waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       make(map[string]interface{}),
	}
	return result, errors.New("Some error")
}

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

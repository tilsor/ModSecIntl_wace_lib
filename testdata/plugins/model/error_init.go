/* Error Init model plugin that raises an error in InitPlugin
 */

package main

import (
	"errors"

	pm "github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"
	"go.opentelemetry.io/otel/metric"
)

// InitPlugin intitalizes the plugins (does nothing in this case)
func InitPlugin(params map[string]string, meter metric.Meter) error {
	return errors.New("Some error")
}

// Process always returns 0 probability of attack
func Process(input pm.ModelInput) (pm.ModelResults, error) {
	result := pm.ModelResults{
		ProbAttack: 0.0,
		Data:       make(map[string]interface{}),
	}
	return result, errors.New("Some error")
}

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

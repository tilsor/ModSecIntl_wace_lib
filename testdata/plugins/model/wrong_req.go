/* Wrong Init model plugin that has wrong InitPlugin type
 */

package main

import "go.opentelemetry.io/otel/metric"

// InitPlugin intitalizes the plugins (does nothing in this case)
func InitPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

// Process always returns 0 probability of attack
func Process() (float64, error) {
	return 0.0, nil
}

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

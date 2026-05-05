/* Trivial Decision Plugin that always returns no attack
 */

package main

import (
	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"go.opentelemetry.io/otel/metric"
)

// InitPlugin intitalizes the plugins (does nothing in this case)
func InitPlugin(params map[string]string) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[simple:InitPlugin] %v\n", params)
	return nil
}

// ReloadPlugin reload the plugin
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

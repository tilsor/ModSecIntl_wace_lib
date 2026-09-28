/* Nil Init model plugin whose NewPlugin returns a nil plugin without an error
 */

package main

import (
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

// NewPlugin returns neither a plugin nor an error
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	return nil, nil
}

/* No Init model plugin that has no InitPlugin function
 */

package main

import "go.opentelemetry.io/otel/metric"

func InitPlugin(params map[string]string, meter metric.Meter) error {
	return nil
}

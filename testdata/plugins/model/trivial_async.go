/* Trivial Async Model Plugin that always returns 0 probability of attack and sleeps for a given time, default 1 second
 */

package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type trivialAsyncModel struct {
	sleepTime float64
}

// NewPlugin intitalizes the plugin reading the optional "sleep_time" param
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[trivial_async:NewPlugin] %v\n", params)
	m := &trivialAsyncModel{sleepTime: 1.0}
	if stringSleepTime, ok := params["sleep_time"]; ok {
		var err error
		m.sleepTime, err = strconv.ParseFloat(stringSleepTime, 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing sleep_time parameter: %v", err)
		}
	}
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return nil, err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "trivial_async"), attribute.String("plugin_type", "model")))
	return m, nil
}

func (m *trivialAsyncModel) Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	time.Sleep(time.Duration(m.sleepTime) * time.Second)
	logger := lg.Get()
	logger.TPrintf(lg.WARN, input.TransactionId, "[trivial_async:Process] \"%v\"\n", input.Payload)
	result := waceapi.ModelResults{
		ProbAttack: 0.0,
		Data:       make(map[string]interface{}),
	}
	return result, nil
}

// Reload reloads the plugin (does nothing in this case)
func (m *trivialAsyncModel) Reload(params map[string]string, meter metric.Meter) error {
	return nil
}

func (m *trivialAsyncModel) Clean() error {
	return nil
}

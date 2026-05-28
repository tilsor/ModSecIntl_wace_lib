package main

import (
	"context"
	"fmt"
	"strconv"

	lg "github.com/tilsor/ModSecIntl_logging/logging"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var result float64

// InitPlugin reads the "result" param and sets the probability that Process will return.
func InitPlugin(params map[string]string, meter metric.Meter) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[param:InitPlugin] %v\n", params)
	resultString, ok := params["result"]
	if !ok {
		return fmt.Errorf("result parameter not found")
	}
	var err error
	result, err = strconv.ParseFloat(resultString, 64)
	if err != nil {
		return fmt.Errorf("error parsing result parameter: %v", err)
	}
	ctx := context.Background()
	pluginCounter, err := meter.Int64Counter("plugin_register")
	if err != nil {
		return err
	}
	pluginCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("plugin_name", "param"), attribute.String("plugin_type", "model")))
	return nil
}

func InitPluginAsync(params map[string]string, meter metric.Meter, natsManager func(func(waceapi.ModelInput) (waceapi.ModelResults, error))) error {
	InitPlugin(params, meter)
	natsManager(Process)
	return nil
}

func Process(input waceapi.ModelInput) (waceapi.ModelResults, error) {
	logger := lg.Get()
	logger.TPrintf(lg.WARN, input.TransactionId, "[param:Process] \"%v\"\n", input.Payload)
	return waceapi.ModelResults{
		ProbAttack: result,
		Data:       input,
	}, nil
}

// ReloadPlugin updates the probability returned by Process from the new params.
func ReloadPlugin(params map[string]string, meter metric.Meter) error {
	logger := lg.Get()
	logger.Printf(lg.WARN, "[param:ReloadPlugin] %v\n", params)
	resultString, ok := params["result"]
	if !ok {
		return fmt.Errorf("result parameter not found")
	}
	var err error
	result, err = strconv.ParseFloat(resultString, 64)
	if err != nil {
		return fmt.Errorf("error parsing result parameter: %v", err)
	}
	return nil
}

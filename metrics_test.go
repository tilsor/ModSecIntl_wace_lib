package wace

import (
	"context"
	"fmt"
	"testing"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gopkg.in/yaml.v3"
)

func parseConfig(t *testing.T, raw []byte) configstore.ConfigFileData {
	t.Helper()
	var conf configstore.ConfigFileData
	if err := yaml.Unmarshal(raw, &conf); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return conf
}

// runParamTransaction analyzes and checks one transaction with the
// param model plugin.
func runParamTransaction(t *testing.T) {
	t.Helper()
	txID := generateRandomID()
	InitTransaction(txID)
	defer CloseTransaction(txID)
	if err := Analyze("Everything", txID, waceapi.HTTPPayload{URI: "/test"}, []string{"param"}); err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, _, err := CheckTransaction(txID, []string{"simple"}, waceapi.WAFData{}); err != nil {
		t.Fatalf("CheckTransaction: %v", err)
	}
}

// findMetric returns the metric with the given name in the scope with
// the given name, and that scope.
func findMetric(t *testing.T, rm metricdata.ResourceMetrics, scope, name string) (metricdata.ScopeMetrics, metricdata.Metrics) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != scope {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name == name {
				return sm, m
			}
		}
	}
	t.Fatalf("metric %q not found in scope %q", name, scope)
	return metricdata.ScopeMetrics{}, metricdata.Metrics{}
}

// TestMetricsBoundedCardinality checks that model results with
// different attack probabilities share a single series per model and
// mode, and that the probability is recorded as a histogram value.
func TestMetricsBoundedCardinality(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	if err := Init(provider, parseConfig(t, configParamWith("0.1")), discardLogger); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer configstore.Clean()

	const n = 9
	for i := 1; i <= n; i++ {
		conf := parseConfig(t, configParamWith(fmt.Sprintf("0.%d", i)))
		if err := Reload(provider, conf, discardLogger); err != nil {
			t.Fatalf("Reload: %v", err)
		}
		runParamTransaction(t)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	wantAttrs := attribute.NewSet(attribute.String(attrModelID, "param"), attribute.String(attrModelMode, modeSync))
	for _, name := range []string{"wace.model.duration", "wace.model.attack_probability"} {
		_, m := findMetric(t, rm, meterScope, name)
		hist, ok := m.Data.(metricdata.Histogram[float64])
		if !ok {
			t.Fatalf("%s: got %T, want a float64 histogram", name, m.Data)
		}
		if len(hist.DataPoints) != 1 {
			t.Fatalf("%s: got %d series, want 1", name, len(hist.DataPoints))
		}
		dp := hist.DataPoints[0]
		if !dp.Attributes.Equals(&wantAttrs) {
			t.Errorf("%s: got attributes %v, want %v", name, dp.Attributes.ToSlice(), wantAttrs.ToSlice())
		}
		if dp.Count != n {
			t.Errorf("%s: got count %d, want %d", name, dp.Count, n)
		}
	}

	_, m := findMetric(t, rm, meterScope, "wace.transaction.checked")
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("wace.transaction.checked: got %T, want an int64 sum", m.Data)
	}
	var total int64
	for _, dp := range sum.DataPoints {
		if _, ok := dp.Attributes.Value(attrBlocked); !ok {
			t.Errorf("wace.transaction.checked: data point without %s attribute", attrBlocked)
		}
		total += dp.Value
	}
	if total != n {
		t.Errorf("wace.transaction.checked: got %d, want %d", total, n)
	}
}

// TestPluginMeterScope checks that each plugin gets a meter whose scope
// identifies the plugin.
func TestPluginMeterScope(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))

	if err := Init(provider, parseConfig(t, configParamWith("0.3")), discardLogger); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer configstore.Clean()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	found := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != "github.com/tilsor/ModSecIntl_wace_lib/plugin" {
			continue
		}
		id, _ := sm.Scope.Attributes.Value(waceapi.LogKeyPlugin)
		found[id.AsString()] = true
	}
	for _, id := range []string{"param", "simple"} {
		if !found[id] {
			t.Errorf("no plugin scope with %s=%s, got %v", waceapi.LogKeyPlugin, id, found)
		}
	}
}

// TestInitNilMeterProvider checks that Init and Reload accept a nil
// meter provider.
func TestInitNilMeterProvider(t *testing.T) {
	if err := Init(nil, parseConfig(t, configParamWith("0.3")), discardLogger); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer configstore.Clean()
	runParamTransaction(t)

	if err := Reload(nil, parseConfig(t, configParamWith("0.5")), discardLogger); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	runParamTransaction(t)
}

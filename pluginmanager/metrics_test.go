package pluginmanager

import (
	"context"
	"log/slog"
	"testing"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gopkg.in/yaml.v3"
)

// collectLoaded returns the wace.plugins.loaded series collected by
// reader, keyed by plugin.type and plugin.mode. It returns nil if the
// metric was not collected.
func collectLoaded(t *testing.T, reader *metric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != meterScope {
			continue
		}
		for _, m := range sm.Metrics {
			if m.Name != "wace.plugins.loaded" {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("wace.plugins.loaded: got %T, want an int64 gauge", m.Data)
			}
			got := make(map[string]int64, len(gauge.DataPoints))
			for _, dp := range gauge.DataPoints {
				kind, _ := dp.Attributes.Value(waceapi.LogKeyPluginType)
				mode, _ := dp.Attributes.Value(attrPluginMode)
				got[kind.AsString()+"/"+mode.AsString()] = dp.Value
			}
			return got
		}
	}
	return nil
}

func checkLoaded(t *testing.T, got map[string]int64, want map[string]int64) {
	t.Helper()
	if len(got) != len(loadedKeys) {
		t.Errorf("got %d series, want %d: %v", len(got), len(loadedKeys), got)
	}
	for _, k := range loadedKeys {
		key := k.kind.logValue() + "/" + k.mode
		if got[key] != want[key] {
			t.Errorf("%s: got %d, want %d", key, got[key], want[key])
		}
	}
}

// TestPluginManagerLoadedMetric checks that wace.plugins.loaded follows
// the plugins loaded and unloaded by Reload, and that Reload moves the
// metric to the new meter provider.
func TestPluginManagerLoadedMetric(t *testing.T) {
	configstore.Clean()
	t.Cleanup(configstore.Clean)
	var aux configstore.ConfigFileData
	config := baseConfig + "model_plugins:\n" + trivialPlugin + trivial2Plugin + "decision_plugins:\n" + simplePlugin + testPlugin
	if err := yaml.Unmarshal([]byte(config), &aux); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if _, err := configstore.SetConfig(aux); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	reader := metric.NewManualReader()
	pm, err := New(metric.NewMeterProvider(metric.WithReader(reader)), discardLogger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	checkLoaded(t, collectLoaded(t, reader), map[string]int64{
		"model/sync":          2,
		"decision/production": 2,
	})

	applyConfig(t, baseConfig+"model_plugins:\n"+trivialTrainingPlugin+"decision_plugins:\n"+simplePlugin)
	newReader := metric.NewManualReader()
	if err := pm.Reload(metric.NewMeterProvider(metric.WithReader(newReader)), discardLogger); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	checkLoaded(t, collectLoaded(t, newReader), map[string]int64{
		"model/training":      1,
		"decision/production": 1,
	})
	if got := collectLoaded(t, reader); len(got) != 0 {
		t.Errorf("old meter provider still reports wace.plugins.loaded: %v", got)
	}
}

// TestPluginManagerNilMeterProvider checks that New and Reload accept a
// nil meter provider.
func TestPluginManagerNilMeterProvider(t *testing.T) {
	configstore.Clean()
	t.Cleanup(configstore.Clean)
	applyConfig(t, baseConfig+"model_plugins:\n"+trivialPlugin+"decision_plugins:\n"+simplePlugin)

	pm, err := New(nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := pm.Reload(nil, discardLogger); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, err := processProb(t, pm, "trivial"); err != nil {
		t.Errorf("Process(\"trivial\"): %v", err)
	}
}

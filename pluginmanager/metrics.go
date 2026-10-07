package pluginmanager

import (
	"context"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meterScope is the instrumentation scope name of the plugin manager
// metrics.
const meterScope = "github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"

// attrPluginMode is the attribute key of the mode of a plugin.
const attrPluginMode = "plugin.mode"

// Values of the plugin.mode attribute.
const (
	modeSync       = "sync"
	modeAsync      = "async"
	modeRemote     = "remote"
	modeTraining   = "training"
	modeProduction = "production"
)

// loadedKey identifies a series of wace.plugins.loaded.
type loadedKey struct {
	kind pluginKind
	mode string
}

// loadedKeys are every series of wace.plugins.loaded. All of them are
// observed, with 0 when no plugin matches, so that a series does not go
// stale when its last plugin is unloaded.
var loadedKeys = []loadedKey{
	{modelKind, modeSync},
	{modelKind, modeAsync},
	{modelKind, modeRemote},
	{modelKind, modeTraining},
	{decisionKind, modeProduction},
	{decisionKind, modeTraining},
}

// loadedAttrs are the precomputed attributes of each loadedKeys series.
var loadedAttrs = func() map[loadedKey]metric.ObserveOption {
	attrs := make(map[loadedKey]metric.ObserveOption, len(loadedKeys))
	for _, k := range loadedKeys {
		attrs[k] = metric.WithAttributes(
			attribute.String(waceapi.LogKeyPluginType, k.kind.logValue()),
			attribute.String(attrPluginMode, k.mode))
	}
	return attrs
}()

// modelMode returns the plugin.mode of the model plugin with the given
// id. It follows the precedence callPlugins uses to dispatch it.
func modelMode(conf *configstore.ConfigStore, id string) string {
	switch {
	case conf.IsAsync(id):
		return modeAsync
	case conf.IsInTraining(id):
		return modeTraining
	case conf.IsRemote(id):
		return modeRemote
	default:
		return modeSync
	}
}

// decisionMode returns the plugin.mode of the decision plugin with the
// given id.
func decisionMode(conf *configstore.ConfigStore, id string) string {
	if conf.IsDecisionInTraining(id) {
		return modeTraining
	}
	return modeProduction
}

// registerMetrics creates the plugin manager instruments with a meter
// of provider, replacing the ones registered before. The caller must
// hold reloadMutex, or be New.
func (pm *PluginManager) registerMetrics(provider metric.MeterProvider) error {
	meter := provider.Meter(meterScope)
	loaded, err := meter.Int64ObservableGauge("wace.plugins.loaded",
		metric.WithDescription("Plugins currently loaded."),
		metric.WithUnit("{plugin}"))
	if err != nil {
		return err
	}
	reg, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for k, n := range pm.countLoaded() {
			o.ObserveInt64(loaded, n, loadedAttrs[k])
		}
		return nil
	}, loaded)
	if err != nil {
		return err
	}

	if pm.metricsReg != nil {
		if err := pm.metricsReg.Unregister(); err != nil {
			pm.getLogger().Warn("cannot unregister metrics callback", "error", err)
		}
	}
	pm.metricsReg = reg
	return nil
}

// countLoaded returns the number of loaded plugins of each loadedKeys
// series. It returns nil if there is no configuration.
func (pm *PluginManager) countLoaded() map[loadedKey]int64 {
	conf, err := configstore.Get()
	if err != nil {
		return nil
	}
	counts := make(map[loadedKey]int64, len(loadedKeys))
	for _, k := range loadedKeys {
		counts[k] = 0
	}

	pm.modelMutex.RLock()
	for id := range pm.modelPlugins {
		counts[loadedKey{modelKind, modelMode(conf, id)}]++
	}
	pm.modelMutex.RUnlock()

	pm.decisionMutex.RLock()
	for id := range pm.decisionPlugins {
		counts[loadedKey{decisionKind, decisionMode(conf, id)}]++
	}
	pm.decisionMutex.RUnlock()
	return counts
}

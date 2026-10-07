package wace

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// meterScope is the instrumentation scope name of the core metrics.
const meterScope = "github.com/tilsor/ModSecIntl_wace_lib"

// Attribute keys of the core metrics. Their values must come from the
// configuration or a fixed set, never from the traffic, to keep the
// cardinality bounded.
const (
	attrModelID   = "model_id"
	attrModelMode = "model_mode"
	attrBlocked   = "blocked"
)

// Values of the model_mode attribute.
const (
	modeSync  = "sync"
	modeAsync = "async"
)

// durationBuckets are the bucket boundaries, in seconds, of
// wace.model.duration: from 100µs to 10s.
var durationBuckets = []float64{
	0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05,
	0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

// probabilityBuckets are the bucket boundaries of
// wace.model.attack_probability.
var probabilityBuckets = []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1}

// coreMetrics holds the instruments of the core. They are created once
// by Init and Reload, never on the request path.
type coreMetrics struct {
	modelDuration metric.Float64Histogram
	attackProb    metric.Float64Histogram
	modelErrors   metric.Int64Counter
	modelTimeouts metric.Int64Counter
	txChecked     metric.Int64Counter
}

// coreMetricsPtr holds the current core instruments. It is replaced by
// Init and Reload while transactions may be reading it.
var coreMetricsPtr atomic.Pointer[coreMetrics]

func init() {
	// noop instruments never fail
	m, _ := newCoreMetrics(noop.NewMeterProvider())
	coreMetricsPtr.Store(m)
}

// getMetrics returns the current core instruments.
func getMetrics() *coreMetrics {
	return coreMetricsPtr.Load()
}

// meterProviderOrNoop returns mp, or a provider that records nothing
// if mp is nil.
func meterProviderOrNoop(mp metric.MeterProvider) metric.MeterProvider {
	if mp == nil {
		return noop.NewMeterProvider()
	}
	return mp
}

// newCoreMetrics creates the core instruments with a meter of mp.
func newCoreMetrics(mp metric.MeterProvider) (*coreMetrics, error) {
	meter := mp.Meter(meterScope)
	var m coreMetrics
	var err, errs error

	m.modelDuration, err = meter.Float64Histogram("wace.model.duration",
		metric.WithDescription("Time from dispatching a payload to a model plugin until the core receives its result."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(durationBuckets...))
	errs = errors.Join(errs, err)

	m.attackProb, err = meter.Float64Histogram("wace.model.attack_probability",
		metric.WithDescription("Attack probability returned by a model plugin."),
		metric.WithUnit("1"),
		metric.WithExplicitBucketBoundaries(probabilityBuckets...))
	errs = errors.Join(errs, err)

	m.modelErrors, err = meter.Int64Counter("wace.model.errors",
		metric.WithDescription("Model plugin calls that returned an error."),
		metric.WithUnit("{call}"))
	errs = errors.Join(errs, err)

	m.modelTimeouts, err = meter.Int64Counter("wace.model.timeouts",
		metric.WithDescription("Model plugin calls abandoned because the model timeout expired."),
		metric.WithUnit("{call}"))
	errs = errors.Join(errs, err)

	m.txChecked, err = meter.Int64Counter("wace.transaction.checked",
		metric.WithDescription("Transactions checked by the decision plugins."),
		metric.WithUnit("{transaction}"))
	errs = errors.Join(errs, err)

	if errs != nil {
		return nil, errs
	}
	return &m, nil
}

// recordModelResult records the duration and, if the call succeeded,
// the attack probability of a model plugin result.
func (m *coreMetrics) recordModelResult(ctx context.Context, modelID, mode string, elapsed time.Duration, probAttack float64, err error) {
	attrs := metric.WithAttributes(attribute.String(attrModelID, modelID), attribute.String(attrModelMode, mode))
	m.modelDuration.Record(ctx, elapsed.Seconds(), attrs)
	if err != nil {
		m.modelErrors.Add(ctx, 1, attrs)
		return
	}
	m.attackProb.Record(ctx, probAttack, attrs)
}

// recordTimeouts records n model plugin calls abandoned by a timeout.
func (m *coreMetrics) recordTimeouts(ctx context.Context, mode string, n int) {
	m.modelTimeouts.Add(ctx, int64(n), metric.WithAttributes(attribute.String(attrModelMode, mode)))
}

// recordChecked records a transaction checked by the decision plugins.
func (m *coreMetrics) recordChecked(ctx context.Context, blocked bool) {
	m.txChecked.Add(ctx, 1, metric.WithAttributes(attribute.Bool(attrBlocked, blocked)))
}

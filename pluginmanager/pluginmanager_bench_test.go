package pluginmanager

import (
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
)

// benchAsyncPhases are the plugin types of the async channels a
// transaction registers, one per phase.
var benchAsyncPhases = []configstore.ModelPluginType{
	configstore.RequestHeaders,
	configstore.RequestBody,
	configstore.ResponseHeaders,
	configstore.ResponseBody,
}

// benchAsyncTxCounter makes transaction ids unique across benchmarks and
// parallel goroutines.
var benchAsyncTxCounter atomic.Uint64

// asyncChannelsLifecycle registers an async channel per phase for a new
// transaction, looks each one up as ModelResultsHandler does, and
// removes them, as the async waiters of callPlugins do.
func asyncChannelsLifecycle(b *testing.B, pm *PluginManager, ch chan ModelStatus) {
	txID := "bench-" + strconv.FormatUint(benchAsyncTxCounter.Add(1), 10)
	for _, t := range benchAsyncPhases {
		pm.AddModelChannel(txID, t, ch, "async")
	}
	for _, t := range benchAsyncPhases {
		if tx, ok := pm.loadAsyncTx(txID); !ok || tx.channel(t) != ch {
			b.Fatalf("async channel of type %v not found", t)
		}
	}
	for _, t := range benchAsyncPhases {
		pm.RemoveAsyncModelChannel(txID, t)
	}
}

// BenchmarkAsyncModelChannels measures the async channel bookkeeping of
// a transaction, one transaction at a time and with GOMAXPROCS
// transactions in flight.
func BenchmarkAsyncModelChannels(b *testing.B) {
	ch := make(chan ModelStatus, 1)
	b.Run("sequential", func(b *testing.B) {
		pm := newDiscardPluginManager()
		b.ReportAllocs()
		for b.Loop() {
			asyncChannelsLifecycle(b, pm, ch)
		}
	})
	b.Run("parallel", func(b *testing.B) {
		pm := newDiscardPluginManager()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				asyncChannelsLifecycle(b, pm, ch)
			}
		})
	})
}

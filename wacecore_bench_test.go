package wace

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/pluginmanager"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
)

// benchModelCounts are the number of sync model plugins analyzing each
// transaction in the end-to-end benchmarks.
var benchModelCounts = []int{1, 4, 16}

// benchTxCounter makes transaction ids unique across benchmarks and
// parallel goroutines.
var benchTxCounter atomic.Uint64

// configWithSyncModels returns a config with n local sync model plugins,
// all backed by trivial.so, and the ids of those models.
func configWithSyncModels(n int) ([]byte, []string) {
	var sb strings.Builder
	sb.WriteString("---\nmodel_plugins:\n")
	ids := make([]string, n)
	for i := range n {
		ids[i] = "trivial" + strconv.Itoa(i)
		fmt.Fprintf(&sb, "  - id: %q\n    plugin_type: RequestHeaders\n    path: \"testdata/plugins/model/trivial.so\"\n", ids[i])
	}
	sb.WriteString("decision_plugins:\n  - id: \"simple\"\n    path: \"testdata/plugins/decision/simple.so\"\n")
	return []byte(sb.String()), ids
}

// runTransaction drives a whole transaction through the public API, the
// same way a WAF connector does.
func runTransaction(b *testing.B, models []string, wafParams waceapi.WAFData) {
	txID := "bench-" + strconv.FormatUint(benchTxCounter.Add(1), 10)
	InitTransaction(txID)
	if err := Analyze("RequestHeaders", txID, requestHeadersPayload, models); err != nil {
		b.Errorf("Analyze: %v", err)
	}
	if _, _, err := CheckTransaction(txID, []string{"simple"}, wafParams); err != nil {
		b.Errorf("CheckTransaction: %v", err)
	}
	CloseTransaction(txID)
}

// BenchmarkTransactionSync measures a full transaction (InitTransaction,
// Analyze, CheckTransaction, CloseTransaction) with n local sync model
// plugins, one transaction at a time.
func BenchmarkTransactionSync(b *testing.B) {
	wafParams := parseWAFParams("inbound_blocking=0,inbound_threshold=5")
	for _, n := range benchModelCounts {
		b.Run(fmt.Sprintf("models=%d", n), func(b *testing.B) {
			conf, models := configWithSyncModels(n)
			if err := initialize(conf); err != nil {
				b.Fatalf("initialize: %v", err)
			}
			defer configstore.Clean()

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				runTransaction(b, models, wafParams)
			}
		})
	}
}

// BenchmarkTransactionSyncParallel is BenchmarkTransactionSync with
// GOMAXPROCS transactions in flight at the same time, which is where
// channel contention would show up.
func BenchmarkTransactionSyncParallel(b *testing.B) {
	wafParams := parseWAFParams("inbound_blocking=0,inbound_threshold=5")
	for _, n := range benchModelCounts {
		b.Run(fmt.Sprintf("models=%d", n), func(b *testing.B) {
			conf, models := configWithSyncModels(n)
			if err := initialize(conf); err != nil {
				b.Fatalf("initialize: %v", err)
			}
			defer configstore.Clean()

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					runTransaction(b, models, wafParams)
				}
			})
		})
	}
}

// BenchmarkStatusChannel isolates the channel pattern used by
// callPlugins: n goroutines report one ModelStatus each and a single
// receiver collects them, with an unbuffered channel versus one
// buffered with room for every status.
func BenchmarkStatusChannel(b *testing.B) {
	for _, n := range benchModelCounts {
		for _, buffered := range []bool{false, true} {
			name := fmt.Sprintf("models=%d/unbuffered", n)
			capacity := 0
			if buffered {
				name = fmt.Sprintf("models=%d/buffered", n)
				capacity = n
			}
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					ch := make(chan pluginmanager.ModelStatus, capacity)
					for i := range n {
						go func() {
							ch <- pluginmanager.ModelStatus{ModelID: "m", ProbAttack: float64(i)}
						}()
					}
					for range n {
						<-ch
					}
				}
			})
		}
	}
}

package wace

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// benchBodySizes are the request body sizes used by the payload
// benchmarks: a small form, a typical API body, and 128KiB, the default
// request body size Coraza keeps in memory.
var benchBodySizes = []int{1 << 10, 16 << 10, 128 << 10}

// formBody returns an urlencoded body of about size bytes. If withPassword
// is true it ends in a password field, so sanitizeCredentials has
// something to replace.
func formBody(size int, withPassword bool) string {
	var sb strings.Builder
	sb.Grow(size + 32)
	for i := 0; sb.Len() < size; i++ {
		fmt.Fprintf(&sb, "field%d=value%d&", i, i)
	}
	if withPassword {
		sb.WriteString("password=hunter2")
	}
	return sb.String()
}

// benchCredentialHeaders mirrors the configstore default for
// credential_headers.
var benchCredentialHeaders = []string{"authorization", "cookie", "set-cookie"}

// credentialHeadersPayload returns request headers that include the
// default credential headers.
func credentialHeadersPayload() []waceapi.HTTPHeader {
	h := append([]waceapi.HTTPHeader{}, requestHeaders...)
	return append(h,
		waceapi.HTTPHeader{Key: "Authorization", Value: "Bearer abcdef0123456789"},
		waceapi.HTTPHeader{Key: "Cookie", Value: "session=0123456789abcdef"},
	)
}

// BenchmarkSanitizeCredentials measures sanitizeCredentials, which runs
// once per Analyze call, for bodies of increasing size with and without
// a password field.
func BenchmarkSanitizeCredentials(b *testing.B) {
	b.Run("headers-only", func(b *testing.B) {
		p := waceapi.HTTPPayload{URI: requestURI, RequestHeaders: credentialHeadersPayload()}
		b.ReportAllocs()
		for b.Loop() {
			sanitizeCredentials(p, benchCredentialHeaders)
		}
	})
	for _, size := range benchBodySizes {
		for _, withPassword := range []bool{false, true} {
			name := fmt.Sprintf("body=%dKiB/password=%t", size>>10, withPassword)
			b.Run(name, func(b *testing.B) {
				p := waceapi.HTTPPayload{URI: requestURI, RequestBody: formBody(size, withPassword)}
				b.SetBytes(int64(len(p.RequestBody)))
				b.ReportAllocs()
				for b.Loop() {
					sanitizeCredentials(p, benchCredentialHeaders)
				}
			})
		}
	}
}

// configWithBodyModel returns a config with a single local sync
// RequestBody model plugin, with or without credential sanitization.
func configWithBodyModel(sanitize bool) []byte {
	return []byte(fmt.Sprintf(`---
model_plugins:
  - id: "body"
    plugin_type: RequestBody
    path: "testdata/plugins/model/trivial.so"
    sanitize: %t
decision_plugins:
  - id: "simple"
    path: "testdata/plugins/decision/simple.so"
`, sanitize))
}

// BenchmarkTransactionBodySize measures a full transaction whose single
// model analyzes the request body, for increasing body sizes, with and
// without credential sanitization enabled for the model.
func BenchmarkTransactionBodySize(b *testing.B) {
	wafParams := parseWAFParams("inbound_blocking=0,inbound_threshold=5")
	models := []string{"body"}
	for _, sanitize := range []bool{false, true} {
		for _, size := range benchBodySizes {
			b.Run(fmt.Sprintf("sanitize=%t/body=%dKiB", sanitize, size>>10), func(b *testing.B) {
				if err := initialize(configWithBodyModel(sanitize)); err != nil {
					b.Fatalf("initialize: %v", err)
				}
				defer configstore.Clean()
				payload := waceapi.HTTPPayload{
					URI:            requestURI,
					Method:         requestMethod,
					HTTPVersion:    requestVersion,
					RequestHeaders: credentialHeadersPayload(),
					RequestBody:    formBody(size, true),
				}

				b.SetBytes(int64(len(payload.RequestBody)))
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					txID := "bench-" + strconv.FormatUint(benchTxCounter.Add(1), 10)
					InitTransaction(txID)
					if err := Analyze("RequestBody", txID, payload, models); err != nil {
						b.Errorf("Analyze: %v", err)
					}
					if _, _, err := CheckTransaction(txID, []string{"simple"}, wafParams); err != nil {
						b.Errorf("CheckTransaction: %v", err)
					}
					CloseTransaction(txID)
				}
			})
		}
	}
}

// BenchmarkTransactionPhases measures the flow of a WAF connector that
// calls Analyze once per phase (request headers, request body, response
// headers, response body) with one model per phase, and checks the
// transaction after each of the request and response sides.
func BenchmarkTransactionPhases(b *testing.B) {
	if err := initialize(configAllModels); err != nil {
		b.Fatalf("initialize: %v", err)
	}
	defer configstore.Clean()
	wafParams := parseWAFParams("inbound_blocking=0,inbound_threshold=5")
	phases := []struct {
		pluginType string
		model      string
		payload    waceapi.HTTPPayload
	}{
		{"RequestHeaders", "trivialRequestHeaders", requestHeadersPayload},
		{"RequestBody", "trivialRequestBody", wholeRequest},
		{"ResponseHeaders", "trivialResponseHeaders", responseHeadersPayload},
		{"ResponseBody", "trivialResponseBody", wholeResponse},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		txID := "bench-" + strconv.FormatUint(benchTxCounter.Add(1), 10)
		InitTransaction(txID)
		for i, ph := range phases {
			if err := Analyze(ph.pluginType, txID, ph.payload, []string{ph.model}); err != nil {
				b.Errorf("Analyze %s: %v", ph.pluginType, err)
			}
			// check after the request body and after the response body
			if i%2 == 1 {
				if _, _, err := CheckTransaction(txID, []string{"simple"}, wafParams); err != nil {
					b.Errorf("CheckTransaction: %v", err)
				}
			}
		}
		CloseTransaction(txID)
	}
}

// configWithDecision returns a config with n local sync model plugins
// and the given decision plugin, weighting every model equally.
func configWithDecision(n int, decision string) ([]byte, []string) {
	var sb strings.Builder
	sb.WriteString("---\nmodel_plugins:\n")
	ids := make([]string, n)
	for i := range n {
		ids[i] = "trivial" + strconv.Itoa(i)
		fmt.Fprintf(&sb, "  - id: %q\n    plugin_type: RequestHeaders\n    path: \"testdata/plugins/model/trivial.so\"\n", ids[i])
	}
	fmt.Fprintf(&sb, "decision_plugins:\n  - id: %q\n    path: \"testdata/plugins/decision/%s.so\"\n    waf_weight: 0.5\n    model_weights:\n", decision, decision)
	for _, id := range ids {
		fmt.Fprintf(&sb, "      %s: 1\n", id)
	}
	return []byte(sb.String()), ids
}

// BenchmarkCheckTransaction isolates CheckTransaction: the models run
// once, and each iteration only gathers their results and calls the
// decision plugin.
func BenchmarkCheckTransaction(b *testing.B) {
	wafParams := parseWAFParams("inbound_blocking=0,inbound_threshold=5")
	for _, decision := range []string{"simple", "weighted_sum"} {
		for _, n := range benchModelCounts {
			b.Run(fmt.Sprintf("decision=%s/models=%d", decision, n), func(b *testing.B) {
				conf, models := configWithDecision(n, decision)
				if err := initialize(conf); err != nil {
					b.Fatalf("initialize: %v", err)
				}
				defer configstore.Clean()
				txID := "bench-" + strconv.FormatUint(benchTxCounter.Add(1), 10)
				InitTransaction(txID)
				defer CloseTransaction(txID)
				if err := Analyze("RequestHeaders", txID, requestHeadersPayload, models); err != nil {
					b.Fatalf("Analyze: %v", err)
				}
				if _, found, err := CheckTransaction(txID, []string{decision}, wafParams); err != nil || !found {
					b.Fatalf("CheckTransaction: found=%t err=%v", found, err)
				}

				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, _, err := CheckTransaction(txID, []string{decision}, wafParams); err != nil {
						b.Errorf("CheckTransaction: %v", err)
					}
				}
			})
		}
	}
}

// BenchmarkTransactionRemote measures a full transaction with n remote
// sync model plugins, which round-trip through NATS. It needs a NATS
// server, given in WACE_BENCH_NATS_URL (e.g. nats://localhost:4222), and
// is skipped otherwise. The model side runs in this same process.
func BenchmarkTransactionRemote(b *testing.B) {
	natsURL := os.Getenv("WACE_BENCH_NATS_URL")
	if natsURL == "" {
		b.Skip("WACE_BENCH_NATS_URL not set")
	}
	wafParams := parseWAFParams("inbound_blocking=0,inbound_threshold=5")
	process := func(context.Context, waceapi.ModelInput) (waceapi.ModelResults, error) {
		return waceapi.ModelResults{}, nil
	}
	for _, n := range benchModelCounts {
		b.Run(fmt.Sprintf("models=%d", n), func(b *testing.B) {
			run := benchTxCounter.Add(1)
			var sb strings.Builder
			fmt.Fprintf(&sb, "---\nnats_url: %q\nmodel_plugins:\n", natsURL)
			models := make([]string, n)
			for i := range n {
				// Handlers are never stopped, so every run (including
				// each -count repetition) needs its own subjects, or the
				// handlers left over from earlier runs answer too.
				models[i] = fmt.Sprintf("remote%d-%d", run, i)
				fmt.Fprintf(&sb, "  - id: %q\n    plugin_type: RequestHeaders\n    path: \"testdata/plugins/model/trivial.so\"\n    remote: true\n", models[i])
			}
			sb.WriteString("decision_plugins:\n  - id: \"simple\"\n    path: \"testdata/plugins/decision/simple.so\"\n")
			if err := initialize([]byte(sb.String())); err != nil {
				b.Fatalf("initialize: %v", err)
			}
			defer configstore.Clean()
			for _, id := range models {
				ph, err := pm.ModelProcessHandler(id, process)
				if err != nil {
					b.Fatalf("ModelProcessHandler: %v", err)
				}
				defer ph.Stop()
				rh, err := pm.ModelResultsHandler(id)
				if err != nil {
					b.Fatalf("ModelResultsHandler: %v", err)
				}
				defer rh.Stop()
			}
			// give the subscriptions time to reach the server
			time.Sleep(100 * time.Millisecond)

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				runTransaction(b, models, wafParams)
			}
		})
	}
}

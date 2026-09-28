# WACElib

The general objective of this project is to build machine
learning-assisted web application firewall mechanisms for the
identification, analysis and prevention of computer attacks on web
applications. The main idea is to combine the flexibility provided by
the classification procedures obtained from machine learning models
with the codified knowledge integrated in the specification of the
[OWASP Core Rule Set](https://coreruleset.org/) used by the [ModSecurity WAF](https://www.modsecurity.org/) to detect attacks, while
reducing false positives. The next figure shows a high-level
overview of the architecture:

![WACE architecture overview](https://github.com/tilsor/ModSecIntl_wace_core/blob/main/docs/images/architecture.jpg?raw=true "WACE architecture overview")

This repository contains the library that implements the core of WACE. A WAF
integrates it directly as a Go library (for example, `wace-coraza` for the
Coraza WAF), or through the WACE server, which exposes it over gRPC (for
example, for ModSecurity).

WACE combines two kinds of plugins, both loaded at runtime as Go plugins (`.so`):

- **Model plugins** analyze a part of the HTTP transaction and return a
  probability of attack (`ModelResults`).
- **Decision plugins** combine the model results with the WAF scores and decide
  whether to block the transaction (`DecisionResult`).

Both kinds of plugins can also run in **training mode**: they run in the
background without affecting the blocking decision, and the samples they
produce are saved to disk.

## Installation

```sh
go get github.com/tilsor/ModSecIntl_wace_lib
```

Requires Go 1.26 or later, on Linux or another platform supported by the Go
`plugin` package.

## Usage

The `wace` package exports the following functions:

| Function | Description |
|---|---|
| `Init(meter, conf) error` | Initializes WACE with a configuration and an OpenTelemetry meter, and loads the plugins. Call it once, before anything else. |
| `Reload(meter, conf) error` | Applies a new configuration: reloads the params of existing plugins, loads new ones, and unloads the ones that were removed. |
| `InitTransaction(id)` | Starts a transaction with the given identifier. Call it once per transaction. |
| `Analyze(modelType, id, payload, models) error` | Runs the given model plugins on a part of the transaction. `modelType` is one of `RequestHeaders`, `RequestBody`, `AllRequest`, `ResponseHeaders`, `ResponseBody`, `AllResponse` or `Everything`. It returns immediately; the models run in the background. |
| `CheckTransaction(id, decisionPlugins, wafData) (block, decided bool, err error)` | Waits for the sync models started so far and runs the given decision plugins. At most one of them may be a production plugin; the others must be in training. `block` is the verdict of the production plugin, and `decided` reports whether one was found. A call with only training plugins returns `block == false`. |
| `CloseTransaction(id)` | Ends the transaction and releases its data. Call it once, when the analysis is complete. |

A transaction follows the order `InitTransaction` → `Analyze` →
`CheckTransaction` → `CloseTransaction`. `Analyze` and `CheckTransaction` can
alternate several times within one transaction (for example, analyze and check
the request, then analyze and check the response).

### Example

```go
package main

import (
	"log"
	"os"

	wace "github.com/tilsor/ModSecIntl_wace_lib"
	"github.com/tilsor/ModSecIntl_wace_lib/configstore"
	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric/noop"
	"gopkg.in/yaml.v3"
)

func main() {
	raw, err := os.ReadFile("wace.yaml")
	if err != nil {
		log.Fatal(err)
	}
	var conf configstore.ConfigFileData
	if err := yaml.Unmarshal(raw, &conf); err != nil {
		log.Fatal(err)
	}

	meter := noop.NewMeterProvider().Meter("wace")
	if err := wace.Init(meter, conf); err != nil {
		log.Fatal(err)
	}

	txID := "tx-0001"
	wace.InitTransaction(txID)
	defer wace.CloseTransaction(txID)

	payload := waceapi.HTTPPayload{
		Method:         "GET",
		URI:            "/login?user=admin",
		HTTPVersion:    "HTTP/1.1",
		RequestHeaders: []waceapi.HTTPHeader{{Key: "Host", Value: "example.com"}},
	}
	if err := wace.Analyze("RequestHeaders", txID, payload, []string{"model_app_a"}); err != nil {
		log.Fatal(err)
	}

	wafData := waceapi.WAFData{Scores: map[string]float64{
		"inbound_blocking":  10,
		"inbound_threshold": 5,
	}}
	block, decided, err := wace.CheckTransaction(txID, []string{"weighted"}, wafData)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("block=%t (production decision plugin found: %t)", block, decided)
}
```

## Configuration

`Init` and `Reload` take a `configstore.ConfigFileData`, usually parsed from a
YAML file:

```yaml
logpath: /var/log/wace/wace.log
loglevel: INFO                    # ERROR, WARN, INFO or DEBUG
natsurl: nats://localhost:4222    # only needed for async or remote models
credential_headers:               # headers masked when a model has sanitize: true
  - Authorization
  - Cookie

model_plugins:
  - id: model_app_a
    path: /opt/wace/plugins/model.so
    plugin_type: RequestHeaders   # part of the transaction the model analyzes
    params:                       # passed to NewPlugin and Reload
      threshold: "0.3"
  - id: model_app_b               # same binary, independent instance and params
    path: /opt/wace/plugins/model.so
    plugin_type: RequestHeaders
    params:
      threshold: "0.8"
    sanitize: true                # mask credentials before calling the model
  - id: model_async
    path: /opt/wace/plugins/slow_model.so
    plugin_type: Everything
    async: true                   # runs through NATS; CheckTransaction does not wait for it
  - id: model_candidate
    path: /opt/wace/plugins/candidate.so
    plugin_type: Everything
    training: true                # shadow mode: never affects the decision
    training_data:
      min_samples: 1000
      max_samples: 10000
      result_file_path: /var/lib/wace/model_candidate.jsonl
      status_file_path: /var/lib/wace/model_candidate.status
      status_update_interval: 100

decision_plugins:
  - id: weighted
    path: /opt/wace/plugins/weighted_sum.so
    model_weights:                # per model id
      model_app_a: 0.5
    waf_weight: 0.5
    params:
      threshold: "0.5"
  - id: weighted_v2
    path: /opt/wace/plugins/weighted_sum_v2.so
    model_weights:
      model_app_a: 0.3
    waf_weight: 0.7
    training: true
    training_data:
      max_samples: 5000
      result_file_path: /var/lib/wace/weighted_v2.jsonl
      status_file_path: /var/lib/wace/weighted_v2.status
```

Notes:

- Several IDs can point to **the same `.so`**: each ID gets its own plugin
  instance with its own params. Copies of the same `.so` at different paths are
  **not** supported. The Go runtime loads only one of them (see
  [ADR 0002](docs/adr/0002-instance-based-plugin-api.md)).
- A model cannot be in training and `async` or `remote` at the same time.
- `status_update_interval` defaults to 10% of `max_samples` (at least 1).
- In training mode each sample is appended to `result_file_path` as one JSON
  line, and the collection status (`Collecting`, `Ready`, `Done`, `Error`) is
  written atomically to `status_file_path`. See
  [ADR 0001](docs/adr/0001-trainable-decision-plugins.md) for how decision
  plugin training works.

## Writing plugins

A plugin is a Go `main` package built with `-buildmode=plugin`. It exports a
single function, `NewPlugin`, which returns an instance that implements the
`waceapi.ModelPlugin` or `waceapi.DecisionPlugin` interface:

```go
type ModelPlugin interface {
	Process(ModelInput) (ModelResults, error)
	Reload(params map[string]string, meter metric.Meter) error
	Clean() error
}

type DecisionPlugin interface {
	CheckResults(DecisionInput) (DecisionResult, error)
	Reload(params map[string]string, meter metric.Meter) error
	Clean() error
}
```

A minimal model plugin:

```go
package main

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/tilsor/ModSecIntl_wace_lib/waceapi"
	"go.opentelemetry.io/otel/metric"
)

type myModel struct {
	mu        sync.RWMutex
	threshold float64
}

// NewPlugin must return the interface type, not *myModel.
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
	m := &myModel{}
	if err := m.Reload(params, meter); err != nil {
		return nil, err
	}
	// One-time setup goes here: register metrics, load model files, ...
	return m, nil
}

func (m *myModel) Process(in waceapi.ModelInput) (waceapi.ModelResults, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// in.Training is true when the call is only collecting a training sample.
	return waceapi.ModelResults{ProbAttack: 0, Data: nil}, nil
}

// Reload applies new params. Parse first and assign only on success, so a
// rejected reload keeps the previous configuration.
func (m *myModel) Reload(params map[string]string, meter metric.Meter) error {
	threshold, err := strconv.ParseFloat(params["threshold"], 64)
	if err != nil {
		return fmt.Errorf("invalid threshold: %v", err)
	}
	m.mu.Lock()
	m.threshold = threshold
	m.mu.Unlock()
	return nil
}

// Clean releases what the instance created. It is called once, when the
// plugin is removed from the configuration.
func (m *myModel) Clean() error { return nil }
```

Rules:

- **Keep all state in the instance**, never in package-level variables:
  instances of the same binary share them.
- `NewPlugin` runs once per configured ID. `Reload` runs on every configuration
  reload and must not repeat one-time setup. If `NewPlugin` fails, it must
  release anything it already acquired.
- `Process` / `CheckResults` can run concurrently with each other and with
  `Reload`, so protect mutable state.
- `ModelResults.Data` and `DecisionResult.Data` are the training sample saved
  when the plugin runs in training mode. `ModelInput.Training` and
  `DecisionInput.Training` tell the plugin whether the current call is a
  training call.
- The same plugin code works for sync, async and remote models. WACE connects
  async and remote models to NATS.

Build the plugin with the **same Go toolchain, the same versions of
`ModSecIntl_wace_lib` and `go.opentelemetry.io/otel/metric`, and the same build
flags** as the host. Otherwise `plugin.Open` refuses to load it:

```sh
go build -buildmode=plugin -o model.so ./path/to/plugin
```

Any change to the `waceapi` package requires rebuilding every plugin.

## Development

The test plugins live in `testdata/plugins/{model,decision}` and must be
compiled before running the tests. With [mage](https://magefile.org/):

```sh
mage plugins        # build every test plugin
mage test           # build the plugins and run the whole test suite
mage testCoverage   # same, with coverage (writes coverage.out)
mage clean          # remove the built plugins
```

Architecture decisions are recorded in [`docs/adr`](docs/adr).

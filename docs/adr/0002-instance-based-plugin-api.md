# ADR 0002 — Instance-based plugin API

- **Status:** Accepted
- **Date:** 2026-09-28
- **Repo context:** `ModSecIntl_wace_lib` (consumed by `wace-coraza`)
- **Supersedes:** the "A distinct `.so` per trainable plugin" operational
  decision of [ADR 0001](0001-trainable-decision-plugins.md)
- **Amended by:** [ADR 0003](0003-plugin-config-and-slog.md): `NewPlugin` and
  `Reload` now receive a `waceapi.PluginConfig` instead of `(params, meter)`,
  and `Process` / `CheckResults` receive a `context.Context`

## Context

Until now a plugin was a set of exported package-level functions:

- Model plugins: `InitPlugin`, `InitPluginAsync`, `Process`, `ReloadPlugin`.
- Decision plugins: `InitPlugin`, `CheckResults`, `ReloadPlugin`.

Every plugin kept its configuration in **package-level variables** (e.g.
`var result float64` in `param.go`, `var threshold float64` in
`weighted_sum.go`), which `InitPlugin` and `ReloadPlugin` overwrote.

We want to **reuse one compiled plugin for several applications with different
parameters**, for example the same model configured with two thresholds under
two IDs. With the old API this is impossible, because of how Go plugins work:

1. **Same path, two IDs:** `plugin.Open` caches plugins by real path and returns
   the same `*plugin.Plugin` again. Package-level variables exist once per
   process, so the second `InitPlugin` overwrites the parameters of the first
   and both IDs end up sharing the last configuration.
2. **A copy of the `.so` at another path:** the runtime identifies plugins by
   their `pluginpath`. For `go build -buildmode=plugin file.go` that is
   `plugin/unnamed-<hash>`, and the hash is derived from the build ID and the
   source contents (`cmd/go/internal/work/gc.go`), not from the file path. A
   byte-for-byte copy has the same `pluginpath`, so `plugin.Open` fails with
   `plugin already loaded` and returns a nil `*Plugin`. The failure is cached for
   that path (`... (previous failure)` on later opens).
3. Go cannot unload a plugin or load it into an isolated namespace (there is no
   `dlmopen` equivalent), so a loader cannot work around either case.

The `plugin.Open` documentation ("If a path has already been opened, then the
existing *Plugin is returned") covers case 1 only. Case 2 is an undocumented
runtime limitation.

## Decision

A plugin exports **one constructor** that returns an **instance**. All
per-configuration state lives in the instance, and the plugin manager creates one
instance per configured ID.

### 1. Plugin contract

`waceapi` defines the interfaces:

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

Every plugin exports exactly one symbol, `NewPlugin`:

```go
// model plugin
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error)

// decision plugin
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error)
```

- `NewPlugin` runs once per ID. It does the one-time setup (register metrics,
  load model files, open connections) and applies the params. If it returns an
  error, it must already have released anything it acquired.
- `Reload` runs on every configuration reload. It only re-applies params and must
  not repeat one-time setup: calling it again must not, for example, increment
  `plugin_register` again. It should parse into locals and assign only on
  success, so a rejected reload leaves the previous configuration in place.
- `Clean` runs once, when the instance is unloaded. It releases what the
  instance created: model memory, files, connections, goroutines. The code of the
  `.so` is never released.
- `NewPlugin` must declare the **interface** as its return type, not the
  concrete type. Go function types are not covariant, so a
  `func(...) (*myPlugin, error)` does not match the manager's type assertion.
- `Clean` is part of the interface, not an optional `io.Closer`. Plugins with
  nothing to release implement it as `return nil`. The compiler then catches a
  missing or misspelled `Clean`, which a runtime type assertion would silently
  skip.

`InitPluginAsync` is removed. The manager calls `NewPlugin` for sync, async and
remote plugins alike, and for async/remote it connects `instance.Process` to NATS
itself (`ModelProcessHandler`). Plugins no longer know how they are dispatched.

### 2. Training awareness

Plugins learn whether a call is a training call from a flag in the input of each
call, not from state set on the instance:

- `ModelInput.Training` is set to `true` by `ProcessTraining` (JSON tag renamed
  from `trainingMode` to `training`).
- `DecisionInput.Training` is set from the decision plugin config in
  `CheckResult`.

The manager already decides per request whether a call is a training call
(`wacecore.callPlugins` routes to `ProcessTraining`, `CheckResult` checks
`dpConf.Training`). With the flag in the input, the plugin sees exactly what the
manager decided for that request. A setter would create a second source of truth
that can disagree with the routing during a reload. It would also have to be
called on internal events that do not change the config: collection ends when
`max_samples` is reached, or when the status file is already `Done`/`Error` at
startup.

If a plugin ever needs to act on the transition itself (allocate buffers when
training starts, flush when it ends), the training flag should reach it through
`NewPlugin`/`Reload`, where configuration changes already arrive. We will not
add a separate `SetTraining` method.

### 3. Loading, reloading and unloading

`loadModelPlugins` / `loadDecisionPlugins`:

- **New ID:** `plugin.Open` → `Lookup("NewPlugin")` → function type assertion →
  `NewPlugin(params, meter)`. Any failure is logged and the ID is skipped:
  `cannot open plugin`, `NewPlugin function not found`,
  `invalid NewPlugin function type`, `cannot initialize plugin` (also when
  `NewPlugin` returns a nil plugin without an error). For async/remote plugins a
  failure to start the NATS handler (`cannot start process handler`) calls
  `Clean` on the new instance before skipping it.
- **Existing ID:** `instance.Reload(params, meter)`. On error the instance keeps
  its previous configuration (`cannot reload plugin`).
- **ID removed from the config:** the entry is removed from the manager, its
  training is cancelled and `Clean` is called. The plugin is removed even if
  `Clean` fails (`cannot clean plugin`).

### 4. Concurrency

- `PluginManager.Reload` is serialized with `reloadMutex`. Only reloads take it,
  never the request path. A second reload waits and then re-reads the config.
  This is safe because a reload applies the current state rather than a diff.
- `modelPlugins` and `decisionPlugins` are each protected by an `RWMutex`.
  Readers (`Process`, `ProcessTraining`, `CheckResult`) take the read lock only
  to copy the map entry, and release it **before** calling into the plugin.
  The writer takes the write lock only around each insert and delete. The slow
  work (`plugin.Open`, `NewPlugin`, `Reload`, `Clean`) runs without the map lock,
  so a reload never blocks requests for more than a map operation.
- Inside the loaders, reads of the maps need no lock: `reloadMutex` guarantees a
  single writer, and concurrent reads of a Go map are safe. `New` calls the
  loaders without `reloadMutex` because the manager is not shared yet.
- An instance's `Process`/`CheckResults` can run concurrently with each other and
  with its own `Reload`. Protecting mutable instance state is the plugin's
  responsibility (e.g. `sync.RWMutex` in `param.go` and `weighted_sum.go`).

### 5. Reusing a binary

To run several configurations of one plugin, point several IDs at **the same
path**:

```yaml
model_plugins:
  - id: model_app_a
    path: /opt/wace/plugins/model.so
    plugin_type: Everything
    params: { threshold: "0.3" }
  - id: model_app_b
    path: /opt/wace/plugins/model.so
    plugin_type: Everything
    params: { threshold: "0.8" }
```

Each ID gets its own instance. **Copies of the same `.so` at different paths are
not supported:** only one of them loads, and which one depends on the iteration
order of the config map. Instances of the same binary share package-level
variables, so plugins must keep all per-configuration state in the instance.

## Consequences

### Positive
- One compiled plugin can serve any number of IDs with independent params.
- Removing a plugin from the config now releases it (`Clean`). Before, removed
  IDs stayed loaded forever (`TODO: remove data from old plugins`).
- Plugins no longer know whether they run sync, async or remote.
- Reloads never block the request path beyond a map operation.
- The compiler checks the plugin contract when a plugin is built. At load time
  there is a single type assertion (`NewPlugin`) instead of one per function.

### Negative / risks
- **Contract change:** every model and decision plugin must be migrated,
  including the ones in `wace-coraza` (see Migration).
- Changing `waceapi` changes its package hash, so **every `.so` must be rebuilt**
  against the new version, even plugins that were not otherwise modified.
  Otherwise `plugin.Open` fails with
  `plugin was built with a different version of package ...`.
- Host and plugins must use the same Go toolchain, the same versions of `waceapi`
  and `otel/metric` (which appears in the `Reload` signature), and the same build
  flags (`-trimpath`, `-race`, `-cover`). This was already required, because the
  old signatures used `waceapi` types.

## Migration

Before:

```go
var threshold float64

func InitPlugin(params map[string]string, meter metric.Meter) error { /* set threshold */ }
func CheckResults(in waceapi.DecisionInput) (waceapi.DecisionResult, error) { /* use threshold */ }
func ReloadPlugin(params map[string]string, meter metric.Meter) error { /* set threshold */ }
```

After:

```go
type myDecision struct {
    mu        sync.RWMutex
    threshold float64
}

func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.DecisionPlugin, error) {
    d := &myDecision{}
    if err := d.Reload(params, meter); err != nil {
        return nil, err
    }
    // one-time setup (metrics, files, ...)
    return d, nil
}

func (d *myDecision) CheckResults(in waceapi.DecisionInput) (waceapi.DecisionResult, error) { /* RLock, use d.threshold */ }
func (d *myDecision) Reload(params map[string]string, meter metric.Meter) error      { /* parse, then Lock and assign */ }
func (d *myDecision) Clean() error                                                  { return nil }
```

Async model plugins drop `InitPluginAsync` and export only `NewPlugin`.

## Alternatives considered

- **Keep package-level state and make each plugin handle the params of several
  applications** (for example, params keyed by application). Rejected: every
  plugin would re-implement multi-tenancy, and the plugin would have to know
  about IDs that the manager already knows.
- **Build one binary per application with `-ldflags=-pluginpath=<name>`.** This
  gives each build a distinct `pluginpath` so several load side by side. Rejected:
  it requires one build per application, which is the reuse we want to avoid, and
  the state of non-`main` dependency packages may still be shared.
- **Deduplicate copies by content hash in the manager** (hash the file and reuse
  an already opened `*plugin.Plugin` with identical content). This would make
  copies behave like the same path. Not adopted: pointing IDs at the same path
  already covers the use case, and the extra mechanism would only paper over a
  runtime limitation.
- **Optional `Clean` via `io.Closer` and a type assertion.** Rejected in favor
  of an explicit interface method (see §1).
- **A `SetTraining(bool)` method on the instance.** Rejected in favor of a
  per-request flag (see §2).
- **Create a new `PluginManager` on every reload** and swap it atomically. This
  would give transactional reloads and one consistent snapshot of models and
  decisions. Rejected for now:
  - every reload would call `NewPlugin` for every plugin, reloading heavy models
    and doubling memory during the switch;
  - the manager also owns state that must survive a reload: in-flight
    transactions (`results`, the model channels), NATS subscriptions that cannot
    be closed today, and training collectors that write to the same files;
  - it would make a manager `Close` mandatory, including waiting for in-flight
    transactions.

  If transactional reloads are needed later, the intermediate step is an
  immutable plugin registry that reuses unchanged instances and is published with
  an `atomic.Pointer`, while transactions, NATS and training stay in the
  long-lived manager.
- **Copy-on-write plugin maps behind an `atomic.Pointer`** instead of `RWMutex`.
  Viable (lock-free reads, models and decisions swapped together), but
  `CheckResult` reads the configstore separately anyway, so the snapshot would
  not be fully consistent. The narrow `RWMutex` is simpler for the same
  protection.

## Known limitations and follow-ups

- **`Clean` and in-flight requests.** A request that copied a map entry just
  before it was deleted can still be inside `Process`/`CheckResults` when `Clean`
  runs. A per-instance usage counter (or a documented rule that `Clean` must
  tolerate in-flight calls) is pending.
- **Unloading async plugins.** `ModelProcessHandler` discards its NATS
  subscription and connection, and `ModelResultsHandler` blocks forever
  (`select {}`). After an async plugin is unloaded, NATS messages can still reach
  the cleaned instance.
- **Path changes are not detected.** Changing the `path` of an existing ID only
  calls `Reload`. It should unload the old instance and load the new binary.
- **Routing after collection ends.** When collection reaches `Done`, the config
  still says `training: true`, so requests keep being routed to `ProcessTraining`
  and the results are dropped.

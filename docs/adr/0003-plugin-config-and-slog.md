# ADR 0003 — `PluginConfig`, structured logging with `log/slog` and a context for plugin calls

- **Status:** Accepted
- **Date:** 2026-09-29
- **Repo context:** `ModSecIntl_wace_lib` (consumed by `wace-coraza`)
- **Amends:** the plugin contract of
  [ADR 0002](0002-instance-based-plugin-api.md) (§1, `NewPlugin`, `Reload`,
  `Process` and `CheckResults` signatures)

## Context

### Logging

WACE logged through `github.com/tilsor/ModSecIntl_logging/logging`, a
process-wide singleton (`logging.Get()`):

- Messages were `printf` strings with ad-hoc prefixes (`"core | ..."`,
  `"| %s | plugin loaded"`, `"%s %s | ..."`), so they could not be filtered or
  queried by plugin, transaction or component.
- The library opened the log file itself, from `logpath` and `loglevel` in the
  WACE configuration, so the host could not choose the destination, the format
  or the handler.
- `LoadLogger` is not synchronized with `Printf`, so reloading the logger while
  requests are being logged is a data race.
- `StartTransaction` created a per-transaction buffer that was never read or
  released inside the library (`EndTransaction` is never called).
- Plugins called `logging.Get()` too, so they depended on the same package and
  on the host having loaded it.

### Plugin constructor

ADR 0002 defined the constructor and reload as:

```go
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error)
Reload(params map[string]string, meter metric.Meter) error
```

Giving plugins a logger with this shape means adding a third parameter. Every
future dependency would do the same, and each time **every plugin fails to load**:
the manager finds `NewPlugin` with a type assertion on the exact function type,
so a plugin with the old signature is rejected at runtime
(`invalid NewPlugin function type`) instead of at compile time.

### Request context

`Process` and `CheckResults` receive no `context.Context`, so a plugin cannot
be cancelled when the request is abandoned, cannot honor a deadline and cannot
receive request-scoped values such as trace spans. Adding it later would be
another signature change for every plugin, so it is added together with this
one.

## Decision

### 1. The host injects a `*slog.Logger`, and can replace it on reload

```go
func Init(met metric.Meter, conf configstore.ConfigFileData, logger *slog.Logger) error
func Reload(met metric.Meter, conf configstore.ConfigFileData, logger *slog.Logger) error
func New(meter metric.Meter, logger *slog.Logger) (*PluginManager, error) // pluginmanager
func (pm *PluginManager) Reload(meter metric.Meter, logger *slog.Logger) error // pluginmanager
```

- The host owns the handler, the destination and the level. A `nil` logger
  means `slog.Default()`, in `Init` and in `Reload`.
- The core and the plugin manager keep their loggers in
  `atomic.Pointer[slog.Logger]`, so a reload can replace them while requests
  are logging.
- A reload replaces the logger only if the configuration is applied: the core,
  the plugin manager (`pluginmanager.Reload`) and the plugins (in
  `Reload(PluginConfig)`) all change together or not at all. If the
  configuration is rejected, everything keeps logging to the previous logger
  (`TestReloadInvalidConfigKeepsLogger`).
- Calls that derive a logger with `tx_id` (`Analyze`, `CheckTransaction`) keep
  it until they finish, so a transaction in flight during a reload can end with
  the previous logger. Long-lived goroutines of the plugin manager (training
  collection, NATS handlers) read the current logger on every record.
- The library no longer calls `LoadLogger` or `StartTransaction`.

### 2. Attributes

The keys are constants in `waceapi`:

| Key | Constant | Values |
|---|---|---|
| `component` | `LogKeyComponent` | `core`, `pluginmanager`, `plugin` |
| `plugin.type` | `LogKeyPluginType` | `model` (`LogValueModelPluginType`), `decision` (`LogValueDecisionPluginType`) |
| `plugin.id` | `LogKeyPlugin` | the plugin ID from the configuration |
| `tx_id` | `LogKeyTxID` | the transaction ID |

`slog` does not deduplicate attributes: `With("component", "a").With("component", "b")`
writes the key twice. So **each attribute is added exactly once, where it becomes
known**:

- `component` is added once per component, always to the **host logger**, never
  to a logger that already has one. `wace.Init` keeps the host logger `l`, uses
  `l.With(component, "core")` for itself, and passes `l` (not its own logger) to
  `pluginmanager.New`. The plugin manager keeps the host logger as `baseLogger`
  and builds from it both its own logger (`component=pluginmanager`) and the
  plugins' loggers (`component=plugin`).
- `tx_id` is added with `With` at the entry points that receive the transaction
  (`Analyze`, `CheckTransaction`), and the derived logger is passed down as a
  parameter (`callPlugins`). The plugin manager derives its own from its logger,
  because the core logger already carries `component=core`.
- `plugin.type` and `plugin.id` are added per plugin by whoever iterates the
  plugins.

`TestPluginManagerPluginLogger` checks that plugin and manager records carry
the right `component` and that no key appears twice.

### 3. `waceapi.PluginConfig`

```go
type PluginConfig struct {
    Params map[string]string // params of the plugin ID in the configuration
    Meter  metric.Meter      // OpenTelemetry meter of the host
    Logger *slog.Logger      // component=plugin, plugin.type, plugin.id; never nil
}

// model plugin
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error)
// decision plugin
func NewPlugin(cfg waceapi.PluginConfig) (waceapi.DecisionPlugin, error)

type ModelPlugin interface {
    Process(ModelInput) (ModelResults, error)
    Reload(PluginConfig) error
    Clean() error
}

type DecisionPlugin interface {
    CheckResults(DecisionInput) (DecisionResult, error)
    Reload(PluginConfig) error
    Clean() error
}
```

- `NewPlugin` and `Reload` receive the same type, so a new dependency is one new
  field and neither signature changes.
- It is passed **by value**. The manager always fills it, so a pointer would only
  add a `nil` case.
- `Logger` already has `component=plugin`, `plugin.type` and `plugin.id`.
  Plugins must not add them again, and add `tx_id` to records about a
  transaction.
- `Reload` can receive a different logger than `NewPlugin`. Plugins must
  replace the one they keep, **even if they reject the params** (the previous
  handler may be closed by the host), and synchronize it with `Process` /
  `CheckResults`, for example with `atomic.Pointer[slog.Logger]`. `Params` and
  `Meter` keep the rules of ADR 0002 (`Reload` re-applies params and must not
  repeat one-time setup).

### 4. `context.Context` in `Process` and `CheckResults`

```go
type ModelPlugin interface {
    Process(context.Context, ModelInput) (ModelResults, error)
    Reload(PluginConfig) error
    Clean() error
}

type DecisionPlugin interface {
    CheckResults(context.Context, DecisionInput) (DecisionResult, error)
    Reload(PluginConfig) error
    Clean() error
}
```

- The context is request scoped. Plugins should stop and return `ctx.Err()`
  when it is cancelled, and may read request-scoped values from it.
- **For now the library passes `context.TODO()`.** The context will come from
  the host through `Analyze` and `CheckTransaction`; that change is to the
  public API of `wace` only, not to the plugins.
- `NewPlugin`, `Reload` and `Clean` do not receive a context. If they need one
  (for example, to cancel a long model load), it can be a new `PluginConfig`
  field without changing the signatures.

Everything else in ADR 0002 (instances, `Clean`, training flag, concurrency)
is unchanged.

## Consequences

### Positive
- Logs are structured and can be filtered by `component`, `plugin.id`,
  `plugin.type` and `tx_id`.
- The host decides how and where WACE logs, and can replace the logger on
  reload. The logger it passes is used as is, with no reload race.
- Plugins can be cancelled and can receive request-scoped values once the host
  context reaches them, without another change to the plugin contract.
- Adding a dependency for plugins (a tracer, a data directory, the training
  flag if ADR 0002 §2 ever needs it) is a new `PluginConfig` field. Plugins that
  do not use it keep compiling unchanged.
- Plugins no longer depend on `ModSecIntl_logging`.

### Negative / risks
- **Contract change:** every model and decision plugin must be migrated once
  more, including the ones in `wace-coraza` (see Migration). A plugin built with
  the old signature is skipped at load time with
  `cannot load plugin: invalid init function type`.
- **`Init` and `Reload` signature change** for every host.
- Plugins that log must keep the logger synchronized, because `Reload` can
  replace it while requests run.
- Adding a field to `PluginConfig` changes `waceapi`, so every `.so` still has to
  be **rebuilt**, as for any `waceapi` change (ADR 0002). What it avoids is
  having to **edit** every plugin.
- **Configuration format change:** `logpath` and `loglevel` are removed from
  `ConfigFileData` and `ConfigStore` (`LogPath`, `LogLevel`), and the log path is
  no longer validated. The host must configure the logger itself. Existing
  YAML files still load, because unknown keys are ignored
  (`TestLoadConfigYamlIgnoresLegacyLogKeys`), unless the host decodes them with
  `KnownFields(true)`.
- `ModSecIntl_logging` is no longer a dependency.
- Hosts that read the per-transaction buffer of the old package
  (`logging.Get().EndTransaction`) get nothing anymore.

## Migration

Host:

```go
logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
err := wace.Init(meter, conf, logger) // do not add a component attribute
// ...
err = wace.Reload(meter, newConf, newLogger)
```

Plugin, before:

```go
func NewPlugin(params map[string]string, meter metric.Meter) (waceapi.ModelPlugin, error) {
    lg.Get().Printf(lg.INFO, "[my:NewPlugin] %v", params)
    m := &myModel{}
    if err := m.Reload(params, meter); err != nil {
        return nil, err
    }
    counter, err := meter.Int64Counter("plugin_register")
    // ...
}

func (m *myModel) Process(in waceapi.ModelInput) (waceapi.ModelResults, error) {
    lg.Get().TPrintf(lg.DEBUG, in.TransactionId, "[my:Process] ...")
    // ...
}

func (m *myModel) Reload(params map[string]string, meter metric.Meter) error { /* params["x"] */ }
```

After:

```go
type myModel struct {
    logger atomic.Pointer[slog.Logger]
    // ...
}

func NewPlugin(cfg waceapi.PluginConfig) (waceapi.ModelPlugin, error) {
    cfg.Logger.Info("initializing", "params", cfg.Params)
    m := &myModel{}
    if err := m.Reload(cfg); err != nil { // Reload stores the logger
        return nil, err
    }
    counter, err := cfg.Meter.Int64Counter("plugin_register")
    // ...
}

func (m *myModel) Process(ctx context.Context, in waceapi.ModelInput) (waceapi.ModelResults, error) {
    m.logger.Load().Debug("processing request", waceapi.LogKeyTxID, in.TransactionId)
    // ...
}

func (m *myModel) Reload(cfg waceapi.PluginConfig) error {
    m.logger.Store(cfg.Logger) // even if the params below are rejected
    /* cfg.Params["x"] */
}
```

Decision plugins do the same with `CheckResults(ctx, in)`.

Drop the `ModSecIntl_logging` import, and the `metric` import if it was only
used in the signatures.

## Alternatives considered

- **Add a `logger *slog.Logger` parameter to `NewPlugin` and `Reload`.**
  Rejected: it breaks every plugin now and again with the next dependency,
  which is the problem this ADR solves.
- **Pass a `*PluginConfig`.** Rejected: the manager always fills it, and a
  pointer adds a `nil` case and lets a plugin mutate what the manager passed.
- **An interface with getters (`cfg.Logger()`, `cfg.Params()`).** It would allow
  computed or lazily created values, but adding a method to an interface is
  also a change for anyone implementing it (for example, fakes in plugin tests).
  A struct is enough for plain values.
- **Functional options (`NewPlugin(opts ...Option)`).** Rejected: more
  machinery in the plugins for the same extensibility, and options are harder
  to inspect than fields.
- **Put the logger in a `context.Context`.** Rejected: it hides the dependency,
  and `NewPlugin`/`Reload` have no request-scoped context to carry.
- **A replaceable `slog.Handler`** (a handler that forwards to the current one
  held in an `atomic.Pointer`) instead of replacing loggers. Every derived
  logger, including the ones plugins keep, would follow a reload without
  plugins doing anything. Rejected: the handler would have to replay every
  `WithAttrs`/`WithGroup` on the new handler after each swap, a custom handler
  is more code to get right than `atomic.Pointer[slog.Logger]`, and plugins
  already receive `Reload` for other configuration changes.
- **A separate `wace.SetLogger`** instead of a `Reload` parameter. Rejected for
  symmetry with `Init`.
- **Replacing the logger before validating the configuration**, so the errors
  of a rejected reload go to the new logger. Rejected: the plugins only receive
  it in their `Reload`, which does not run for a rejected configuration, so the
  core and the plugins would end up on different loggers.
- **A context in `NewPlugin`, `Reload` and `Clean`** too. Deferred: they are not
  request scoped, and a `PluginConfig` field can add it later.
- **Plugins keep calling `slog.Default()`.** Rejected: the plugin would have to
  add `component`, `plugin.type` and `plugin.id` itself, and a binary loaded
  under several IDs (ADR 0002 §5) does not know which ID an instance serves.
- **Give plugins the plugin manager's logger (`component=pluginmanager`)
  plus the plugin attributes.** Rejected: plugin records could not be told apart
  from the manager's records by `component`.

## Known limitations and follow-ups

- **The context is not wired yet.** `Analyze`, `CheckTransaction` and the NATS
  handlers pass `context.TODO()`. When the host context is wired in, the
  training calls (`ProcessTraining` and training decision plugins in
  `CheckResult`) run after the request finishes, so they must use
  `context.WithoutCancel` rather than the request context. For async and
  remote models the context does not cross NATS; at most its deadline can be
  sent in `ModelInput`.
- **Cost of the atomic logger.** `atomic.Pointer.Load` is a plain load (~0.5 ns
  in local benchmarks, against ~450 ns for a JSON record). What costs is `With`
  (~370 ns and 7 allocations per call, even when the level is disabled), so
  code on hot paths must not call `With` per record. Training collection
  derives its logger again only when the pointer changes.
- `meter` in the core is still a plain package variable replaced by `Reload`,
  not synchronized with the requests that read it.

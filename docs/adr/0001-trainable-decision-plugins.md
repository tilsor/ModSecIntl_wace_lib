# ADR 0001 — Trainable decision plugins

- **Status:** Proposed
- **Date:** 2026-06-25
- **Repo context:** `ModSecIntl_wace_lib` (consumed by `wace-coraza`)

## Context

Today WACE runs, per transaction, **N model plugins** and a **single decision
plugin**:

- Model plugins produce `ModelResults{ProbAttack float64, Data any}` and can be
  sync, async (NATS), remote, or **in training**.
- A model marked `training: true` runs in **shadow mode**: in `callPlugins` it is
  routed to `ProcessTraining` and does **not** increment `syncCounter` nor write
  to `p.results`, so it **does not participate in the blocking decision**.
  Collection is handled by `handleTrainingModel`, which persists each sample as
  JSONL with a status state machine (`Collecting`/`Ready`/`Done`/`Error`),
  `min/max_samples`, and an atomic status file for fsnotify watchers.
- The decision plugin, by contrast, is **neither plural nor configurable as a
  shadow**: the caller selects it at runtime via
  `CheckTransaction(transactionID, decisionPlugin, wafParams)`, and its contract
  exposes only `CheckResults(DecisionInput) (bool, error)`. There is exactly one
  plugin deciding the block.

We want to be able to **train a new decision plugin** (e.g. an ML-based one that
replaces or complements a heuristic) **without interrupting the blocking** that
the current production decision plugin provides.

## Problem

1. Putting the production decision plugin "in training" would lose the blocking
   capability (it is the only one deciding).
2. A decision plugin's training sample is not its boolean verdict: it depends on
   each plugin (it may be the raw `DecisionInput` or some plugin-specific
   processing), just like the `Data any` field of model plugins.
3. The sample must be produced by a **copy/instance in training**, not by the
   production execution.
4. That sample can only be captured inside `CheckResult`, which is where the
   `DecisionInput` is assembled (results of **all** models + weights + WAFdata).

## Decision

Mirror the model training mechanism for decision plugins: **config-driven shadow
decision plugins**.

### 1. Decision plugin contract

Introduce a type analogous to `ModelResults`:

```go
type DecisionResult struct {
    Block bool `json:"block"` // blocking verdict
    Data  any  `json:"data"`  // plugin-specific training sample
}
```

`CheckResults` now returns this struct:

```go
CheckResults(DecisionInput) (DecisionResult, error)
```

- In **production**, only `Block` is used.
- In **training (shadow)**, `Data` is persisted. Each plugin decides what to put
  there: the raw `DecisionInput` or some processing.

`DecisionInput` also changes: the WAF signal is now the structured
`WAFData{Scores map[string]float64, Rules map[int]int}` (replacing the old
`map[string]string`), and it carries a `WAFWeight float64` alongside the
per-model `ModelWeight` (see §6).

> Contract change: breaks all existing decision plugins
> (`testdata/plugins/decision/*`, and those in `wace-coraza`). See Migration.

### 2. Configuration

Decision plugins gain the same training fields as models, **plus the weights that
move here from the model config** (see §6):

```yaml
decisionplugins:
  - id: decision_prod
    path: ...
    # training: false  (production)
    weights:              # per-model weights (keyed by model id)
      model_a: 0.25
      model_b: 0.25
    waf_weight: 0.5       # weight of the WAF/CRS signal (sibling field)
  - id: decision_v2
    path: ...
    training: true
    training_data:
      min_samples: 1000
      max_samples: 10000
      result_file_path: /var/lib/wace/decision_v2.jsonl
      status_file_path: /var/lib/wace/decision_v2.status
      status_update_interval: 100
    weights:
      model_a: 0.3
      model_b: 0.2
    waf_weight: 0.5
```

`weights` is a per-model map keyed by model id; the WAF/CRS weight lives in the
sibling `waf_weight` field (the WAF is not a model, so it does not belong inside
`weights`). Both fields are optional — decision plugins that ignore models or the
WAF (e.g. a CRS-only plugin) simply omit them.

The existing `TrainingData` struct and status state machine are reused. The
async/remote validations do not apply (decision plugins are local).

### 3. Production vs. training selection

`wace-coraza` passes a **list** of decision plugins to `CheckTransaction`
(mirroring `Analyze(... models []string ...)`), allowing per-app/route scoping.
Rules:

- **At most one** plugin in the list may be production (`training:false`).
- The rest may be in `training:true`.
- A **training-only** call (zero production plugins) is valid: the transaction is
  collected for training but no blocking decision is made.

Validation in two layers:

- **Config load:** validate the `training_data` of each `training:true` decision
  plugin (same as models: `max>0`, `0<=min<=max`,
  `max>=status_update_interval`).
- **Runtime (`CheckTransaction`):** count the plugins in the list with
  `training==false`.
  - If there is exactly **one** production plugin, its `Block` is returned.
  - If there are **zero** production plugins (training-only call),
    `CheckTransaction` returns `false` (no block) and no error — the shadow
    plugins still collect samples.
  - If there is **more than one** production plugin, `CheckTransaction` returns
    an error (the list is misconfigured).

### 4. Execution flow

- The production plugin runs as it does today and its `Block` **decides the
  blocking, with no change in semantics**.
- The model results (`modelResultMap`) are shared across all plugins in the call,
  but the **weights are per decision plugin** (see §6): the `DecisionInput` handed
  to each plugin carries that plugin's own `ModelWeight` and `WAFWeight`.
- For each `training:true` decision plugin in the list, a **snapshot of the
  `DecisionInput`** is taken (the `modelResultMap` is already built as a fresh
  map in `CheckResult`, so it is safe to hand off to another goroutine) and
  pushed to a training channel.
- A `handleTrainingDecision` (analogous to `handleTrainingModel`) consumes that
  channel **off the request path**, runs the shadow instance's `CheckResults` to
  obtain `DecisionResult.Data`, and persists it using the same status/file
  machinery.
- `decisionPlugin` gains `trainingChannel`, `trainingCtx`, `trainingCancel`
  (just like `modelPlugin`), and `loadDecisionPlugins` starts the collection
  goroutine when `IsInTraining`.

### 5. Suggested refactor

Generalize `pluginmanager/training_models.go` into a shared collector
(`training.go`) that persists both model samples (`ModelResults`) and decision
samples (`DecisionResult.Data`), since the status state machine, sample
counting, and atomic writes are identical.

### 6. Weights move from the model config to the decision plugin

Today the per-model `Weight` lives in `modelPluginConfig` and is injected into
the decision plugin via `DecisionInput.ModelWeight`, while the WAF weight is a
plugin `param` (e.g. `weighted_sum` reads `waf_weight`). This splits related
configuration across two places and forces every decision plugin to share the
same model weights.

Weights are conceptually owned by the decision plugin (it is what interprets
them), so they move there:

- `Weight` is **removed** from `modelPluginConfig`.
- Each decision plugin config declares a `weights` map (per model id) and a
  `waf_weight` field.
- The manager sources `DecisionInput.ModelWeight` from the current plugin's
  `weights` and adds a new `DecisionInput.WAFWeight` field from `waf_weight`
  (replacing the ad-hoc `waf_weight` param).
- **Validation:** keys in `weights` should reference model ids present in the
  configuration.

This is what lets production and training plugins weigh the same models
differently, and it makes weight tuning a first-class, trainable parameter (see
the open question on user-configurable trainable plugins).

### 7. Baseline decision plugins (default set)

The library ships two ready-made decision plugins:

- **CRS-only:** defers entirely to the WAF/CRS verdict (via `WAFData`), ignoring
  model results. Used as the production plugin when an app is first deployed and
  no trained plugin exists yet, while a trainable plugin collects samples in the
  background.
- **Weighted:** a weighted sum over model results and the WAF signal, defaulting
  to **50/50** between the models (as a group) and the WAF. This generalizes the
  current `weighted_sum` plugin (already partially implemented in
  `testdata/plugins/decision/weighted_sum.go`), now sourcing its weights from the
  decision plugin config per §6. The CRS-only plugin is a similar, simpler
  variant.

## Consequences

### Positive
- Production blocking is unchanged; collection is transparent.
- Pattern unified with model training (config, status, files).
- `wace-coraza` adopts the same mental model it already uses for models.

### Negative / risks
- **Contract change** in all decision plugins (`bool` → `DecisionResult`).
  Requires coordinated migration with `wace-coraza`.
- **Signature change** of `CheckTransaction` (single id → list) and of
  `CheckResult` in the `PluginManager`.
- **Extra per-transaction cost**: running the shadow instance(s). Mitigated by
  executing them off the response path (goroutine + channel).

### Ordering dependency to document
The `DecisionInput` only contains the models that **contribute to `results`**. A
model in training (shadow) **does not appear** in that input. Therefore: models
must first be trained and **promoted to production**, and only then does it make
sense to collect consistent decision data (otherwise the decision plugin would
be trained on a feature set that is incomplete relative to production).

## Alternatives considered

- **Single production id + config-based discovery** (without changing the
  `CheckTransaction` signature): simpler, but collection would be global and not
  per-app/route. Rejected for consistency with the model mechanism.
- **Label from the production verdict / from the WAF (CRS)**: rejected — the
  sample is defined by each decision plugin via `Data`, not a fixed label
  imposed by the library.
- **New `CollectTrainingData` function or extending `CheckResults` with a flag**:
  rejected in favor of `DecisionResult` to unify with `ModelResults`.

## Operational decisions

- **Plugin count per call → at most one production.** Zero production plugins
  (training-only call) is valid and returns `false` (no block). More than one
  production plugin is a misconfiguration and makes `CheckTransaction` return an
  error, delegating the policy (block / let through) to the caller
  (`wace-coraza`).
- **A distinct `.so` per trainable plugin.** Each new decision plugin (including
  the copy/successor in training) is registered as its own binary, because they
  use parameters loaded inside the plugin (`InitPlugin`/`ReloadPlugin`). The same
  `.so` is not reused by registering it twice.

## Open questions

- Schema/versioning of the decision-sample JSONL (`Data any`).
- **User-configurable decision plugins that can also be trained.** We should
  consider decision plugins that the user can tune directly through configuration
  — e.g. the per-model `weights` of §6 — and that are *also* trainable. The
  configured values would act as a starting point, and the training mechanism
  could later refine them from collected samples.

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
type DecisionResults struct {
    Block bool `json:"block"` // blocking verdict
    Data  any  `json:"data"`  // plugin-specific training sample
}
```

`CheckResults` now returns this struct:

```go
CheckResults(DecisionInput) (DecisionResults, error)
```

- In **production**, only `Block` is used.
- In **training (shadow)**, `Data` is persisted. Each plugin decides what to put
  there: the raw `DecisionInput` or some processing.

> Contract change: breaks all existing decision plugins
> (`testdata/plugins/decision/*`, and those in `wace-coraza`). See Migration.

### 2. Configuration

Decision plugins gain the same fields as models:

```yaml
decisionplugins:
  - id: decision_prod
    path: ...
    # training: false  (production)
  - id: decision_v2
    path: ...
    training: true
    training_data:
      min_samples: 1000
      max_samples: 10000
      result_file_path: /var/lib/wace/decision_v2.jsonl
      status_file_path: /var/lib/wace/decision_v2.status
      status_update_interval: 100
```

The existing `TrainingData` struct and status state machine are reused. The
async/remote validations do not apply (decision plugins are local).

### 3. Production vs. training selection

`wace-coraza` passes a **list** of decision plugins to `CheckTransaction`
(mirroring `Analyze(... models []string ...)`), allowing per-app/route scoping.
Rules:

- Exactly **one** plugin in the list must be production (`training:false`).
- The rest may be in `training:true`.

Validation in two layers:

- **Config load:** validate the `training_data` of each `training:true` decision
  plugin (same as models: `max>0`, `0<=min<=max`,
  `max>=status_update_interval`).
- **Runtime (`CheckTransaction`):** count the plugins in the list with
  `training==false`; it must be exactly 1. Otherwise, **`CheckTransaction`
  returns an error** and delegates the response policy to the caller
  (`wace-coraza`); WACE does not assume fail-open or fail-closed on its own.

### 4. Execution flow

- The production plugin runs as it does today and its `Block` **decides the
  blocking, with no change in semantics**.
- For each `training:true` decision plugin in the list, a **snapshot of the
  `DecisionInput`** is taken (the `modelResultMap` is already built as a fresh
  map in `CheckResult`, so it is safe to hand off to another goroutine) and
  pushed to a training channel.
- A `handleTrainingDecision` (analogous to `handleTrainingModel`) consumes that
  channel **off the request path**, runs the shadow instance's `CheckResults` to
  obtain `DecisionResults.Data`, and persists it using the same status/file
  machinery.
- `decisionPlugin` gains `trainingChannel`, `trainingCtx`, `trainingCancel`
  (just like `modelPlugin`), and `loadDecisionPlugins` starts the collection
  goroutine when `IsInTraining`.

### 5. Suggested refactor

Generalize `pluginmanager/training_models.go` into a shared collector
(`training.go`) that persists both model samples (`ModelResults`) and decision
samples (`DecisionResults.Data`), since the status state machine, sample
counting, and atomic writes are identical.

## Consequences

### Positive
- Production blocking is unchanged; collection is transparent.
- Pattern unified with model training (config, status, files).
- `wace-coraza` adopts the same mental model it already uses for models.

### Negative / risks
- **Contract change** in all decision plugins (`bool` → `DecisionResults`).
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
  rejected in favor of `DecisionResults` to unify with `ModelResults`.

## Operational decisions

- **Failed validation → error.** If the list passed to `CheckTransaction` does
  not contain exactly one production plugin, WACE returns an error and the
  policy (block / let through) is defined by the caller (`wace-coraza`).
- **A distinct `.so` per trainable plugin.** Each new decision plugin (including
  the copy/successor in training) is registered as its own binary, because they
  use parameters loaded inside the plugin (`InitPlugin`/`ReloadPlugin`). The same
  `.so` is not reused by registering it twice.

## Open questions

- Schema/versioning of the decision-sample JSONL (`Data any`).
- **Baseline decision plugins generated in this repo.** We should provide a set
  of ready-made decision plugins — e.g. a `DetectionOnly` plugin (never blocks,
  just records) and a "WAF-only" plugin (defers entirely to the WAF's own verdict
  via `WAFdata`). These cover the case where an app is deployed for the first
  time and there is no trained decision plugin yet: it would run with one of
  these as the production plugin while a trainable plugin collects samples in the
  background.
- **User-configurable decision plugins that can also be trained.** We should
  consider decision plugins that the user can tune directly through configuration
  — e.g. per-model weights — and that are *also* trainable. The configured values
  would act as a starting point, and the training mechanism could later refine
  them from collected samples.
- **Should model weights (`Weight`) move from the model config to the decision
  plugin config?** Today `Weight` lives in `modelPluginConfig` and is injected
  into the decision plugin via `DecisionInput.ModelWeight`. If a decision plugin
  defines its own model weighting, those weights are conceptually owned by the
  decision plugin, not the model. To evaluate: let each decision plugin that
  supports it declare its own per-model weights in its config, and have
  `DecisionInput` source them from there (with a fallback to the model's weight
  when the plugin does not define them). This matters especially with several
  decision plugins running in parallel (prod + training), which may want
  different weights over the same models.

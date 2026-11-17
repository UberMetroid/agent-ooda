---
name: ooda
description: >-
  Opt-in 4-phase Observe-Orient-Decide-Act (OODA) engineering and debugging
  workflow for multi-file changes, root-cause analysis, and verification using
  agy-router across isolated git worktrees.
---

# `/ooda` — 4-Phase Engineering & Debugging Loop

Use the OODA loop for non-trivial bugs, multi-file changes, and architectural work:

## Phase 1: Observe (Read-Only Ground Truth)
Never edit before observing the real files, state, logs, and exact line numbers.
For broad repo sweeps across multiple modules, offload read-only recon instead of scanning sequentially in main context:
```bash
agy-router --recon "Map callers, schemas, and invariants for <target> with exact file:line locations."
```
Confirm dirty state (`git status`), test entry points, and failure outputs directly.

## Phase 2: Orient (Root Cause & Blast Radius)
Synthesize root cause, invariants, coupling, and explicit file scope. For complex multi-file design or cross-module bugs, offload read-only orient synthesis:
```bash
agy-router --orient "Diagnose root cause and safest minimal change for <issue> across <files>."
```
Identify which files belong in scope (`files`) and what concrete checks prove the fix works.

## Phase 3: Decide (Plan & Fleet Routing)
Break work into minimal independent tasks before editing:
- Use `/lane` or check `agy-router --status` and `agy-router --usage` before selecting lanes, matching each CLI's live model, effort control, UTC tariff windows, and quota health.
- Keep `Decide` in the orchestrator / native Plan Mode; never delegate product decisions to workers.

## Phase 4: Act + Verify (Scoped Change, Cross-Model Diff Gate & Cleanup)
### Single-Task Path (1-2 coupled tasks)
1. Execute the scoped change (`agy-router --task "<prompt>" --files a.go,b.go` or minimal direct edit).
2. Inspect `git diff` directly, run real build/test gates (never guess), and pass the independent cross-model diff gate:
   ```bash
   agy-router --review .
   ```

### Parallel Worktree Path (`2+` independent scopes)
1. Ensure clean tracked worktrees (or commit staged prerequisites) so worktrees fork from valid `HEAD`.
2. Write `.worktrees/tasks.json` (`{"tasks":[{"id":"t1","prompt":"...","files":["..."],"skills":["..."]}]}`) and execute:
   ```bash
   agy-router .worktrees/tasks.json
   ```
3. Inspect `.worktrees/results.json` and real `git diff` on every `ooda/<id>` branch; treat unverified output as unreliable.
4. Merge clean accepted branches (`git merge --no-ff ooda/<id>`), remove temporary worktrees/branches (`agy-router --cleanup`), and re-run full repo tests on the merged tree.

### Loop 2 Mismatch Pivot Rule
If two attempts on the same files/scope fail (`verify` REJECT or failing tests), do **not** retry blindly on the same model family: pass `--exclude-lane <failed-lane>` or switch model family via `agy-router --select` and re-Orient before acting.

---
name: ooda
description: >-
  Strict 4-phase Observe-Orient-Decide-Act (OODA) development methodology for
  complex multi-file bugs, features, and refactors spanning 2+ modules. Enforces
  zero edits before Phase 4, root-cause and file-coupling analysis, isolated git
  worktrees, mandatory build/test gates, independent diff review, and a hard cap
  of 3 Act loops.
---

# OODA — Engineering Development Methodology

## Overview

A discipline-first development loop (**Observe -> Orient -> Decide -> Act**) for
multi-file changes, non-obvious bugs, and architectural refactors:

- **Zero file edits before Phase 4.** Never speculate or patch prematurely.
- **Max 3 Act loops.** If a change fails verification 3 times, hard-stop and
  reassess with the user instead of thrashing.
- **Verify against real artifacts, never worker self-reports.** Every change is
  judged strictly by actual `git diff`, scope compliance, independent code
  review, and real compiler/test gate output.

When NOT to use: single-line typos, trivial edits, or pure Q&A.

## Phase 1 — OBSERVE (Zero Edits)

1. Read `AGENTS.md` (if present), `git status`, failing tests, and compiler or
   runtime error output.
2. For large repos, delegate read-only codebase exploration via `router`
   instead of loading dozens of files directly:
   ```bash
   router --recon "<specific question>"
   ```
3. Output verified facts in `<= 5` concise bullets. Zero edits or fixes here.

## Phase 2 — ORIENT (Zero Edits)

Determine the root cause, system invariants, blast radius, and file coupling:

- **Coupled / overlapping files:** subtasks share files or depend on shared
  signatures -> plan **Sequential** execution.
- **Disjoint modules / clean boundaries:** subtasks have zero overlapping
  `files` (including independent implementation + test slices) -> plan
  **Parallel Worktrees**.

### Implicit Guidance & Control (`Orient --> Act` Fast Path)

Per John Boyd's full OODA sketch, explicit `DECIDE` ceremony is only needed when
facing novel trade-offs, multi-module coupling, or post-mismatch re-orientation.
When **OBSERVE + ORIENT** isolate an unambiguous, low-blast-radius 1-2 file
change with known invariants and an obvious build/test gate, flow directly
`Orient --> Act` without unnecessary planning overhead.

## Phase 3 — DECIDE (Zero Edits)

Lock in the minimal concrete plan before touching any code:

- **Files in scope** and **off-limits files**
- **Execution mode:** Sequential vs. Parallel Worktrees
- **E-M Complexity Tags & Fast Transients:** label each slice `fast` (`5m` timeout,
  mechanical / tests / boilerplate on free `swe-2-flash-max`, capped at **4
  parallel workers**), standard (`8m` timeout, E-M $P_s$-protected routine `act`),
  `heavy` (`10m`), or `deep` (`15m`, pulls full $G$ on `agy-opus` / `muse` /
  `grok`). Read-only `OBSERVE` defaults to `3m` (`--recon`).
- **Exact build/test gate command** (e.g. `pytest -q`, `npm test`,
  `go test ./...`, `make test`)

## Phase 4 — ACT (Max 3 Loops)

### Mode A — Sequential (1-2 Coupled Files)

1. Execute the scoped change (via `router --task "<prompt>" --files ...`
   or minimal direct edit).
2. Run the repo's DECIDE build/test gate command.
3. Run an independent read-only diff review:
   ```bash
   router --review .
   ```

### Mode B — Parallel Worktrees (2+ Disjoint Modules)

1. Write `.worktrees/tasks.json` containing
   `{id, prompt, files, complexity?, lane?, exclude_lanes?, timeout?, review?}`
   with strictly non-overlapping `files` per task. Prefix every task prompt
   with the worker contract:
   *Observe first, modify only listed `files`, run `<DECIDE gate command>` before
   exiting, and end with a diff summary.*
2. Execute the parallel worktree runner (`--jobs` up to `8`; `devin-fast`
   `swe-2-flash-max` is automatically capped at `4` concurrent workers with spill
   overflow):
   ```bash
   router .worktrees/tasks.json
   ```
3. Inspect `.worktrees/results.json` and read the actual `git diff` in every
   worktree branch (`ooda/<id>`):
   - Reject any worktree with non-empty `scope_violations` or
     `review.verdict == "REJECT"`.
   - Fix rejected or failed tasks in a follow-up loop (excluding the failed lane
     so Loop 2 pivots model families).
4. Merge only clean, accepted branches in task order:
   ```bash
   git merge --no-ff ooda/<id>
   ```
5. Clean up worktrees and temporary branches:
   ```bash
   router --cleanup
   ```
6. Re-run the DECIDE build/test gate on the merged branch immediately.

### Loop Exit & Escalation (Cross-Model Snowmobiling)

- If the merged build/test gate is green and reviews are `ACCEPT`, report the
  final diff summary and stop.
- Otherwise, treat the failure as a **mismatch** (*Destruction & Creation*): feed
  the exact compiler/test failure or `REJECT` finding back into **Phase 1 —
  OBSERVE** / **Phase 2 — ORIENT**, increment the loop counter, and **pivot to a
  different model family**:
  - `router` automatically remembers crashed, timed-out, or `REJECT`-ed runs for
    30 minutes and rotates model families (`claude-opus`, `swe-2`, `grok`,
    `glm`, `muse`, `minimax`) on the next loop.
  - If a downstream build/test gate fails even after `VERDICT: ACCEPT`, pass
    `--exclude-lane <prev_lane>` in Loop 2 so the same model never repeats its
    own blind spot.
- Hard-stop and reassess with the user after 3 failed loops.

## Composing with `router`

`ooda` governs **engineering discipline** (when to explore vs. edit, how work is
isolated, and what verification gates must pass). `router` governs
**execution routing & token optimization** (`~/.local/state/boyd/quota.json`,
active capped-lane draining, task-complexity model selection, parallel striping,
and M3.1-Flash review). Use `ooda` together with `router` for multi-module work,
or invoke `router` standalone for quick recon, single-shot tasks, and reviews.

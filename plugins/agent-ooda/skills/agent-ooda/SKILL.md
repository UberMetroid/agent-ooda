---
name: agent-ooda
description: >-
  Unified 4-phase Observe-Orient-Decide-Act (OODA) engineering methodology and
  multi-agent Energy-Maneuverability CLI router (`agent-ooda`) for Antigravity
  (`agy`). Governs disciplined exploration, isolated git worktrees, live fleet
  dispatch across local agent CLIs (`agy`, `devin`, `muse`, `mcode`, `grok`,
  `vibe`), dynamic `--lane`/`--model`/`--effort`/`--skill` overrides, mandatory
  build/test gates, cross-model diff verification, and a 3-loop cap.
---

# agent-ooda — OODA Discipline & Multi-Agent Router

Use `agent-ooda` for multi-file changes, non-obvious bugs, architectural refactors,
parallel worktree execution, and cross-model verification. Skip heavy ceremony for
single-line typos or pure Q&A.

## Part I — 4-Phase OODA Engineering Methodology

### Phase 1 — OBSERVE (Zero Edits)
1. Read `AGENTS.md` (if present), `git status`, failing tests, and compiler or
   runtime error output.
2. Offload broad codebase reconnaissance rather than reading dozens of files inline:
   ```bash
   agent-ooda --recon "<specific question>"
   ```
3. Synthesize verified facts in `<= 5` concise bullets before orienting.

### Phase 2 — ORIENT (Zero Edits)
Determine root cause, invariants, blast radius, and file coupling:
- **Coupled / overlapping files:** shared files or interdependent signatures -> **Sequential**.
- **Disjoint modules:** zero overlapping `files` -> **Parallel Worktrees**.

**Implicit Guidance & Control (`Orient --> Act` Fast Path):** Explicit `DECIDE` ceremony
is needed for novel trade-offs, cross-module coupling, or post-mismatch re-orientation.
When OBSERVE + ORIENT isolate an unambiguous 1-2 file fix with known invariants and
a clear test gate, move directly from Orient to Act while keeping the orchestrator thin
(`<10%` of total token burn).

### Phase 3 — DECIDE (Zero Edits)
Lock in before any code edits:
- **Files in scope** and **off-limits files**
- **Execution mode:** Sequential vs. Parallel Worktrees
- **Live Fleet Selection:** Check `agent-ooda --status` (or `--discover`) and
  `agent-ooda --usage`, matching each CLI's live active model, effort control, UTC
  off-peak tariff, and discovered `--skill` catalog. Override dynamically via `--lane`
  (single or priority list like `mcode,devin-fast`), `--model`, `--effort`, and
  `--timeout` (`fast` `5m`, `act` `8m`, `heavy` `10m`, `deep` `15m`, `recon` `3m`).
  When omitted, single-pool CLIs keep their user defaults while `devin-*` uses its
  configured family pool (`fusion`, `swe-2`, `swe-2-flash`).
- **Build/Test Gate:** Exact gate command (e.g., `make all`, `go test -race ./...`, `pytest -q`).

### Phase 4 — ACT (Max 3 Loops)

#### Mode A — Sequential (1-2 Coupled Files)
1. Execute the scoped change (`agent-ooda --task "<prompt>" --files ...` or minimal direct edit).
2. Run the repo build/test gate command.
3. Run an independent read-only cross-model diff review:
   ```bash
   agent-ooda --review .
   ```

#### Mode B — Parallel Worktrees (2+ Disjoint Modules)
1. Write `.worktrees/tasks.json` (`{id, prompt, files, complexity?, lane?, exclude_lanes?, timeout?, review?}`)
   with strictly non-overlapping `files`. Instruct workers to inspect first, touch only listed
   `files`, run the repo gate before exiting, and end with a diff summary.
2. Execute the parallel worktree runner (`--jobs` up to `8`; `devin-fast` & `mcode` capped at `4` concurrent slots):
   ```bash
   agent-ooda .worktrees/tasks.json
   ```
3. Inspect `.worktrees/results.json` and real `git diff` on every `ooda/<id>` branch;
   reject non-empty `scope_violations` or `review.verdict != "ACCEPT"`.
4. Merge clean accepted branches (`git merge --no-ff ooda/<id>`), clean worktrees (`agent-ooda --cleanup`),
   and re-run the repo build/test gate on the merged branch.

#### Loop Exit & Mismatch Pivoting (*Destruction & Creation*)
- If the merged gate is green and reviews return `VERDICT: ACCEPT`, stop cleanly.
- Otherwise feed exact compiler/test failures or `REJECT` findings back into Phase 1/2,
  increment the loop counter, and pivot model families (automatic `30m` mismatch memory in
  `mismatch.json`; pass `--exclude-lane <prev_lane>` after downstream gate failures).
- Hard-stop and reassess with the user after `3` failed loops.

---

## Part II — Dynamic Fleet Routing, Windows, Tariffs & CLI Reference

Runtime quota state lives in `~/.local/state/agent-ooda/quota.json` (`v8`, `$XDG_STATE_HOME/agent-ooda`;
overridable via `AGENT_OODA_QUOTA_FILE`). HUD markers and `flock` leases live in `/tmp/agent-ooda-hud`
(`AGENT_OODA_HUD_DIR`).

| Lane | CLI | Effort Control & Defaults | Tier | Cycle / Cap |
|---|---|---|---|---|
| `agy-opus` | `agy` | `--effort (low..max)` + model tier; non-Gemini Claude models | `capped` | `5h + 7d` |
| `devin-fusion` | `devin` | `via-model` (no `--effort`); family `fusion` | `credits` | `credits` (reserve) |
| `devin-pro` | `devin` | `via-model`; family `swe-2` | `unlimited` | `24h + 7d` (`$20/mo` Pro) |
| `devin-fast` | `devin` | `via-model`; family `swe-2-flash` · max `4` parallel | `unlimited` | `24h + 7d` |
| `muse` | `muse` | `--reasoning-effort (none..ultra)`; default `muse-ember-1.0` (`muse-wonder-1.0` on `deep`) | `capped` | `5h + 7d` (auto-skips PAYG `api_key`) |
| `mcode` | `mcode` | `--effort (low..max)` · max `4` parallel; primary reviewer | `high-cap` | `5h + 7d` (deprioritizes `15:00-17:30 UTC` weekday peak) |
| `grok` | `grok` | `--reasoning-effort (medium\|high\|xhigh)` | `capped` | `7d` (rolling weekly) |
| `mistral` | `vibe` | `via-model` (`glm-5-3[high]`, etc.) | `capped` | `30d` (monthly; `0.5x` off-peak Z.ai UTC tariff) |

### Key Engine Guarantees
- **Expiring Window Draining (`CORNER`) & Stall Shield (`CONSERVE`):** Prioritizes `0.5x` off-peak tariffs and expiring capped clocks; protects the final active capped lane by shifting routine `act` work to `devin-pro` -> `devin-fast` -> `mcode`.
- **Auto Complexity & Skill Matching:** Omitted `--complexity` and up to 2 non-core `--skill` entries are inferred from prompt & files (`--skill none` or `OODA_NO_AUTO_SKILL=1` opts out).
- **Gemini Worker Guardrail (`exit 2`):** Rejects `gemini*` / `orchestrator*` models across `--model`, env vars, `--set-cycle`, and manifests.
- **Recursion Guard (`exit 2`):** Workers run with `AGENT_OODA_DEPTH>=1` and `ROUTER_DEPTH>=1`, blocking nested `agent-ooda` dispatch/admin calls.
- **Live Quota Kill & Cross-Model Review:** Unambiguous mid-stream quota errors trigger `SIGKILL` (<100ms failover); `--review` excludes the authoring family and never spends `devin-fusion` credits.

### CLI Command Reference

| Action | Command |
|---|---|
| Show binary version | `agent-ooda --version` |
| Show fleet status, live models, efforts & skills | `agent-ooda --status` |
| Refresh & cache live CLI model/config/skill probes | `agent-ooda --discover` |
| Run minimal 1-token prompt canaries & `muse` PAYG check | `agent-ooda --probe` |
| Show per-lane multi-window usage & UTC tariffs | `agent-ooda --usage` |
| OODA Observe: read-only codebase recon | `agent-ooda --recon "<question>"` |
| OODA Act: single-shot coding task in CWD | `agent-ooda --task "<prompt>" --files a.go,b.go` |
| Inject skill guidance into worker | `agent-ooda --task "<prompt>" --skill devin` |
| Route deep reasoning task (`xhigh` on muse/grok) | `agent-ooda --task "<prompt>" --complexity deep` |
| Override lane / model / effort | `agent-ooda --task "<prompt>" --lane agy-opus --model sonnet` |
| OODA Verify: cross-model diff gate (`mcode` first) | `agent-ooda --review [repo_dir]` |
| OODA Act: parallel worktrees | `agent-ooda .worktrees/tasks.json` |
| Parallel worktrees without striping | `agent-ooda .worktrees/tasks.json --no-stripe` |
| Exclude a failed lane on Loop 2 | `agent-ooda --task "<prompt>" --exclude-lane agy-opus` |
| Configure cycle / concurrency cap / model / effort | `agent-ooda --set-cycle devin-fast --max-parallel 4` |
| Enable / disable cooldown | `agent-ooda --enable grok` / `agent-ooda --disable grok --hours 5 --auto-wake` |
| Remove `.worktrees` and `ooda/*` branches | `agent-ooda --cleanup` |

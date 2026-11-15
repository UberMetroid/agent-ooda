---
name: router
description: >-
  Multi-agent token router and CLI dispatcher (`router` / `boyd`) for Antigravity
  (agy). Use whenever dispatching coding, reconnaissance, debugging, test writing,
  or code review to local agent CLIs (Antigravity Claude Opus 5.5 `agy-opus`,
  Devin Fusion / SWE-2 Max / SWE-2 Flash Max, Muse Ember 1.0, MiniMax mcode
  M3.1-Flash, xAI Grok 4.7, Mistral Vibe GLM-5.3) with automatic cycle reset
  tracking, expiring-budget drain priority, credit-to-free fallback, and `--skill`
  injection.
---

# Router — Multi-Agent CLI Dispatcher & Cycle Tracker

## Overview & Antigravity Conservation Strategy

Antigravity's top-level Gemini orchestrator stays strictly thin (`<10%` of total
token burn) for planning, verification, and synthesis. Offload heavy reading,
implementation, test sweeps, and diff reviews to external CLIs and your separate
Antigravity Claude pool via `router` (`~/.local/bin/router` ->
`~/.gemini/config/plugins/boyd/bin/boyd`):

- **Broad exploration & reading (`--recon`):** Offload codebase sweeps to free
  `devin-fast` (`swe-2-flash-max`) before pulling dozens of files into context.
- **Hard coding & deep reasoning (`--task`):** Drain renewable `capped` 5h
  cycles first — including your separate **Antigravity Claude 5.5 pool**
  (`agy-opus`, `claude-opus-5-5-high`, exact JSON token tracking, protected by
  `ROUTER_DEPTH` recursion guard) — then paid `devin-fusion` credits, auto-falling
  back to free `devin-fast` / `devin-pro` / `muse`.
- **Parallel multi-module work (`.worktrees/tasks.json`):** Stripe concurrent
  subtasks across isolated git worktrees with per-task `--skill` guidance and
  automatic `mcode` M3.1-Flash diff review.
- **Independent code review (`--review`):** Run read-only `mcode`
  (`minimax/MiniMax-M3.1-Flash`) over diffs instead of spending orchestrator
  tokens on line-by-line review.

## Configured Agent Fleet, Models & Effort Standards

Live quota state and cycle telemetry live outside git in
`~/.local/state/boyd/quota.json` (`v6`, overrideable via `AGENT_QUOTA_FILE`).

| Lane | CLI | Default Model | Standard Effort | Tier | Cycle / Cap |
|---|---|---|---|---|---|
| `agy-opus` | `agy` | `claude-opus-5-5-high` | `high` | `capped` | `5h` |
| `devin-fusion` | `devin` | `fusion-claude-opus-5-5-high-sidekick-swe-2-medium` | `high` (+ free `medium` sidekick) | `credits` | top-up |
| `devin-pro` | `devin` | `swe-2-max` | `max` | `unlimited` | `1h` |
| `devin-fast` | `devin` | `swe-2-flash-max` | `max` | `unlimited` | `1h` · max `4` parallel |
| `muse` | `muse` | `muse-ember-1.0` | `high` (`xhigh` on `deep`) | `capped` | `5h` |
| `mcode` | `mcode` | `minimax/MiniMax-M3.1-Flash` | `high` | `high-cap` | `5h` |
| `grok` | `grok` | `grok-4.7` | `high` (`xhigh` on `deep`) | `capped` | `5h` |
| `mistral` | `vibe` | `glm-5-3` (`zai-glm-5-3`; `--model ml4` for `Mistral Large 4`) | `high` | `capped` | `5h` |

## E-M Fly-By-Wire Routing, Parallel Caps & Mismatch Pivoting

1. **Drain Expiring Capped Cycles First (`CORNER` / $P_s \ge 0$):**
   Active `capped` lanes resetting within `90m` (`CORNER`) take priority for
   coding work so renewable windows are spent before reset.
2. **Stall Protection (`CONSERVE` / $P_s < 0$ Guard):**
   - If `<= 1` `capped` lane remains active (none in `CORNER`) and the next
     cooldown wake is `> 45m` away, `CONSERVE` triggers: routine `act` work
     spares `agy-opus` and paid `devin-fusion` by routing to free `devin-pro`
     -> `devin-fast` -> `mcode`, while `--complexity deep` / `heavy` still pulls
     full $G$ on `agy-opus`.
   - `fast` / `mechanical` / `tests` / `docs` / `recon` skips `credits`
     (`devin-fusion`) and defers all `capped` lanes behind free `devin-fast` ->
     `mcode` -> `devin-pro`.
   - `review`: `mcode` (`MiniMax-M3.1-Flash`) -> `muse` -> `devin-pro` ->
     `devin-fast` -> `agy-opus`.
3. **Max-4 Parallel Cap on `swe-2-flash-max` (`devin-fast`):**
   Parallel manifests support up to `--jobs 8`, while cross-process `flock`
   leases cap `devin-fast` at `4` concurrent workers (`max_parallel: 4`). In
   `auto` mode, a 5th concurrent task automatically spills to another free lane
   (or queues cleanly until a slot frees when `--lane devin-fast` is forced).
4. **Fast-Transient Timeouts & Cross-Model Mismatch Pivoting:**
   - Default timeouts scale by OODA regime (`3m` `observe`, `5m` `fast`, `8m`
     `act`, `10m` `heavy`, `15m` `deep`, `4m` `verify`; override via `--timeout`).
   - On crash, timeout, or `VERDICT: REJECT`, `router` pivots to a different
     model family (`claude-opus`, `swe-2`, `grok`, `glm`, `muse`, `minimax`) —
     both inline (once) and across OODA loops (`30m` memory in
     `mismatch.json`). Use `--exclude-lane` to force rotation after a failing
     test gate, or `--no-pivot` to disable.
5. **Recursion Guard (`ROUTER_DEPTH`):**
   Every dispatched worker runs with `ROUTER_DEPTH>=1` (and `agy-opus` strips
   parent `ANTIGRAVITY_*` env vars and runs with `--disable-slash-commands`).
   Nested `router --recon/--task/--review` or manifest dispatches are blocked
   with exit code `2` so `agy-opus` workers execute directly and cannot recurse.
6. **Automatic Cycle Tracking & Wakeup:**
   - Every run logs cycle starts, run counts, and tokens/cost (using exact JSON
     token totals on `agy-opus` and `vibe` `export.json`, estimated otherwise).
   - On `429` / rate-limit / quota / `status != SUCCESS` errors, `router` parses
     any explicit server `resets at <ISO-timestamp>` (falling back to
     `next_reset_at` or `now + cycle_hours`) so cyclic lanes wake automatically
     at their true reset time. `devin-fusion` marks credits exhausted and
     immediately fails over to free SWE-2 lanes.
   - `router --usage` polls `muse usage` for display without letting
     `server_sent_no_usage` override an active `quota.json` cooldown timer.

## Commands Reference

| Action | Command |
|---|---|
| Show fleet status, efforts & reset timers | `router --status` |
| Show per-lane cycle usage & live `muse` probe | `router --usage` |
| OODA Observe: read-only codebase recon | `router --recon "<question>"` |
| OODA Act: single-shot coding task in CWD | `router --task "<prompt>" --files a.py,b.py` |
| Inject skill instructions into worker | `router --task "<prompt>" --skill ooda` |
| Route deep reasoning task (`xhigh` on muse/grok) | `router --task "<prompt>" --complexity deep` |
| Override model (e.g. Sonnet on agy-opus / ML4 on vibe) | `router --task "<prompt>" --lane agy-opus --model sonnet` |
| OODA Verify: standalone M3.1-Flash diff review | `router --review [repo_dir]` |
| OODA Act: parallel worktrees (striped) | `router .worktrees/tasks.json` |
| OODA Act: parallel worktrees without striping | `router .worktrees/tasks.json --no-stripe` |
| Force a specific lane | `router --task "<prompt>" --lane agy-opus` |
| Exclude a failed lane on Loop 2 | `router --task "<prompt>" --exclude-lane agy-opus` |
| Configure cycle / concurrency cap or reset | `router --set-cycle devin-fast --max-parallel 4` |
| Re-enable a provider | `router --enable grok` |
| Put provider on cooldown (auto-wake optional) | `router --disable grok --hours 5 --auto-wake --reason "5h cap"` |
| Clean up worktrees & branches | `router --cleanup` |

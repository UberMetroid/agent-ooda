---
name: router
description: >-
  Multi-agent token router and CLI dispatcher (`router` / `ooda`) for Antigravity
  (agy). Dispatches coding, reconnaissance, debugging, test writing, and code
  review to local agent CLIs (`agy`, `devin`, `muse`, `mcode`, `grok`, `vibe`)
  without hardcoding models or efforts — each CLI uses its own default unless
  dynamically overridden via `--lane` (single or comma-separated priority),
  `--model`, `--effort`, or `--set-cycle`.
---

# Router — Multi-Agent CLI Dispatcher & Cycle Tracker

## Overview & Dynamic OODA Orchestration

Antigravity's top-level orchestrator stays thin (`<10%` of total token burn) for
planning, live agent selection, verification, and synthesis. Rather than pinning
static model names or effort levels in config, `agy` checks `router --status` /
`router --usage` and adjusts routing on the fly — letting each CLI use its own
user-configured default when omitted, or passing `--lane` (single lane or
comma-separated priority like `--lane mcode,devin-fast`), `--model`, `--effort`,
and `--timeout` per task:

- **Broad exploration & reading (`--recon`):** Offload read-only codebase sweeps
  (prefers `devin-fast` -> `mcode` -> `devin-pro` in `auto`, or follow `--lane`)
  before pulling dozens of files into context.
- **Coding & reasoning (`--task`):** Drain renewable `capped` 5h cycles first
  (`agy-opus`, `muse`, `grok`, `mistral`), keep paid `devin-fusion` credits in
  reserve, and fall back seamlessly to `unlimited`/`high-cap` lanes (`devin-pro`,
  `devin-fast`, `mcode`).
- **Parallel multi-module work (`.worktrees/tasks.json`):** Stripe concurrent
  subtasks across isolated git worktrees with per-task `--skill` guidance,
  optional per-task `lane`/`model`/`effort`, and automatic cross-model review.
- **Independent code review (`--review`):** Run cross-model read-only diff
  review (`mcode` first in `auto`, or pass `--lane`, `--model`, `--effort`).
  Targeted working-tree diffs (`<= 8 KiB`) on `mcode` invoke native `mcode exec
  review` (`--permission smart --max-steps 15`), while larger (`> 8 KiB`),
  committed, or non-`mcode` diffs run a single-pass inline patch review,
  falling back automatically across off-family backup lanes on cooldown,
  non-zero exit, or `UNKNOWN` verdicts.

## Configured Agent Fleet & Dynamic Defaults

Live quota state and cycle telemetry live outside git in
`~/.local/state/ooda/quota.json` (`v7`, `$XDG_STATE_HOME/ooda` when set; overrideable via `AGENT_QUOTA_FILE`).
All lanes default to `model: null` and `effort: null` so each CLI uses its own user default unless overridden at dispatch time (`--model`, `--effort`, `AGENT_MODEL`/`AGENT_EFFORT`) or persisted per pool (`--set-cycle`).

| Lane | CLI | Model & Effort Default | Tier | Cycle / Cap |
|---|---|---|---|---|
| `agy-opus` | `agy` | CLI/user default (`--model` / `--effort` optional) | `capped` | `5h` |
| `devin-fusion` | `devin` | CLI/user default (`--model` optional) | `credits` | top-up |
| `devin-pro` | `devin` | CLI/user default (`--model` optional) | `unlimited` | `1h` |
| `devin-fast` | `devin` | CLI/user default (`--model` optional) | `unlimited` | `1h` · max `4` parallel |
| `muse` | `muse` | CLI/user default (`--model` / `--effort` optional) | `capped` | `5h` |
| `mcode` | `mcode` | CLI/user default (`--model` / `--effort` optional) | `high-cap` | `5h` · max `4` parallel |
| `grok` | `grok` | CLI/user default (`--model` / `--effort` optional) | `capped` | `5h` |
| `mistral` | `vibe` | CLI/user default (`--model` optional) | `capped` | `5h` |

## E-M Fly-By-Wire Routing, Parallel Caps & Mismatch Pivoting

1. **Drain Expiring Capped Cycles First (`CORNER` / $P_s \ge 0$):**
   Active `capped` lanes resetting within `90m` (`CORNER`) take priority for
   coding work so renewable windows are spent before reset.
2. **Stall Protection (`CONSERVE` / $P_s < 0$ Guard):**
   - If `<= 1` `capped` lane remains active (none in `CORNER`) and the next
     cooldown wake is `> 45m` away, `CONSERVE` triggers: routine `act` work
     spares `agy-opus` and paid `devin-fusion` by routing to `devin-pro`
     -> `devin-fast` -> `mcode`, while `--complexity deep` / `heavy` still pulls
     full $G$ on `agy-opus`.
   - `fast` / `mechanical` / `tests` / `docs` / `recon` skips `credits`
     (`devin-fusion`) and defers `capped` lanes behind `devin-fast` ->
     `mcode` -> `devin-pro`.
   - `review` (cross-model backup order): `mcode` -> `devin-fast` -> `devin-pro`
     -> `grok` -> `mistral` -> `muse` -> `agy-opus` (or any custom priority via
     `--review --lane <lanes>`). The author lane's model family is pushed to the
     back so a model never grades its own diff, and `devin-fusion` credits are
     never spent on review. Busy, quota-/auth-failed, timed-out, non-zero-exit,
     or `UNKNOWN`-verdict reviewers hand off automatically; `[P0-3]` findings
     always fail closed.
3. **Max-4 Parallel Caps on `devin-fast` and `mcode`:**
   Parallel manifests support up to `--jobs 8`, while cross-process `flock`
   leases cap both `devin-fast` and `mcode` at `4` concurrent workers each
   (`max_parallel: 4`). In `auto` mode, a 5th concurrent task or review spills
   to another free lane (or queues cleanly when the lane is forced). Single-pass
   inline `mcode` reviews keep the verify stage fast even across 4 parallel
   worktrees.
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
   - 5h capped clocks (`agy-opus`, `muse`, `grok`, `mistral`) auto-wake: once
     `cooldown_until` / `next_reset_at` passes, the lane flips back to `active`
     on the next `quota.json` load and resumes draining in every mode — single
     `--task` (capped-first chain), parallel manifests (`muse` / `grok` /
     `mistral` lead the stripe pool), `--complexity deep` (`agy-opus` -> `muse`
     -> `grok` -> `mistral`), mismatch pivots (family rotation lands on the
     freshly woken lanes), and review fallback (`grok` -> `mistral` -> `muse`).
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
| OODA Verify: standalone cross-model diff review (mcode first) | `router --review [repo_dir]` |
| OODA Act: parallel worktrees (striped) | `router .worktrees/tasks.json` |
| OODA Act: parallel worktrees without striping | `router .worktrees/tasks.json --no-stripe` |
| Force a specific lane | `router --task "<prompt>" --lane agy-opus` |
| Exclude a failed lane on Loop 2 | `router --task "<prompt>" --exclude-lane agy-opus` |
| Configure cycle / concurrency cap or reset | `router --set-cycle devin-fast --max-parallel 4` |
| Re-enable a provider | `router --enable grok` |
| Put provider on cooldown (auto-wake optional) | `router --disable grok --hours 5 --auto-wake --reason "5h cap"` |
| Clean up worktrees & branches | `router --cleanup` |

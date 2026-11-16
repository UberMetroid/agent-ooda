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
  before pulling dozens of files into context. Omitted `--complexity` defaults to
  `recon`, and non-core domain `--skill` entries auto-match the prompt and files
  (pass `--skill none` or `OODA_NO_AUTO_SKILL=1` to disable auto-matching).
- **Coding & reasoning (`--task`):** Drain renewable `capped` pools (`agy-opus`,
  `muse`, `grok`, `mistral`; prioritizing off-peak `0.5x` UTC tariff lanes and
  soonest resets), keep paid `devin-fusion` credits in reserve, and fall back
  seamlessly to `devin-pro`, `devin-fast`, `mcode`. Omitted `--complexity` and
  `--skill` are inferred automatically and tune worker model tier + `--effort`.
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
`~/.local/state/ooda/quota.json` (`v8`, `$XDG_STATE_HOME/ooda` when set; overrideable via `AGENT_QUOTA_FILE`).
All lanes default to `model: null` and `effort: null` so each CLI uses its own user default unless overridden at dispatch time (`--model`, `--effort`, `AGENT_MODEL`/`AGENT_EFFORT`), persisted per pool (`--set-cycle`), or tuned by complexity regime.

| Lane | CLI | Model & Effort Default | Tier | Cycle / Cap |
|---|---|---|---|---|
| `agy-opus` | `agy` | CLI/user default (`--model` / `--effort` optional) | `capped` | `5h + 7d` |
| `devin-fusion` | `devin` | CLI/user default (`--model` optional) | `credits` | `credits` (top-up reserve) |
| `devin-pro` | `devin` | CLI/user default (`--model` optional) | `unlimited` | `24h + 7d` (`$20/mo` Devin Pro) |
| `devin-fast` | `devin` | CLI/user default (`--model` optional) | `unlimited` | `24h + 7d` · max `4` parallel |
| `muse` | `muse` | `muse-ember-1.0` (`muse-wonder-1.0` on `deep`) | `capped` | `5h + 7d` (skips PAYG `api_key`) |
| `mcode` | `mcode` | CLI/user default (`--model` / `--effort` optional) | `high-cap` | `5h + 7d` · max `4` parallel |
| `grok` | `grok` | CLI/user default (`--model` / `--effort` optional) | `capped` | `7d` (rolling weekly) |
| `mistral` | `vibe` | CLI/user default (`--model` optional; `0.5x` off-peak Z.ai) | `capped` | `30d` (monthly) |

## Live Capability & Skill Discovery (`--status` / `--discover` / `--probe`) + Gemini Guardrail

`router --status` and `router --discover` introspect non-secret local CLI configs, cached CLI model probes (`discovery.json` beside `quota.json`, `6h` TTL), live UTC time-of-day tariff badges (`Z.ai` `0.5x` off-peak / `2.0x` `06:00-10:00 UTC` Mon-Fri, `DeepSeek` `0.5x` off-peak, `mcode` `1.25x` `15:00-17:30 UTC` Mon-Fri), and installed `SKILL.md` frontmatter. `router --probe` runs minimal 1-token prompt canaries (plus `muse usage --json`, skipping `credits`; updates local `quota.json`) to detect out-of-band exhaustion:

- **Per-CLI active models & effort mechanics:**
  - `agy-opus` (`agy`): reads `~/.gemini/antigravity-cli/settings.json` + `agy models`; effort controlled via `--effort` (`low..max`) and model tier suffix.
  - `devin-fusion` / `devin-pro` / `devin-fast` (`devin`): resolves `agent.preferred_family_models` (`fusion`, `swe-2`, `swe-2-flash`) from `~/.config/devin/config.json` + `devin models`; **no `--effort` flag** (`via-model` suffix like `-high`/`-max`).
  - `muse` (`muse`): reads `~/.config/muse/settings.json` (`model`, `model_reasoning_efforts`); effort via `--reasoning-effort` (`none..ultra`).
  - `mcode` (`mcode`): reads `~/.minimax/config.yaml` (`defaultModel`, `defaultModelThinking.effort`, MiniMax models); effort via `--effort` (`low..max`).
  - `grok` (`grok`): reads `~/.grok/config.toml` + `models_cache.json` + `grok models`; effort via `--reasoning-effort` (`medium|high|xhigh`).
  - `mistral` (`vibe`): reads `~/.vibe/config.toml` (`active_model`, `models` alias/thinking entries such as `glm-5-3[high]` or `mistral-medium-3.5[off]`); **no `--effort` flag** (`via-model` alias in `config.toml`).
- **Live `--skill` catalog:** Scans workspace `.agents/skills`, plugin `skills`, and `~/.agents/skills` for `SKILL.md` names and 1-line descriptions.
- **Hard Gemini-pool guardrail:** Any `gemini*` or `orchestrator*` model on a worker lane (`--model`, `AGENT_MODEL`, `--set-cycle`, manifest `tasks.json`, or direct worker build) is rejected with `exit 2`. When `agy` defaults to Gemini/Orchestrator in `settings.json`, `router` resolves a safe non-Gemini Claude model from `agy models` (refusing unguarded invocation if none is found).

## E-M Fly-By-Wire Routing, Parallel Caps & Mismatch Pivoting

1. **Drain Expiring Capped Cycles First (`CORNER` / $P_s \ge 0$):**
   Active `capped` lanes nearing reset (90m on 5h, 2h on 24h, 12h on 7d, 36h
   on 30d windows) take priority for coding work so renewable windows are spent
   before reset.
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
   Nested `router --recon/--task/--review/--probe` or manifest dispatches are blocked
   with exit code `2` so `agy-opus` workers execute directly and cannot recurse.
6. **Automatic Cycle Tracking, Wakeup & Live Quota Kill:**
   - Every run logs inner/outer window cycle starts, run counts, and tokens/cost
     (using exact JSON token totals on `agy-opus` and `vibe` `export.json`, estimated
     otherwise).
   - Unambiguous rate-limit/quota errors mid-stream trigger immediate `SIGKILL`
     (<100ms failover, without false-positive killing plain mentions of `429`).
     On quota/rate-limit failure, `router` parses explicit ISO timestamps OR
     relative reset strings (`"resets in 2d 5h"`, `Retry-After`) and wakes at the
     tracked `7d`/`30d` reset when known (or escalates `24h` -> `7d` when exhausted
     out-of-band). `muse` automatically skips `auto` routing and HUD counts while
     `reason: "api_key"` (PAYG) is active, remaining usable via explicit `--lane muse`.
   - `router --usage` shows inner + outer windows, UTC tariffs, and `muse`
     subscription/PAYG status without overriding active `quota.json` cooldowns.

## Commands Reference

| Action | Command |
|---|---|
| Show fleet status, live models, efforts & skills | `router --status` |
| Refresh & cache live CLI model/config/skill probes | `router --discover` |
| Run minimal 1-token prompt canaries & `muse` PAYG check | `router --probe` |
| Show per-lane multi-window usage & UTC tariffs | `router --usage` |
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

---
name: agy-router
description: >-
  Zero-trust, quota-aware multi-agent CLI router (`agy-router`) for Antigravity:
  per-stage routing matrix (`observe`, `orient`, `act`, `verify`), CLI fleet
  discovery, isolated git worktrees, and independent cross-model diff review.
---

# `agy-router` — Multi-Agent Fleet Router

Use `agy-router` to offload codebase recon, architecture orientation, single-shot edits, parallel `.worktrees/tasks.json` sweeps, and independent cross-model diff verification across your installed agent CLIs. For the full 4-phase engineering discipline, invoke `/ooda`; for security auditing, invoke `/harden`; to configure stage pins interactively, invoke `/lane`.

## Per-Stage Routing Matrix (`v3.1.0`)
Each routed stage (`observe`, `orient`, `act`, `verify`) defaults to `auto` and resolves via:
**`CLI flag > --local > --profile > global (--set-stage / --select) > auto`**.

- **Runtime State:** Global pins & cooldowns live in `~/.local/state/agy-router/quota.json` (`v11`, `$XDG_STATE_HOME/agy-router`; overridable via `AGY_ROUTER_QUOTA_FILE`). Per-repo `--local` pins live out-of-git in `~/.local/state/agy-router/workspaces/<id>.json` (surviving `agy-router --cleanup`). HUD markers & `flock` leases live in `/tmp/agy-router-hud` (`AGY_ROUTER_HUD_DIR`).
- **4 Selector Surfaces:**
  1. `agy-router --select` (`-s`): Terminal arrow-key TUI (`↑`/`↓`, `Enter`, `b`, `q`, `Tab` scope toggle, `--until-reset`).
  2. `/lane`: Conversational `ask_question` wizard inside Antigravity.
  3. CLI pins: `--set-stage <stage|all|preset>=<val>` (`auto`, `conserve`, `deep`, `devin`, `grok`, `mcode`, `muse`, `mistral`) and `--reset-stages [--local]`.
  4. `8` agent profiles under `plugins/agy-router/agents/`: `router-{auto,conserve,deep,devin,grok,mcode,mistral,muse}`.
- **Guardrails:**
  - **Recursion Guard (`exit 2`):** Workers run with `AGY_ROUTER_DEPTH>=1`, `AGENT_OODA_DEPTH>=1`, and `ROUTER_DEPTH>=1`, blocking nested dispatch/admin calls.
  - **Gemini & PAYG Guardrails:** Rejects Gemini orchestrator/flash models as headless workers; skips `[PAYG api_key]` Muse unless `--lane muse`; treats `devin-fusion` (`24h + 7d`) as a capped subscription pool drained automatically before reset (excluding explicit-only `tier: "credits"` pools from unattended `auto`/`--review`).
  - **Fail-Closed Read-Only & Merkle Cache:** `--recon`, `--orient`, and `--review` snapshot the repo, fail closed (`REJECT`/`exit 1`) on unexpected writes, and cache identical Merkle-matched trees (`--no-cache` or `AGY_ROUTER_NO_CACHE=1` to bypass).
  - **Auto Complexity & Skills:** Omitted `--complexity` and up to 2 non-core skills are inferred from prompt & files (`--skill none` or `AGY_ROUTER_NO_AUTO_SKILL=1` opts out). HUD & TUI colors follow `agy` themes (`AGY_ROUTER_THEME`).

## Essential Commands

| Operation | Command |
| :--- | :--- |
| Show version | `agy-router --version` |
| Fleet status, stage matrix, models & skills | `agy-router --status` |
| Interactive arrow-key stage configurator | `agy-router --select` (`-s`, add `--local`) |
| Pin stage or apply 1 of 8 presets | `agy-router --set-stage act=grok --effort xhigh` / `--set-stage preset=deep` |
| Mask stage to `auto` in this repo only | `agy-router --set-stage act=auto --local` |
| Reset global or `--local` pins | `agy-router --reset-stages [--local]` |
| Refresh live CLI model/config/skill probes | `agy-router --discover` |
| Run 1-token prompt canaries & `muse` PAYG check | `agy-router --probe` |
| Show per-lane multi-window usage & UTC tariffs | `agy-router --usage` |
| Read-only codebase recon (`observe` stage) | `agy-router --recon "<question>"` |
| Read-only root-cause & plan (`orient` stage) | `agy-router --orient "<question>"` |
| Single-shot scoped coding task (`act` stage) | `agy-router --task "<prompt>" --files a.go,b.go` |
| Apply temporary preset / skill / complexity | `agy-router --task "<prompt>" --profile conserve --skill <name> --complexity deep` |
| Explicit lane / model / effort / exclusion | `agy-router --task "<prompt>" --lane agy-claude --model opus --exclude-lane grok` |
| Auto-expire stage pin when lane quota resets | `agy-router --set-stage act=muse --until-reset` |
| Cross-model diff verification + P0 hygiene | `agy-router --review [repo_dir]` |
| 0-token P0 secret & P1 hardening hygiene scan | `agy-router --hygiene [repo_dir]` |
| Parallel worktree swarm (`--no-stripe` disables round-robin) | `agy-router .worktrees/tasks.json [--no-stripe]` |
| Configure cycle / max-parallel / model / effort | `agy-router --set-cycle devin-fast --max-parallel 4` |
| Enable / disable lane cooldown | `agy-router --enable grok` / `agy-router --disable grok --hours 5 --auto-wake` |
| Remove `.worktrees` and `ooda/*` branches | `agy-router --cleanup` |

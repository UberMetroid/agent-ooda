# agy-router

**Static Go multi-agent fleet router, quota-aware scheduler, opt-in `/ooda` + `/harden` skills, zero-trust cross-model verifier, and live thermal `tok/s` statusLine HUD.**

`UberMetroid/agy-router` packages one zero-dependency Go 1.22+ (`CGO_ENABLED=0`, zero external `require`s) engine paired with host plugin adapters under `plugins/`—starting with [`plugins/agy-router`](plugins/agy-router) for Antigravity (`agy`) and structured so `mcode` and future coding hosts share the same fleet state, `flock` slot leases, git worktrees, and diff verifier.

- **Site & web installer:** **<https://ubermetroid.github.io/agy-router/>**
- **Releases & static binaries (`SHA256SUMS`):** **<https://github.com/UberMetroid/agy-router/releases/latest>**

## Quick Install (Zero Go Required)

```bash
curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash
```

Installs or updates `UberMetroid/agy-router` in `${XDG_DATA_HOME:-~/.local/share}/agy-router` (override with `AGY_ROUTER_HOME`). If Go 1.22+ is on `PATH`, it builds [`plugins/agy-router/bin/agy-router`](plugins/agy-router/bin/agy-router) locally with `CGO_ENABLED=0`; otherwise it downloads the matching static GitHub Release binary (`linux`/`darwin`, `amd64`/`arm64`) and verifies its SHA-256 digest against `SHA256SUMS` before installing.

## Architecture & Multi-Call Binary

All runtime roles compile into one static binary (`plugins/agy-router/bin/agy-router`) dispatched via `argv[0]` basename:

| Binary / Symlink | Entrypoint (`cmd/agy-router`) | Responsibility |
| --- | --- | --- |
| `agy-router` | CLI dispatcher & fleet admin | `--version`, `--recon`, `--orient`, `--task`, manifests, `--review`, `--select`/`-s`, `--set-stage`, `--status`, `--discover`, `--usage`, `--probe` |
| `agy-router-hud` / `statusline.py` | Live thermal statusLine HUD | Renders live ANSI statusBar telemetry from NDJSON worker markers + `quota.json` |
| `agy-router-hook` | Antigravity lifecycle hook | Tracks injected skills per session and garbage-collects dead worker markers |
| `__setup` / `__wire` | Installer helpers | Safe JSON validation and idempotent `settings.json` `statusLine` wiring |

```text
agy-router/
├── cmd/agy-router/      # multi-call entrypoint (agy-router / agy-router-hud / agy-router-hook)
├── internal/
│   ├── fleet/           # quota store (v12), stage matrix, multi-window cycles, flock leases
│   ├── envelope/        # quota-aware lane selection, UTC tariffs, CLI discovery, mismatch pivot
│   ├── dispatch/        # io.Pipe NDJSON stream pump, tok/s rate meter, live quota kill, worktrees
│   ├── verify/          # HeadRef/unborn diff gathering, untracked hunks, off-family review
│   ├── worktree/        # isolated ooda/<id> git worktrees, stage resolver, scope & skill loader
│   ├── hud/             # ANSI statusLine renderer, 5-band tok/s gradient, hook recorder
│   ├── cli/             # arg parser, --select TUI, depth guard, stage & fleet reporting
│   └── install/         # cross-host setup/unwire helpers
├── plugins/agy-router/  # Antigravity (agy) plugin manifest, skills (/lane, /ooda, /harden), rule, hooks & bin shims
└── qa/gates/            # AST & repo House-Law gate suite (`make all`)
```

## First Principles & Zero-Trust Design

- **First-Principles Quota Scheduling (`EXPIRING` / `READY` / `SPARING`):** Renewable `5h`/`7d`/`30d` subscription pools (`agy-claude`, `devin-fusion`, `muse`, `mcode`, `grok`, `mistral`) reset to zero on a clock, so `auto` spends expiring windows and `0.5x` UTC off-peak tariffs first (`EXPIRING`), balances healthy bounded pools (`READY`), and spares your final active bounded lane (`SPARING`) by shifting routine tasks to unlimited Devin Pro pools (`devin-pro`, `devin-fast`).
- **Thin Orchestrator & Isolated Execution:** Top-level chat stays in Decide (`<10%` token burn) while workers run read-only recon (`--recon`), deep root-cause synthesis (`--orient`), and isolated `git worktree` tasks (`ooda/<id>`) gated by atomic `flock` concurrency leases.
- **Zero-Trust Verification & Spend Guardrails:** No model family ever grades its own code (`verify` excludes all authoring families across attempts and fails closed on `[P0-3]`, `UNKNOWN`, `SKIPPED`, or scope violations); `tier: "credits"` pools (`CREDITS`) and PAYG `muse` (`PAYG`) never run unattended or in `--review`, while subscription `devin-fusion` (`capped`, `24h + 7d`) drains automatically before reset; worker lanes hard-reject `gemini*`/`orchestrator*` models and recursive sub-router calls (`exit 2`).

## The 8-Lane Fleet

`agy-router` is currently configured for the **6 agent CLIs** (`8` routing lanes) we actively run and subscribe to:

| Lane | Engine / CLI | Tier / clock |
| --- | --- | --- |
| `agy-claude` | [Google Antigravity CLI](https://antigravity.google/) (`agy`, Sonnet `act` / Opus `deep`) | capped, `5h + 7d` |
| `muse` | [Meta Muse Code](https://dev.meta.ai/products/muse-code) (`muse`, `muse-ember-1.0` / `muse-wonder-1.0` on `deep`) | capped, `5h + 7d` (auto-skips PAYG `api_key`) |
| `grok` | [xAI Grok Build](https://github.com/xai-org/grok-build) (`grok`) | capped, `7d` rolling weekly |
| `mistral` | [Mistral Vibe](https://github.com/mistralai/mistral-vibe) (`vibe`, optional [Z.ai](https://docs.z.ai/) GLM `0.5x` off-peak) | capped, `30d` monthly |
| `mcode` | [MiniMax Code](https://github.com/MiniMax-AI/minimax-code) (`mcode`, off-family reviewer) | capped `5h + 7d`, **max 4 parallel** |
| `devin-fast` | [Cognition Devin CLI](https://docs.devin.ai/) Fast pool (`devin`) | unlimited `$20/mo` Pro, `24h + 7d`, **max 4 parallel** |
| `devin-pro` | [Cognition Devin CLI](https://docs.devin.ai/) Pro pool (`devin`) | unlimited `$20/mo` Pro, `24h + 7d` |
| `devin-fusion` | [Cognition Devin CLI](https://docs.devin.ai/) Fusion pool (`devin`) | capped, `24h + 7d` (**max 4 parallel**, subscription) |

Model IDs and efforts are **discovered dynamically, not hardcoded**: `agy-router --status` and
`agy-router --discover` inspect non-secret local CLI configs, cached CLI model lists, effort flags
(`--effort`/`--reasoning-effort` vs `via-model`), live UTC off-peak tariffs (`Z.ai` / `DeepSeek`
`0.5x` off-peak, `mcode` weekday peak-jam avoidance), and installed `--skill` definitions. When
`--complexity` or `--skill` are omitted, `agy-router` infers the task regime and matches up to 2
non-core domain skills automatically, tuning model tier and `--effort`. `agy-router --probe` runs
minimal 1-token prompt canaries (never probing `credits`; updates local `quota.json`). Hard binary guardrails exit `2`
on any `gemini*`/`orchestrator*` worker model and block recursive execution (`agy-router --recon`,
`--task`, `--review`, `--probe`, `--cleanup`, `--enable`, `--disable`, `--set-cycle`) whenever
`AGY_ROUTER_DEPTH>=1`, `AGENT_OODA_DEPTH>=1`, or `ROUTER_DEPTH>=1`.

**Direct `<cli>:<model>` Routing & OODA + Verify Matrix (`v3.2.1`):**

- **OODA + Verify Matrix (`observe`, `orient`, `decide [Orchestrator]`, `act`, `verify`):** `--status` lists all 15 selectable CLI models (including `agy:claude-opus-5-5-high [opt-in]`, `agy:claude-sonnet-5-5-high`, `mcode:MiniMax-M3`, `mcode:MiniMax-M3.1-Flash`, `muse:*`, `devin:*`, `grok:*`, and `vibe:*`) and displays the full 5-row matrix where worker stages resolve **`CLI (--model / --lane) > --local > --profile > global > auto`** while `Decide` stays with you and the top-level orchestrator. Pass `--model agy:claude-opus-5-5-high`, `--set-stage orient=agy:claude-opus-5-5-high`, or `--exclude-model` directly without thinking in "lanes" (legacy `--lane` aliases still work); `--select` opens a 1-step `<cli>:<model>` arrow-key picker, and post-task `verify` follows `--local`/`--profile`/global pins so `--task` worker flags never leak onto the off-family reviewer.
- **Soft-Pin Failover & Off-Family Verify:** Pinned stages soft-failover on 429s/cooldown down their lane CSV or quota-aware `auto`, scrubbing primary-lane `--model`/`--effort` overrides when failing over to another lane (and never falling back onto `tier: "credits"` pools or PAYG `muse`); `verify` always enforces a non-authoring model family.
- **Selectors & Opt-In Skills (`0` tokens unless invoked):**
  - `agy-router --select` (`-s`): Interactive terminal TUI (`↑`/`↓`, `Enter`, `b`/Backspace to go back, `q` to quit, `Tab` to switch scope; live `agy` theme colors, sticky or `--until-reset`, global `quota.json` `v12` or `--local` `~/.local/state/agy-router/workspaces/<id>.json`).
  - `/lane`: Native Antigravity in-chat selector skill (`ask_question`) for per-stage pins, presets, and resets.
  - `/ooda`: Standalone opt-in 4-phase OODA debugging & multi-file engineering workflow (`agy-router --recon`, `--orient`, `--task`, `--review .`).
  - `/harden`: Standalone opt-in 4-phase zero-hallucination trust-boundary skill (`--hygiene` + Finder `--orient` + off-family Skeptic falsification + compile-clean Red-to-Green refusal unit tests).
  - `/agents`: `8` native `agy` workflow profiles (`router-auto`, `router-conserve`, `router-deep`, `router-devin`, `router-grok`, `router-mcode`, `router-muse`, `router-mistral`).
- **Observe & Orient (Token-Lean & Read-Only Guarded):** `--recon` (`observe`) and `--orient` (`orient`) skip auto-matched workspace skills unless `--skill` is passed, run `mcode` read-only recon/orient with `--max-steps 20` (while inline `mcode` `--review` scales dynamically up to `35`/`45` steps), cache identical Merkle-hashed read-only results across `--recon`/`--orient`/`--review` (`--no-cache` to bypass), strip `GH_TOKEN`/`GITHUB_TOKEN` (plus `ANTIGRAVITY_*`) from read-only workers, snapshot the repo before/after to fail closed on any unexpected file write, and isolate clean worktrees via `git worktree add -B`.
- **Act & Parallel:** Soonest-resetting capped lanes drain first with EWMA health scoring (`health_ewma` demoting flaky lanes and recovering on clean runs) plus an NDJSON stream stall watchdog (`180s` idle kill + immediate failover); manifest batches isolate `git worktree` stripes with atomic `flock` slot leases.
- **Conserve & Verify:** When one capped lane remains, unlimited `devin-pro` / `devin-fast` absorb routine work; every non-empty diff runs through an independent model family.

### Other Agent CLIs (Not Currently Configured)

These coding-agent CLIs are **not** currently wired in `agy-router` because we don't carry paid subscriptions for them today. If you use them and want to add a lane, adapters live modularly under `internal/dispatch/` and `internal/fleet/`:

- **[Anthropic Claude Code](https://code.claude.com/docs/en/overview)** (`claude`) — no standalone Claude subscription configured (Claude Sonnet/Opus worker execution in our fleet currently routes through Antigravity's `agy-claude` pool).
- **[OpenAI Codex CLI](https://github.com/openai/codex)** (`codex`) — requires a ChatGPT plan or OpenAI API subscription.
- **[Cursor CLI](https://cursor.com/cli)** (`cursor-agent`) — requires a Cursor Pro/paid plan.
- **[GitHub Copilot CLI](https://github.com/features/copilot)** (`copilot`) — requires a GitHub Copilot paid plan.
- **[Sourcegraph Amp](https://ampcode.com/)** (`amp`) — requires an Amp account / credits.
- **[Factory Droid](https://factory.ai/)** (`droid`) — requires a Factory subscription.

## Cross-Model Verification

`devin-pro` (`swe-2-high`) leads off-family diff review when uncapped (rotating to `mcode` / `grok` / `mistral` whenever Devin authored the code). Uncommitted,
staged (including unborn `HEAD`), untracked (synthetic hunks), and worker-committed changes
(detected via pre-task `HeadRef` comparison so `git commit` inside a task/worktree never evades
review or scope checks) are all audited fail-closed with a `240s` review budget:

- **Native repo review (`<= 8 KiB` working tree):** `mcode exec review --permission smart --max-steps 15`.
- **Inline stream audit (`> 8 KiB` or committed diffs):** `--permission smart --prompt-mode coding --max-steps 25`.
- **Fail-closed fallback:** on cooldown, nonzero exit, `SKIPPED`, `UNKNOWN`, or `[P0-3]` findings, review rotates off-family (`devin-pro → mcode → grok → mistral → muse → devin-fusion → agy-claude → devin-fast`) or fails non-zero.

## Live Theme-Synced `tok/s` HUD (`bin/agy-router-hud`)

`bin/agy-router-hud` (`statusline.py`) reads live NDJSON stream telemetry and matches `agy`'s live theme (`colorScheme` in `~/.gemini/antigravity-cli/settings.json` — `dark`, `light`, `tokyo night`, `solarized dark`/`light`, `colorblind-friendly dark`/`light`, seasonal `halloween`, `COLORFGBG` terminal auto-detect, or `AGY_ROUTER_THEME`), swapping colors immediately when `agy` swaps themes.

It displays a crisp `ROUTER` standby badge when idle and renders live phase with distinct static icons (`◎ observe`, `◈ orient`, `◇ decide`, `▸ act`, `✦ verify`), active agent + model, `tool Xs` waits, a **5-band ANSI 256-color thermal `tok/s` heat ramp** (blue `<25` → teal → amber → orange → red `>=180`), a 4-second `✓`/`✗` completion flash, and one dedicated line per parallel worker:

```text
ROUTER
◈ orient · Gemini 4 Argon (Low)
◇ decide · Gemini 4 Argon (Low)
◎ observe · devin-fast (swe-2-flash-max) · ~42 tok/s
▸ act · muse (muse-ember-1.0) · ~96 tok/s
▸ act · devin-pro (swe-2-max) · ~118 tok/s
▸ act · devin-pro (swe-2-max) · ~112 tok/s
✦ verify · mcode (MiniMax-M3.1-Flash) · ~195 tok/s
```

## CLI Quick Reference

```bash
agy-router --version                        # print binary version
agy-router --status                         # router stage matrix, fleet regimes, models & skills
agy-router --select                         # interactive arrow-key TUI (-s; add --local for repo)
agy-router --set-stage act=grok --effort xhigh
agy-router --set-stage act=auto --local     # mask one stage to auto in this repo
agy-router --set-stage preset=conserve      # presets: auto, conserve, deep, devin, grok, mcode, muse, mistral
agy-router --reset-stages                   # reset global pins to auto (--local clears repo overrides)
agy-router --discover                       # refresh live CLI capability probe cache
agy-router --probe                          # minimal 1-token prompt canary & muse PAYG check
agy-router --usage                          # per-lane 5h/24h/7d/30d windows, UTC tariffs & quota state telemetry
agy-router --recon "Map auth flow"          # Observe: read-only recon (free lanes first)
agy-router --orient "Trace cache race"      # Orient: read-only root-cause & architecture plan
agy-router --task "Fix parser bug" --files internal/cli/parse_args.go --skill devin
agy-router .worktrees/tasks.json -j 4
agy-router --review .                       # standalone cross-model working-tree/HEAD diff gate + P0 hygiene check
agy-router --hygiene .                      # 0-token P0 secret, key & P1 hardening footgun scan
agy-router --cleanup                        # remove .worktrees and temporary ooda/* branches
```

## Install, Update & Uninstall

### Web installer or local checkout

```bash
# Web installer (clones/updates ~/.local/share/agy-router, uses Go or SHA-256 prebuilt binary)
curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash

# Local source checkout
git clone https://github.com/UberMetroid/agy-router.git && cd agy-router
./install.sh            # or: make install   (./install.sh --help lists options)
agy plugin list         # restart agy first, then confirm "agy-router" is enabled
```

`install.sh` is idempotent:

- **Links** `~/.gemini/config/plugins/agy-router` → `plugins/agy-router`,
  `~/.local/bin/agy-router` → `plugins/agy-router/bin/agy-router`, and
  `~/.gemini/antigravity-cli/statusline.py` → `plugins/agy-router/bin/agy-router-hud`.
- **Migrates & archives** legacy `agent-ooda` and `v1.x` owned `ooda`/`router` symlinks (leaving unrelated `ooda` binaries such as `openOODA-core` untouched) and `${XDG_STATE_HOME:-~/.local/state}/{agent-ooda,ooda}/` state into `${XDG_STATE_HOME:-~/.local/state}/agy-router/`.
- **Seeds** `${XDG_STATE_HOME:-~/.local/state}/agy-router/quota.json` and registers `~/.agents/skills` in `~/.gemini/config/skills.json`.
- **Wires `statusLine`** in `~/.gemini/antigravity-cli/settings.json` (skip with `AGY_ROUTER_NO_STATUSLINE=1`):

  ```json
  {
    "statusLine": {
      "type": "command",
      "command": "~/.gemini/antigravity-cli/statusline.py",
      "enabled": true,
      "padding": 0,
      "stack_with_default": true
    }
  }
  ```

- **Validates both JSON files first** (malformed JSON fails closed before touching disk) and prints a CLI availability summary.

### Update & uninstall

```bash
# Update via web installer (guards against local changes or unpushed commits)
curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash

# Update from local checkout (links resolve directly into the checkout)
git pull && ./install.sh

# Uninstall (removes agy-router symlinks & restores previous statusLine; add --purge to remove state/checkout)
./install.sh --uninstall --purge
# or via web installer:
curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash -s -- --uninstall --purge
```

Runtime state persists under `${XDG_STATE_HOME:-~/.local/state}/agy-router/quota.json` (`v12`, `AGY_ROUTER_QUOTA_FILE`) and per-repo `--local` pins under `~/.local/state/agy-router/workspaces/<id>.json`; live HUD markers and `flock` leases live in `/tmp/agy-router-hud` (`AGY_ROUTER_HUD_DIR`).

## House-Law Verification

```bash
make all   # portable, line-cap, density, vocab, shape, plugin-check, go vet, go test -race, dogfood
```

See [`AGENTS.md`](AGENTS.md) for the enforced rules: stdlib-only Go (`0` external `require`s), 16–256 lines & `<= 128` cols per file, `<= 8` entries per directory, `<= 8` funcs per file, `<= 64` code lines per function, nesting `<= 4`, `<= 1` statement per line, identifiers `<= 32` chars, and zero hard-coded home paths, secrets, or `sh -c`.

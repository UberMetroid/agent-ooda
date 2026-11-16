# agent-ooda

**Static Go OODA multi-agent router, Energy-Maneuverability quota scheduler, cross-model diff verifier, and live `tok/s` Bubble-Canopy HUD.**

`UberMetroid/agent-ooda` packages one zero-dependency Go 1.22+ (`CGO_ENABLED=0`, zero external `require`s) engine paired with host plugin adapters under `plugins/`—starting with [`plugins/agent-ooda`](plugins/agent-ooda) for Antigravity (`agy`) and structured so `mcode` and future coding hosts share the same fleet state, `flock` slot leases, git worktrees, and diff verifier.

- **Site & web installer:** **<https://ubermetroid.github.io/agent-ooda/>**
- **Releases & static binaries (`SHA256SUMS`):** **<https://github.com/UberMetroid/agent-ooda/releases/latest>**

## Quick Install (Zero Go Required)

```bash
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash
```

Installs or updates `UberMetroid/agent-ooda` in `${XDG_DATA_HOME:-~/.local/share}/agent-ooda` (override with `AGENT_OODA_HOME`). If Go 1.22+ is on `PATH`, it builds [`plugins/agent-ooda/bin/agent-ooda`](plugins/agent-ooda/bin/agent-ooda) locally with `CGO_ENABLED=0`; otherwise it downloads the matching static GitHub Release binary (`linux`/`darwin`, `amd64`/`arm64`) and verifies its SHA-256 digest against `SHA256SUMS` before installing.

## Architecture & Multi-Call Binary

All runtime roles compile into one static binary (`plugins/agent-ooda/bin/agent-ooda`) dispatched via `argv[0]` basename:

| Binary / Symlink | Entrypoint (`cmd/agent-ooda`) | Responsibility |
| --- | --- | --- |
| `agent-ooda` | CLI dispatcher & fleet admin | `--version`, `--recon`, `--orient`, `--task`, manifests, `--review`, `--select`/`-s`, `--set-stage`, `--status`, `--discover`, `--usage`, `--probe` |
| `agent-ooda-hud` / `statusline.py` | Bubble-Canopy HUD | Renders live ANSI statusBar telemetry from NDJSON worker markers + `quota.json` |
| `agent-ooda-hook` | Antigravity lifecycle hook | Tracks injected skills per session and garbage-collects dead worker markers |
| `__setup` / `__wire` | Installer helpers | Safe JSON validation and idempotent `settings.json` `statusLine` wiring |

```text
agent-ooda/
├── cmd/agent-ooda/      # multi-call entrypoint (agent-ooda / agent-ooda-hud / agent-ooda-hook)
├── internal/
│   ├── fleet/           # quota store (v9), stage matrix, multi-window cycles, flock leases
│   ├── envelope/        # E-M regime selection, UTC tariffs, CLI discovery, mismatch pivot
│   ├── dispatch/        # io.Pipe NDJSON stream pump, tok/s rate meter, live quota kill, worktrees
│   ├── verify/          # HeadRef/unborn diff gathering, untracked hunks, off-family review
│   ├── worktree/        # isolated ooda/<id> git worktrees, stage resolver, scope & skill loader
│   ├── hud/             # ANSI Bubble-Canopy line renderer, 5-band tok/s gradient, hook recorder
│   ├── cli/             # arg parser, --select TUI, depth guard, stage & fleet reporting
│   └── install/         # cross-host setup/unwire helpers
├── plugins/agent-ooda/  # Antigravity (agy) plugin manifest, unified skill, rule, hooks & bin shims
└── qa/gates/            # AST & repo House-Law gate suite (`make all`)
```

## Credits: Col. John Boyd (USAF)

The scheduler borrows two principles from fighter tactician **Col. John Boyd**:

- **OODA loop (Observe → Orient → Decide → Act).** *Implicit Guidance & Control*
  provides the fast path: well-scoped work flows straight from Observe to Act while
  keeping the top-level orchestrator thin (`<10%` token burn). *Destruction & Creation*
  drives **cross-model mismatch pivoting**: when an off-family reviewer rejects a patch,
  the loop discards the stale frame and pivots to a different model family (hard cap of 3 Act loops).
- **Energy-Maneuverability theory, `Ps = V(T − D) / W`.** Every lane's renewable
  quota acts as specific excess power. The router drains energy closest to reset (*CORNER*),
  rotates evenly across healthy pools (*CRUISE*), shields the last capped lane (*CONSERVE*),
  holds paid credits in *RESERVE*, and rests on 429s until reset (*REGEN*).

## The 8-Lane Fleet

`agent-ooda` is currently configured for the **6 agent CLIs** (`8` routing lanes) we actively run and subscribe to:

| Lane | Engine / CLI | Tier / clock |
| --- | --- | --- |
| `agy-opus` | [Google Antigravity CLI](https://antigravity.google/) (`agy`) | capped, `5h + 7d` |
| `muse` | [Meta Muse Code](https://dev.meta.ai/products/muse-code) (`muse`, `muse-ember-1.0` / `muse-wonder-1.0` on `deep`) | capped, `5h + 7d` (auto-skips PAYG `api_key`) |
| `grok` | [xAI Grok Build](https://github.com/xai-org/grok-build) (`grok`) | capped, `7d` rolling weekly |
| `mistral` | [Mistral Vibe](https://github.com/mistralai/mistral-vibe) (`vibe`, optional [Z.ai](https://docs.z.ai/) GLM `0.5x` off-peak) | capped, `30d` monthly |
| `mcode` | [MiniMax Code](https://github.com/MiniMax-AI/minimax-code) (`mcode`, primary reviewer) | high-cap `5h + 7d`, **max 4 parallel** |
| `devin-fast` | [Cognition Devin CLI](https://docs.devin.ai/) Fast pool (`devin`) | unlimited `$20/mo` Pro, `24h + 7d`, **max 4 parallel** |
| `devin-pro` | [Cognition Devin CLI](https://docs.devin.ai/) Pro pool (`devin`) | unlimited `$20/mo` Pro, `24h + 7d` |
| `devin-fusion` | [Cognition Devin CLI](https://docs.devin.ai/) Fusion pool (`devin`) | paid credits (reserve) |

Model IDs and efforts are **discovered dynamically, not hardcoded**: `agent-ooda --status` and
`agent-ooda --discover` inspect non-secret local CLI configs, cached CLI model lists, effort flags
(`--effort`/`--reasoning-effort` vs `via-model`), live UTC off-peak tariffs (`Z.ai` / `DeepSeek`
`0.5x` off-peak, `mcode` weekday peak-jam avoidance), and installed `--skill` definitions. When
`--complexity` or `--skill` are omitted, `agent-ooda` infers the task regime and matches up to 2
non-core domain skills automatically, tuning model tier and `--effort`. `agent-ooda --probe` runs
minimal 1-token prompt canaries (never probing `credits`; updates local `quota.json`). Hard binary guardrails exit `2`
on any `gemini*`/`orchestrator*` worker model and block recursive execution (`agent-ooda --recon`,
`--task`, `--review`, `--probe`, `--cleanup`, `--enable`, `--disable`, `--set-cycle`) whenever
`AGENT_OODA_DEPTH>=1` or `ROUTER_DEPTH>=1`.

**Stage clocks & Per-Stage OODA Routing (`v2.1.0`):**

- **Per-Stage Matrix (`observe`, `orient`, `act`, `verify`):** Each worker stage defaults to `auto` (`Decide` remains with you and the top-level orchestrator) and resolves via **`CLI flag > --local > --profile > global > auto`**. CLI `--lane`/`--model`/`--effort` target the primary command stage (`--recon`, `--orient`, `--task`, or standalone `--review`); post-task `verify` follows `--local`/`--profile`/global pins so `--task` worker flags never leak onto the off-family reviewer.
- **Soft-Pin Failover & Off-Family Verify:** Pinned stages soft-failover on 429s/cooldown down their lane CSV or `E-M auto`, scrubbing primary-lane `--model`/`--effort` overrides when failing over to another lane; `verify` always enforces a non-authoring model family.
- **3 Synchronized Arrow-Key Selectors:**
  - `agent-ooda --select` (`-s`): Interactive terminal TUI (`↑`/`↓` + `Enter`, sticky or `--until-reset`, global `quota.json` `v9` or `--local` `~/.local/state/agent-ooda/workspaces/<id>.json`).
  - `/lane`: Native Antigravity in-chat selector skill (`ask_question`) for per-stage pins, presets, and resets.
  - `/agents`: `8` native `agy` workflow profiles (`ooda-auto`, `ooda-conserve`, `ooda-deep`, `ooda-devin`, `ooda-grok`, `ooda-mcode`, `ooda-muse`, `ooda-mistral`).
- **Observe & Orient:** `--recon` (`observe`) dispatches read-only lookup to free lanes (`devin-fast`, `mcode`); `--orient` (`orient`) runs deep read-only root-cause & architecture synthesis (`OODA:ORIENT`).
- **Act & Parallel:** Soonest-resetting capped lanes drain first; manifest batches isolate `git worktree` stripes with atomic `flock` slot leases.
- **Conserve & Verify:** When one capped lane remains, free `devin-*` and `mcode` absorb routine work; every non-empty diff runs through an independent model family.

### Other Agent CLIs (Not Currently Configured)

These coding-agent CLIs are **not** currently wired in `agent-ooda` because we don't carry paid subscriptions for them today. If you use them and want to add a lane, adapters live modularly under `internal/dispatch/` and `internal/fleet/`:

- **[Anthropic Claude Code](https://code.claude.com/docs/en/overview)** (`claude`) — no standalone Claude subscription configured (Claude Opus/Sonnet worker execution in our fleet currently routes through Antigravity's `agy-opus` pool).
- **[OpenAI Codex CLI](https://github.com/openai/codex)** (`codex`) — requires a ChatGPT plan or OpenAI API subscription.
- **[Cursor CLI](https://cursor.com/cli)** (`cursor-agent`) — requires a Cursor Pro/paid plan.
- **[GitHub Copilot CLI](https://github.com/features/copilot)** (`copilot`) — requires a GitHub Copilot paid plan.
- **[Sourcegraph Amp](https://ampcode.com/)** (`amp`) — requires an Amp account / credits.
- **[Factory Droid](https://factory.ai/)** (`droid`) — requires a Factory subscription.

## Cross-Model Verification

`mcode` leads diff review while strictly excluding the authoring family. Uncommitted,
staged (including unborn `HEAD`), untracked (synthetic hunks), and worker-committed changes
(detected via pre-task `HeadRef` comparison so `git commit` inside a task/worktree never evades
review or scope checks) are all audited fail-closed with a `240s` review budget:

- **Native repo review (`<= 8 KiB` working tree):** `mcode exec review --permission smart --max-steps 15`.
- **Inline stream audit (`> 8 KiB` or committed diffs):** `--permission smart --prompt-mode coding --max-steps 25`.
- **Fail-closed fallback:** on cooldown, nonzero exit, `SKIPPED`, `UNKNOWN`, or `[P0-3]` findings, review rotates off-family (`mcode → devin-fast → devin-pro → grok → mistral → muse → agy-opus`) or fails non-zero.

## Bubble-Canopy HUD (`bin/agent-ooda-hud`)

`bin/agent-ooda-hud` (`statusline.py`) reads live NDJSON stream telemetry written over `io.Pipe()` and
renders OODA phase (`○ agent-ooda`, `◐ orient/decide`, `⠋ observe`, `⠋ orient`, `⠋ act`, `⠋ verify`), active
stage pins on standby (`│ pin ...`), `tool Xs` waits, injected skills, `fleet X/8 (next …)` wake countdowns, and a
**5-band ANSI 256-color thermal `tok/s` heat ramp**:

- **Cold (`<25 tok/s`):** Ice Blue (`ANSI 75`)
- **Cruise (`25–59 tok/s`):** Cyan-Green (`ANSI 49`)
- **Warm (`60–109 tok/s`):** Solar Amber (`ANSI 220`)
- **Hot (`110–179 tok/s`):** Bold Orange (`ANSI 208`)
- **Afterburner (`>=180 tok/s`):** Bold Red-Hot Crimson (`ANSI 196`)

```text
○ agent-ooda │ pin act:grok, verify:mcode │ fleet 8/8
◐ orient/decide │ skill devin │ fleet 7/8 (next 2h59m)
⠋ orient · claude-opus-5-5-high · ~18 tok/s │ fleet 7/8 (next 2h59m)
⠋ observe · swe-2-flash-max · ~42 tok/s │ fleet 8/8
⠋ act · grok-4.7 · ~78 tok/s │ skill devin │ fleet 7/8
⠋ act ×2 · claude-opus-5-5-high, grok-4.7 · ~138 tok/s │ fleet 7/8
⠋ verify · MiniMax-M3.1-Flash · ~195 tok/s │ fleet 7/8
```

## CLI Quick Reference

```bash
agent-ooda --version                        # print binary version
agent-ooda --status                         # OODA stage matrix, fleet regimes, models & skills
agent-ooda --select                         # interactive arrow-key TUI (-s; add --local for repo)
agent-ooda --set-stage act=grok --effort xhigh
agent-ooda --set-stage act=auto --local     # mask one stage to auto in this repo
agent-ooda --set-stage preset=conserve      # presets: auto, conserve, deep, devin, grok, mcode, muse, mistral
agent-ooda --reset-stages                   # reset global pins to auto (--local clears repo overrides)
agent-ooda --discover                       # refresh live CLI capability probe cache
agent-ooda --probe                          # minimal 1-token prompt canary & muse PAYG check
agent-ooda --usage                          # per-lane 5h/24h/7d/30d windows, UTC tariffs & E-M regime telemetry
agent-ooda --recon "Map auth flow"          # OODA Observe: read-only recon (free lanes first)
agent-ooda --orient "Trace cache race"      # OODA Orient: read-only root-cause & architecture plan
agent-ooda --task "Fix parser bug" --files internal/cli/parse_args.go --skill devin
agent-ooda .worktrees/tasks.json -j 4
agent-ooda --review .                       # standalone cross-model working-tree/HEAD diff gate
agent-ooda --cleanup                        # remove .worktrees and temporary ooda/* branches
```

## Install, Update & Uninstall

### Web installer or local checkout

```bash
# Web installer (clones/updates ~/.local/share/agent-ooda, uses Go or SHA-256 prebuilt binary)
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash

# Local source checkout
git clone https://github.com/UberMetroid/agent-ooda.git && cd agent-ooda
./install.sh            # or: make install   (./install.sh --help lists options)
agy plugin list         # restart agy first, then confirm "agent-ooda" is enabled
```

`install.sh` is idempotent:

- **Links** `~/.gemini/config/plugins/agent-ooda` → `plugins/agent-ooda`,
  `~/.local/bin/agent-ooda` → `plugins/agent-ooda/bin/agent-ooda`, and
  `~/.gemini/antigravity-cli/statusline.py` → `plugins/agent-ooda/bin/agent-ooda-hud`.
- **Migrates & archives** legacy `v1.x` owned `ooda`/`router` symlinks (leaving unrelated `ooda` binaries such as `openOODA-core` untouched) and `${XDG_STATE_HOME:-~/.local/state}/ooda/` state into `${XDG_STATE_HOME:-~/.local/state}/agent-ooda/backups/`.
- **Seeds** `${XDG_STATE_HOME:-~/.local/state}/agent-ooda/quota.json` and registers `~/.agents/skills` in `~/.gemini/config/skills.json`.
- **Wires `statusLine`** in `~/.gemini/antigravity-cli/settings.json` (skip with `AGENT_OODA_NO_STATUSLINE=1`):

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
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash

# Update from local checkout (links resolve directly into the checkout)
git pull && ./install.sh

# Uninstall (removes agent-ooda symlinks & restores previous statusLine; add --purge to remove state/checkout)
./install.sh --uninstall --purge
# or via web installer:
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash -s -- --uninstall --purge
```

Runtime state persists under `${XDG_STATE_HOME:-~/.local/state}/agent-ooda/quota.json` (`v9`, `AGENT_OODA_QUOTA_FILE`) and per-repo `--local` pins under `~/.local/state/agent-ooda/workspaces/<id>.json`; live HUD markers and `flock` leases live in `/tmp/agent-ooda-hud` (`AGENT_OODA_HUD_DIR`).

## House-Law Verification

```bash
make all   # portable, line-cap, density, vocab, shape, plugin-check, go vet, go test -race, dogfood
```

See [`AGENTS.md`](AGENTS.md) for the enforced rules: stdlib-only Go (`0` external `require`s), 16–256 lines & `<= 128` cols per file, `<= 8` entries per directory, `<= 8` funcs per file, `<= 64` code lines per function, nesting `<= 4`, `<= 1` statement per line, identifiers `<= 32` chars, and zero hard-coded home paths, secrets, or `sh -c`.

# agent-ooda

**Static Go OODA multi-agent router, Energy-Maneuverability quota scheduler, cross-model diff verifier, and live `tok/s` Bubble-Canopy HUD.**

`UberMetroid/agent-ooda` packages one zero-dependency Go 1.22+ (`CGO_ENABLED=0`, zero external `require`s) engine paired with host plugin adapters under `plugins/`—starting with [`plugins/ooda`](plugins/ooda) for Antigravity (`agy`) and structured so `mcode` and future coding hosts share the same fleet state, `flock` slot leases, git worktrees, and diff verifier.

- **Site & web installer:** **<https://ubermetroid.github.io/agent-ooda/>**
- **Releases & static binaries (`SHA256SUMS`):** **<https://github.com/UberMetroid/agent-ooda/releases/latest>**

## Quick Install (Zero Go Required)

```bash
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash
```

Installs or updates `UberMetroid/agent-ooda` in `${XDG_DATA_HOME:-~/.local/share}/agent-ooda` (override with `AGENT_OODA_HOME`). If Go 1.22+ is on `PATH`, it builds [`plugins/ooda/bin/ooda`](plugins/ooda/bin/ooda) locally with `CGO_ENABLED=0`; otherwise it downloads the matching static GitHub Release binary (`linux`/`darwin`, `amd64`/`arm64`) and verifies its SHA-256 digest against `SHA256SUMS` before installing.

## Architecture & Multi-Call Binary

All runtime roles compile into one static binary (`plugins/ooda/bin/ooda`) dispatched via `argv[0]` basename:

| Binary / Symlink | Entrypoint (`cmd/ooda`) | Responsibility |
| --- | --- | --- |
| `ooda` / `router` | CLI dispatcher & fleet admin | `--recon`, `--task`, parallel worktree manifests, `--review`, `--status`, `--discover`, `--usage`, `--probe` |
| `ooda-hud` / `statusline.py` | Bubble-Canopy HUD | Renders live ANSI statusBar telemetry from NDJSON worker markers + `quota.json` |
| `ooda-hook` | Antigravity lifecycle hook | Tracks injected skills per session and garbage-collects dead worker markers |
| `__setup` / `__wire` | Installer helpers | Safe JSON validation and idempotent `settings.json` `statusLine` wiring |

```text
agent-ooda/
├── cmd/ooda/            # multi-call entrypoint (router / ooda-hud / ooda-hook)
├── internal/
│   ├── fleet/           # quota store (v8), multi-window cycles, model aliases, flock slot leases
│   ├── envelope/        # E-M regime selection, UTC tariffs, CLI discovery, mismatch pivot
│   ├── dispatch/        # io.Pipe NDJSON stream pump, tok/s rate meter, live quota kill, worktrees
│   ├── verify/          # HeadRef/unborn diff gathering, untracked hunks, off-family review
│   ├── worktree/        # isolated ooda/<id> git worktrees, scope rules, skill loader
│   ├── hud/             # ANSI Bubble-Canopy line renderer & hook event recorder
│   ├── cli/             # arg parser, conflict validation, ROUTER_DEPTH>=1 guard
│   └── install/         # cross-host setup/unwire helpers
├── plugins/ooda/        # Antigravity (agy) plugin manifest, skills, rules, hooks & bin shims
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

| Lane | Engine / CLI | Tier / clock |
| --- | --- | --- |
| `agy-opus` | Antigravity CLI (`agy`) | capped, `5h + 7d` |
| `muse` | Muse CLI (`muse`, `muse-ember-1.0` / `muse-wonder-1.0` on `deep`) | capped, `5h + 7d` (auto-skips PAYG `api_key`) |
| `grok` | xAI Grok CLI (`grok`) | capped, `7d` rolling weekly |
| `mistral` | Mistral Vibe CLI (`vibe`) | capped, `30d` monthly (`0.5x` off-peak Z.ai) |
| `mcode` | MiniMax mcode CLI (primary reviewer) | high-cap `5h + 7d`, **max 4 parallel** |
| `devin-fast` | Devin Fast pool (`devin`) | unlimited `$20/mo` Pro, `24h + 7d`, **max 4 parallel** |
| `devin-pro` | Devin Pro pool (`devin`) | unlimited `$20/mo` Pro, `24h + 7d` |
| `devin-fusion` | Devin Fusion pool (`devin`) | paid credits (reserve) |

Model IDs and efforts are **discovered dynamically, not hardcoded**: `router --status` and
`router --discover` inspect non-secret local CLI configs, cached CLI model lists, effort flags
(`--effort`/`--reasoning-effort` vs `via-model`), live UTC off-peak tariffs (`Z.ai` / `DeepSeek`
`0.5x` off-peak, `mcode` weekday peak-jam avoidance), and installed `--skill` definitions. When
`--complexity` or `--skill` are omitted, `router` infers the task regime and matches up to 2
non-core domain skills automatically, tuning model tier and `--effort`. `router --probe` runs
minimal 1-token prompt canaries (never probing `credits`; updates local `quota.json`). Hard binary guardrails exit `2`
on any `gemini*`/`orchestrator*` worker model and block recursive execution (`router --recon`,
`--task`, `--review`, `--probe`, `--cleanup`, `--enable`, `--disable`, `--set-cycle`) whenever
`ROUTER_DEPTH>=1`.

**Stage clocks:**

- **Observe:** read-only recon dispatches to free lanes first (`devin-fast`, `mcode`).
- **Act:** soonest-resetting capped lanes drain first, followed by remaining capped and free lanes.
- **Parallel:** manifest batches create one isolated `git worktree` per task with atomic `flock` slot leases on 4-wide pools.
- **Deep:** capped priority order (`agy-opus → muse → grok → mistral`).
- **Conserve:** when one capped lane remains, free `devin-*` and `mcode` absorb traffic first.
- **Verify:** every non-empty diff runs through an independent model family.

## Cross-Model Verification

`mcode` leads diff review while strictly excluding the authoring family. Uncommitted,
staged (including unborn `HEAD`), untracked (synthetic hunks), and worker-committed changes
(detected via pre-task `HeadRef` comparison so `git commit` inside a task/worktree never evades
review or scope checks) are all audited fail-closed with a `240s` review budget:

- **Native repo review (`<= 8 KiB` working tree):** `mcode exec review --permission smart --max-steps 15`.
- **Inline stream audit (`> 8 KiB` or committed diffs):** `--permission smart --prompt-mode coding --max-steps 25`.
- **Fail-closed fallback:** on cooldown, nonzero exit, `SKIPPED`, `UNKNOWN`, or `[P0-3]` findings, review rotates off-family (`mcode → devin-fast → devin-pro → grok → mistral → muse → agy-opus`) or fails non-zero.

## Bubble-Canopy HUD (`bin/ooda-hud`)

`bin/ooda-hud` (`statusline.py`) reads live NDJSON stream telemetry written over `io.Pipe()` and
renders OODA phase (`○ ooda`, `◐ orient/decide`, `⠋ observe`, `⠋ act`, `⠋ verify`), live `~tok/s`
(or exact `tok/s`), active `tool Xs` waits, injected skills, and `fleet X/8 (next …)` wake countdowns:

```text
○ ooda │ fleet 8/8
◐ orient/decide │ skill devin │ fleet 7/8 (next 2h59m)
⠋ observe · swe-2-flash-max · ~64 tok/s │ fleet 8/8
⠋ act · claude-opus-5-5-high · tool 4s │ skill devin │ fleet 7/8
⠋ act ×2 · claude-opus-5-5-high, grok-4.7 · ~112 tok/s │ fleet 7/8
⠋ verify · MiniMax-M3.1-Flash · ~48 tok/s │ fleet 7/8
```

## CLI Quick Reference

```bash
router --status                 # live fleet regimes, models, effort modes, skills & next wake
router --discover               # refresh live CLI capability probe cache
router --probe                  # minimal 1-token prompt canary & muse PAYG check
router --usage                  # per-lane 5h/24h/7d/30d windows, UTC tariffs & E-M regime telemetry
router --recon "Map auth flow"  # read-only Observe recon (free lanes first)
router --task "Fix parser bug" --files internal/cli/parse_args.go --skill devin
router .worktrees/tasks.json -j 4
router --review .               # standalone cross-model working-tree/HEAD diff gate
router --cleanup                # remove .worktrees and temporary ooda/* branches
```

## Install, Update & Uninstall

### Web installer or local checkout

```bash
# Web installer (clones/updates ~/.local/share/agent-ooda, uses Go or SHA-256 prebuilt binary)
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash

# Local source checkout
git clone https://github.com/UberMetroid/agent-ooda.git && cd agent-ooda
./install.sh            # or: make install   (./install.sh --help lists options)
agy plugin list         # restart agy first, then confirm "ooda" is enabled
```

`install.sh` is idempotent:

- **Links** `~/.gemini/config/plugins/ooda` → `plugins/ooda`,
  `~/.local/bin/ooda` and `~/.local/bin/router` → `plugins/ooda/bin/ooda`, and
  `~/.gemini/antigravity-cli/statusline.py` → `plugins/ooda/bin/ooda-hud`.
- **Seeds** `${XDG_STATE_HOME:-~/.local/state}/ooda/quota.json` (migrating legacy state if present)
  and registers `~/.agents/skills` in `~/.gemini/config/skills.json`.
- **Archives** displaced symlinks/files under `${XDG_STATE_HOME:-~/.local/state}/ooda/backups/` without deleting them.
- **Wires `statusLine`** in `~/.gemini/antigravity-cli/settings.json` (skip with `OODA_NO_STATUSLINE=1`):

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

# Uninstall (removes ooda symlinks & restores previous statusLine; add --purge to remove state/checkout)
./install.sh --uninstall --purge
# or via web installer:
curl -fsSL https://ubermetroid.github.io/agent-ooda/install.sh | bash -s -- --uninstall --purge
```

Runtime state persists under `${XDG_STATE_HOME:-~/.local/state}/ooda/quota.json` (`AGENT_QUOTA_FILE`); live HUD markers and `flock` leases live in `/tmp/router-hud` (`ROUTER_HUD_DIR`).

## House-Law Verification

```bash
make all   # portable, line-cap, density, vocab, shape, plugin-check, go vet, go test -race, dogfood
```

See [`AGENTS.md`](AGENTS.md) for the enforced rules: stdlib-only Go (`0` external `require`s), 16–256 lines & `<= 128` cols per file, `<= 8` entries per directory, `<= 8` funcs per file, `<= 64` code lines per function, nesting `<= 4`, `<= 1` statement per line, identifiers `<= 32` chars, and zero hard-coded home paths, secrets, or `sh -c`.

# agy-skills

`UberMetroid/agy-skills` ships **`ooda`**, an Antigravity (`agy`) plugin at
[`plugins/ooda`](plugins/ooda) that turns one orchestrator session into a
token-routed, cross-model **agent fleet**. Stdlib-only Python 3.8+, zero
third-party dependencies.

Live site: **<https://ubermetroid.github.io/agy-skills/>**

## Credits: Col. John Boyd (USAF)

The design borrows two ideas from fighter pilot and strategist **Col. John Boyd**:

- **OODA loop (Observe → Orient → Decide → Act).** *Implicit Guidance & Control*
  is the fast path: routine work skips explicit deliberation and goes straight
  from Observe to Act. *Destruction & Creation* drives **cross-model mismatch
  pivoting**: when a reviewer from one model family rejects another family's
  diff, the loop discards the old frame and re-plans on a different family.
- **Energy-Maneuverability theory, `Ps = V(T − D) / W`.** Each lane's renewable
  quota is treated as specific excess power. The router works like fly-by-wire:
  it spends energy that is about to reset (*CORNER*), cruises on healthy lanes,
  conserves when one capped lane is left, keeps paid credits in *RESERVE*, and
  waits out *REGEN* cooldowns.

## The 8-lane fleet

| Lane | Engine | Tier / clock |
| --- | --- | --- |
| `agy-opus` | Antigravity Claude Opus 5.5 High | capped, 5h renewable |
| `devin-fusion` | Devin Fusion (Opus 5.5 + SWE-2 sidekick) | paid credits (reserve) |
| `devin-pro` | SWE-2 Max | free |
| `devin-fast` | SWE-2 Flash Max | free, **max 4 parallel** |
| `mcode` | MiniMax M3.1-Flash (primary reviewer) | high-cap 5h, **max 4 parallel** |
| `muse` | Muse Ember 1.0 | capped, 5h renewable |
| `grok` | xAI Grok 4.7 | capped, 5h renewable |
| `mistral` | Mistral Vibe GLM-5.3 | capped, 5h renewable |

**How the clocks are spent.** Each capped lane runs on a 5h renewable cycle.
Quota errors (HTTP 429, server reset timestamps) put a lane into cooldown, and
it wakes up on its own when the cycle resets. The router spends the lanes in a
different order for each stage:

- **Observe:** read-only recon goes to the free lanes (`devin-fast`, `mcode`).
- **Act:** lanes whose reset is near come first, then the other capped lanes, then the free lanes.
- **Parallel:** manifests rotate tasks across capped lanes and use one isolated
  git worktree per task, with `flock` slot leases on the 4-wide lanes.
- **Deep:** order is `agy-opus → muse → grok → mistral`.
- **Conserve:** when one capped lane is left, the free SWE-2 and `mcode` lanes go first.
- **Verify:** an independent diff review runs on a *different* model family.

**Cross-model verification.** `mcode` reviews every diff first, including
untracked files rendered as synthetic hunks. Targeted working-tree diffs
(`<= 8 KiB`) invoke native `mcode exec review` (`--permission smart
--max-steps 15`), while larger (`> 8 KiB`) or committed diffs run a single-pass
inline patch review (`--mode lightweight --permission off --max-steps 4`).
The author's own family is never used for review. On cooldown, non-zero exit,
or an `UNKNOWN` verdict, review falls back automatically to the next
off-family backup lane (for example `grok`); `[P0-3]` findings always fail closed.

**Bubble-canopy HUD.** `bin/ooda-hud` is the Antigravity `statusLine`. It shows
the current OODA step (`○ ooda`, `◐ orient/decide`, `⠋ act ×2`), the worker
models, active custom skills, and `fleet X/8 (next …)` wake countdowns.
`./install.sh` links it to `~/.gemini/antigravity-cli/statusline.py` and adds
the `statusLine` block (shown under [Install](#install)) to
`~/.gemini/antigravity-cli/settings.json`, so agy runs it after a restart.

## Install

```bash
git clone https://github.com/UberMetroid/agy-skills.git && cd agy-skills
./install.sh            # or: make install   (./install.sh --help lists options)
# restart agy, then:
agy plugin list         # confirm "ooda" is registered
router --status         # fleet regimes, cooldowns, next wake (ooda shim also in ~/.local/bin)
router --usage          # per-lane cycle usage
```

`install.sh` is idempotent. It does the following:

- **Links** `~/.gemini/config/plugins/ooda` → `plugins/ooda`,
  `~/.local/bin/ooda` and `~/.local/bin/router` → `plugins/ooda/bin/ooda`, and
  `~/.gemini/antigravity-cli/statusline.py` → `plugins/ooda/bin/ooda-hud`.
- **Seeds** `${XDG_STATE_HOME:-~/.local/state}/ooda/quota.json` (it migrates
  legacy router/boyd state when it finds any) and adds `~/.agents/skills` to
  `~/.gemini/config/skills.json`.
- **Archives** displaced files and legacy installs under
  `${XDG_STATE_HOME:-~/.local/state}/ooda/backups/`. Nothing is deleted.
- **Wires the statusLine** in `~/.gemini/antigravity-cli/settings.json`. All
  other keys are kept. If you already had a different `statusLine`, it is
  saved to `…/ooda/statusline.prev.json`. On re-runs, your
  `enabled`/`padding`/`stack_with_default` edits are left alone:

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

  Set `OODA_NO_STATUSLINE=1 ./install.sh` to skip this step. The HUD symlink
  is still created.
- **Checks both JSON files first.** If `settings.json` or `skills.json` is
  malformed, install exits non-zero and changes nothing.
- **Prints a health report.** It warns if `~/.local/bin` is not on `PATH` (and
  prints the `export` line to add), says whether `agy` is on `PATH`, reports
  which lane CLIs are found or missing (`agy devin muse grok vibe mcode`; this
  is informational and never fails the install), and reminds you to restart agy.

## Uninstall

```bash
./install.sh --uninstall          # or ./uninstall.sh, or make uninstall
./install.sh --uninstall --purge  # also delete ${XDG_STATE_HOME:-~/.local/state}/ooda
```

Uninstall removes the four links above, but only when they are symlinks that
resolve into this repo's `plugins/ooda`. Anything else (for example your own
`~/.local/bin/router`) is left in place and listed as such. It removes our
`statusLine` block and restores the saved previous block if there is one. It
keeps the `~/.agents/skills` entry in `skills.json` because that directory is
shared. Quota state and `backups/` are kept unless you pass `--purge`; restore
legacy items from `backups/` by hand. Running it twice is safe: the second run
reports nothing to remove.

## Update

```bash
git pull && ./install.sh   # links point into the checkout, so a pull updates the code; re-run to pick up new wiring
```

Runtime state lives in `${XDG_STATE_HOME:-~/.local/state}/ooda/quota.json`
(override with `AGENT_QUOTA_FILE`). Live HUD and lease markers go in
`/tmp/router-hud` (override with `ROUTER_HUD_DIR`).

## House-Law verification

```bash
make all   # portable, line-cap, density, vocab, shape, plugin-check, test, dogfood
```

See [`AGENTS.md`](AGENTS.md) for the rules: stdlib only, 16–256 lines per file,
at most 8 `.py` files per directory, at most 8 defs per file, at most 64 lines
per function, and no hard-coded home paths, secrets, or `shell=True`.

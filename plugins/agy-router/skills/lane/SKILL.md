---
name: lane
description: >-
  Interactive arrow-key selector inside Antigravity chat to inspect and configure
  which agy-router lane, model, and effort run at each router stage (observe,
  orient, act, verify) globally or for this repository, or apply a preset.
---

# `/lane` — Interactive Router Stage & Model Selector

Use this skill when the user invokes `/lane` or asks to switch, pin, or reset the
models/lanes used across router stages (`observe`, `orient`, `act`, `verify`).

## Procedure (Arrow-Key TUI via `ask_question`)

1. Inspect live fleet capabilities and current stage bindings:
   ```bash
   agy-router --status
   ```
2. Call `ask_question` so the user gets native `↑` / `↓` + `Enter` selection:
   - **Prompt 1 (What to configure & Scope):**
     - `Action`: choose from `Change a single router stage (observe / orient / act / verify)`, `Apply a workflow preset (auto / conserve / deep / devin / grok / mcode / muse / mistral)`, `Reset all stages to auto`
     - `Scope`: `Global (~/.local/state/agy-router/quota.json)` vs `Repo-local (this workspace only, --local)`
3. If applying a preset, present the 8 presets via `ask_question` and execute:
   ```bash
   agy-router --set-stage preset=<name> [--local]
   ```
4. If configuring a single stage:
   - Step A: Pick stage (`observe`, `orient`, `act`, `verify`) and target lane (`auto`, or any non-paid lane from `--status` — persistent pins exclude paid-credits `devin-fusion` and PAYG `muse`; pass `--lane` per run instead).
   - Step B (when lane != `auto`): Pick one of that lane's discovered models from `agy-router --status`, effort (for `agy-opus`, `muse`, `mcode`, `grok`; skip `--effort` for `via-model` `devin-*` and `mistral`), and persistence (`Sticky` vs `--until-reset`).
   - Run:
     ```bash
     agy-router --set-stage <stage>=<lane> [--model <model>] [--effort <effort>] [--until-reset] [--local]
     ```
5. If resetting, run `agy-router --reset-stages [--local]` and print the updated stage matrix concisely.

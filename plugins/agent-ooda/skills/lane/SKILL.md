---
name: lane
description: >-
  Interactive arrow-key selector inside Antigravity chat to inspect and configure
  which agent-ooda lane, model, and effort run at each OODA stage (observe,
  orient, act, verify) globally or for this repository, or apply a preset.
---

# `/lane` — Interactive OODA Stage & Model Selector

Use this skill when the user invokes `/lane` or asks to switch, pin, or reset the
models/lanes used across OODA stages (`observe`, `orient`, `act`, `verify`).

## Procedure (Arrow-Key TUI via `ask_question`)

1. Inspect live fleet capabilities and current stage bindings:
   ```bash
   agent-ooda --status
   ```
2. Call `ask_question` so the user gets native `↑` / `↓` + `Enter` selection:
   - **Prompt 1 (What to configure & Scope):**
     - `Action`: choose from `Change a single OODA stage (observe / orient / act / verify)`, `Apply a workflow preset (auto / conserve / deep / devin / grok / mcode / muse / mistral)`, `Reset all stages to auto`
     - `Scope`: `Global (~/.local/state/agent-ooda/quota.json)` vs `Repo-local (this workspace only, --local)`
3. If applying a preset, present the 8 presets via `ask_question` and execute:
   ```bash
   agent-ooda --set-stage preset=<name> [--local]
   ```
4. If configuring a single stage:
   - Step A: Pick stage (`observe`, `orient`, `act`, `verify`) and target lane (`auto`, or any non-paid lane from `--status` — persistent pins exclude paid-credits `devin-fusion` and PAYG `muse`; pass `--lane` per run instead).
   - Step B (when lane != `auto`): Pick one of that lane's discovered models from `agent-ooda --status`, effort (for `agy-opus`, `muse`, `mcode`, `grok`; skip `--effort` for `via-model` `devin-*` and `mistral`), and persistence (`Sticky` vs `--until-reset`).
   - Run:
     ```bash
     agent-ooda --set-stage <stage>=<lane> [--model <model>] [--effort <effort>] [--until-reset] [--local]
     ```
5. If resetting, run `agent-ooda --reset-stages [--local]` and print the updated stage matrix concisely.

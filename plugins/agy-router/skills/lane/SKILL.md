---
name: lane
description: >-
  Interactive arrow-key selector inside Antigravity chat to inspect and configure
  which agy-router lane, model, and effort run at each router stage (observe,
  orient, act, verify) globally or for this repository, or apply a preset.
---

# `/lane` — Interactive Router Stage & Model Selector

Use this skill when the user invokes `/lane` or asks to switch, pin, or reset the
`<cli>:<model>` bindings used across OODA + Verify stages (`observe`, `orient`, `act`, `verify`;
`decide` always runs natively in Plan Mode + Human).

## Procedure (Arrow-Key TUI via `ask_question`)

1. Inspect live model catalog, quota pools, and current stage bindings:
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
   - Step A: Pick stage (`observe`, `orient`, `act`, `verify`) and target `<cli>:<model>` directly from `agy-router --status` (e.g. `agy:claude-opus-5-5-high`, `agy:claude-sonnet-5-5-high`, `mcode:MiniMax-M3`, `mcode:MiniMax-M3.1-Flash`, `devin:swe-2-high`, `devin:swe-2-flash-high`, `muse:muse-wonder-full-1.0`, `grok:grok-4.7`, `vibe:glm-5-3`, or `auto`). Persistent pins exclude PAYG `muse` and `tier: "credits"` pools (`--model <cli>:<model>` per run still works).
   - Step B: Pick effort (for `agy:*`, `muse:*`, `mcode:MiniMax-M3.1-Flash`, `grok:*`; skip `--effort` for `devin:*`, `vibe:*`, and `mcode:MiniMax-M3`), and persistence (`Sticky` vs `--until-reset`).
   - Run:
     ```bash
     agy-router --set-stage <stage>=<cli>:<model> [--effort <effort>] [--until-reset] [--local]
     ```
5. If resetting, run `agy-router --reset-stages [--local]` and print the updated stage matrix concisely.

# OODA & Router Orchestration Policy

## 1. `ROUTER_DEPTH` Recursion Guard (Check First)
- Check whether `ROUTER_DEPTH` in your process environment is `1` or greater.
- When `ROUTER_DEPTH >= 1`, you are running as a headless worker dispatched by `router` / `ooda`. Execute the assigned task directly with native file and command tools (`read_file`, `edit_file`, `write_file`, `run_command`).
- **NEVER** invoke `router`, `ooda`, or `dispatch.py` when `ROUTER_DEPTH >= 1`.

## 2. Thin Orchestrator & Default `router` Execution (`ROUTER_DEPTH` unset or `0`)
- Keep the top-level Antigravity session as a thin orchestrator (`<10%` of total token burn): own planning, architecture decisions, build/test gate verification, and user reporting while offloading bulk work to `router` (`~/.local/bin/router`, provided by the `ooda` plugin).
- **Codebase exploration:** Run `router --recon "<specific question>"` (routes to free `devin-fast` / `mcode`) before reading large sets of files directly.
- **Coding, debugging & test sweeps:** Dispatch via `router --task "<prompt>" --files ...` or parallel worktree manifests (`router .worktrees/tasks.json` for 2+ disjoint modules). By default `router` drains renewable `capped` 5h cycles (`agy-opus` Claude Opus 5.5 High, `muse`, `grok`, `mistral`) ahead of paid `devin-fusion` credits and free SWE-2 / `mcode` lanes.
- **Multi-module work (2+ modules or non-trivial bugs):** Activate and follow the `ooda` skill (`Observe -> Orient -> Decide -> Act`, zero edits before Phase 4, hard cap of 3 Act loops).
- **Independent code review:** Run `router --review .` (lightweight `mcode` MiniMax-M3.1-Flash first, cross-model fallback off the author family, never `devin-fusion` credits) on diffs rather than spending orchestrator tokens reviewing diffs inline.
- **Quota protection:** Never configure Antigravity Gemini models as `router` worker lanes; keep the Gemini quota pool reserved for the top-level orchestrator.
- **TUI step badges (`toolSummary`):** When invoking `router` via `run_command`, include the OODA stage and lane/effort in `toolSummary` (e.g., `'OODA Observe [devin-fast]'`, `'OODA Act [agy-opus:high]'`, `'OODA Act parallel ×2'`, `'OODA Verify [mcode]'`).

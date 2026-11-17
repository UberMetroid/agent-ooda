---
name: harden
description: >-
  4-phase OODA trust-boundary verification workflow (/harden [path] or /harden
  fix [path]) combining 0-token deterministic hygiene, source-to-sink mapping,
  off-family skeptic falsification, and compile-clean Red-to-Green refusal unit
  tests with zero exploit trigger words.
---

# `/harden` — Zero-Hallucination Trust-Boundary Verification

Use `/harden` to audit a repository, module directory, or branch for real
trust-boundary defects without fabricating noise.

**Modes:**
- `/harden [path]` *(default)*: Runs Phases 1–2, then pauses at Phase 3 with
  `ask_question` (*Fix all confirmed issues*, *Select subset*, *Report only*).
- `/harden fix [path]`: Runs Phases 1–2 falsification first, then proceeds
  straight into Phase 4 fixes + refusal unit tests for confirmed items.

**Vocabulary Rule:** Frame every `--recon`, `--orient`, `--task`, and `--review`
prompt strictly in systems-engineering terms (*trust boundaries*, *input
invariants*, *path/command containment*, *capability-token integrity*,
*ambient-authority bypass*, *arithmetic/overflow invariants*, *spatial/temporal
memory-safety & lifetime invariants*, *fail-closed error handling*, *refusal unit
tests*). Do **not** use offensive or dual-use trigger words across any fleet CLI.

---

## Gate 1 — OBSERVE (`0` Edits): Deterministic Hygiene + Boundary Recon

1. Run the `0`-token deterministic hygiene scanner first:
   ```bash
   agy-router --hygiene [target]
   ```
   Collect all `P0` credential/key/file findings and `P1` hardening footguns
   (`0777`/`0666` permissions, `InsecureSkipVerify: true` outside tests,
   `curl|sh` outside docs/`usage:`/`# hygiene:allow`, non-loopback `http://` fetches).
2. Run a snapshot-guarded read-only boundary reconnaissance pass (scope to
   target production files and pass `--timeout 300` so workers don't crawl
   unrelated tests):
   ```bash
   agy-router --recon "Map external untrusted input sources (CLI args, env vars, HTTP/RPC/IPC requests, git/file reads) and sensitive sinks in [target] with exact file:line locations. Read-only, cite only verified paths; skip test files." --timeout 300 --skill harden
   ```

---

## Gate 2 — ORIENT Pass A: Structured Finder (`0` Edits)

Propose at most **`8`** concrete candidate chains (`H1..Hn`) via `agy-router --orient`
with `--timeout 480`:
```bash
agy-router --orient "Inspect verified entry points and sinks from recon in [target]. Propose <= 8 candidate trust-boundary defects H1..Hn (skip test files). Every candidate MUST include:
  SOURCE: <file:line> (external/untrusted input only; compile-time constants and test mocks forbidden)
  MISSING_GUARD: <exact unchecked invariant along the call chain>
  SINK: <file:line> (concrete failure impact)
Allowed SINK categories:
  1. Path escape or symlink traversal outside workspace/root
  2. Subprocess command/argv injection or unsafe child environment inheritance
  3. Capability-token forgery/widening or ambient-authority bypass (opening raw host FS/net/process/IPC handles outside granted capability tokens)
  4. Fail-open auth, signature, snapshot, or review verdict handling
  5. Unbounded memory, file, goroutine, or stream growth from untrusted input
  6. Credential/secret leakage into diffs, logs, prompts, or child processes
  7. State-corrupting concurrency or lock races
  8. Arithmetic, overflow & numerical invariants (signed/unsigned wraparound or truncation before allocation/indexing, division/modulo by zero, NaN/Inf fail-open comparisons, off-by-one bounds, quota/fee rounding drift)
  9. Spatial/temporal memory-safety & lifetime invariants (out-of-bounds slice/array/pointer indexing, length-vs-capacity mismatches, borrowed-buffer/reference lifetime escapes across async/FFI boundaries, uninitialized reads, unsafe/ptrCast alignment or aliasing violations)" --timeout 480 --skill harden
```

Disqualify any claim missing an exact `SOURCE -> MISSING_GUARD -> SINK` chain
or referencing constants/test doubles. Remember `--recon`/`--orient` read-only
runs return cached results at `0` tokens when files have not changed.

---

## Gate 3 — ORIENT Pass B: Off-Family Skeptic Falsification (`0` Edits)

Whenever Finder produces `>= 1` LLM candidates `H1..Hn`, launch a **different
model family** (`--exclude-lane <finder_lane> --skill none --timeout 480`; use
`--effort high` when `mcode` is selected so multi-file checks finish well within
budget) and **inline Finder's concise `H1..Hn` chains directly in the prompt**
(read-only worker runs are stateless across invocations):
```bash
agy-router --orient "Skeptic falsification pass (do not crawl unrelated test suites). Read callers, type invariants, bounds checks, capability enforcement, and sanitizer helpers around these inlined candidates H1..Hn in [target] and try to DISPROVE each one:
<paste Finder's H1..Hn SOURCE -> MISSING_GUARD -> SINK summary + exact line ranges>
Classify strictly as:
  - DISPROVED: cite the exact file:line upstream guard preventing reachability
  - THEORETICAL: unreachable or requires caller to violate non-external contracts
  - CONFIRMED: verified reachable source-to-sink path with no missing-guard doubt
Keep only CONFIRMED candidates with verified file:line evidence." --exclude-lane <finder_lane> --skill none --timeout 480
```
Discard all `DISPROVED` and `THEORETICAL` items immediately.

---

## Phase 3 — DECIDE (`0` Edits)

1. If `--hygiene` is clean **and** `0` `CONFIRMED` chains remain, report a clean
   status summary and stop.
2. Otherwise present a concise verification table (`ID`, `Severity`,
   `Source -> Sink`, `Proposed Guard + Refusal Test`) and:
   - In `/harden` mode: invoke `ask_question` (*Fix all confirmed issues*,
     *Select specific IDs*, *Report only*).
   - In `/harden fix` mode: lock scope (`files`, off-limits files, repo test gate)
     and proceed directly to Gate 4.

---

## Gate 4 — ACT + VERIFY: Compile-Clean Red-to-Green Proof (Max `3` Loops)

For each approved finding (use sequential `--task --files ...` or parallel
`.worktrees/tasks.json` when findings live in disjoint modules):

1. **Compile-Clean Red Proof (Mandatory Before Editing Production Code):**
   - Write a standard boundary/refusal unit test (`Test...`) using safe malformed
     or out-of-scope inputs (`../outside`, forged/missing capability scope,
     oversized payload, malformed header, integer-boundary wraparound,
     out-of-bounds/lifetime-escape slice or buffer) against **untouched
     production code**.
   - Run the unit test command directly (with `-race`, ASan/UBSan, or Zig
     `ReleaseSafe` / `std.testing.allocator` leak & bounds checks where supported
     by the target language):
     - It **MUST compile cleanly** (`0` missing-symbol, signature, or syntax
       errors) against the existing production code and **fail at runtime**
       via test assertion (*Red*).
     - If it fails to compile, fix the test to match real symbols before continuing.
     - If it compiles cleanly and **already passes** on untouched production code,
       abort the production edit, revert the test, and reclassify the finding as
       `FALSE_POSITIVE`.
2. **Minimal Green Guard:**
   - Add the minimal fail-closed check at the invariant boundary and confirm the
     refusal unit test passes (*Green*).
3. **Final Verification Gates:**
   - Run the repository's full test/build suite (e.g., `make all`, `go test -race ./...`,
     `pytest -q`).
   - Re-run `agy-router --hygiene [target]` -> must exit `0` for `P0` and fixed `P1` items.
   - Run off-family `agy-router --review .` -> require `VERDICT: ACCEPT`.

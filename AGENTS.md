# agent-ooda — House Laws & Agent Engineering Standards

Canonical engineering rules for humans and agents working in `agent-ooda`.
Every `[GATE]` rule below is machine-enforced by `Makefile` targets and
`qa/gates/`; every `[POLICY]` rule is enforced during code review.

---

## 1. Zero Third-Party Dependencies

- `[GATE]` **Stdlib only (`make portable`):** `go.mod` contains zero external
  `require` directives (`Go 1.22+`). Go packages import strictly from the Go
  standard library and `github.com/UberMetroid/agent-ooda/internal/...`, built
  as a static `CGO_ENABLED=0` multi-call binary (`plugins/ooda/bin/ooda`).
- `[POLICY]` Zero third-party modules or vendored dependencies anywhere in
  `cmd/`, `internal/`, `plugins/ooda/`, or `qa/`.

## 2. Power-of-2 Code Shape (`1 / 2 / 4 / 8 / 16 / 32 / 64 / 128 / 256`)

- `[GATE]` **16–256 Lines & 128 Cols (`make line-cap`):** Every `.go` source and
  test file must be **16 to 256 lines** inclusive, with every line at most
  **128 display columns** (tabs expanded to 4 spaces).
- `[GATE]` **Directory Density <= 8 (`make density`):** At most **8 entries per
  directory** under `cmd/`, `internal/`, `plugins/ooda/`, and `qa/`
  (excluding `.git`, `.worktrees`, `__pycache__`).
- `[GATE]` **Function Shape (`make shape`):** Across every `.go` file AST: at
  most **8 function declarations**, at most **8 parameters** per function, at
  most **64 non-blank non-comment lines** per function body, at most **4 levels**
  of control-flow block nesting, and max **1 statement per line**.
- `[GATE]` **Banned Drawer Vocabulary & ID Cap (`make vocab`):** Zero files named
  `util.go`, `utils.go`, `helper.go`, `helpers.go`, `common.go`, `misc.go`,
  `shared.go`, `base.go`, or `core.go`; no `*_part*` or `*_ext*` splits; all
  `.go` filenames and declared AST identifiers at most **32 characters**.

## 3. Portability, Security & Process Hygiene

- `[GATE]` **Portability (`make portable`):** Zero author-machine home paths,
  zero embedded credentials/API keys, and zero `sh -c` / `bash -c` invocations
  inside source or tests.
- `[POLICY]` **Decoupled Mutable State:** Runtime state (`quota.json`, active
  worker HUD markers, `flock` leases, `mismatch.json`) lives under
  `~/.local/state/ooda/` or `/tmp/router-hud`, never mutated inside git.
- `[POLICY]` **Atomic Writes & Bounded Execution:** Persistent JSON state uses
  temp-file + `os.Rename`. Subprocesses are `argv`-only with explicit timeouts.

## 4. Antigravity Plugin Contract & Verification Ladder

- `[GATE]` **Plugin Validation (`make plugin-check`):**
  `agy plugin validate plugins/ooda --strict --json` must exit `0` with zero
  findings.
- `[GATE]` **Race Tests & Dogfood (`make test`, `make dogfood`):** `go vet ./...`,
  `go test -race -count=1 ./...`, and isolated binary CLI/HUD/hook dogfood smoke
  checks must pass with zero skips before any commit.
- Run the complete gate ladder with `make all`.

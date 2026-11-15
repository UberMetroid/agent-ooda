# agy-skills — House Laws & Agent Engineering Standards

Canonical engineering rules for humans and agents working in `agy-skills`.
Every `[GATE]` rule below is machine-enforced by `Makefile` targets and
`qa/gates/`; every `[POLICY]` rule is enforced during code review.

---

## 1. Zero Third-Party Dependencies

- `[GATE]` **Stdlib only (`make portable`):** Python modules import strictly
  from the Python standard library (`Python 3.8+`) and local repo packages
  (`fleet`, `envelope`, `worktree`, `dispatch`, `verify`, `hud`, `cli`, `qa`).
- `[POLICY]` Zero PyPI / `pip` / third-party packages anywhere in `plugins/ooda`
  or `qa/`.

## 2. The Page Rule (Code Layout & Sizing)

- `[GATE]` **16–256 Lines (`make line-cap`):** Every committed `.py` file and
  `plugins/ooda/bin/*` script must be **16 to 256 lines** inclusive (comments and
  blank lines counted).
- `[GATE]` **Shim Exemption (Floor Only):** `__init__.py` files whose AST body
  consists solely of imports and an optional `__all__` assignment skip the
  16-line floor. The 256-line ceiling still applies. Never pad lines to hit 16.
- `[GATE]` **Directory Density <= 8 (`make density`):** At most **8 `.py` files
  per directory** (excluding `.git`, `.worktrees`, `__pycache__`).
- `[GATE]` **Function Shape (`make shape`):** Across the entire AST of any
  file, at most **8 `def`/`async def` definitions** (including nested helpers)
  and at most **64 lines per function/method**.
- `[GATE]` **Banned Drawer Vocabulary (`make vocab`):** Zero files named
  `util.py`, `utils.py`, `helper.py`, `helpers.py`, `common.py`, `misc.py`,
  `shared.py`, `base.py`, `core.py`. Name the capability or action instead.

## 3. Portability, Security & Process Hygiene

- `[GATE]` **Portability (`make portable`):** Zero author-machine home paths,
  zero embedded credentials/API keys, zero `shell=True`, and zero `sh -c` /
  `bash -c` invocations inside Python source.
- `[POLICY]` **Decoupled Mutable State:** Runtime state (`quota.json`, active
  worker HUD markers, `flock` leases, `mismatch.json`) lives under
  `~/.local/state/ooda/` or `/tmp/router-hud`, never mutated inside git.
- `[POLICY]` **Atomic Writes & Bounded Execution:** Persistent JSON state uses
  temp-file + `os.replace`. Subprocesses are `argv`-only with explicit timeouts.

## 4. Antigravity Plugin Contract & Verification Ladder

- `[GATE]` **Plugin Validation (`make plugin-check`):**
  `agy plugin validate plugins/ooda --strict --json` must exit `0` with zero
  findings.
- `[GATE]` **Automated Tests & Dogfood (`make test`, `make dogfood`):**
  Full `unittest` suite and isolated CLI/HUD/hook dogfood smoke checks must
  pass before any commit.
- Run the complete gate ladder with `make all`.

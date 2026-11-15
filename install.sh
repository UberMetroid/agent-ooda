#!/usr/bin/env bash
# install.sh — wire the ooda Antigravity plugin, CLI shims, and statusLine HUD into $HOME.
# Idempotent: safe to re-run. Displaced paths and legacy router/boyd installs are archived
# under "$STATE_DIR/backups" (never deleted) only AFTER quota state is migrated and validated.
# `./install.sh --uninstall [--purge]` reverses it (logic lives in uninstall.sh).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$REPO_ROOT/plugins/ooda"
STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}"
STATE_DIR="$STATE_BASE/ooda"
QUOTA_FILE="$STATE_DIR/quota.json"
DEFAULT_QUOTA="$PLUGIN_DIR/config/quota.default.json"
QUOTA_SOURCE="existing"
AGY_DIR="$HOME/.gemini/antigravity-cli"
SETTINGS_JSON="$AGY_DIR/settings.json"
WIRE="$PLUGIN_DIR/src/cli/wire_settings.py"

usage() {
  cat <<'EOF'
usage: ./install.sh [--uninstall [--purge]] [--help]

  (no options)         install / update: link plugin, CLI shims, statusLine HUD; wire settings.json
  --uninstall          remove ooda symlinks that point into this repo and unwire statusLine
  --uninstall --purge  also delete ${XDG_STATE_HOME:-~/.local/state}/ooda (quota state + backups)
  -h, --help           show this help

environment:
  OODA_NO_STATUSLINE=1  do not add the statusLine block to ~/.gemini/antigravity-cli/settings.json
  XDG_STATE_HOME        base for runtime state (default ~/.local/state)
EOF
}

MODE="install"
case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --uninstall) MODE="uninstall"; shift ;;
  "") ;;
  *) echo "error: unknown option: $1 (see --help)" >&2; exit 2 ;;
esac
[ "$MODE" = "uninstall" ] || [ "$#" -eq 0 ] || { echo "error: unexpected argument: $1" >&2; exit 2; }

command -v python3 >/dev/null 2>&1 || { echo "error: python3 (3.8+) is required" >&2; exit 1; }
python3 -c 'import sys; sys.exit(0 if sys.version_info >= (3, 8) else 1)' \
  || { echo "error: python3 >= 3.8 is required" >&2; exit 1; }
[ -d "$PLUGIN_DIR" ] || { echo "error: plugin not found at $PLUGIN_DIR" >&2; exit 1; }

# Guard STATE_DIR before install or uninstall touches anything: --purge runs `rm -rf "$STATE_DIR"`,
# so a relative or overlapping XDG_STATE_HOME (e.g. `plugins`) must never resolve into the repo.
python3 - "$STATE_BASE" "$STATE_DIR" "$HOME" "$REPO_ROOT" <<'PY' || exit 1
import os
import sys

base, state, home, repo = sys.argv[1:5]
if not os.path.isabs(base):
    sys.stderr.write(f"error: state base {base!r} (XDG_STATE_HOME) must be an absolute path\n")
    sys.exit(1)
real_state, real_repo = os.path.realpath(state), os.path.realpath(repo)
bad = {"/", os.path.realpath(home), real_repo}
if (real_state in bad or real_state.startswith(real_repo + os.sep)
        or real_repo.startswith(real_state + os.sep)):
    sys.stderr.write(f"error: unsafe state dir {state} (resolves to {real_state}); it must not be /, "
                     f"$HOME, or overlap the repo {real_repo}. Fix XDG_STATE_HOME.\n")
    sys.exit(1)
PY

if [ "$MODE" = "uninstall" ]; then
  # shellcheck source=uninstall.sh
  . "$REPO_ROOT/uninstall.sh"
  ooda_uninstall "$@"
  exit $?
fi

# valid_quota FILE -> exit 0 when FILE parses as a JSON object containing "providers".
valid_quota() {
  [ -f "$1" ] || return 1
  python3 - "$1" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as fh:
        data = json.load(fh)
except (OSError, ValueError):
    sys.exit(1)
sys.exit(0 if isinstance(data, dict) and isinstance(data.get("providers"), dict) and len(data["providers"]) > 0 else 1)
PY
}

BACKUP_ROOT="$STATE_DIR/backups"
BACKUP_DIR=""

# archive_path LABEL PATH -> move displaced file/dir/foreign-symlink into $BACKUP_DIR/LABEL.
archive_path() {
  if [ -z "$BACKUP_DIR" ]; then
    BACKUP_DIR="$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$$"
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_ROOT" "$BACKUP_DIR"
  fi
  mv "$2" "$BACKUP_DIR/$1"
}

# link TARGET LINK_PATH LABEL -> idempotent symlink; archives any displaced path first.
link() {
  if [ -L "$2" ] && [ "$(readlink "$2")" = "$1" ]; then
    return 0
  fi
  if [ -e "$2" ] || [ -L "$2" ]; then
    archive_path "$3" "$2"
  fi
  ln -sfn "$1" "$2"
}

# retire_legacy LABEL PATH -> archive legacy plugin/skill/shim into $BACKUP_DIR/LABEL if present.
retire_legacy() {
  if [ -e "$2" ] || [ -L "$2" ]; then
    archive_path "$1" "$2"
  fi
}

# skills_json check|update FILE -> validate (exit 1, never write, when malformed); update also
# drops legacy router/ooda skill entries and ensures ~/.agents/skills, written atomically.
skills_json() {
  python3 - "$1" "$2" <<'PY'
import json
import os
import sys

mode, path = sys.argv[1], sys.argv[2]
norm = lambda p: os.path.normpath(os.path.expanduser(str(p)))
data = {}
if os.path.lexists(path) and not os.path.isfile(path):
    sys.stderr.write(f"error: {path} is not a regular file; refusing to overwrite\n")
    sys.exit(1)
if os.path.isfile(path) and os.path.getsize(path) > 0:
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, ValueError) as exc:
        sys.stderr.write(f"error: malformed JSON in {path}; refusing to overwrite: {exc}\n")
        sys.exit(1)
raw = data.get("entries") if isinstance(data, dict) else None
if not isinstance(data, dict) or not (raw is None or isinstance(raw, list)):
    sys.stderr.write(f"error: {path} must be a JSON object with a list 'entries'; refusing to overwrite\n")
    sys.exit(1)
if not all(isinstance(e, dict) for e in raw or []):
    sys.stderr.write(f"error: {path} 'entries' must contain only JSON objects; refusing to overwrite\n")
    sys.exit(1)
if mode == "check":
    # Preflight writability: the file itself (if present) and the deepest existing ancestor dir.
    probe = os.path.dirname(os.path.abspath(path))
    while not os.path.isdir(probe) and os.path.dirname(probe) != probe:
        probe = os.path.dirname(probe)
    if os.path.exists(path) and not os.access(path, os.W_OK):
        sys.stderr.write(f"error: {path} is not writable\n")
        sys.exit(1)
    if not os.access(probe, os.W_OK | os.X_OK):
        sys.stderr.write(f"error: directory {probe} is not writable; cannot update {path}\n")
        sys.exit(1)
    sys.exit(0)
legacy = {norm("~/.agents/skills/router"), norm("~/.agents/skills/ooda")}
entries = [e for e in raw or [] if norm(e.get("path", "")) not in legacy]
if not any(norm(e.get("path", "")) == norm("~/.agents/skills") for e in entries):
    entries.append({"path": "~/.agents/skills"})
data["entries"] = entries
tmp = f"{path}.tmp.{os.getpid()}"
try:
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(data, fh, indent=2)
        fh.write("\n")
    os.replace(tmp, path)
except BaseException:
    if os.path.lexists(tmp):
        os.unlink(tmp)
    raise
PY
}

# health_report -> informational PATH / agy / lane CLI checks; never fails the install.
health_report() {
  local cli lanes=""
  echo "health:"
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) echo "  PATH        : ok (~/.local/bin is on PATH)" ;;
    *) echo "  warning     : ~/.local/bin is not on PATH; add to your shell rc:"
       echo "                export PATH=\"\$HOME/.local/bin:\$PATH\"" ;;
  esac
  if command -v agy >/dev/null 2>&1; then
    echo "  agy         : $(command -v agy) (confirm registration: agy plugin list)"
  else
    echo "  warning     : agy not found on PATH; install it, then confirm with: agy plugin list"
  fi
  for cli in agy devin muse grok vibe mcode; do
    if command -v "$cli" >/dev/null 2>&1; then lanes="$lanes $cli:found"; else lanes="$lanes $cli:missing"; fi
  done
  echo "  lane CLIs   :$lanes (informational)"
  echo "  reminder    : restart agy so the ooda plugin and statusLine HUD load"
}

# Preflight directory layout + skills.json + settings.json before mutating any state, plugins, shims, or legacy installs.
python3 - "$STATE_DIR" "$HOME/.gemini/config/plugins" "$HOME/.local/bin" "$AGY_DIR" "$HOME/.agents/skills" <<'PY' || exit 1
import os
import sys

for target in sys.argv[1:]:
    cur = os.path.abspath(target)
    while True:
        if os.path.lexists(cur):
            if not os.path.isdir(cur):
                sys.stderr.write(f"error: {cur} exists and is not a directory; refusing to install\n")
                sys.exit(1)
            if not os.access(cur, os.W_OK | os.X_OK):
                sys.stderr.write(f"error: directory {cur} is not writable; cannot create {target}\n")
                sys.exit(1)
            break
        parent = os.path.dirname(cur)
        if parent == cur:
            break
        cur = parent
PY
# skills.json is always checked (validity + writability of it or its deepest existing ancestor).
skills_json check "$HOME/.gemini/config/skills.json"
if [ "${OODA_NO_STATUSLINE:-0}" != "1" ]; then
  python3 "$WIRE" check "$SETTINGS_JSON" "$STATE_DIR"
fi

# 1. Mutable state lives outside git, private to the user.
mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"

# 2. Seed or migrate quota.json (legacy router state first, then boyd, else defaults).
if [ ! -f "$QUOTA_FILE" ]; then
  QUOTA_SOURCE="$DEFAULT_QUOTA"
  for legacy in "$HOME/.agents/skills/router/quota.json" "$STATE_BASE/boyd/quota.json"; do
    if valid_quota "$legacy"; then
      QUOTA_SOURCE="$legacy"
      break
    fi
  done
  cp -p "$QUOTA_SOURCE" "$QUOTA_FILE"
fi
if ! valid_quota "$QUOTA_FILE"; then
  echo "error: $QUOTA_FILE is not a JSON object with 'providers'; legacy installs left untouched" >&2
  exit 1
fi
chmod 600 "$QUOTA_FILE"

# 3. Update skills.json atomically before removing any legacy installs, plugins, or skills.
#    Always register ~/.agents/skills, creating ~/.gemini/config on a fresh install.
SKILLS_JSON="$HOME/.gemini/config/skills.json"
mkdir -p "$HOME/.gemini/config"
skills_json update "$SKILLS_JSON"

# 4. Antigravity plugin registration (archive obsolete router-core / boyd plugins).
mkdir -p "$HOME/.gemini/config/plugins"
link "$PLUGIN_DIR" "$HOME/.gemini/config/plugins/ooda" "plugin-ooda"
retire_legacy "plugin-router-core" "$HOME/.gemini/config/plugins/router-core"
retire_legacy "plugin-boyd" "$HOME/.gemini/config/plugins/boyd"

# 5. CLI shims: `ooda` and its `router` alias.
mkdir -p "$HOME/.local/bin"
link "$PLUGIN_DIR/bin/ooda" "$HOME/.local/bin/ooda" "bin-ooda"
link "$PLUGIN_DIR/bin/ooda" "$HOME/.local/bin/router" "bin-router"
retire_legacy "bin-boyd" "$HOME/.local/bin/boyd"

# 6. Bubble-canopy statusLine HUD: symlink, then point settings.json statusLine at it.
mkdir -p "$AGY_DIR"
link "$PLUGIN_DIR/bin/ooda-hud" "$AGY_DIR/statusline.py" "statusline.py"
SETTINGS_NOTE="$SETTINGS_JSON (statusLine -> ~/.gemini/antigravity-cli/statusline.py)"
WIRE_OUT=""
if [ "${OODA_NO_STATUSLINE:-0}" = "1" ]; then
  SETTINGS_NOTE="(skipped: OODA_NO_STATUSLINE=1; $SETTINGS_JSON untouched)"
else
  WIRE_OUT="$(python3 "$WIRE" wire "$SETTINGS_JSON" "$STATE_DIR")"
fi

# 7. Archive duplicate legacy skill copies (the plugin now ships ooda + router skills).
retire_legacy "skill-router" "$HOME/.agents/skills/router"
retire_legacy "skill-ooda" "$HOME/.agents/skills/ooda"
mkdir -p "$HOME/.agents/skills"

ACTIVE_OODA="$(command -v ooda 2>/dev/null || true)"
PATH_NOTE=""
if [ -n "$ACTIVE_OODA" ] && [ "$ACTIVE_OODA" != "$HOME/.local/bin/ooda" ] && [ "$ACTIVE_OODA" != "$PLUGIN_DIR/bin/ooda" ]; then
  PATH_NOTE="  note        : 'ooda' on PATH resolves to $ACTIVE_OODA; use 'router' (or ~/.local/bin/ooda)"
fi

cat <<EOF
ooda install complete
  repo        : $REPO_ROOT
  quota       : $QUOTA_FILE (source: $QUOTA_SOURCE)
  plugin      : ~/.gemini/config/plugins/ooda -> plugins/ooda
  cli         : ~/.local/bin/ooda, ~/.local/bin/router -> plugins/ooda/bin/ooda
  statusLine  : ~/.gemini/antigravity-cli/statusline.py -> plugins/ooda/bin/ooda-hud
  settings    : $SETTINGS_NOTE
  skills.json : $SKILLS_JSON
  archived    : ${BACKUP_DIR:-(none displaced)}
EOF
[ -z "$WIRE_OUT" ] || printf '%s\n' "$WIRE_OUT" | sed 's/^/  wire        : /'
[ -n "$PATH_NOTE" ] && printf '%s\n' "$PATH_NOTE"
health_report
echo "Next: restart agy, then: agy plugin list && router --status && router --usage"

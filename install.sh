#!/usr/bin/env bash
# install.sh — wire the ooda Antigravity plugin, CLI shims, and statusLine HUD into $HOME.
# Idempotent: safe to re-run. Displaced paths and legacy router/boyd installs are archived
# under "$STATE_DIR/backups" (never deleted) only AFTER quota state is migrated and validated.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$REPO_ROOT/plugins/ooda"
STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}"
STATE_DIR="$STATE_BASE/ooda"
QUOTA_FILE="$STATE_DIR/quota.json"
DEFAULT_QUOTA="$PLUGIN_DIR/config/quota.default.json"
QUOTA_SOURCE="existing"

command -v python3 >/dev/null 2>&1 || { echo "error: python3 (3.8+) is required" >&2; exit 1; }
python3 -c 'import sys; sys.exit(0 if sys.version_info >= (3, 8) else 1)' \
  || { echo "error: python3 >= 3.8 is required" >&2; exit 1; }
[ -d "$PLUGIN_DIR" ] || { echo "error: plugin not found at $PLUGIN_DIR" >&2; exit 1; }

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

# validate_skills_json FILE -> exit 1 without writing when FILE is malformed or has non-list entries.
validate_skills_json() {
  [ -f "$1" ] || return 0
  python3 - "$1" <<'PY'
import json
import os
import sys

path = sys.argv[1]
if os.path.getsize(path) == 0:
    sys.exit(0)
try:
    with open(path, encoding="utf-8") as fh:
        data = json.load(fh)
except (OSError, ValueError) as exc:
    sys.stderr.write(f"error: malformed JSON in {path}; refusing to overwrite: {exc}\n")
    sys.exit(1)
if not isinstance(data, dict) or (data.get("entries") is not None and not isinstance(data.get("entries"), list)):
    sys.stderr.write(f"error: {path} must be a JSON object with a list 'entries'; refusing to overwrite\n")
    sys.exit(1)
entries = data.get("entries")
if isinstance(entries, list) and not all(isinstance(e, dict) for e in entries):
    sys.stderr.write(f"error: {path} 'entries' must contain only JSON objects; refusing to overwrite\n")
    sys.exit(1)
PY
}

# Preflight skills.json before mutating any state, plugins, shims, or legacy installs.
if [ -d "$HOME/.gemini/config" ]; then
  validate_skills_json "$HOME/.gemini/config/skills.json"
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
SKILLS_JSON="(skipped: no ~/.gemini/config)"
if [ -d "$HOME/.gemini/config" ]; then
  SKILLS_JSON="$HOME/.gemini/config/skills.json"
  python3 - "$SKILLS_JSON" <<'PY'
import json
import os
import sys

path = sys.argv[1]
norm = lambda p: os.path.normpath(os.path.expanduser(str(p)))
legacy = {norm("~/.agents/skills/router"), norm("~/.agents/skills/ooda")}
wanted = norm("~/.agents/skills")
data = {}
if os.path.isfile(path) and os.path.getsize(path) > 0:
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, ValueError):
        sys.stderr.write(f"error: malformed JSON in {path}; refusing to overwrite\n")
        sys.exit(1)
if not isinstance(data, dict) or not (
        data.get("entries") is None or isinstance(data.get("entries"), list)):
    sys.stderr.write(f"error: unexpected skills.json shape in {path}; refusing to overwrite\n")
    sys.exit(1)
raw = data.get("entries") or []
if not all(isinstance(e, dict) for e in raw):
    sys.stderr.write(f"error: {path} 'entries' must contain only JSON objects; refusing to overwrite\n")
    sys.exit(1)
entries = [e for e in raw if norm(e.get("path", "")) not in legacy]
if not any(norm(e.get("path", "")) == wanted for e in entries):
    entries.append({"path": "~/.agents/skills"})
data["entries"] = entries
tmp = f"{path}.tmp.{os.getpid()}"
with open(tmp, "w", encoding="utf-8") as fh:
    json.dump(data, fh, indent=2)
    fh.write("\n")
os.replace(tmp, path)
PY
fi

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

# 6. Bubble-canopy statusLine HUD.
mkdir -p "$HOME/.gemini/antigravity-cli"
link "$PLUGIN_DIR/bin/ooda-hud" "$HOME/.gemini/antigravity-cli/statusline.py" "statusline.py"

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
  skills.json : $SKILLS_JSON
  archived    : ${BACKUP_DIR:-(none displaced)}
EOF
[ -n "$PATH_NOTE" ] && printf '%s\n' "$PATH_NOTE"
echo "Next: agy plugin list && router --status && router --usage"

#!/usr/bin/env bash
# install.sh — wire the ooda Antigravity plugin, CLI shims, and statusLine HUD into $HOME.
# Idempotent: safe to re-run. Displaced paths and legacy router/boyd installs are archived
# under "$STATE_DIR/backups" (never deleted) only AFTER quota state is migrated and validated.
# `./install.sh --uninstall [--purge]` reverses it (logic lives in uninstall.sh).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$REPO_ROOT/plugins/ooda"
OODA_BIN="$PLUGIN_DIR/bin/ooda"
STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}"
STATE_DIR="$STATE_BASE/ooda"
QUOTA_FILE="$STATE_DIR/quota.json"
DEFAULT_QUOTA="$PLUGIN_DIR/config/quota.default.json"
QUOTA_SOURCE="existing"
AGY_DIR="$HOME/.gemini/antigravity-cli"
SETTINGS_JSON="$AGY_DIR/settings.json"

usage() {
  cat <<'EOF'
usage: ./install.sh [--uninstall [--purge]] [--help]

  (no options)         build (if go present) & install: link plugin, CLI shims, statusLine HUD; wire settings.json
  --uninstall          remove ooda symlinks that point into this repo and unwire statusLine
  --uninstall --purge  also delete ${XDG_STATE_HOME:-~/.local/state}/ooda (quota state + backups)
  -h, --help           show this help

environment:
  OODA_NO_STATUSLINE=1  do not add the statusLine block to ~/.gemini/antigravity-cli/settings.json
  OODA_NO_BUILD=1       use existing plugins/ooda/bin/ooda without rebuilding/downloading
  OODA_RELEASE_BASE     override base URL for prebuilt binaries + SHA256SUMS
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
[ -d "$PLUGIN_DIR" ] || { echo "error: plugin not found at $PLUGIN_DIR" >&2; exit 1; }

if [ "$MODE" = "uninstall" ]; then
  # Guard STATE_DIR before uninstall or purge touches anything, even when $OODA_BIN is absent.
  if [ -x "$OODA_BIN" ]; then
    "$OODA_BIN" __setup check-state "$STATE_BASE" "$STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1
  else
    case "$STATE_BASE" in /*) ;; *) echo "error: state base must be absolute: $STATE_BASE" >&2; exit 1 ;; esac
    case "$STATE_DIR" in /*) ;; *) echo "error: state dir must be absolute: $STATE_DIR" >&2; exit 1 ;; esac
    case "$STATE_DIR" in
      "$HOME"|"$HOME/"|"$REPO_ROOT"|"$REPO_ROOT"/*)
        echo "error: refusing unsafe state dir: $STATE_DIR" >&2; exit 1 ;;
    esac
  fi
  # shellcheck source=uninstall.sh
  . "$REPO_ROOT/uninstall.sh"
  ooda_uninstall "$@"
  exit $?
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "error: sha256sum or shasum required to verify downloaded binary" >&2
    return 1
  fi
}

download_prebuilt_binary() {
  local base os arch asset
  base="${OODA_RELEASE_BASE:-https://github.com/UberMetroid/agent-ooda/releases/latest/download}"
  base="${base%/}"
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) echo "error: unsupported OS '$(uname -s)' for prebuilt ooda (install Go 1.22+)" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "error: unsupported arch '$(uname -m)' for prebuilt ooda (install Go 1.22+)" >&2; return 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || {
    echo "error: curl required to download prebuilt ooda binary (or install Go 1.22+)" >&2
    return 1
  }
  asset="ooda-${os}-${arch}"
  (
    local stage expected actual cur_hash
    stage="$(mktemp -d)"
    trap 'rm -rf -- "$stage"' EXIT
    curl -fsSL "$base/SHA256SUMS" -o "$stage/SHA256SUMS" || {
      echo "error: failed to download $base/SHA256SUMS" >&2
      return 1
    }
    expected="$(awk -v f="$asset" '($2 == f || $2 == "*"f) {print $1; exit}' "$stage/SHA256SUMS")"
    [ -n "$expected" ] || { echo "error: checksum for $asset missing in SHA256SUMS" >&2; return 1; }
    if [ -x "$OODA_BIN" ]; then
      cur_hash="$(sha256_file "$OODA_BIN")"
      if [ "$cur_hash" = "$expected" ]; then
        return 0
      fi
    fi
    echo "downloading prebuilt $asset from $base/$asset ..."
    curl -fsSL "$base/$asset" -o "$stage/$asset" || {
      echo "error: failed to download $base/$asset" >&2
      return 1
    }
    actual="$(sha256_file "$stage/$asset")"
    if [ "$expected" != "$actual" ]; then
      echo "error: SHA-256 mismatch for $asset (expected $expected, got $actual)" >&2
      return 1
    fi
    install -m 0755 "$stage/$asset" "$OODA_BIN"
  )
}

mkdir -p "$PLUGIN_DIR/bin"
if [ "${OODA_NO_BUILD:-0}" != "1" ]; then
  if command -v go >/dev/null 2>&1 && [ -w "$PLUGIN_DIR/bin" ]; then
    (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OODA_BIN" ./cmd/ooda)
  else
    download_prebuilt_binary
  fi
fi
[ -x "$OODA_BIN" ] || { echo "error: executable not found at $OODA_BIN" >&2; exit 1; }
ln -sfn ooda "$PLUGIN_DIR/bin/ooda-hud"
ln -sfn ooda "$PLUGIN_DIR/bin/ooda-hook"

"$OODA_BIN" __setup check-state "$STATE_BASE" "$STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1

valid_quota() {
  [ -f "$1" ] || return 1
  "$OODA_BIN" __setup valid-quota "$1"
}

BACKUP_ROOT="$STATE_DIR/backups"
BACKUP_DIR=""

archive_path() {
  if [ -z "$BACKUP_DIR" ]; then
    BACKUP_DIR="$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$$"
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_ROOT" "$BACKUP_DIR"
  fi
  mv "$2" "$BACKUP_DIR/$1"
}

link() {
  if [ -L "$2" ] && [ "$(readlink "$2")" = "$1" ]; then
    return 0
  fi
  if [ -e "$2" ] || [ -L "$2" ]; then
    archive_path "$3" "$2"
  fi
  ln -sfn "$1" "$2"
}

retire_legacy() {
  if [ -e "$2" ] || [ -L "$2" ]; then
    archive_path "$1" "$2"
  fi
}

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
"$OODA_BIN" __setup check-dirs "$STATE_DIR" "$HOME/.gemini/config/plugins" "$HOME/.local/bin" "$AGY_DIR" "$HOME/.agents/skills" || exit 1
"$OODA_BIN" __setup skills-check "$HOME/.gemini/config/skills.json"
if [ "${OODA_NO_STATUSLINE:-0}" != "1" ]; then
  "$OODA_BIN" __wire check "$SETTINGS_JSON" "$STATE_DIR"
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
SKILLS_JSON="$HOME/.gemini/config/skills.json"
mkdir -p "$HOME/.gemini/config"
"$OODA_BIN" __setup skills-update "$SKILLS_JSON"

# 4. Antigravity plugin registration (archive obsolete router-core / boyd plugins).
mkdir -p "$HOME/.gemini/config/plugins"
link "$PLUGIN_DIR" "$HOME/.gemini/config/plugins/ooda" "plugin-ooda"
retire_legacy "plugin-router-core" "$HOME/.gemini/config/plugins/router-core"
retire_legacy "plugin-boyd" "$HOME/.gemini/config/plugins/boyd"

# 5. CLI shims: `ooda` and its `router` alias.
mkdir -p "$HOME/.local/bin"
link "$OODA_BIN" "$HOME/.local/bin/ooda" "bin-ooda"
link "$OODA_BIN" "$HOME/.local/bin/router" "bin-router"
retire_legacy "bin-boyd" "$HOME/.local/bin/boyd"

# 6. Bubble-canopy statusLine HUD: symlink, then point settings.json statusLine at it.
mkdir -p "$AGY_DIR"
link "$PLUGIN_DIR/bin/ooda-hud" "$AGY_DIR/statusline.py" "statusline.py"
SETTINGS_NOTE="$SETTINGS_JSON (statusLine -> ~/.gemini/antigravity-cli/statusline.py)"
WIRE_OUT=""
if [ "${OODA_NO_STATUSLINE:-0}" = "1" ]; then
  SETTINGS_NOTE="(skipped: OODA_NO_STATUSLINE=1; $SETTINGS_JSON untouched)"
else
  WIRE_OUT="$("$OODA_BIN" __wire wire "$SETTINGS_JSON" "$STATE_DIR")"
fi

# 7. Archive duplicate legacy skill copies (the plugin now ships ooda + router skills).
retire_legacy "skill-router" "$HOME/.agents/skills/router"
retire_legacy "skill-ooda" "$HOME/.agents/skills/ooda"
mkdir -p "$HOME/.agents/skills"

ACTIVE_OODA="$(command -v ooda 2>/dev/null || true)"
PATH_NOTE=""
if [ -n "$ACTIVE_OODA" ] && [ "$ACTIVE_OODA" != "$HOME/.local/bin/ooda" ] && [ "$ACTIVE_OODA" != "$OODA_BIN" ]; then
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

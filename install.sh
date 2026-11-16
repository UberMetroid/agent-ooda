#!/usr/bin/env bash
# install.sh — wire the agent-ooda Antigravity plugin, CLI binary, and statusLine HUD into $HOME.
# Idempotent: safe to re-run. Migrates legacy ooda/router installs cleanly, archiving displaced
# items under "$STATE_DIR/backups" (never deleted) AFTER quota state is migrated and validated.
# `./install.sh --uninstall [--purge]` reverses it (logic lives in uninstall.sh).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$REPO_ROOT/plugins/agent-ooda"
OODA_BIN="$PLUGIN_DIR/bin/agent-ooda"
STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}"
STATE_DIR="$STATE_BASE/agent-ooda"
LEGACY_STATE_DIR="$STATE_BASE/ooda"
QUOTA_FILE="$STATE_DIR/quota.json"
DEFAULT_QUOTA="$PLUGIN_DIR/config/quota.default.json"
QUOTA_SOURCE="existing"
AGY_DIR="$HOME/.gemini/antigravity-cli"
SETTINGS_JSON="$AGY_DIR/settings.json"
NO_STATUSLINE="${AGENT_OODA_NO_STATUSLINE:-${OODA_NO_STATUSLINE:-0}}"
NO_BUILD="${AGENT_OODA_NO_BUILD:-${OODA_NO_BUILD:-0}}"
RELEASE_BASE="${AGENT_OODA_RELEASE_BASE:-${OODA_RELEASE_BASE:-https://github.com/UberMetroid/agent-ooda/releases/latest/download}}"

usage() {
  cat <<'EOF'
usage: ./install.sh [--uninstall [--purge]] [--help]

  (no options)         build (if go present) & install: link plugin, CLI, statusLine HUD; wire settings.json
  --uninstall          remove agent-ooda symlinks pointing into this repo and unwire statusLine
  --uninstall --purge  also delete ${XDG_STATE_HOME:-~/.local/state}/agent-ooda (quota state + backups)
  -h, --help           show this help

environment:
  AGENT_OODA_NO_STATUSLINE=1  skip adding statusLine to ~/.gemini/antigravity-cli/settings.json
  AGENT_OODA_NO_BUILD=1       use existing plugins/agent-ooda/bin/agent-ooda without rebuilding/downloading
  AGENT_OODA_RELEASE_BASE     override base URL for prebuilt binaries + SHA256SUMS
  XDG_STATE_HOME              base for runtime state (default ~/.local/state)
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
  if [ -x "$OODA_BIN" ]; then
    "$OODA_BIN" __setup check-state "$STATE_BASE" "$STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1
    "$OODA_BIN" __setup check-state "$STATE_BASE" "$LEGACY_STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1
  else
    case "$STATE_BASE" in /*) ;; *) echo "error: state base must be absolute: $STATE_BASE" >&2; exit 1 ;; esac
    case "$STATE_DIR" in /*) ;; *) echo "error: state dir must be absolute: $STATE_DIR" >&2; exit 1 ;; esac
    case "$STATE_DIR" in
      "$HOME"|"$HOME/"|"$REPO_ROOT"|"$REPO_ROOT"/*)
        echo "error: refusing unsafe state dir: $STATE_DIR" >&2; exit 1 ;;
    esac
    case "$LEGACY_STATE_DIR" in
      /|"$HOME"|"$HOME/"|"$REPO_ROOT"|"$REPO_ROOT"/*)
        echo "error: refusing unsafe legacy state dir: $LEGACY_STATE_DIR" >&2; exit 1 ;;
    esac
  fi
  # shellcheck source=uninstall.sh
  . "$REPO_ROOT/uninstall.sh"
  agent_ooda_uninstall "$@"
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
  base="${RELEASE_BASE%/}"
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) echo "error: unsupported OS '$(uname -s)' for prebuilt agent-ooda (install Go 1.22+)" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "error: unsupported arch '$(uname -m)' for prebuilt agent-ooda (install Go 1.22+)" >&2; return 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || {
    echo "error: curl required to download prebuilt agent-ooda binary (or install Go 1.22+)" >&2
    return 1
  }
  asset="agent-ooda-${os}-${arch}"
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
if [ "$NO_BUILD" != "1" ]; then
  if command -v go >/dev/null 2>&1 && [ -w "$PLUGIN_DIR/bin" ]; then
    (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OODA_BIN" ./cmd/agent-ooda)
  else
    download_prebuilt_binary
  fi
fi
[ -x "$OODA_BIN" ] || { echo "error: executable not found at $OODA_BIN" >&2; exit 1; }
ln -sfn agent-ooda "$PLUGIN_DIR/bin/agent-ooda-hud"
ln -sfn agent-ooda "$PLUGIN_DIR/bin/agent-ooda-hook"

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

DATA_CLONE="${XDG_DATA_HOME:-$HOME/.local/share}/agent-ooda"

retire_owned_legacy() {
  local target
  [ -L "$2" ] || return 0
  target="$(readlink "$2" || true)"
  case "$target" in
    "$REPO_ROOT"|"$REPO_ROOT"/*|"$DATA_CLONE"|"$DATA_CLONE"/*|"$HOME/.agents/skills/router"|"$HOME/.agents/skills/router"/*)
      archive_path "$1" "$2" ;;
  esac
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
  echo "  reminder    : restart agy so the agent-ooda plugin and statusLine HUD load"
}

# Preflight directory layout + skills.json + settings.json before mutating any state, plugins, shims, or legacy installs.
"$OODA_BIN" __setup check-dirs "$STATE_DIR" "$HOME/.gemini/config/plugins" "$HOME/.local/bin" "$AGY_DIR" "$HOME/.agents/skills" || exit 1
"$OODA_BIN" __setup skills-check "$HOME/.gemini/config/skills.json"
if [ "$NO_STATUSLINE" != "1" ]; then
  "$OODA_BIN" __wire check "$SETTINGS_JSON" "$STATE_DIR"
fi

# 1. Mutable state lives outside git, private to the user.
mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"

# 2. Seed or migrate quota.json (legacy state/ooda, router, boyd, else defaults).
if [ ! -f "$QUOTA_FILE" ]; then
  QUOTA_SOURCE="$DEFAULT_QUOTA"
  for legacy in "$LEGACY_STATE_DIR/quota.json" "$HOME/.agents/skills/router/quota.json" "$STATE_BASE/boyd/quota.json"; do
    if valid_quota "$legacy"; then
      QUOTA_SOURCE="$legacy"
      break
    fi
  done
  cp -p "$QUOTA_SOURCE" "$QUOTA_FILE"
fi
if [ -f "$LEGACY_STATE_DIR/statusline.prev.json" ] && [ ! -e "$STATE_DIR/statusline.prev.json" ]; then
  cp -p "$LEGACY_STATE_DIR/statusline.prev.json" "$STATE_DIR/statusline.prev.json"
fi
if [ -f "$LEGACY_STATE_DIR/discovery.json" ] && [ ! -e "$STATE_DIR/discovery.json" ]; then
  cp -p "$LEGACY_STATE_DIR/discovery.json" "$STATE_DIR/discovery.json"
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

# 4. Antigravity plugin registration (retire owned ooda plugin & obsolete plugins).
mkdir -p "$HOME/.gemini/config/plugins"
link "$PLUGIN_DIR" "$HOME/.gemini/config/plugins/agent-ooda" "plugin-agent-ooda"
retire_owned_legacy "plugin-ooda" "$HOME/.gemini/config/plugins/ooda"
retire_legacy "plugin-router-core" "$HOME/.gemini/config/plugins/router-core"
retire_legacy "plugin-boyd" "$HOME/.gemini/config/plugins/boyd"

# 5. Single CLI binary: ~/.local/bin/agent-ooda (retire owned legacy ooda/router shims safely).
mkdir -p "$HOME/.local/bin"
link "$OODA_BIN" "$HOME/.local/bin/agent-ooda" "bin-agent-ooda"
retire_owned_legacy "bin-ooda" "$HOME/.local/bin/ooda"
retire_owned_legacy "bin-router" "$HOME/.local/bin/router"
retire_legacy "bin-boyd" "$HOME/.local/bin/boyd"

# 6. Bubble-canopy statusLine HUD: symlink, then point settings.json statusLine at it.
mkdir -p "$AGY_DIR"
link "$PLUGIN_DIR/bin/agent-ooda-hud" "$AGY_DIR/statusline.py" "statusline.py"
SETTINGS_NOTE="$SETTINGS_JSON (statusLine -> ~/.gemini/antigravity-cli/statusline.py)"
WIRE_OUT=""
if [ "$NO_STATUSLINE" = "1" ]; then
  SETTINGS_NOTE="(skipped: AGENT_OODA_NO_STATUSLINE=1; $SETTINGS_JSON untouched)"
else
  WIRE_OUT="$("$OODA_BIN" __wire wire "$SETTINGS_JSON" "$STATE_DIR")"
fi

# 7. Retire legacy state directory and duplicate standalone skills after steps 1-6 succeed.
if [ "$LEGACY_STATE_DIR" != "$STATE_DIR" ]; then
  retire_legacy "state-ooda" "$LEGACY_STATE_DIR"
fi
retire_legacy "skill-agent-ooda" "$HOME/.agents/skills/agent-ooda"
retire_legacy "skill-router" "$HOME/.agents/skills/router"
retire_legacy "skill-ooda" "$HOME/.agents/skills/ooda"
mkdir -p "$HOME/.agents/skills"

ACTIVE_BIN="$(command -v agent-ooda 2>/dev/null || true)"
PATH_NOTE=""
if [ -n "$ACTIVE_BIN" ] && [ "$ACTIVE_BIN" != "$HOME/.local/bin/agent-ooda" ] && [ "$ACTIVE_BIN" != "$OODA_BIN" ]; then
  PATH_NOTE="  note        : 'agent-ooda' on PATH resolves to $ACTIVE_BIN; use ~/.local/bin/agent-ooda"
fi

cat <<EOF
agent-ooda install complete
  repo        : $REPO_ROOT
  quota       : $QUOTA_FILE (source: $QUOTA_SOURCE)
  plugin      : ~/.gemini/config/plugins/agent-ooda -> plugins/agent-ooda
  cli         : ~/.local/bin/agent-ooda -> plugins/agent-ooda/bin/agent-ooda
  statusLine  : ~/.gemini/antigravity-cli/statusline.py -> plugins/agent-ooda/bin/agent-ooda-hud
  settings    : $SETTINGS_NOTE
  skills.json : $SKILLS_JSON
  archived    : ${BACKUP_DIR:-(none displaced)}
EOF
[ -z "$WIRE_OUT" ] || printf '%s\n' "$WIRE_OUT" | sed 's/^/  wire        : /'
[ -n "$PATH_NOTE" ] && printf '%s\n' "$PATH_NOTE"
health_report
echo "Next: restart agy, then: agy plugin list && agent-ooda --version && agent-ooda --status && agent-ooda --usage"

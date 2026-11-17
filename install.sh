#!/usr/bin/env bash
# install.sh — wire the agy-router Antigravity plugin, CLI binary, and statusLine HUD into $HOME.
# Idempotent: safe to re-run. Migrates legacy agent-ooda/ooda/router installs cleanly, archiving
# displaced items under "$STATE_DIR/backups" AFTER quota state is migrated and validated.
# `./install.sh --uninstall [--purge]` reverses it (logic lives in uninstall.sh).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
PLUGIN_DIR="$REPO_ROOT/plugins/agy-router"
OODA_BIN="$PLUGIN_DIR/bin/agy-router"
STATE_BASE="${XDG_STATE_HOME:-$HOME/.local/state}"
STATE_DIR="$STATE_BASE/agy-router"
LEGACY_AGENT_OODA_STATE_DIR="$STATE_BASE/agent-ooda"
LEGACY_STATE_DIR="$STATE_BASE/ooda"
QUOTA_FILE="$STATE_DIR/quota.json"
DEFAULT_QUOTA="$PLUGIN_DIR/config/quota.default.json"
QUOTA_SOURCE="existing"
AGY_DIR="$HOME/.gemini/antigravity-cli"
SETTINGS_JSON="$AGY_DIR/settings.json"
NO_STATUSLINE="${AGY_ROUTER_NO_STATUSLINE:-${AGENT_OODA_NO_STATUSLINE:-${OODA_NO_STATUSLINE:-0}}}"
NO_BUILD="${AGY_ROUTER_NO_BUILD:-${AGENT_OODA_NO_BUILD:-${OODA_NO_BUILD:-0}}}"
RELEASE_BASE="${AGY_ROUTER_RELEASE_BASE:-${AGENT_OODA_RELEASE_BASE:-${OODA_RELEASE_BASE:-https://github.com/UberMetroid/agy-router/releases/latest/download}}}"

init_theme() {
  if [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != "dumb" ]; then
    C_RST=$'\033[0m'
    C_BOLD=$'\033[1;38;5;255m'
    C_CYAN=$'\033[1;38;5;45m'
    C_GREEN=$'\033[1;38;5;48m'
    C_AMBER=$'\033[1;38;5;214m'
    C_VIOLET=$'\033[1;38;5;177m'
    C_BLUE=$'\033[38;5;75m'
    C_TEAL=$'\033[38;5;49m'
    C_ORANGE=$'\033[1;38;5;208m'
    C_REDHOT=$'\033[1;38;5;196m'
    C_MUTED=$'\033[38;5;244m'
    C_RED=$'\033[1;38;5;203m'
  else
    C_RST="" C_BOLD="" C_CYAN="" C_GREEN="" C_AMBER="" C_VIOLET=""
    C_BLUE="" C_TEAL="" C_ORANGE="" C_REDHOT="" C_MUTED="" C_RED=""
  fi
}

print_banner() {
  printf '\n%s╭──────────────────────────────────────────────────────────────────────╮%s\n' "$C_MUTED" "$C_RST"
  printf '%s│%s  %s◈ AGY-ROUTER%s  %s·%s  %sMulti-Agent Fleet Router & Live Thermal HUD%s        %s│%s\n' \
    "$C_MUTED" "$C_RST" "$C_CYAN" "$C_RST" "$C_MUTED" "$C_RST" "$C_BOLD" "$C_RST" "$C_MUTED" "$C_RST"
  printf '%s│%s  %s◎ observe  ◈ orient  ◇ decide  ▸ act  ✦ verify  %s·%s  %sCGO_ENABLED=0%s    %s│%s\n' \
    "$C_MUTED" "$C_RST" "$C_MUTED" "$C_MUTED" "$C_RST" "$C_AMBER" "$C_RST" "$C_MUTED" "$C_RST"
  printf '%s╰──────────────────────────────────────────────────────────────────────╯%s\n\n' "$C_MUTED" "$C_RST"
}

run_spin() {
  local label="$1"
  shift
  local log rc=0 pid i=0
  log="$(mktemp)"
  if [ -t 1 ]; then
    local frames=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
    "$@" >"$log" 2>&1 &
    pid=$!
    while kill -0 "$pid" 2>/dev/null; do
      printf '\r\033[K  %s%s%s %s%s%s' "$C_CYAN" "${frames[$((i % 10))]}" "$C_RST" "$C_MUTED" "$label..." "$C_RST"
      i=$((i + 1))
      sleep 0.1 2>/dev/null || break
    done
    set +e
    wait "$pid"
    rc=$?
    set -e
    printf '\r\033[K'
  else
    set +e
    "$@" >"$log" 2>&1
    rc=$?
    set -e
  fi
  if [ "$rc" -ne 0 ]; then
    cat "$log" >&2 || true
  fi
  rm -f -- "$log"
  return "$rc"
}

init_theme

usage() {
  cat <<'EOF'
usage: ./install.sh [--uninstall [--purge]] [--help]

  (no options)         build (if go present) & install: link plugin, CLI, statusLine HUD; wire settings.json
  --uninstall          remove agy-router symlinks pointing into this repo and unwire statusLine
  --uninstall --purge  also delete ${XDG_STATE_HOME:-~/.local/state}/agy-router (quota state + backups)
  -h, --help           show this help

environment:
  AGY_ROUTER_NO_STATUSLINE=1  skip adding statusLine to ~/.gemini/antigravity-cli/settings.json
  AGY_ROUTER_NO_BUILD=1       use existing plugins/agy-router/bin/agy-router without rebuilding/downloading
  AGY_ROUTER_RELEASE_BASE     override base URL for prebuilt binaries + SHA256SUMS
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
    "$OODA_BIN" __setup check-state "$STATE_BASE" "$LEGACY_AGENT_OODA_STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1
    "$OODA_BIN" __setup check-state "$STATE_BASE" "$LEGACY_STATE_DIR" "$HOME" "$REPO_ROOT" || exit 1
  else
    case "$STATE_BASE" in /*) ;; *) echo "error: state base must be absolute: $STATE_BASE" >&2; exit 1 ;; esac
    case "$STATE_DIR" in /*) ;; *) echo "error: state dir must be absolute: $STATE_DIR" >&2; exit 1 ;; esac
    for chk in "$STATE_DIR" "$LEGACY_AGENT_OODA_STATE_DIR" "$LEGACY_STATE_DIR"; do
      case "$chk" in
        /|"$HOME"|"$HOME/"|"$REPO_ROOT"|"$REPO_ROOT"/*)
          echo "error: refusing unsafe state dir: $chk" >&2; exit 1 ;;
      esac
    done
  fi
  # shellcheck source=uninstall.sh
  . "$REPO_ROOT/uninstall.sh"
  agy_router_uninstall "$@"
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

build_go_binary() {
  cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OODA_BIN" ./cmd/agy-router
}

download_prebuilt_binary() {
  local base os arch asset
  base="${RELEASE_BASE%/}"
  case "$(uname -s)" in
    Linux) os="linux" ;;
    Darwin) os="darwin" ;;
    *) echo "error: unsupported OS '$(uname -s)' for prebuilt agy-router (install Go 1.22+)" >&2; return 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) echo "error: unsupported arch '$(uname -m)' for prebuilt agy-router (install Go 1.22+)" >&2; return 1 ;;
  esac
  command -v curl >/dev/null 2>&1 || {
    echo "error: curl required to download prebuilt agy-router binary (or install Go 1.22+)" >&2
    return 1
  }
  asset="agy-router-${os}-${arch}"
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
    run_spin "Downloading prebuilt $asset ($base)" \
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

if [ "${AGY_ROUTER_WEB_BOOTSTRAPPED:-0}" = "1" ]; then
  STEP_OFF=1
  STEP_TOT=6
else
  print_banner
  STEP_OFF=0
  STEP_TOT=5
fi

step_done() {
  local idx=$((STEP_OFF + $1))
  printf '  %s✓%s %s[%d/%d]%s %s\n' "$C_GREEN" "$C_RST" "$C_MUTED" "$idx" "$STEP_TOT" "$C_RST" "$2"
}

mkdir -p "$PLUGIN_DIR/bin"
BIN_ORIGIN="prebuilt/verified"
if [ "$NO_BUILD" != "1" ]; then
  if command -v go >/dev/null 2>&1 && [ -w "$PLUGIN_DIR/bin" ]; then
    run_spin "[$((STEP_OFF + 1))/${STEP_TOT}] Compiling static binary (CGO_ENABLED=0)" build_go_binary
    BIN_ORIGIN="built with go"
  else
    download_prebuilt_binary
    BIN_ORIGIN="release SHA-256"
  fi
fi
[ -x "$OODA_BIN" ] || { echo "error: executable not found at $OODA_BIN" >&2; exit 1; }
ln -sfn agy-router "$PLUGIN_DIR/bin/agy-router-hud"
ln -sfn agy-router "$PLUGIN_DIR/bin/agy-router-hook"

BIN_VER="$("$OODA_BIN" --version 2>/dev/null || echo "agy-router")"
BIN_HASH=""
if command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1; then
  BIN_HASH=" · sha256:$(sha256_file "$OODA_BIN" | awk '{print substr($1,1,12)}')"
fi
step_done 1 "${C_BOLD}${BIN_VER}${C_RST} ${C_MUTED}(${BIN_ORIGIN}${BIN_HASH})${C_RST}"

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

DATA_CLONE="${XDG_DATA_HOME:-$HOME/.local/share}/agy-router"
LEGACY_DATA_CLONE="${XDG_DATA_HOME:-$HOME/.local/share}/agent-ooda"

retire_owned_legacy() {
  local target
  [ -L "$2" ] || return 0
  target="$(readlink "$2" || true)"
  case "$target" in
    "$REPO_ROOT"|"$REPO_ROOT"/*|"$DATA_CLONE"|"$DATA_CLONE"/*|"$LEGACY_DATA_CLONE"|"$LEGACY_DATA_CLONE"/*|"$HOME/.agents/skills/router"|"$HOME/.agents/skills/router"/*)
      archive_path "$1" "$2" ;;
  esac
}

cli_present() {
  local cli="$1"
  command -v "$cli" >/dev/null 2>&1 && return 0
  [ -x "$HOME/.local/bin/$cli" ] && return 0
  case "$cli" in
    mcode) [ -x "$HOME/.minimax-code/bin/mcode" ] && return 0 ;;
    grok) [ -x "$HOME/.grok/bin/grok" ] && return 0 ;;
    devin) [ -x "$HOME/.local/share/devin/cli/_versions/current/bin/devin" ] && return 0 ;;
    vibe) [ -x "$HOME/.local/share/uv/tools/mistral-vibe/bin/vibe" ] && return 0 ;;
  esac
  return 1
}

"$OODA_BIN" __setup check-dirs "$STATE_DIR" "$HOME/.gemini/config/plugins" "$HOME/.local/bin" "$AGY_DIR" "$HOME/.agents/skills" || exit 1
"$OODA_BIN" __setup skills-check "$HOME/.gemini/config/skills.json"
if [ "$NO_STATUSLINE" != "1" ]; then
  "$OODA_BIN" __wire check "$SETTINGS_JSON" "$STATE_DIR"
fi

mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"

if [ ! -f "$QUOTA_FILE" ]; then
  QUOTA_SOURCE="$DEFAULT_QUOTA"
  for legacy in "$LEGACY_AGENT_OODA_STATE_DIR/quota.json" "$LEGACY_STATE_DIR/quota.json" "$HOME/.agents/skills/router/quota.json" "$STATE_BASE/boyd/quota.json"; do
    if valid_quota "$legacy"; then
      QUOTA_SOURCE="$legacy"
      break
    fi
  done
  cp -p "$QUOTA_SOURCE" "$QUOTA_FILE"
fi
for legdir in "$LEGACY_AGENT_OODA_STATE_DIR" "$LEGACY_STATE_DIR"; do
  for fname in statusline.prev.json discovery.json; do
    if [ -f "$legdir/$fname" ] && [ ! -e "$STATE_DIR/$fname" ]; then
      cp -p "$legdir/$fname" "$STATE_DIR/$fname"
    fi
  done
  if [ -d "$legdir/workspaces" ] && [ ! -e "$STATE_DIR/workspaces" ]; then
    cp -a "$legdir/workspaces" "$STATE_DIR/workspaces"
  fi
done
if ! valid_quota "$QUOTA_FILE"; then
  echo "error: $QUOTA_FILE is not a JSON object with 'providers'; legacy installs left untouched" >&2
  exit 1
fi
chmod 600 "$QUOTA_FILE"
step_done 2 "Quota state ready · source: ${C_CYAN}${QUOTA_SOURCE}${C_RST}"

SKILLS_JSON="$HOME/.gemini/config/skills.json"
mkdir -p "$HOME/.gemini/config"
"$OODA_BIN" __setup skills-update "$SKILLS_JSON"

mkdir -p "$HOME/.gemini/config/plugins"
link "$PLUGIN_DIR" "$HOME/.gemini/config/plugins/agy-router" "plugin-agy-router"
retire_owned_legacy "plugin-agent-ooda" "$HOME/.gemini/config/plugins/agent-ooda"
retire_owned_legacy "plugin-ooda" "$HOME/.gemini/config/plugins/ooda"
retire_legacy "plugin-router-core" "$HOME/.gemini/config/plugins/router-core"
retire_legacy "plugin-boyd" "$HOME/.gemini/config/plugins/boyd"

mkdir -p "$HOME/.local/bin"
link "$OODA_BIN" "$HOME/.local/bin/agy-router" "bin-agy-router"
retire_owned_legacy "bin-agent-ooda" "$HOME/.local/bin/agent-ooda"
retire_owned_legacy "bin-ooda" "$HOME/.local/bin/ooda"
retire_owned_legacy "bin-router" "$HOME/.local/bin/router"
retire_legacy "bin-boyd" "$HOME/.local/bin/boyd"
step_done 3 "Plugin & CLI linked · ~/.gemini/config/plugins/agy-router · ~/.local/bin/agy-router"

mkdir -p "$AGY_DIR"
link "$PLUGIN_DIR/bin/agy-router-hud" "$AGY_DIR/statusline.py" "statusline.py"
SETTINGS_NOTE="$SETTINGS_JSON (statusLine -> ~/.gemini/antigravity-cli/statusline.py)"
WIRE_OUT=""
if [ "$NO_STATUSLINE" = "1" ]; then
  SETTINGS_NOTE="(skipped: AGY_ROUTER_NO_STATUSLINE=1; $SETTINGS_JSON untouched)"
  step_done 4 "Live statusLine symlinked ${C_MUTED}(settings.json skipped)${C_RST}"
else
  WIRE_OUT="$("$OODA_BIN" __wire wire "$SETTINGS_JSON" "$STATE_DIR")"
  step_done 4 "Live thermal HUD wired · ~/.gemini/antigravity-cli/statusline.py"
fi

if [ "$LEGACY_AGENT_OODA_STATE_DIR" != "$STATE_DIR" ]; then
  retire_legacy "state-agent-ooda" "$LEGACY_AGENT_OODA_STATE_DIR"
fi
if [ "$LEGACY_STATE_DIR" != "$STATE_DIR" ]; then
  retire_legacy "state-ooda" "$LEGACY_STATE_DIR"
fi
retire_legacy "skill-agent-ooda" "$HOME/.agents/skills/agent-ooda"
retire_legacy "skill-router" "$HOME/.agents/skills/router"
retire_legacy "skill-ooda" "$HOME/.agents/skills/ooda"
mkdir -p "$HOME/.agents/skills"

fleet_pills=""
found_n=0
for cli in agy devin muse grok vibe mcode; do
  if cli_present "$cli"; then
    fleet_pills="${fleet_pills} ${C_GREEN}● ${cli}${C_RST}"
    found_n=$((found_n + 1))
  else
    fleet_pills="${fleet_pills} ${C_MUTED}○ ${cli}${C_RST}"
  fi
done
step_done 5 "Fleet readiness (${found_n}/6 CLIs):${fleet_pills}"

ACTIVE_BIN="$(command -v agy-router 2>/dev/null || true)"
PATH_NOTE=""
if [ -n "$ACTIVE_BIN" ] && [ "$ACTIVE_BIN" != "$HOME/.local/bin/agy-router" ] && [ "$ACTIVE_BIN" != "$OODA_BIN" ]; then
  PATH_NOTE="  note        : 'agy-router' on PATH resolves to $ACTIVE_BIN; use ~/.local/bin/agy-router"
fi

printf '\n%s┌──────────────────────────────────────────────────────────────────────┐%s\n' "$C_MUTED" "$C_RST"
printf '%s│%s  %s✦%s %sagy-router install complete%s\n' "$C_MUTED" "$C_RST" "$C_GREEN" "$C_RST" "$C_BOLD" "$C_RST"
cat <<EOF
  repo        : $REPO_ROOT
  quota       : $QUOTA_FILE (source: $QUOTA_SOURCE)
  plugin      : ~/.gemini/config/plugins/agy-router -> plugins/agy-router
  cli         : ~/.local/bin/agy-router -> plugins/agy-router/bin/agy-router
  statusLine  : ~/.gemini/antigravity-cli/statusline.py -> plugins/agy-router/bin/agy-router-hud
  settings    : $SETTINGS_NOTE
  skills.json : $SKILLS_JSON
  archived    : ${BACKUP_DIR:-(none displaced)}
EOF
[ -z "$WIRE_OUT" ] || printf '%s\n' "$WIRE_OUT" | sed 's/^/  wire        : /'
[ -n "$PATH_NOTE" ] && printf '%s\n' "$PATH_NOTE"
case ":$PATH:" in
  *":$HOME/.local/bin:"*) printf '  PATH        : %sok (~/.local/bin is on PATH)%s\n' "$C_GREEN" "$C_RST" ;;
  *) printf '  PATH        : %sadd export PATH="$HOME/.local/bin:$PATH" to your shell rc%s\n' "$C_AMBER" "$C_RST" ;;
esac
printf '  HUD preview : %sROUTER%s %s│%s %s◎ observe%s %s◈ orient%s %s▸ act%s %s✦ verify%s %s·%s %s~18%s %s~45%s %s~88%s %s~132%s %s~195 tok/s%s\n' \
  "$C_BOLD" "$C_RST" "$C_MUTED" "$C_RST" \
  "$C_CYAN" "$C_RST" "$C_AMBER" "$C_RST" "$C_GREEN" "$C_RST" "$C_VIOLET" "$C_RST" \
  "$C_MUTED" "$C_RST" "$C_BLUE" "$C_RST" "$C_TEAL" "$C_RST" "$C_AMBER" "$C_RST" "$C_ORANGE" "$C_RST" "$C_REDHOT" "$C_RST"
printf '%s└──────────────────────────────────────────────────────────────────────┘%s\n' "$C_MUTED" "$C_RST"

printf '\n  %sNext:%s restart %sagy%s so the plugin, %s/lane%s, %s/ooda%s, %s/harden%s & live HUD load, then run:\n' \
  "$C_BOLD" "$C_RST" "$C_CYAN" "$C_RST" "$C_CYAN" "$C_RST" "$C_AMBER" "$C_RST" "$C_VIOLET" "$C_RST"
printf '    %sagy plugin list && agy-router --version && agy-router --status && agy-router --select%s\n\n' "$C_MUTED" "$C_RST"

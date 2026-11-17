#!/usr/bin/env bash
# install.sh (web installer) — one-line install, update, or uninstall for UberMetroid/agy-router.
# Usage:
#   curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash
#   curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash -s -- --uninstall [--purge]
set -euo pipefail

case "${1:-}" in
  -h|--help)
    cat <<'EOF'
usage: curl -fsSL https://ubermetroid.github.io/agy-router/install.sh | bash [-s -- OPTIONS]

  (no options)         clone/update agy-router into ~/.local/share/agy-router and run ./install.sh
  --uninstall          remove agy-router symlinks and unwire statusLine via installed checkout
  --uninstall --purge  also delete ~/.local/state/agy-router and ~/.local/share/agy-router

environment:
  AGY_ROUTER_HOME             override checkout path (default ${XDG_DATA_HOME:-~/.local/share}/agy-router)
  AGY_ROUTER_REPO_URL         override git repository URL
  AGY_ROUTER_NO_STATUSLINE=1  skip adding statusLine to ~/.gemini/antigravity-cli/settings.json
  AGY_ROUTER_RELEASE_BASE     override prebuilt binary + SHA256SUMS base URL
EOF
    exit 0 ;;
esac

REPO_URL="${AGY_ROUTER_REPO_URL:-${AGENT_OODA_REPO_URL:-https://github.com/UberMetroid/agy-router.git}}"
INSTALL_HOME="${AGY_ROUTER_HOME:-${AGENT_OODA_HOME:-${XDG_DATA_HOME:-$HOME/.local/share}/agy-router}}"

init_theme() {
  if [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != "dumb" ]; then
    C_RST=$'\033[0m'
    C_BOLD=$'\033[1;38;5;255m'
    C_CYAN=$'\033[1;38;5;45m'
    C_GREEN=$'\033[1;38;5;48m'
    C_AMBER=$'\033[1;38;5;214m'
    C_VIOLET=$'\033[1;38;5;177m'
    C_MUTED=$'\033[38;5;244m'
    C_RED=$'\033[1;38;5;203m'
  else
    C_RST="" C_BOLD="" C_CYAN="" C_GREEN="" C_AMBER="" C_VIOLET="" C_MUTED="" C_RED=""
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

clean_home="${HOME%/}"
clean_dest="${INSTALL_HOME%/}"
case "$INSTALL_HOME" in
  /*) ;;
  *) echo "error: AGY_ROUTER_HOME must be absolute: $INSTALL_HOME" >&2; exit 1 ;;
esac
if [ -z "$clean_dest" ] || [ "$clean_dest" = "$clean_home" ]; then
  echo "error: refusing unsafe AGY_ROUTER_HOME: $INSTALL_HOME" >&2
  exit 1
fi

check_clean_clone() {
  [ -d "$INSTALL_HOME/.git" ] || return 0
  if [ -n "$(git -C "$INSTALL_HOME" status --porcelain)" ]; then
    echo "error: local modifications in $INSTALL_HOME; commit or stash before $1" >&2
    exit 1
  fi
}

is_shallow_clone() {
  [ "$(git -C "$INSTALL_HOME" rev-parse --is-shallow-repository 2>/dev/null || echo false)" = "true" ]
}

if [ "${1:-}" = "--uninstall" ]; then
  shift
  purge=0
  for arg in ${@+"$@"}; do
    case "$arg" in
      --purge) purge=1 ;;
    esac
  done
  if [ "$purge" -eq 1 ] && [ -d "$INSTALL_HOME/.git" ]; then
    check_clean_clone purging
    refspec="+refs/heads/main:refs/remotes/origin/main"
    unpushed="$(git -C "$INSTALL_HOME" rev-list --count --branches --not --remotes 2>/dev/null || echo 0)"
    if [ "$unpushed" -gt 0 ] && is_shallow_clone; then
      git -C "$INSTALL_HOME" fetch -q --unshallow "$REPO_URL" "$refspec" >/dev/null 2>&1 || true
      unpushed="$(git -C "$INSTALL_HOME" rev-list --count --branches --not --remotes 2>/dev/null || echo 0)"
    fi
    if [ "$unpushed" -gt 0 ]; then
      echo "error: $INSTALL_HOME has $unpushed unpushed local commit(s); refusing --purge" >&2
      exit 1
    fi
  fi
  if [ -x "$INSTALL_HOME/install.sh" ]; then
    "$INSTALL_HOME/install.sh" --uninstall ${@+"$@"}
  else
    echo "note: $INSTALL_HOME/install.sh not found; nothing to uninstall"
  fi
  if [ "$purge" -eq 1 ] && [ -d "$INSTALL_HOME/.git" ]; then
    rm -rf -- "$INSTALL_HOME"
    printf '  %s✦ purged%s      : checkout %s\n' "$C_AMBER" "$C_RST" "$INSTALL_HOME"
  fi
  exit 0
fi

command -v git >/dev/null 2>&1 || { echo "error: git is required" >&2; exit 1; }

print_banner

if [ -d "$INSTALL_HOME/.git" ]; then
  check_clean_clone updating
  git -C "$INSTALL_HOME" remote set-url origin "$REPO_URL" >/dev/null 2>&1 || true
  if is_shallow_clone; then
    run_spin "[1/6] Unshallowing & syncing $REPO_URL" \
      git -C "$INSTALL_HOME" fetch -q --unshallow "$REPO_URL" +refs/heads/main:refs/remotes/origin/main
  else
    run_spin "[1/6] Syncing $REPO_URL" \
      git -C "$INSTALL_HOME" fetch -q "$REPO_URL" +refs/heads/main:refs/remotes/origin/main
  fi
  ahead="$(git -C "$INSTALL_HOME" rev-list --count FETCH_HEAD..HEAD 2>/dev/null || echo 0)"
  if [ "$ahead" -gt 0 ]; then
    echo "error: $INSTALL_HOME has $ahead unpushed local commit(s); push or reset before updating" >&2
    exit 1
  fi
  git -C "$INSTALL_HOME" checkout -q -B main FETCH_HEAD
elif [ -e "$INSTALL_HOME" ]; then
  echo "error: $INSTALL_HOME exists and is not a git checkout of agy-router" >&2
  exit 1
else
  mkdir -p "$(dirname "$INSTALL_HOME")"
  run_spin "[1/6] Cloning UberMetroid/agy-router into $INSTALL_HOME" \
    git clone -q "$REPO_URL" "$INSTALL_HOME"
fi

short_sha="$(git -C "$INSTALL_HOME" rev-parse --short HEAD 2>/dev/null || echo "main")"
printf '  %s✓%s %s[1/6]%s Repository ready %s·%s %smain @ %s%s\n' \
  "$C_GREEN" "$C_RST" "$C_MUTED" "$C_RST" "$C_MUTED" "$C_RST" "$C_BOLD" "$short_sha" "$C_RST"

export AGY_ROUTER_WEB_BOOTSTRAPPED=1
exec "$INSTALL_HOME/install.sh" ${@+"$@"}

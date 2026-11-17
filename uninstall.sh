#!/usr/bin/env bash
# uninstall.sh — thin wrapper: `./uninstall.sh [--purge]` runs `./install.sh --uninstall [--purge]`.
# install.sh sources this file for removal logic; executing it directly re-enters install.sh.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  exec "$(cd "$(dirname "$0")" && pwd)/install.sh" --uninstall "$@"
fi

owned_link() {
  local base="${2:-$PLUGIN_DIR}"
  if [ -x "$OODA_BIN" ]; then
    "$OODA_BIN" __setup owned-link "$1" "$base"
    return $?
  fi
  [ -L "$1" ] || return 1
  local target
  target="$(readlink "$1" || true)"
  case "$target" in
    "$base"|"$base"/*) return 0 ;;
    *) return 1 ;;
  esac
}

check_link_writable() {
  local p="$1" b="${2:-$PLUGIN_DIR}"
  if owned_link "$p" "$b" && { [ ! -w "$(dirname "$p")" ] || [ ! -x "$(dirname "$p")" ]; }; then
    echo "error: $(dirname "$p") is not writable (or searchable); cannot remove $p (nothing changed)" >&2
    return 1
  fi
}

agy_router_uninstall() {
  local purge=0 arg path removed=0 unwire=0 hud="$AGY_DIR/statusline.py" wire_out
  local data_clone="${XDG_DATA_HOME:-$HOME/.local/share}/agy-router"
  local legacy_clone="${XDG_DATA_HOME:-$HOME/.local/share}/agent-ooda"
  local links=("$HOME/.gemini/config/plugins/agy-router" "$HOME/.local/bin/agy-router" "$hud")
  local legacy_links=("$HOME/.gemini/config/plugins/agent-ooda" "$HOME/.local/bin/agent-ooda" \
    "$HOME/.gemini/config/plugins/ooda" "$HOME/.local/bin/ooda" "$HOME/.local/bin/router")
  for arg in "$@"; do
    case "$arg" in
      --purge) purge=1 ;;
      *) echo "error: unknown uninstall option: $arg (see --help)" >&2; return 2 ;;
    esac
  done
  if { [ ! -e "$hud" ] && [ ! -L "$hud" ]; } || owned_link "$hud" "$PLUGIN_DIR" || owned_link "$hud" "$REPO_ROOT"; then
    unwire=1
  fi
  if [ "$unwire" -eq 1 ]; then
    if [ ! -f "$SETTINGS_JSON" ]; then
      unwire=0
    elif [ -x "$OODA_BIN" ]; then
      "$OODA_BIN" __wire check "$SETTINGS_JSON" "$STATE_DIR" || return 1
    fi
  fi
  for path in "${links[@]}"; do
    check_link_writable "$path" "$PLUGIN_DIR" || return 1
  done
  for path in "${legacy_links[@]}"; do
    check_link_writable "$path" "$REPO_ROOT" || return 1
    check_link_writable "$path" "$data_clone" || return 1
    check_link_writable "$path" "$legacy_clone" || return 1
  done

  printf '\n%s◈ agy-router uninstall%s\n' "${C_AMBER:-}" "${C_RST:-}"
  if [ "$unwire" -eq 1 ] && [ -x "$OODA_BIN" ]; then
    wire_out="$("$OODA_BIN" __wire unwire "$SETTINGS_JSON" "$STATE_DIR")" || {
      echo "error: failed to unwire statusLine in $SETTINGS_JSON; no symlinks removed" >&2
      return 1
    }
    printf '%s\n' "$wire_out" | sed 's/^/  settings    : /'
  elif [ "$unwire" -eq 1 ]; then
    echo "  settings    : left $SETTINGS_JSON untouched (agy-router binary absent to rewrite JSON)"
  else
    echo "  settings    : statusLine left as-is (statusline.py is not ours)"
  fi

  for path in "${links[@]}"; do
    if owned_link "$path" "$PLUGIN_DIR"; then
      rm -f "$path"
      printf '  %s✓ removed%s     : %s\n' "${C_GREEN:-}" "${C_RST:-}" "$path"
      removed=$((removed + 1))
    elif [ -e "$path" ] || [ -L "$path" ]; then
      printf '  %s○ left alone%s  : %s (not a symlink into %s)\n' "${C_MUTED:-}" "${C_RST:-}" "$path" "$PLUGIN_DIR"
    fi
  done
  for path in "${legacy_links[@]}"; do
    if owned_link "$path" "$REPO_ROOT" || owned_link "$path" "$data_clone" || owned_link "$path" "$legacy_clone"; then
      rm -f "$path"
      printf '  %s✓ removed%s     : %s (legacy)\n' "${C_GREEN:-}" "${C_RST:-}" "$path"
      removed=$((removed + 1))
    fi
  done
  [ "$removed" -gt 0 ] || printf '  %s○ symlinks%s    : nothing to remove\n' "${C_MUTED:-}" "${C_RST:-}"
  echo "  skills.json : ~/.agents/skills entry kept (shared skills dir)"

  if [ "$purge" -eq 1 ]; then
    if [ -d "$STATE_DIR" ]; then
      rm -rf "$STATE_DIR"
      printf '  %s✦ purged%s      : %s (quota + backups)\n' "${C_AMBER:-}" "${C_RST:-}" "$STATE_DIR"
    else
      printf '  %s○ state%s       : %s already absent\n' "${C_MUTED:-}" "${C_RST:-}" "$STATE_DIR"
    fi
    for leg in "$LEGACY_AGENT_OODA_STATE_DIR" "$LEGACY_STATE_DIR"; do
      if [ -d "$leg" ] && [ "$leg" != "$STATE_DIR" ]; then
        rm -rf "$leg"
        printf '  %s✦ purged%s      : legacy %s\n' "${C_AMBER:-}" "${C_RST:-}" "$leg"
      fi
    done
  elif [ -d "$STATE_DIR" ]; then
    echo "  state       : kept $STATE_DIR (re-run with --purge to delete)"
    if [ -d "$STATE_DIR/backups" ]; then
      echo "  backups     : $STATE_DIR/backups holds archived legacy items; restore manually if needed"
    fi
  fi
  printf 'Next: restart %sagy%s so the plugin and statusLine unload\n' "${C_CYAN:-}" "${C_RST:-}"
}

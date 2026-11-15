#!/usr/bin/env bash
# uninstall.sh — thin wrapper: `./uninstall.sh [--purge]` runs `./install.sh --uninstall [--purge]`.
# install.sh sources this file for the removal logic (ooda_uninstall); executing it directly
# just re-enters install.sh so both entrypoints share one preflight path.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  exec "$(cd "$(dirname "$0")" && pwd)/install.sh" --uninstall "$@"
fi

owned_link() {
  if [ -x "$OODA_BIN" ]; then
    "$OODA_BIN" __setup owned-link "$1" "$PLUGIN_DIR"
    return $?
  fi
  [ -L "$1" ] || return 1
  local target
  target="$(readlink "$1" || true)"
  case "$target" in
    "$PLUGIN_DIR"|"$PLUGIN_DIR"/*) return 0 ;;
    *) return 1 ;;
  esac
}

ooda_uninstall() {
  local purge=0 arg path removed=0 unwire=0 hud="$AGY_DIR/statusline.py" wire_out
  local links=("$HOME/.gemini/config/plugins/ooda" "$HOME/.local/bin/ooda" "$HOME/.local/bin/router" "$hud")
  for arg in "$@"; do
    case "$arg" in
      --purge) purge=1 ;;
      *) echo "error: unknown uninstall option: $arg (see --help)" >&2; return 2 ;;
    esac
  done
  if { [ ! -e "$hud" ] && [ ! -L "$hud" ]; } || owned_link "$hud"; then
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
    if owned_link "$path" && { [ ! -w "$(dirname "$path")" ] || [ ! -x "$(dirname "$path")" ]; }; then
      echo "error: $(dirname "$path") is not writable (or searchable); cannot remove $path (nothing changed)" >&2
      return 1
    fi
  done

  echo "ooda uninstall"
  if [ "$unwire" -eq 1 ] && [ -x "$OODA_BIN" ]; then
    wire_out="$("$OODA_BIN" __wire unwire "$SETTINGS_JSON" "$STATE_DIR")" || {
      echo "error: failed to unwire statusLine in $SETTINGS_JSON; no symlinks removed" >&2
      return 1
    }
    printf '%s\n' "$wire_out" | sed 's/^/  settings    : /'
  elif [ "$unwire" -eq 1 ]; then
    echo "  settings    : left $SETTINGS_JSON untouched (ooda binary absent to rewrite JSON)"
  else
    echo "  settings    : statusLine left as-is (statusline.py is not ours)"
  fi

  for path in "${links[@]}"; do
    if owned_link "$path"; then
      rm -f "$path"
      echo "  removed     : $path"
      removed=$((removed + 1))
    elif [ -e "$path" ] || [ -L "$path" ]; then
      echo "  left alone  : $path (not a symlink into $PLUGIN_DIR)"
    fi
  done
  [ "$removed" -gt 0 ] || echo "  symlinks    : nothing to remove"
  echo "  skills.json : ~/.agents/skills entry kept (shared skills dir)"

  if [ "$purge" -eq 1 ]; then
    if [ -d "$STATE_DIR" ]; then
      rm -rf "$STATE_DIR"
      echo "  state       : purged $STATE_DIR (quota + backups)"
    else
      echo "  state       : $STATE_DIR already absent"
    fi
  elif [ -d "$STATE_DIR" ]; then
    echo "  state       : kept $STATE_DIR (re-run with --purge to delete)"
    if [ -d "$STATE_DIR/backups" ]; then
      echo "  backups     : $STATE_DIR/backups holds archived legacy items; restore manually if needed"
    fi
  fi
  echo "Next: restart agy so the plugin and statusLine unload"
}

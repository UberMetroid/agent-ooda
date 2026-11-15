#!/usr/bin/env bash
# uninstall.sh — thin wrapper: `./uninstall.sh [--purge]` runs `./install.sh --uninstall [--purge]`.
# install.sh sources this file for the removal logic (ooda_uninstall); executing it directly
# just re-enters install.sh so both entrypoints share one preflight path.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  exec "$(cd "$(dirname "$0")" && pwd)/install.sh" --uninstall "$@"
fi

# owned_link PATH -> exit 0 when PATH is a symlink whose real target lies inside $PLUGIN_DIR.
owned_link() {
  [ -L "$1" ] || return 1
  python3 - "$1" "$PLUGIN_DIR" <<'PY'
import os
import sys

link = os.path.abspath(sys.argv[1])
target = os.path.realpath(os.path.join(os.path.dirname(link), os.readlink(link)))
root = os.path.realpath(sys.argv[2])
sys.exit(0 if target == root or target.startswith(root + os.sep) else 1)
PY
}

# ooda_uninstall [--purge] -> preflight-first, in this order: (1) preflight settings.json,
# statusline.prev.json, and write+search access to every repo-owned symlink's parent dir; (2) unwire
# statusLine; (3) only then `rm -f` owned symlinks (settings.json never points at a missing HUD);
# (4) optionally purge state. Any preflight failure aborts before anything is mutated.
ooda_uninstall() {
  local purge=0 arg path removed=0 unwire=0 hud="$AGY_DIR/statusline.py" wire_out
  local links=("$HOME/.gemini/config/plugins/ooda" "$HOME/.local/bin/ooda" "$HOME/.local/bin/router" "$hud")
  for arg in "$@"; do
    case "$arg" in
      --purge) purge=1 ;;
      *) echo "error: unknown uninstall option: $arg (see --help)" >&2; return 2 ;;
    esac
  done
  # Decide ownership first: only unwire when statusline.py is ours or already absent.
  if { [ ! -e "$hud" ] && [ ! -L "$hud" ]; } || owned_link "$hud"; then
    unwire=1
  fi
  # Preflight settings.json + statusline.prev.json so corruption aborts before anything is removed.
  if [ "$unwire" -eq 1 ]; then
    python3 "$WIRE" check "$SETTINGS_JSON" "$STATE_DIR" || return 1
  fi
  # Preflight: every owned symlink we will unlink must sit in a writable/searchable directory.
  for path in "${links[@]}"; do
    if owned_link "$path" && { [ ! -w "$(dirname "$path")" ] || [ ! -x "$(dirname "$path")" ]; }; then
      echo "error: $(dirname "$path") is not writable (or searchable); cannot remove $path (nothing changed)" >&2
      return 1
    fi
  done

  echo "ooda uninstall"
  if [ "$unwire" -eq 1 ]; then
    wire_out="$(python3 "$WIRE" unwire "$SETTINGS_JSON" "$STATE_DIR")" || {
      echo "error: failed to unwire statusLine in $SETTINGS_JSON; no symlinks removed" >&2
      return 1
    }
    printf '%s\n' "$wire_out" | sed 's/^/  settings    : /'
  else
    echo "  settings    : statusLine left as-is (statusline.py is not ours)"
  fi

  # Unwire succeeded (or was skipped); now unlink the repo-owned symlinks.
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

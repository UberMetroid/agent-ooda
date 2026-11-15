#!/usr/bin/env python3
"""Wire or unwire the ooda Bubble-canopy HUD in Antigravity's settings.json statusLine block.

Usage: python3 wire_settings.py check|wire|unwire <settings.json> [<state_dir>]
"""

import json
import os
import shlex
import sys

AGY_DIR = "~/.gemini/antigravity-cli"
PREV_NAME = "statusline.prev.json"


def _fail(msg):
    sys.stderr.write(f"error: {msg}\n")
    sys.exit(1)


def load_settings(path, settings=True):
    """Return parsed JSON at path; exit 1 on non-regular-file, malformed, or invalid content.

    settings=True: missing/empty -> {} and the top level must be an object.
    settings=False (statusline.prev.json): the file must exist and parse as any JSON value.
    """
    if os.path.lexists(path) and not os.path.isfile(path):
        _fail(f"{path} is not a regular file; refusing to overwrite")
    if settings and (not os.path.isfile(path) or os.path.getsize(path) == 0):
        return {}
    try:
        with open(path, encoding="utf-8") as fh:
            data = json.load(fh)
    except (OSError, ValueError) as exc:
        _fail(f"malformed JSON in {path}; refusing to overwrite: {exc}")
    if settings and not isinstance(data, dict):
        _fail(f"{path} must contain a JSON object; refusing to overwrite")
    return data


def write_json(path, data):
    """Atomically write data as indent=2 JSON with a trailing newline, keeping any file mode."""
    parent = os.path.dirname(os.path.abspath(path))
    os.makedirs(parent, exist_ok=True)
    tmp = os.path.join(parent, f".{os.path.basename(path)}.tmp.{os.getpid()}")
    try:
        with open(tmp, "w", encoding="utf-8") as fh:
            json.dump(data, fh, indent=2)
            fh.write("\n")
        if os.path.exists(path):
            os.chmod(tmp, os.stat(path).st_mode & 0o7777)
        os.replace(tmp, path)
    except BaseException:
        if os.path.lexists(tmp):
            os.unlink(tmp)
        raise


def our_targets(settings_path):
    """Return (command string to write, set of normalized statusline.py paths we own)."""
    sibling = os.path.join(os.path.dirname(os.path.abspath(settings_path)), "statusline.py")
    canonical = os.path.normpath(os.path.expanduser(f"{AGY_DIR}/statusline.py"))
    command = f"{AGY_DIR}/statusline.py" if os.path.normpath(sibling) == canonical else sibling
    return command, {canonical, os.path.normpath(sibling)}


def is_ours(block, owned):
    """True only when a statusLine command *executes* one of our statusline.py paths.

    Accepts `[env [-S] [KEY=VAL ...]] <owned>` or `[env ...] python* [flags] <owned>`; a command
    that merely mentions statusline.py as an argument (e.g. `echo .../statusline.py`) is foreign.
    """
    if not isinstance(block, dict) or not isinstance(block.get("command"), str):
        return False
    try:
        toks = shlex.split(block["command"])
    except ValueError:
        toks = block["command"].split()
    mine = lambda tok: os.path.normpath(os.path.expandvars(os.path.expanduser(tok))) in owned
    if toks and os.path.basename(toks[0]) == "env":
        toks = toks[1:]
        while toks and (toks[0].startswith("-S") or ("=" in toks[0] and not toks[0].startswith("-"))):
            head, toks = toks[0], toks[1:]
            if head == "-S" and toks:
                toks = toks[0].split() + toks[1:]
            elif head.startswith("-S"):
                toks = head[2:].split() + toks
    if not toks:
        return False
    if mine(toks[0]):
        return True
    if not os.path.basename(toks[0]).startswith("python"):
        return False
    rest = iter(toks[1:])
    for tok in rest:
        if tok in ("-c", "-m", "-"):
            return False
        if tok in ("-W", "-X"):
            next(rest, None)
        elif not tok.startswith("-"):
            return mine(tok)
    return False


def wire(settings_path, state_dir):
    """Install our statusLine block; prev always holds the newest foreign block (older archived)."""
    data = load_settings(settings_path)
    command, owned = our_targets(settings_path)
    current = data.get("statusLine")
    if is_ours(current, owned):
        print(f"statusLine already wired in {settings_path}")
        return
    if "statusLine" in data:  # an explicit `null` is archived too, so unwire restores it verbatim
        prev = os.path.join(state_dir, PREV_NAME)
        had_prev = os.path.lexists(prev)
        older = load_settings(prev, settings=False) if had_prev else None
        if not had_prev or older != current:
            if had_prev:
                backups = os.path.join(state_dir, "backups")
                os.makedirs(backups, mode=0o700, exist_ok=True)
                archive = os.path.join(backups, f"statusline-prev-{os.getpid()}.json")
                write_json(archive, older)
                print(f"archived older statusLine to {archive}")
            write_json(prev, current)
            print(f"saved previous statusLine to {prev}")
    data["statusLine"] = {"type": "command", "command": command, "enabled": True,
                          "padding": 0, "stack_with_default": True}
    write_json(settings_path, data)
    print(f"statusLine wired in {settings_path}")


def unwire(settings_path, state_dir):
    """Remove our statusLine block and restore the archived foreign block when one exists."""
    if not os.path.isfile(settings_path):
        print(f"{settings_path} not present; nothing to unwire")
        return
    data = load_settings(settings_path)
    _, owned = our_targets(settings_path)
    if not is_ours(data.get("statusLine"), owned):
        print(f"no ooda statusLine block in {settings_path}; nothing to unwire")
        return
    del data["statusLine"]
    prev = os.path.join(state_dir, PREV_NAME)
    restored = os.path.lexists(prev)
    if restored:
        data["statusLine"] = load_settings(prev, settings=False)
    write_json(settings_path, data)
    print(f"statusLine removed from {settings_path}")
    if restored:
        os.remove(prev)
        print(f"statusLine restored from {prev}")


def main(argv):
    """CLI entrypoint: check|wire|unwire <settings.json> [<state_dir>]."""
    if len(argv) < 3 or argv[1] not in ("check", "wire", "unwire"):
        sys.stderr.write("usage: wire_settings.py check|wire|unwire <settings.json> [<state_dir>]\n")
        return 2
    action, settings_path = argv[1], os.path.expanduser(argv[2])
    if action == "check":
        # Preflight: JSON validity, writability (file + deepest existing dir), and prev integrity.
        load_settings(settings_path)
        probe = os.path.dirname(os.path.abspath(settings_path))
        while not os.path.isdir(probe) and os.path.dirname(probe) != probe:
            probe = os.path.dirname(probe)
        if os.path.exists(settings_path) and not os.access(settings_path, os.W_OK):
            _fail(f"{settings_path} is not writable")
        if not os.access(probe, os.W_OK | os.X_OK):
            _fail(f"directory {probe} is not writable; cannot update {settings_path}")
        if len(argv) > 3:
            prev = os.path.join(os.path.expanduser(argv[3]), PREV_NAME)
            if os.path.lexists(prev):
                load_settings(prev, settings=False)
        return 0
    if len(argv) < 4:
        sys.stderr.write(f"usage: wire_settings.py {action} <settings.json> <state_dir>\n")
        return 2
    state_dir = os.path.expanduser(argv[3])
    (wire if action == "wire" else unwire)(settings_path, state_dir)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

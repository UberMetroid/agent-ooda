"""ANSI 256-color palette, spinner frames, and width-safe text formatting for the HUD."""

import re

from fleet import CORE_HIDDEN_SKILLS

ANSI_RE = re.compile(r"\x1b\[[0-9;]*m")
ANSI_TOKEN_RE = re.compile(r"(\x1b\[[0-9;]*m)")
IGNORED_SKILLS = frozenset(CORE_HIDDEN_SKILLS | {"ooda", "ooda:ooda", "ooda:router"})

C_IDLE_ICON = "\033[38;5;245m"
C_IDLE_TEXT = "\033[1;38;5;252m"
C_ORIENT = "\033[1;38;5;214m"
C_OBSERVE = "\033[1;38;5;45m"
C_OBSERVE_MODEL = "\033[38;5;117m"
C_ACT = "\033[1;38;5;48m"
C_ACT_MODEL = "\033[38;5;156m"
C_VERIFY = "\033[1;38;5;177m"
C_VERIFY_MODEL = "\033[38;5;219m"
C_FLEET_FULL = "\033[38;5;78m"
C_FLEET_PARTIAL = "\033[38;5;221m"
C_FLEET_LOW = "\033[1;38;5;203m"
C_MUTED = "\033[38;5;244m"
RESET = "\033[0m"

BRAILLE_FRAMES = ("⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏")
ORIENT_FRAMES = ("◐", "◓", "◑", "◒")


def colorize(text, code, use_color=True):
    """Wrap text in an ANSI color code (plain str when color is disabled)."""
    return f"{code}{text}{RESET}" if use_color else str(text)


def visible_len(text):
    """Return printable width of text with ANSI escape sequences removed."""
    return len(ANSI_RE.sub("", text))


def truncate_ansi(text, max_width):
    """Truncate ANSI-styled text safely to at most max_width visible characters."""
    if visible_len(text) <= max_width:
        return text
    if max_width <= 1:
        return "." * max(0, max_width)
    budget = max_width - 1
    out = []
    used = 0
    for token in ANSI_TOKEN_RE.split(text):
        if not token:
            continue
        if ANSI_TOKEN_RE.fullmatch(token):
            out.append(token)
            continue
        rem = budget - used
        if rem <= 0:
            break
        if len(token) <= rem:
            out.append(token)
            used += len(token)
        else:
            out.append(token[:rem])
            used += rem
            break
    out.append("…")
    if "\033[" in text:
        out.append(RESET)
    return "".join(out)


def format_cd(delta_s):
    """Format a non-negative seconds delta as a compact countdown (1d2h, 3h05m, 7m, 9s)."""
    secs = max(0, int(delta_s))
    hours, rem = divmod(secs, 3600)
    mins = rem // 60
    if hours >= 24:
        days, rem_h = divmod(hours, 24)
        return f"{days}d{rem_h}h"
    if hours > 0:
        return f"{hours}h{mins:02d}m"
    if mins > 0:
        return f"{mins}m"
    return f"{secs}s"


def short_model(model_name):
    """Return a compact display name for long worker model identifiers."""
    m = str(model_name or "-")
    if "fusion-claude-" in m:
        return "devin-fusion"
    if m == "minimax/MiniMax-M3.1-Flash":
        return "MiniMax-M3.1-Flash"
    if m == "mistral-large-4-0-3":
        return "mistral-large-4"
    return m

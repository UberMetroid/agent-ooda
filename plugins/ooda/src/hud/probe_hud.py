"""Read-only probes feeding the HUD: live worker markers, session skills, fleet summary."""

from datetime import datetime, timezone
import errno
import json
import os
import time

from fleet import parse_iso_utc, quota_file_path, safe_id

from .ansi_format import IGNORED_SKILLS, format_cd

SESSION_TTL_SECS = 43200
DEFAULT_SUMMARY = (0, 0, None, "agy-opus", "high", "devin-fast", "max")
LEGACY_CODE_CHAIN = ("agy-opus", "devin-fusion", "devin-fast", "devin-pro", "muse", "mcode")
FAST_CHAIN = ("devin-fast", "mcode", "devin-pro", "muse")


def pid_alive(pid):
    """Return True when pid refers to a live (or permission-protected) process."""
    if pid <= 0:
        return False
    try:
        os.kill(pid, 0)
        return True
    except PermissionError:
        return True
    except ProcessLookupError:
        return False
    except OSError as e:
        return e.errno == errno.EPERM


def load_active_workers(hud_base):
    """Load live worker markers from hud_base/active, pruning corrupt or dead entries."""
    active_dir = hud_base / "active"
    if not active_dir.is_dir():
        return []
    workers = []
    for path in active_dir.glob("*.json"):
        try:
            entry = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            try:
                path.unlink()
            except OSError:
                pass
            continue
        if not isinstance(entry, dict):
            continue
        try:
            pid = int(entry.get("pid") or 0)
        except (TypeError, ValueError):
            pid = 0
        if not pid_alive(pid):
            try:
                path.unlink()
            except OSError:
                pass
            continue
        workers.append(entry)
    workers.sort(key=lambda w: float(w.get("started_at") or 0.0))
    return workers


def extract_stdin_skills(data):
    """Return skill names advertised in the agy statusLine stdin payload."""
    raw = data.get("skills")
    out = []
    if isinstance(raw, list):
        for item in raw:
            if isinstance(item, str) and item.strip():
                out.append(item.strip())
            elif isinstance(item, dict) and str(item.get("name") or "").strip():
                out.append(str(item["name"]).strip())
    return out


def collect_skills(hud_base, data, workers):
    """Merge stdin, recorded-session, and live-worker skills (deduped, ignore-filtered)."""
    sources = [extract_stdin_skills(data)]
    conv_id = str(data.get("conversation_id") or data.get("conversationId") or "").strip()
    if not conv_id:
        cur_file = hud_base / "current_session.json"
        if cur_file.is_file():
            try:
                cur = json.loads(cur_file.read_text(encoding="utf-8"))
                if isinstance(cur, dict):
                    upd = float(cur.get("updated_at") or 0.0)
                    if (time.time() - upd) <= SESSION_TTL_SECS:
                        conv_id = str(cur.get("conversation_id") or "").strip()
            except (OSError, json.JSONDecodeError, TypeError, ValueError):
                pass
    if conv_id:
        sess_file = hud_base / "sessions" / f"{safe_id(conv_id)}.json"
        if sess_file.is_file():
            try:
                sess = json.loads(sess_file.read_text(encoding="utf-8"))
                if isinstance(sess, dict):
                    sources.append(sess.get("skills"))
            except (OSError, json.JSONDecodeError):
                pass
    sources.extend(w.get("skills") for w in workers)
    merged = []
    for items in sources:
        for s in items or []:
            name = str(s or "").strip()
            if name == "agent-router":
                name = "router"
            if name and name not in IGNORED_SKILLS and name not in merged:
                merged.append(name)
    return merged


def load_fleet_summary():
    """Return (active, total, wake_cd, code_lane, code_eff, fast_lane, fast_eff) from quota."""
    qfile = quota_file_path()
    if not qfile.is_file():
        return DEFAULT_SUMMARY
    try:
        qdata = json.loads(qfile.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return DEFAULT_SUMMARY
    providers = qdata.get("providers") or {}
    now_utc = datetime.now(timezone.utc)
    active_set = set()
    soonest_wake_s = None
    for name, info in providers.items():
        if not isinstance(info, dict):
            continue
        status = info.get("status")
        cd_ts = info.get("cooldown_until") or info.get("next_reset_at")
        cd_dt = parse_iso_utc(cd_ts) if cd_ts else None
        if status == "active":
            active_set.add(name)
        elif status == "cooldown" and cd_dt is not None:
            if cd_dt <= now_utc and not bool(info.get("manual_disable", False)):
                active_set.add(name)
            else:
                rem = (cd_dt - now_utc).total_seconds()
                if rem > 0 and (soonest_wake_s is None or rem < soonest_wake_s):
                    soonest_wake_s = rem
    chain = qdata.get("default_fallback_chain") or list(LEGACY_CODE_CHAIN)
    code_lane = next((c for c in chain if c in active_set), "none")
    if code_lane == "none" and active_set:
        code_lane = next(iter(active_set))
    code_eff = str((providers.get(code_lane) or {}).get("effort") or "high")
    fast_lane = next((c for c in FAST_CHAIN if c in active_set), "none")
    fast_eff = str((providers.get(fast_lane) or {}).get("effort") or "max")
    wake_cd = format_cd(soonest_wake_s) if soonest_wake_s is not None else None
    return len(active_set), len(providers), wake_cd, code_lane, code_eff, fast_lane, fast_eff

"""Per-CLI headless worker argv/env construction and single bounded execution."""

import os
import shutil
import subprocess
import time

from fleet import (
    DEFAULT_AGY_OPUS_MODEL,
    GROK_EFFORT_MAP,
    MCODE_EFFORT_MAP,
    MUSE_VALID_EFFORTS,
    normalize_model_for_provider,
    router_depth,
    run,
    safe_id,
)
from worktree import safe_worktree_files

from .telemetry import parse_agy_output, parse_vibe_export

DEVIN_LANES = ("devin-fusion", "devin-pro", "devin-fast")
PROMPT_FILE_LANES = DEVIN_LANES + ("grok", "mistral", "muse")
OUTPUT_TAIL = 20000


def _agy_cmd(cli, model, effort, ctx):
    """agy-opus: strip ANTIGRAVITY_* env, accept-edits JSON headless run scoped to worktree."""
    for key in [k for k in ctx["env"] if k.startswith("ANTIGRAVITY_")]:
        ctx["env"].pop(key, None)
    agy_model = model or DEFAULT_AGY_OPUS_MODEL
    eff_key = str(effort or "high").lower()
    if eff_key in ("low", "medium") and agy_model.endswith("-high"):
        agy_model = agy_model[:-5] + f"-{eff_key}"
    ctx["meta"]["parse_agy_json"] = True
    return [cli, "-p", ctx["prompt"], "--model", agy_model, "--mode", "accept-edits",
            "--dangerously-skip-permissions", "--disable-slash-commands",
            "--output-format", "json", "--add-dir", str(ctx["worktree"])]


def _devin_cmd(cli, model, effort, ctx):
    """devin-*: prompt-file driven; inherits env untouched (never overrides MINIMAX_DATA_DIR)."""
    perm_mode = "auto" if ctx["read_only"] else "smart"
    return [cli, "--model", model, "--permission-mode", perm_mode,
            "--respect-workspace-trust", "false", "--prompt-file", str(ctx["prompt_file"]), "-p"]


def _mcode_cmd(cli, model, effort, ctx):
    """mcode: exec in worktree with scoped --file args; env untouched (MINIMAX_DATA_DIR kept)."""
    mcode_effort = MCODE_EFFORT_MAP.get(str(effort or "high").lower(), "high")
    file_args = []
    for path_str in safe_worktree_files(ctx["files"], ctx["worktree"]):
        file_args += ["--file", path_str]
    perm = "smart" if ctx["read_only"] else "full"
    return [cli, "exec", "--cwd", str(ctx["worktree"]), "--model", model, "--effort", mcode_effort,
            "--permission", perm, "--prompt-mode", "coding", "--output-format", "text",
            ] + file_args + [ctx["prompt"]]


def _grok_cmd(cli, model, effort, ctx):
    """grok: bypassPermissions prompt-file run with mapped reasoning effort."""
    grok_effort = GROK_EFFORT_MAP.get(str(effort or "high").lower(), "high")
    return [cli, "--cwd", str(ctx["worktree"]), "--model", model, "--reasoning-effort", grok_effort,
            "--permission-mode", "bypassPermissions", "--prompt-file", str(ctx["prompt_file"])]


def _mistral_cmd(cli, model, effort, ctx):
    """mistral vibe: trusted workdir run exporting usage JSON into a fresh slot directory."""
    export_dir = ctx["slot"] / f"vibe-export-{ctx['safe_tid']}"
    if export_dir.exists():
        shutil.rmtree(export_dir, ignore_errors=True)
    cmd = [cli, "--workdir", str(ctx["worktree"]), "--trust",
           "--smart-approve" if ctx["read_only"] else "--auto-approve",
           "--output", "text", "--output-dir", str(export_dir),
           "--prompt-file", str(ctx["prompt_file"])]
    if model and model != "default":
        cmd += ["--model", model]
    ctx["meta"]["vibe_export_dir"] = export_dir
    return cmd


def _muse_cmd(cli, model, effort, ctx):
    """muse: yolo exec in worktree with validated reasoning effort."""
    muse_effort = str(effort or "high").lower()
    if muse_effort not in MUSE_VALID_EFFORTS:
        muse_effort = "high"
    cmd = [cli, "exec", "--workspace", str(ctx["worktree"]), "--yolo",
           "--reasoning-effort", muse_effort, "--prompt-file", str(ctx["prompt_file"])]
    if model and model != "default":
        cmd += ["--model", model]
    return cmd


_BUILDERS = {"agy-opus": _agy_cmd, "mcode": _mcode_cmd, "grok": _grok_cmd,
             "mistral": _mistral_cmd, "muse": _muse_cmd}
_BUILDERS.update({lane: _devin_cmd for lane in DEVIN_LANES})


def build_worker_command(provider_name, cli_path, model, effort, prompt, files,
                         worktree, slot, task_id, read_only=False):
    """Build (argv, env, meta) for the chosen headless agent CLI (ROUTER_DEPTH incremented)."""
    builder = _BUILDERS.get(provider_name)
    if builder is None:
        raise ValueError(f"unsupported provider {provider_name!r}")
    env = dict(os.environ)
    env["ROUTER_DEPTH"] = str(router_depth() + 1)
    safe_tid = safe_id(task_id)
    ctx = {"env": env, "meta": {}, "prompt": prompt, "files": files, "worktree": worktree,
           "slot": slot, "safe_tid": safe_tid, "read_only": read_only,
           "prompt_file": slot / f"prompt-{safe_tid}.md"}
    if provider_name in PROMPT_FILE_LANES:
        ctx["prompt_file"].write_text(prompt, encoding="utf-8")
        os.chmod(ctx["prompt_file"], 0o600)
    resolved_model = normalize_model_for_provider(provider_name, model)
    cmd = builder(cli_path, resolved_model, effort, ctx)
    return cmd, env, ctx["meta"]


def execute_once(provider_name, info, cli_path, model, effort, prompt, files,
                 worktree, slot, task_id, timeout, read_only=False):
    """Run one worker invocation and return (normalized result dict, duration_s)."""
    cmd, env, meta = build_worker_command(provider_name, cli_path, model, effort, prompt, files,
                                          worktree, slot, task_id, read_only=read_only)
    export_dir = meta.get("vibe_export_dir")
    start = time.time()
    try:
        p = run(cmd, cwd=worktree, env=env, timeout=timeout)
    except subprocess.TimeoutExpired as e:
        raw = getattr(e, "stdout", None)
        out = raw.decode(errors="replace") if isinstance(raw, bytes) else (raw or "")
        return {
            "exit_code": -1,
            "output": (out + f"\n<TIMEOUT after {timeout}s>")[-OUTPUT_TAIL:],
            "timed_out": True,
            "export_info": parse_vibe_export(export_dir) if export_dir else None,
        }, time.time() - start
    raw_out = p.stdout or ""
    if meta.get("parse_agy_json"):
        clean_out, eff_code, export_info = parse_agy_output(raw_out, p.returncode)
    else:
        clean_out, eff_code = raw_out, p.returncode
        export_info = parse_vibe_export(export_dir) if export_dir else None
    return {
        "exit_code": eff_code,
        "output": clean_out[-OUTPUT_TAIL:],
        "timed_out": False,
        "export_info": export_info,
    }, time.time() - start

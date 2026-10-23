#!/usr/bin/env python3
"""Route Codex plugin events through the established Windows hook launcher.

Codex plugin installs live below a cache path that can contain spaces.  The
Windows hook runner has changed how it quotes command lines across releases, so
the descriptor starts one quote-free PowerShell command and this dispatcher
selects the existing event handler from the JSON payload.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

import codex_hook_adapter as adapter


ROUTES: dict[str, tuple[str, ...]] = {
    "SessionStart": ("run_sh.py", "session-start.sh"),
    "UserPromptSubmit": ("run_sh.py", "user-prompt-submit.sh"),
    "PreToolUse": ("pre_tool_use.py",),
    "PermissionRequest": ("permission_request.py",),
    "PostToolUse": ("post_tool_use.py",),
    "PreCompact": ("run_sh.py", "pre-compact.sh"),
    "Stop": ("stop.py",),
}


def local_directory(value: str | Path) -> Path:
    path = Path(value)
    if not path.is_absolute() or str(path).startswith(('\\\\', '//')):
        raise ValueError('project binding requires an absolute local directory')
    if adapter._has_reparse_component(path) or not path.is_dir():
        raise ValueError('project binding directory is missing or linked')
    return path.resolve(strict=True)


def binding_file(cwd: Path, session_id: str) -> Path:
    key = adapter.state_key('codex', cwd, session_id)
    if not key:
        raise ValueError('project binding requires a native session ID')
    return cwd / '.planning' / 'bindings' / (key + '.json')


def validate_target(target: Path) -> None:
    if not any((target / name).is_file() and not adapter._has_reparse_component(target / name)
               for name in ('task_plan.md', '.workflow/state.json')):
        raise ValueError('bound project has no existing plan or workflow state')


def bind_project(cwd: Path, session_id: str, target: Path) -> Path:
    """Persist a selector only; never copy or initialize a project plan."""
    cwd, target = local_directory(cwd), local_directory(target)
    validate_target(target)
    filename = binding_file(cwd, session_id)
    for directory in (filename.parent.parent, filename.parent):
        directory.mkdir(exist_ok=True)
        local_directory(directory)
    if filename.exists() and adapter._is_reparse_or_link(filename):
        raise ValueError('refusing linked project binding')
    record = {'schema': 1, 'cwd': adapter.canonical_project_identity(cwd),
              'session_id': session_id, 'project': str(target)}
    # Atomic replacement preserves the previous selector on an interrupted write.
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(mode='w', encoding='utf-8', dir=filename.parent,
                                         delete=False) as output:
            temporary = Path(output.name)
            json.dump(record, output, ensure_ascii=False)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, filename)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
    return filename


def project_payload(data: dict) -> dict | None:
    """Resolve an explicit per-session selection before every lifecycle route."""
    cwd = adapter.cwd_from_payload(data)
    sid = adapter.session_id_from_payload(data)
    if not sid:
        return data if os.environ.get('PWF_PLAN_ROOT') else None
    filename = binding_file(cwd, sid)
    try:
        filename.lstat()
    except FileNotFoundError:
        return data if os.environ.get('PWF_PLAN_ROOT') else None
    try:
        if adapter._has_reparse_component(filename) or filename.stat().st_size > 8192:
            raise ValueError('unsafe project binding')
        record = json.loads(filename.read_text(encoding='utf-8'))
        if (record.get('schema') != 1 or record.get('session_id') != sid or
                record.get('cwd') != adapter.canonical_project_identity(cwd)):
            raise ValueError('project binding identity mismatch')
        target = local_directory(record['project'])
        validate_target(target)
        pin = os.environ.get('PWF_PLAN_ROOT')
        if pin and local_directory(pin) != target:
            raise ValueError('project binding conflicts with PWF_PLAN_ROOT')
        return {**data, 'cwd': str(target)}
    except (OSError, KeyError, TypeError, AttributeError, ValueError) as exc:
        raise ValueError('invalid project binding; nothing injected; repair the session selector') from exc


def main() -> int:
    payload = sys.stdin.buffer.read()
    try:
        data = json.loads(payload.decode("utf-8-sig"))
        event = data.get("hook_event_name")
    except (UnicodeDecodeError, json.JSONDecodeError, AttributeError):
        return 0

    route = ROUTES.get(event)
    if route is None:
        return 0

    if os.environ.get('PLANNING_DISABLED') == '1':
        return 0
    try:
        data = project_payload(data)
        if data is None:
            return 0  # An unbound chat must not inherit the host's unrelated plan.
        root = adapter.effective_plan_root(adapter.cwd_from_payload(data))
        if root is None:
            raise ValueError('invalid PWF_PLAN_ROOT; nothing injected')
        data = {**data, 'cwd': str(root.resolve(strict=True))}
    except (ValueError, OSError, RuntimeError) as exc:
        print(json.dumps({'systemMessage': '[planning-with-files] ' + str(exc)}))
        return 0
    payload = json.dumps(data).encode('utf-8')

    hooks_dir = Path(__file__).resolve().parent
    # Structured projects own their state; legacy PWF must not rewrite generated views.
    cwd = Path(data.get("cwd") or ".").resolve()
    if event == 'PostToolUse':
        return 0  # Record decisions/results, not reminders after ordinary tools.
    if event == 'PreToolUse':
        from context_frame import read_optional_regular_bytes
        mode = read_optional_regular_bytes(root / '.mode', max_source_bytes=256)
        if mode is None or not set(mode.decode('ascii').split()) & {'gate', 'autonomous'}:
            return 0  # CURRENT is already supplied by prompt/restore hooks.
    state_root = next((p for p in (cwd, *cwd.parents)
                       if (p / ".workflow/state.json").is_file()), None)
    if state_root is not None:
        message = ("Structured evidence workflow: " + str(state_root) +
                   ". Read .workflow/state.json through workflow.py check/next; "
                   "task_plan.md and progress.md are generated views. "
                   "If view_issues is nonempty or recovery_blocked is true, reconcile evidence before resuming. "
                   "Do not edit them, initialize another PWF plan, or replay uncertain side effects. "
                   "Recover actual application state before resuming. Hooks do not certify completion.")
        if event in ("SessionStart", "UserPromptSubmit", "PreToolUse", "PreCompact"):
            print(json.dumps({"hookSpecificOutput": {"hookEventName": event,
                                                      "additionalContext": message}}))
        else:
            print(json.dumps({"systemMessage": message}))
        return 0
    result = subprocess.run(
        [sys.executable, str(hooks_dir / route[0]), *route[1:]],
        input=payload,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
        cwd=str(cwd),
    )
    sys.stdout.buffer.write(result.stdout)
    sys.stderr.buffer.write(result.stderr)
    return result.returncode


if __name__ == "__main__":
    if len(sys.argv) > 1:
        import argparse
        parser = argparse.ArgumentParser(description='Bind this native session to an existing project.')
        parser.add_argument('--bind-project', required=True, type=Path)
        parser.add_argument('--cwd', required=True, type=Path)
        parser.add_argument('--session-id', required=True)
        args = parser.parse_args()
        print(bind_project(args.cwd, args.session_id, args.bind_project))
    else:
        raise SystemExit(main())

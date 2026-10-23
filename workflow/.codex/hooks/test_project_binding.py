"""Offline dispatch tests: never read an unrelated cwd plan after explicit binding."""
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import subprocess
import sys
import unittest
from unittest.mock import patch

import codex_hook_adapter as adapter
import plugin_dispatch as dispatch


class ProjectBindingTests(unittest.TestCase):
    def test_fresh_hook_process_injects_bound_plan_not_host_plan(self):
        with tempfile.TemporaryDirectory() as temp:
            host, target = Path(temp) / 'host', Path(temp) / 'target'
            host.mkdir(); target.mkdir()
            (host / 'task_plan.md').write_text('# Wrong host project\nOLD_PROJECT_SENTINEL', encoding='utf-8')
            (target / 'task_plan.md').write_text('# Correct project\nCURRENT_PROJECT_SENTINEL', encoding='utf-8')
            dispatch.bind_project(host, 'thread-1', target)
            env = {k: v for k, v in os.environ.items() if not k.startswith(('PWF_', 'PLAN_ID', 'PLANNING_'))}
            payload = {'cwd': str(host), 'session_id': 'thread-1', 'hook_event_name': 'UserPromptSubmit'}
            result = subprocess.run([sys.executable, dispatch.__file__], input=json.dumps(payload),
                                    text=True, encoding='utf-8', capture_output=True, env=env,
                                    timeout=20, cwd=host)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('CURRENT_PROJECT_SENTINEL', result.stdout)
            self.assertNotIn('OLD_PROJECT_SENTINEL', result.stdout)

    def test_binding_routes_all_events_without_affecting_other_sessions(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True):
            host, target = Path(temp) / 'host', Path(temp) / 'target'
            host.mkdir(); target.mkdir()
            (host / 'task_plan.md').write_text('OLD', encoding='utf-8')
            (target / 'task_plan.md').write_text('CURRENT', encoding='utf-8')
            dispatch.bind_project(host, 'thread-1', target)
            for event in dispatch.ROUTES:
                payload = {'cwd': str(host), 'session_id': 'thread-1', 'hook_event_name': event}
                self.assertEqual(dispatch.project_payload(payload)['cwd'], str(target.resolve()))
            other = {'cwd': str(host), 'session_id': 'thread-2'}
            self.assertIsNone(dispatch.project_payload(other))
            (target / 'task_plan.md').unlink()
            with self.assertRaises(ValueError):
                dispatch.project_payload(payload)

    def test_unbound_process_injects_nothing_and_ordinary_tools_do_not_remind(self):
        with tempfile.TemporaryDirectory() as temp:
            host = Path(temp)
            (host / 'task_plan.md').write_text('UNRELATED_PLAN_SENTINEL', encoding='utf-8')
            env = {k:v for k,v in os.environ.items() if not k.startswith(('PWF_', 'PLAN_ID', 'PLANNING_'))}
            def run(event):
                return subprocess.run([sys.executable, '-B', dispatch.__file__],
                    input=json.dumps({'cwd':str(host), 'session_id':'s', 'hook_event_name':event}),
                    text=True, encoding='utf-8', capture_output=True, env=env, timeout=20)
            self.assertEqual(run('UserPromptSubmit').stdout, '')
            dispatch.bind_project(host, 's', host)
            for event in ('PreToolUse', 'PostToolUse'):
                result = run(event)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, '')

    def test_invalid_binding_never_falls_back_and_does_not_use_other_identity(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True):
            host, target = Path(temp) / 'host', Path(temp) / 'target'
            host.mkdir(); target.mkdir()
            (target / 'task_plan.md').write_text('CURRENT', encoding='utf-8')
            filename = dispatch.bind_project(host, 'thread-1', target)
            record = json.loads(filename.read_text(encoding='utf-8'))
            record['session_id'] = 'thread-2'
            filename.write_text(json.dumps(record), encoding='utf-8')
            payload = {'cwd': str(host), 'session_id': 'thread-1'}
            with self.assertRaises(ValueError):
                dispatch.project_payload(payload)
            filename.write_text('{broken', encoding='utf-8')
            with self.assertRaises(ValueError):
                dispatch.project_payload(payload)

    def test_structured_project_and_explicit_pin_conflict(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True):
            host, target = Path(temp) / 'host', Path(temp) / 'target'
            host.mkdir(); target.mkdir(); (target / '.workflow').mkdir()
            (target / '.workflow/state.json').write_text('{}', encoding='utf-8')
            dispatch.bind_project(host, 'thread-1', target)
            with patch.dict(os.environ, {'PWF_PLAN_ROOT': str(host)}):
                with self.assertRaises(ValueError):
                    dispatch.project_payload({'cwd': str(host), 'session_id': 'thread-1'})

    def test_dispatch_executes_child_from_target_and_reports_invalid_binding(self):
        with tempfile.TemporaryDirectory() as temp, patch.dict(os.environ, {}, clear=True):
            host, target = Path(temp) / 'host', Path(temp) / 'target'
            host.mkdir(); target.mkdir()
            (target / 'task_plan.md').write_text('CURRENT', encoding='utf-8')
            filename = dispatch.bind_project(host, 'thread-1', target)
            payload = {'cwd': str(host), 'session_id': 'thread-1', 'hook_event_name': 'UserPromptSubmit'}
            class Input:
                buffer = io.BytesIO(json.dumps(payload).encode())
            class Output:
                buffer = io.BytesIO()
            with patch.object(dispatch.sys, 'stdin', Input()), patch.object(dispatch.sys, 'stdout', Output()), \
                    patch.object(dispatch.subprocess, 'run') as run:
                run.return_value.stdout = b''; run.return_value.stderr = b''; run.return_value.returncode = 0
                dispatch.main()
                self.assertEqual(run.call_args.kwargs['cwd'], str(target.resolve()))
                self.assertEqual(json.loads(run.call_args.kwargs['input'])['cwd'], str(target.resolve()))
            filename.write_text('{}', encoding='utf-8')
            Input.buffer = io.BytesIO(json.dumps(payload).encode())
            output = io.StringIO()
            with patch.object(dispatch.sys, 'stdin', Input()), contextlib.redirect_stdout(output), \
                    patch.object(dispatch.subprocess, 'run') as run:
                dispatch.main()
                run.assert_not_called()
            self.assertIn('nothing injected', output.getvalue())


if __name__ == '__main__':
    unittest.main()

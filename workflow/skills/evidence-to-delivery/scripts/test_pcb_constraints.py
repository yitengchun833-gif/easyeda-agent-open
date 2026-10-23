"""Workflow integration tests; native invocation is mocked, no EDA writes."""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import workflow as w
import pcb_constraints as pcb

IR = {'version': 'pcb-constraint/v0', 'unit': 'mm', 'target': {'projectUuid': 'p', 'documentUuid': 'pcb'},
      'constraints': [{'type': 'track_width', 'target': {'kind': 'net', 'name': 'GND'}, 'min': .254, 'preferred': .508, 'max': .762}]}
CHECKS = {'version': 'pcb-professional-checks/v0', 'unit': 'mm', 'target': IR['target'],
          'checks': [{'id': 'pin', 'type': 'pin_role', 'role': 'fixture ground', 'evidenceRef': 'S',
                      'pin': {'designator': 'U1', 'padNumber': '1'}, 'expectedNet': 'GND'}]}


class PcbPreparation(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.state = {'schema': 1, 'generation': 0, 'goal': 'module prep', 'sources': {}, 'plan': {},
                      'pending': {}, 'changes': [], 'retired': [], 'reviews': {}, 'results': [], 'context': {}}
        self.state = w.apply(self.root, self.state, {'op': 'source', 'source': {'id': 'S', 'kind': 'decision', 'text': 'fixture design', 'locator': 'fixture'}})
        self.item = {'description': 'module layout/routing', 'acceptance': 'test readback', 'domain': 'pcb', 'sources': ['S'],
                     'depends_on': [], 'objects': ['GND'], 'required_checks': ['execution_rules', 'rule_application', 'pcb_professional_checks'],
                     'pcb_constraint_ir': copy.deepcopy(IR), 'pcb_checks': copy.deepcopy(CHECKS)}
        self.state = w.apply(self.root, self.state, {'op': 'propose', 'id': 'C', 'reason': 'fixture', 'delta': [{'id': 'M', 'action': 'ADDED', 'item': self.item}]})
        self.state = w.apply(self.root, self.state, {'op': 'approve', 'id': 'C', 'confirmation': 'fixture, no EDA authorization'})

    def native(self, cmd, **kwargs):
        self.last_command = cmd
        dry_run = '--dry-run' in cmd
        self.assertNotIn('--apply', cmd)
        ir_file = Path(cmd[cmd.index('--ir') + 1]);out = Path(cmd[cmd.index('--output') + 1])
        evidence = {'dryRun': dry_run, 'review': '--review' in cmd, 'ir': w.read(ir_file), 'status': self.native_status,
                    'receipts': [{'ok': True}] * (1 if dry_run else 3 if '--review' in cmd else 2),
                    'preview': self.preview, 'identity': {'uuid': 'pcb', 'parentProjectUuid': 'p', 'documentType': 3}}
        checks_file = Path(cmd[cmd.index('--checks') + 1])
        evidence['professionalSpec'] = {'value': w.read(checks_file), 'sha256': w.file_hash(checks_file)}
        if not dry_run and hasattr(self, 'native_apply_result'):
            evidence['plan'] = {'status': 'ready'}
            evidence['result'] = dict(self.native_apply_result, target=IR['target'])
        if '--review' in cmd:
            after = {'lines': [], 'vias': [], 'pads': [], 'source': 'fixture'}
            evidence['result']['after'] = after
            evidence['reviewAfter'] = {'identity': evidence['identity'], 'before': copy.deepcopy(after)}
            evidence.update(geometryStatus=self.geometry_status, snapshotStable=True,
                            geometry={'summary': {'rightAngles': int(self.geometry_status == 'fail')}, 'limitations': ['fixture only']},
                            professional={'status': getattr(self, 'professional_status', 'pass'), 'checks': []})
        w.write(out, evidence)
        return type('Response', (), {'stdout': json.dumps({'status': self.native_status, 'evidence': str(out), 'preview': self.preview})})()

    def prepare(self, status='ready'):
        self.native_status = status
        self.preview = {'rulesToCreate': [{'name': 'CCv0_track'}], 'bindings': [{'net': 'GND'}], 'conflicts': [], 'unsupported': [], 'unknown': []}
        with patch.object(pcb.subprocess, 'run', self.native):
            self.state = w.apply(self.root, self.state, {'op': 'pcb_dry_run', 'item': 'M', 'revision': 1, 'window': 'window'})
        return self.state['results'][-1]

    def test_prepare_generates_ir_and_review_with_partial_not_execution(self):
        r = self.prepare()
        self.assertTrue(r['preparation_only']);self.assertEqual(r['status'], 'partial')
        self.assertEqual(w.read(r['pcb_constraint_dry_run']['ir_file']), IR)
        view = w.view_contents(self.root, self.state)['task_plan.md']
        for text in ['CCv0_track', 'GND', '拟建规则', '不支持约束', '未 apply']:
            self.assertIn(text, view)
        self.assertNotIn('M', w.status(self.root, self.state)['recovery'])

    def test_nested_release_uses_sibling_open_compiler(self):
        script = self.root / 'release/workflow/skills/evidence-to-delivery/scripts/pcb_constraints.py'
        compiler = self.root / 'release/tools/pcb_constraint_apply.mjs'
        compiler.parent.mkdir(parents=True)
        compiler.write_text('// fixture', encoding='utf-8')
        with patch.object(pcb, '__file__', str(script)):
            self.prepare()
        self.assertEqual(Path(self.last_command[1]), compiler.resolve())

    def test_conflict_and_unsupported_stay_blocked(self):
        for status in ['partial', 'fail', 'unknown', 'unsupported_in_constraint_v0']:
            self.assertEqual(self.prepare(status)['status'], 'blocked')

    def test_partial_source_candidate_is_preserved_and_cannot_start_layout(self):
        self.native_status = 'partial'
        self.preview = {'rulesToCreate': [{'name': 'CCv0_preview_track', 'dryRunOnly': True,
                         'unspecifiedFields': ['preferred', 'max'], 'retainedNativeValues': [{'field': 'defaultValue', 'value': .254}],
                         'changedCells': [{'field': 'tables.1.content.0.0', 'objects': ['Track', 'Track'], 'before': .1524, 'after': .1778}]}],
                        'unresolved': [{'index': 0, 'type': 'track_width', 'reason': 'source unspecified'}], 'candidateOnly': True}
        op = {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'in_progress', 'note': 'fixture start', 'window': 'w'}
        with patch.object(w, 'check_evidence', return_value=[]), patch.object(pcb.subprocess, 'run', self.native):
            state = w.apply(self.root, copy.deepcopy(self.state), op)
        result = state['results'][-1]
        self.assertEqual(result['status'], 'blocked');self.assertEqual(result['pcb_constraint_apply']['status'], 'partial')
        self.assertTrue(result['preparation_only'])
        view = w.view_contents(self.root, state)['task_plan.md']
        for text in ['preferred', 'max', '不是来源要求', 'CCv0_preview_track', 'source unspecified', 'Track', 'tables.1.content.0.0']:
            self.assertIn(text, view)

    def test_revision_and_window_required(self):
        with self.assertRaisesRegex(ValueError, 'old revision'):
            w.apply(self.root, self.state, {'op': 'pcb_dry_run', 'item': 'M', 'revision': 0, 'window': 'w'})
        with self.assertRaisesRegex(ValueError, 'window'):
            w.apply(self.root, self.state, {'op': 'pcb_dry_run', 'item': 'M', 'revision': 1})

    def test_module_ir_mandatory_for_new_pcb_proposals(self):
        del self.item['pcb_constraint_ir']
        with self.assertRaisesRegex(ValueError, 'IR'):
            w.apply(self.root, self.state, {'op': 'propose', 'id': 'C2', 'reason': 'fixture', 'delta': [{'id': 'M2', 'action': 'ADDED', 'item': self.item}]})

    def test_start_automatically_invokes_apply_and_blocks_conflict(self):
        self.native_status = 'fail';self.preview = {'conflicts': [{'reason': 'existing rule'}]}
        op = {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'in_progress', 'note': 'start requested', 'window': 'w'}
        with patch.object(w, 'check_evidence', return_value=[]), patch.object(pcb.subprocess, 'run', self.native):
            state = w.apply(self.root, copy.deepcopy(self.state), op)
        r = state['results'][-1]
        self.assertEqual(r['status'], 'blocked');self.assertEqual(r['requested_status'], 'in_progress')
        self.assertTrue(r['preparation_only']);self.assertFalse(r['pcb_constraint_apply']['rules_applied'])

    def test_start_ready_preserves_existing_execution_gate_and_adds_receipt(self):
        self.native_status = 'pass';self.preview = {}
        self.native_apply_result = {'status': 'pass', 'configurationReadback': True, 'netRulesReadback': True,
                                    'operations': [{'label': 'overwriteNetRules', 'return': True},
                                                   {'label': 'pcb_Drc.check(true,false,true)', 'return': []}],
                                    'drc': [], 'boardStatus': 'pass'}
        op = {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'in_progress', 'note': 'start requested', 'window': 'w'}
        with self.assertRaisesRegex(ValueError, 'execution_rules'):
            w.apply(self.root, copy.deepcopy(self.state), op)
        with patch.object(w, 'check_evidence', return_value=[]), patch.object(pcb.subprocess, 'run', self.native):
            state = w.apply(self.root, copy.deepcopy(self.state), op)
        self.assertEqual(state['results'][-1]['status'], 'in_progress')
        self.assertTrue(state['results'][-1]['pcb_constraint_apply']['rules_applied'])

    def test_pcb_apply_operation_records_actual_helper_receipt(self):
        self.native_status = 'pass';self.preview = {}
        self.native_apply_result = {'status': 'pass', 'configurationReadback': True, 'netRulesReadback': True,
                                    'operations': [{'label': 'overwriteNetRules', 'return': True},
                                                   {'label': 'pcb_Drc.check(true,false,true)', 'return': []}],
                                    'drc': [], 'boardStatus': 'pass'}
        with patch.object(pcb.subprocess, 'run', self.native):
            state = w.apply(self.root, copy.deepcopy(self.state),
                            {'op': 'pcb_apply', 'item': 'M', 'revision': 1, 'window': 'window'})
        r = state['results'][-1]
        self.assertEqual(r['op'], 'pcb_apply');self.assertEqual(r['status'], 'partial')
        self.assertTrue(r['pcb_constraint_apply']['rules_applied'])

    def test_verified_cannot_bypass_missing_or_failing_pcb_apply(self):
        external = self.root / 'external.json';w.write(external, {'fixture': True})
        verified = {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'verified', 'note': 'external passed',
                    'artifact': 'fixture', 'checks_passed': True, 'saved': True, 'evidence': [str(external)]}
        with patch.object(w, 'check_evidence', return_value=[]), self.assertRaisesRegex(ValueError, 'PCB verified requires'):
            w.apply(self.root, copy.deepcopy(self.state), verified)
        self.native_status = 'fail';self.preview = {}
        self.native_apply_result = {'status': 'fail', 'configurationReadback': True, 'netRulesReadback': True,
                                    'operations': [{'label': 'overwriteNetRules', 'return': True},
                                                   {'label': 'pcb_Drc.check(true,false,true)', 'return': [{'errorType': 'clearance'}]}],
                                    'drc': [{'errorType': 'clearance'}], 'boardStatus': 'fail'}
        with patch.object(pcb.subprocess, 'run', self.native):
            applied = w.apply(self.root, copy.deepcopy(self.state),
                              {'op': 'pcb_apply', 'item': 'M', 'revision': 1, 'window': 'window'})
        with patch.object(w, 'check_evidence', return_value=[]), self.assertRaisesRegex(ValueError, 'PCB verified requires'):
            w.apply(self.root, applied, verified)

    def test_failed_native_invocation_is_unknown_and_saved(self):
        with patch.object(pcb.subprocess, 'run', side_effect=OSError('missing CLI')):
            self.state = w.apply(self.root, self.state, {'op': 'pcb_dry_run', 'item': 'M', 'revision': 1, 'window': 'w'})
        r = self.state['results'][-1]
        self.assertEqual(r['status'], 'blocked');self.assertEqual(r['pcb_constraint_dry_run']['status'], 'unknown')
        self.assertEqual(w.read(r['pcb_constraint_dry_run']['report'])['receipts'], [])

    def test_verified_runs_fresh_readonly_review_and_keeps_failures_partial(self):
        self.native_status = 'pass';self.preview = {}
        self.native_apply_result = {'status': 'pass', 'configurationReadback': True, 'netRulesReadback': True,
                                    'operations': [{'label': 'pcb_Drc.check(true,false,true)', 'return': []}],
                                    'drc': [], 'boardStatus': 'fail'}
        with patch.object(pcb.subprocess, 'run', self.native):
            applied = w.apply(self.root, copy.deepcopy(self.state),
                              {'op': 'pcb_apply', 'item': 'M', 'revision': 1, 'window': 'window'})
        external = self.root / 'external.json';w.write(external, {'fixture': True})
        verified = {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'verified', 'note': 'fixture review',
                    'window': 'window', 'artifact': 'fixture', 'checks_passed': True, 'saved': True, 'evidence': [str(external)]}
        self.native_apply_result['boardStatus'] = 'pass'
        for geometry_status, professional_status in [('pass','pass'),('fail','pass'),('unknown','pass'),('pass','fail'),('pass','unknown')]:
            with self.subTest(geometry_status=geometry_status, professional_status=professional_status):
                self.geometry_status = geometry_status;self.native_status = geometry_status
                self.professional_status = professional_status
                with patch.object(w, 'check_evidence', return_value=[]), patch.object(pcb.subprocess, 'run', self.native):
                    state = w.apply(self.root, copy.deepcopy(applied), verified)
                result = state['results'][-1];receipt = result['pcb_constraint_review']
                self.assertEqual(result['status'], 'verified' if geometry_status == professional_status == 'pass' else 'partial')
                self.assertEqual(receipt['mode'], 'review');self.assertFalse(receipt['rules_applied'])
                self.assertTrue(receipt['bindings_verified']);self.assertEqual(receipt['geometry_status'], geometry_status)
                self.assertIn('布线后只读复查', w.view_contents(self.root, state)['task_plan.md'])
                self.assertTrue(w.read(receipt['report'])['review'])

    def test_professional_source_and_automatic_result_cannot_be_fabricated(self):
        bad = copy.deepcopy(self.item);bad['pcb_checks']['checks'][0]['evidenceRef'] = 'unselected'
        with self.assertRaisesRegex(ValueError, 'selected module source'):
            w.apply(self.root, copy.deepcopy(self.state), {'op': 'propose', 'id': 'bad', 'reason': 'fixture',
                    'delta': [{'id': 'M2', 'action': 'ADDED', 'item': bad}]})
        with self.assertRaisesRegex(ValueError, 'cannot be self-declared'):
            w.check_evidence(self.root, self.item, {'status': 'partial', 'checks': [
                {'id': 'pcb_professional_checks', 'status': 'pass', 'note': 'fake', 'evidence': ['fake.json']}]})

    def test_review_requires_final_native_readback(self):
        self.native_status = 'pass';self.preview = {};self.geometry_status = 'pass'
        self.native_apply_result = {'status': 'pass', 'configurationReadback': True, 'netRulesReadback': True,
                                    'operations': [{'label': 'pcb_Drc.check(true,false,true)', 'return': []}],
                                    'drc': [], 'boardStatus': 'pass'}
        def incomplete(cmd, **kwargs):
            response = self.native(cmd, **kwargs)
            path = Path(cmd[cmd.index('--output') + 1]);e = w.read(path)
            e['receipts'].pop();e.pop('reviewAfter');w.write(path, e)
            return response
        with patch.object(pcb.subprocess, 'run', incomplete):
            receipt = pcb.run(w, self.root, 'M', self.state['plan']['M'], 'window', review=True)
        self.assertEqual(receipt['status'], 'unknown');self.assertFalse(receipt['bindings_verified'])
        self.assertFalse(receipt['snapshot_stable'])

    def test_dry_run_does_not_hide_prior_unfinished_hardware_recovery(self):
        self.state = w.apply(self.root, self.state, {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'partial', 'note': 'fixture earlier edits'})
        self.prepare()
        self.assertTrue(w.status(self.root, self.state)['recovery']['M']['readback_required'])

    def test_external_report_cannot_claim_preparation(self):
        with self.assertRaisesRegex(ValueError, 'only by the workflow'):
            w.apply(self.root, self.state, {'op': 'result', 'item': 'M', 'revision': 1, 'status': 'partial', 'note': 'fake', 'preparation_only': True})

    def test_ready_requires_native_identity_and_read_success(self):
        self.native_status = 'ready';self.preview = {}
        def wrong_identity(cmd, **kwargs):
            response = self.native(cmd, **kwargs)
            out = Path(cmd[cmd.index('--output') + 1]);e = w.read(out);e['identity']['uuid'] = 'other';w.write(out, e)
            return response
        with patch.object(pcb.subprocess, 'run', wrong_identity), self.assertRaisesRegex(ValueError, 'native identity'):
            w.apply(self.root, copy.deepcopy(self.state), {'op': 'pcb_dry_run', 'item': 'M', 'revision': 1, 'window': 'w'})

    def test_revised_module_never_displays_previous_dry_run_as_current(self):
        self.prepare()
        updated = copy.deepcopy(self.item);updated['pcb_constraint_ir']['constraints'][0]['min'] = .508
        self.state = w.apply(self.root, self.state, {'op': 'propose', 'id': 'C2', 'reason': 'fixture revision', 'delta': [{'id': 'M', 'action': 'MODIFIED', 'item': updated}]})
        self.state = w.apply(self.root, self.state, {'op': 'approve', 'id': 'C2', 'confirmation': 'fixture revision only'})
        self.assertIn('尚未取得当前修订', w.view_contents(self.root, self.state)['task_plan.md'])

    def test_apply_keeps_rule_success_separate_from_failing_board_drc(self):
        self.native_status = 'fail';self.preview = {}
        self.native_apply_result = {'status': 'fail', 'before': {'rules': 'before'}, 'after': {'rules': 'after'},
                                    'configurationReadback': True, 'netRulesReadback': True,
                                    'operations': [{'label': 'overwriteNetRules', 'return': True},
                                                   {'label': 'pcb_Drc.check(true,false,true)', 'return': [{'errorType': 'clearance'}]}],
                                    'drc': [{'errorType': 'clearance'}], 'boardStatus': 'fail'}
        with patch.object(pcb.subprocess, 'run', self.native):
            receipt = pcb.run(w, self.root, 'M', self.state['plan']['M'], 'window', apply=True)
        self.assertTrue(receipt['rules_applied']);self.assertTrue(receipt['drc_checked'])
        self.assertEqual(receipt['constraint_status'], 'fail');self.assertEqual(receipt['board_status'], 'fail')
        self.assertEqual(w.read(receipt['report'])['result']['before'], self.native_apply_result['before'])

    def test_apply_does_not_accept_failed_rule_readback(self):
        self.native_status = 'unknown';self.preview = {}
        self.native_apply_result = {'status': 'unknown', 'before': {'rules': 'before'}, 'after': {'rules': 'different'},
                                    'configurationReadback': False, 'netRulesReadback': True,
                                    'operations': [{'label': 'overwriteCurrentRuleConfiguration', 'return': True},
                                                   {'label': 'pcb_Drc.check(true,false,true)', 'return': []}],
                                    'drc': [], 'boardStatus': 'pass', 'reason': 'Rule or explicit binding readback differs'}
        with patch.object(pcb.subprocess, 'run', self.native):
            receipt = pcb.run(w, self.root, 'M', self.state['plan']['M'], 'window', apply=True)
        self.assertFalse(receipt['rules_applied']);self.assertTrue(receipt['drc_checked'])
        self.assertEqual(receipt['status'], 'unknown');self.assertEqual(receipt['board_status'], 'pass')
        self.native_status = 'pass'
        self.native_apply_result.update(status='pass', configurationReadback=True)
        self.native_apply_result.pop('reason')
        def wrong_identity(cmd, **kwargs):
            response = self.native(cmd, **kwargs)
            out = Path(cmd[cmd.index('--output') + 1]);e = w.read(out);e['identity']['uuid'] = 'other';w.write(out, e)
            return response
        with patch.object(pcb.subprocess, 'run', wrong_identity):
            receipt = pcb.run(w, self.root, 'M', self.state['plan']['M'], 'window', apply=True)
        self.assertFalse(receipt['rules_applied']);self.assertEqual(receipt['status'], 'unknown')
        self.assertIn('identity', receipt['reason'])


if __name__ == '__main__':
    unittest.main()

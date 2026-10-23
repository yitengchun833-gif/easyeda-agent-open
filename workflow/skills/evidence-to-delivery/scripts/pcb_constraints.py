"""Run the existing PCB constraint compiler in read-only or apply mode."""
import json
from pathlib import Path
import subprocess


def run(workflow, root, key, item, window, apply=False, review=False):
    w = workflow
    w.require(item.get('domain') == 'pcb', 'PCB dry-run requires a PCB module plan')
    w.require(w.nonempty(window), 'PCB preparation requires the current explicit window')
    ir = item.get('pcb_constraint_ir')
    validate(w, ir)
    directory = root / '.workflow' / 'pcb-constraints'
    directory.mkdir(parents=True, exist_ok=True)
    # Per-revision paths keep earlier result hashes valid when a module is revised.
    stem = f'{key}-r{item["revision"]}-{w.now().replace(":", "-")}'
    ir_file = directory / (stem + '.ir.json')
    out = directory / (stem + ('.review.json' if review else '.apply.json' if apply else '.dry-run.json'))
    w.write(ir_file, ir)
    checks = item.get('pcb_checks')
    checks_file = directory / (stem + '.checks.json') if checks is not None else None
    if checks_file:
        validate_checks(w, checks, ir['target'], item.get('sources'))
        w.write(checks_file, checks)
    runner = Path(__file__).resolve().parents[3] / 'tools' / 'pcb_constraint_apply.mjs'
    if not runner.is_file():
        runner = Path(__file__).resolve().parents[4] / 'tools' / 'pcb_constraint_apply.mjs'
    try:
        command = ['node', str(runner), '--ir', str(ir_file), '--window', window]
        if review:
            command.append('--review')
        elif not apply:
            command.append('--dry-run')
        command += ['--output', str(out)]
        if checks_file:
            command += ['--checks', str(checks_file)]
        response = subprocess.run(command,
                                  capture_output=True, text=True, encoding='utf-8', timeout=310 if review else 80)
        result = json.loads(response.stdout)
        w.require(isinstance(result, dict) and result.get('status') in
                  ('ready', 'partial', 'pass', 'fail', 'unknown', 'unsupported_in_constraint_v0'), 'Invalid compiler receipt')
    except (OSError, subprocess.TimeoutExpired, ValueError) as error:
        result = {'status': 'unknown', 'reason': str(error)}
    evidence = w.read(out) if out.is_file() else None
    if evidence is None:
        # Failed invocation is evidence, not a fabricated native success.
        evidence = {'status': 'unknown', 'dryRun': not apply and not review, 'review': review, 'ir': ir, 'reason': result.get('reason'), 'receipts': []}
        w.write(out, evidence)
        result['status'] = 'unknown'
    w.require(evidence.get('dryRun') is (not apply and not review) and evidence.get('ir') == ir and
              (not review or evidence.get('review') is True), 'Wrong compiler mode or IR in evidence')
    checks_ok = (checks is not None and isinstance(evidence.get('professionalSpec'), dict) and
                 evidence['professionalSpec'].get('value') == checks and
                 evidence['professionalSpec'].get('sha256') == w.file_hash(checks_file))
    if not apply and not review:
        w.require(not evidence.get('result'), 'Preparation must not include rule application')
    if not apply and not review and result['status'] in ('ready', 'partial'):
        identity = evidence.get('identity', {})
        w.require(evidence.get('status') == result['status'] and identity.get('uuid') == ir['target']['documentUuid'] and
                  identity.get('parentProjectUuid') == ir['target']['projectUuid'] and identity.get('documentType') == 3 and
                  evidence.get('receipts') and all(r.get('ok') is True for r in evidence['receipts']),
                  'Candidate preparation requires matching native identity and successful read receipts')
    if not apply and not review:
        return {'status': result['status'], 'reason': result.get('reason'), 'preview': result.get('preview', {}),
                'ir_file': str(ir_file), 'report': str(out), 'window': window, 'mode': 'dry_run',
                'rules_applied': False, 'drc_checked': False}
    native = evidence.get('result')
    native = native if isinstance(native, dict) else None
    identity = evidence.get('identity', {})
    identity_ok = (identity.get('uuid') == ir['target']['documentUuid'] and
                   identity.get('parentProjectUuid') == ir['target']['projectUuid'] and
                   identity.get('documentType') == 3)
    plan = evidence.get('plan')
    plan_ok = isinstance(plan, dict) and plan.get('status') == 'ready'
    if native is None or not plan_ok:
        # The compiler stopped before writes; preserve its planning result rather than mislabeling it as failed apply.
        status = result.get('status')
        if status not in ('partial', 'fail', 'unknown', 'unsupported_in_constraint_v0'):
            status = 'unknown'
        return {'status': status, 'reason': result.get('reason') or evidence.get('reason'),
                'preview': result.get('preview', {}), 'ir_file': str(ir_file), 'report': str(out), 'window': window,
                'mode': 'review' if review else 'apply', 'rules_applied': False, 'bindings_verified': False,
                'geometry_status': 'unknown', 'snapshot_stable': False, 'configuration_readback': False,
                'professional_status': 'unknown',
                'net_rules_readback': False, 'drc_checked': False, 'board_status': 'unknown',
                'constraint_status': 'unknown'}
    receipts = evidence.get('receipts')
    receipts_ok = isinstance(receipts, list) and len(receipts) >= (3 if review else 2) and all(
        isinstance(row, dict) and row.get('ok') is True for row in receipts)
    review_after = evidence.get('reviewAfter')
    review_after_ok = (not review or isinstance(review_after, dict) and
                       review_after.get('identity') == identity and isinstance(review_after.get('before'), dict) and
                       review_after['before'] == native.get('after'))
    result_ok = evidence.get('status') == result.get('status')
    target_ok = native.get('target') == ir['target']
    configuration_readback = native.get('configurationReadback') is True
    net_rules_readback = native.get('netRulesReadback') is True
    operations = native.get('operations')
    drc_checked = isinstance(operations, list) and any(
        row.get('label') == 'pcb_Drc.check(true,false,true)' for row in operations if isinstance(row, dict)) and 'drc' in native
    rules_applied = (identity_ok and target_ok and plan_ok and result_ok and receipts_ok and review_after_ok and bool(operations) and
                     configuration_readback and net_rules_readback)
    # A successful binding and a clean board are separate claims: existing geometry can still fail DRC.
    status = (evidence.get('status') if review else native.get('status')) if rules_applied else 'unknown'
    if status not in ('pass', 'fail', 'unknown', 'partial', 'ready', 'unsupported_in_constraint_v0'):
        status = 'unknown'
    unconfirmed = []
    if not identity_ok: unconfirmed.append('native identity differs')
    if not target_ok: unconfirmed.append('native result target differs')
    if not plan_ok: unconfirmed.append('native plan is not ready')
    if not result_ok: unconfirmed.append('CLI status differs from evidence')
    if not receipts_ok: unconfirmed.append('read/apply receipts are incomplete or unsuccessful')
    if not review_after_ok: unconfirmed.append('final review readback is missing or differs')
    if not configuration_readback: unconfirmed.append('configuration readback failed')
    if not net_rules_readback: unconfirmed.append('Net rule readback failed')
    if not operations: unconfirmed.append('native apply operations are absent')
    return {'status': status, 'reason': native.get('reason') or result.get('reason') or
            ('; '.join(unconfirmed) if unconfirmed else None), 'preview': result.get('preview', {}),
            'ir_file': str(ir_file), 'report': str(out), 'window': window, 'mode': 'review' if review else 'apply',
            'rules_applied': rules_applied and not review, 'bindings_verified': rules_applied,
            **({'geometry_status': evidence.get('geometryStatus', 'unknown'),
                'professional_status': evidence.get('professional', {}).get('status', 'unknown') if checks_ok else 'unknown',
                'professional_checks': evidence.get('professional', {}).get('checks', []) if checks_ok else [],
                'snapshot_stable': evidence.get('snapshotStable') is True and review_after_ok,
                'geometry_summary': (evidence.get('geometry') or {}).get('summary', {}),
                'geometry_limitations': (evidence.get('geometry') or {}).get('limitations', [])} if review else {}),
            'configuration_readback': configuration_readback,
            'net_rules_readback': net_rules_readback, 'drc_checked': drc_checked,
            'board_status': native.get('boardStatus') if drc_checked else 'unknown',
            'constraint_status': native.get('status') if isinstance(native.get('status'), str) else 'unknown'}


def validate(w, ir):
    w.require(isinstance(ir, dict) and ir.get('version') == 'pcb-constraint/v0' and ir.get('unit') == 'mm',
              'PCB module requires explicit pcb-constraint/v0 IR in mm')
    target = ir.get('target', {})
    w.require(isinstance(target, dict) and all(w.nonempty(target.get(k)) for k in ('projectUuid', 'documentUuid')),
              'PCB module IR requires exact project/document UUIDs')
    w.require(isinstance(ir.get('constraints'), list) and ir['constraints'], 'PCB module IR requires constraints; do not invent defaults')


def validate_checks(w, spec, target, sources=None):
    w.require(isinstance(spec, dict) and spec.get('version') == 'pcb-professional-checks/v0' and
              spec.get('unit') == 'mm' and spec.get('target') == target,
              'PCB professional checks require explicit version, mm and the same IR target')
    checks = spec.get('checks')
    w.require(isinstance(checks, list) and checks and all(isinstance(c, dict) and
              all(w.nonempty(c.get(k)) for k in ('id', 'type', 'role', 'evidenceRef')) for c in checks),
              'PCB professional checks require explicit objects, roles and source references')
    w.require(len({c['id'] for c in checks}) == len(checks), 'Duplicate PCB professional check IDs')
    if sources is not None:
        w.require(all(c['evidenceRef'] in sources for c in checks), 'PCB check evidenceRef must reference a selected module source')


def review_lines(w, key, receipt):
    if receipt.get('mode') == 'review':
        return ['', '### ' + key + ' PCB 布线后只读复查', '',
                '- 结果：' + w.escaped(receipt['status']) + '；原生DRC：' + w.escaped(receipt.get('board_status', 'unknown')),
                '- 几何检查：' + w.escaped(receipt.get('geometry_status', 'unknown')) + '；读前/读后现场一致：' + str(receipt.get('snapshot_stable')),
                '- 几何摘要：' + w.escaped(receipt.get('geometry_summary', {})),
                '- 专业检查：' + w.escaped(receipt.get('professional_status', 'unknown')) + '；逐项：' + w.escaped(receipt.get('professional_checks', [])),
                '- 覆盖限制：' + w.escaped(receipt.get('geometry_limitations', [])),
                '- 原始回执：[review JSON](<' + Path(receipt['report']).as_posix() + '>)',
                '- 本次只读，不创建规则、修改布局布线或保存；多分支INFO需图面审阅，不等于全铜轮廓DFM通过。']
    p = receipt.get('preview', {})
    applied = receipt.get('mode') == 'apply'
    title = 'apply' if applied else 'dry-run'
    lines = ['', '### ' + key + ' PCB Constraint ' + title, '', '- 结果：' + w.escaped(receipt['status']),
             '- 批次问题：' + w.escaped(p.get('batchIssue') or receipt.get('reason') or '无'),
             '- IR：[' + key + ' IR](<' + Path(receipt['ir_file']).as_posix() + '>)',
             '- 原始回执：[' + title + ' JSON](<' + Path(receipt['report']).as_posix() + '>)']
    if applied:
        lines += ['- 规则/显式绑定回读：' + ('成功' if receipt.get('rules_applied') else '未确认') +
                  '；配置回读：' + ('成功' if receipt.get('configuration_readback') else '未确认') +
                  '；Net 规则回读：' + ('成功' if receipt.get('net_rules_readback') else '未确认') + '。',
                  '- 原生 DRC：' + ('已调用' if receipt.get('drc_checked') else '未确认') +
                  '；整板 DRC 状态：' + w.escaped(receipt.get('board_status', 'unknown')) +
                  '；约束对象状态：' + w.escaped(receipt.get('constraint_status', 'unknown')) + '。']
    else:
        lines.append('- 仅准备方案，未 apply 规则、未调用 DRC、未授权布局/布线。')
    lines += ['',
             '拟建规则（数值单位 mm）：', '', '| 类型 | 规则名 | 有效数值 |', '|---|---|---|']
    for rule in p.get('rulesToCreate', []):
        lines.append('| ' + ' | '.join(w.escaped(rule.get(k, '')) for k in ('field', 'name', 'values')) + ' |')
    if not p.get('rulesToCreate'):
        lines.append('| 无 | — | — |')
    for rule in p.get('rulesToCreate', []):
        if rule.get('changedCells'):
            lines += ['', '- ' + w.escaped(rule.get('name')) + ' 对象对候选（原生字段/前值/后值）：' + w.escaped(rule['changedCells'])]
        if rule.get('retainedNativeValues'):
            lines += ['', '- ' + w.escaped(rule.get('name')) + ' 来源未指定：' + w.escaped(rule.get('unspecifiedFields', [])) +
                      '；原生模板保留值（不是来源要求）：' + w.escaped(rule.get('retainedNativeValues', []))]
    lines += ['', '成员 Net 显式绑定：', '', '| Net | 类型 | 规则名 | 复用已有 |', '|---|---|---|---|']
    for binding in p.get('bindings', []):
        lines.append('| ' + ' | '.join(w.escaped(binding.get(k, '')) for k in ('net', 'field', 'ruleName')) +
                     ' | ' + ('是' if binding.get('reused') else '否') + ' |')
    if not p.get('bindings'):
        lines.append('| 无 | — | — | — |')
    for field, label in [('conflicts', '冲突或无效约束'), ('unsupported', '不支持约束'), ('unknown', '未知'), ('unresolved', '来源未指定/仅候选')]:
        lines += ['', label + '：', '']
        lines += ['- ' + w.escaped(f'#{r.get("index")} {r.get("type")}: {r.get("reason")}') for r in p.get(field, [])] or ['- 无']
    if p.get('candidateOnly'):
        lines.append('- 上述规则/绑定仅候选；整批未就绪。')
    return lines

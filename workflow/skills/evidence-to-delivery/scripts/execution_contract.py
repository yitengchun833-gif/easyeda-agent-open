"""Action evidence inside workflow.py's existing state; no application writes."""
import copy
import importlib.util
from pathlib import Path


def modules():
    directory = Path(__file__).resolve().parents[2] / 'easyeda-agent/scripts'
    result = []
    for name in ('schematic_preflight', 'schematic_layout'):
        spec = importlib.util.spec_from_file_location(name, directory / (name + '.py'))
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        result.append(module)
    return result


def action_identity(plan, action, digest):
    # Content identity survives ordering. Changed payload/postcondition gets a new ID.
    return 'A-' + digest({'route': plan['route'], 'action': action['action'],
                          'payload': action.get('payload', {}), 'postcondition': action.get('postcondition')})


def stamp(packet, require):
    require(packet.get('ok') is True and packet.get('unordered') is not True, 'ordered successful readback required')
    require(isinstance(packet.get('runtimeInstance'), str) and packet['runtimeInstance'], 'runtime instance missing')
    for key in ('seq', 'seqAbandoned', 'mutationSeq'):
        require(type(packet.get(key)) is int and packet[key] >= 0, 'freshness field missing: ' + key)
    return {k: packet[k] for k in ('runtimeInstance', 'seq', 'seqAbandoned', 'mutationSeq')}


def postcondition(action, snapshot, require):
    """Exact object/field predicates, independent of transport success."""
    rules = action.get('postcondition')
    require(isinstance(rules, list) and rules, 'postcondition list required')
    inventory = snapshot['result'].get('pagePrimitives', {})
    observations = []
    for rule in rules:
        require(isinstance(rule, dict) and not set(rule) - {'collection', 'match', 'equals', 'absent'}, 'unknown postcondition fields')
        rows, match = inventory.get(rule.get('collection')), rule.get('match')
        require(isinstance(rows, list) and isinstance(match, dict) and match, 'postcondition inventory or identity missing')
        require(rule.get('absent') is True or isinstance(rule.get('equals'), dict) and rule['equals'], 'postcondition must assert fields or absence')
        selected = [r for r in rows if all(k in r and r[k] == v for k, v in match.items())]
        ok = not selected if rule.get('absent') is True else len(selected) == 1 and all(k in selected[0] and selected[0][k] == v for k, v in rule['equals'].items())
        observations.append(ok)
    payload = action.get('payload', {})
    if action['action'] == 'schematic.attribute.modify':
        expected = {k[0].upper()+k[1:]: v for k, v in payload.get('patch', {}).items()}
        require(expected and set(payload['patch']) <= {'x','y','rotation'} and any(r.get('collection') == 'attributes' and
                r.get('match') == {'primitiveId': payload.get('primitiveId')} and all(r.get('equals', {}).get(k) == v for k,v in expected.items()) for r in rules), 'attribute postcondition incomplete')
    if action['action'] == 'schematic.component.modify':
        # A check on an unrelated row cannot prove this modification.
        require(any(r.get('collection') == 'components' and r.get('match') == {'primitiveId': payload.get('primitiveId')} and
                    all(r.get('equals', {}).get(k) == v for k, v in payload.get('patch', {}).items()) for r in rules),
                'postcondition does not cover modified component fields')
    if action['action'] == 'schematic.wire.create':
        preflight, _ = modules()
        wanted = {tuple(sorted((a, b))) for a, b in preflight.segments(payload.get('points'))}
        wires = snapshot['result'].get('wires', [])
        groups = {}
        for wire in wires:
            if payload.get('net') is not None and wire.get('net') != payload['net']:
                continue
            groups.setdefault(wire['primitiveId'], set()).add(tuple(sorted(((wire['x0'], wire['y0']), (wire['x1'], wire['y1'])))))
        observations.append(sum(v == wanted for v in groups.values()) == 1)
    if action['action'] == 'schematic.netflag.create':
        require(payload.get('kind') == 'net_label', 'only native net_label has a bound readback predicate here')
        rows = [r for r in inventory.get('attributes', []) if r.get('Key') == 'Name' and r.get('Value') == payload.get('net') and
                r.get('X') == payload.get('x') and r.get('Y') == payload.get('y') and r.get('ValueVisible') is True]
        observations.append(len(rows) == 1 and any(w['primitiveId'] == rows[0].get('ParentPrimitiveId') and w.get('net') == payload['net'] for w in snapshot['result'].get('wires', [])))
    if action['action'] == 'schematic.primitives.delete':
        ids = payload.get('primitiveIds')
        require(isinstance(ids, list) and ids and all(isinstance(i, str) and i for i in ids) and len(ids) == len(set(ids)),
                'rewire deletion needs distinct wire IDs')
        require(all(any(r.get('collection') == 'wires' and r.get('match') == {'primitiveId': i} and r.get('absent') is True for r in rules)
                    for i in ids), 'wire deletion postcondition incomplete')
    return all(observations), any(observations)


def creation_scope(action, before, after, scope, require):
    """Only exact new rows covered by this action's postconditions may be adopted."""
    allowed = copy.deepcopy(scope['mutable_ids'])
    kind = {'schematic.wire.create': 'wires', 'schematic.netflag.create': 'attributes',
            'schematic.component.place': 'components'}.get(action['action'])
    if not kind:
        return allowed
    if kind == 'components':
        require(scope.get('mode') == 'design', 'component creation requires design mode')
    old_ids = {r['primitiveId'] for r in before['result']['pagePrimitives'][kind]}
    for rule in action.get('postcondition', []):
        if rule.get('collection') != kind or rule.get('absent'):
            continue
        rows = [r for r in after['result']['pagePrimitives'][kind] if r['primitiveId'] not in old_ids and
                all(r.get(k) == v for k, v in {**rule['match'], **rule['equals']}.items())]
        if len(rows) == 1:
            allowed.setdefault(kind, []).append(rows[0]['primitiveId'])
    # Dependent attributes must belong to this newly created object, not an
    # existing unrelated owner. Full identity/net checks still run independently.
    owners = set(allowed.get(kind, [])) - old_ids
    if kind == 'components':
        owners.update(p['primitiveId'] for c in after['result']['components'] if c['primitiveId'] in owners for p in c.get('pins', []) if p.get('primitiveId'))
    old_attrs = {r['primitiveId'] for r in before['result']['pagePrimitives']['attributes']}
    for row in after['result']['pagePrimitives']['attributes']:
        if row['primitiveId'] not in old_attrs and row.get('ParentPrimitiveId') in owners:
            allowed.setdefault('attributes', []).append(row['primitiveId'])
    return {k: list(dict.fromkeys(v)) for k, v in allowed.items()}


def operate(w, root, state, data):
    preflight, layout = modules()
    require = w.require
    require(data.get('operation') in ('prepare', 'record', 'recover', 'inspect'), 'unknown execution operation')
    req_path, plan_path = (w.resolve(root, data[k]) for k in ('requirements', 'plan'))
    requirements, plan = w.read(req_path), w.read(plan_path)
    gate = preflight.audit(requirements, plan, req_path.parent)
    require(gate['passed'], 'preflight rejected: ' + str(gate['findings'][:5]))
    require(plan.get('batch_id') and isinstance(plan.get('runtime'), dict), 'batch_id and pinned runtime required')
    ids = [action_identity(plan, a, w.digest) for a in plan['actions']]
    require(len(set(ids)) == len(ids), 'duplicate actions need distinct explicit postconditions')
    for a, key in zip(plan['actions'], ids):
        require(a.get('action_id', key) == key, 'action_id does not bind exact content')
    scope = requirements['execution_rules'].get('edit_scope', {})
    rewire = plan.get('rewire') is True and isinstance(scope.get('rewire'), dict)
    require(plan.get('rewire') in (None, False, True) and (plan.get('rewire') is not True or rewire), 'unconfirmed rewire plan')
    if rewire:
        require(scope.get('mode') in ('layout', 'design') and scope.get('acceptance') == 'page', 'rewire requires whole-page scope')
        baseline = w.read(w.resolve(root, data['baseline']))
        original = {r['primitiveId'] for r in baseline['result']['pagePrimitives']['wires']}
        deletions = [a.get('payload', {}).get('primitiveIds') for a in plan['actions'] if a['action'] == 'schematic.primitives.delete']
        listed = [i for group in deletions if isinstance(group, list) for i in group]
        require(original and all(isinstance(group, list) and group for group in deletions) and
                len(listed) == len(original) and set(listed) == original and
                original <= set(scope['mutable_ids'].get('wires', [])), 'rewire must enumerate every original wire once inside scope')
        require(all(a['action'] in {'schematic.primitives.delete','schematic.component.modify',
                'schematic.attribute.modify','schematic.wire.create','schematic.netflag.create'} for a in plan['actions']),
                'unsupported action in rewire batch')
    if data['operation'] == 'inspect':
        return {'passed': True, 'actions': ids, 'runtime': plan['runtime'], 'route': plan['route']}
    key = data.get('action_id')
    require(key in ids, 'action is not in current plan')
    action = plan['actions'][ids.index(key)]
    supported = {'schematic.component.modify', 'schematic.attribute.modify', 'schematic.wire.create',
                 'schematic.netflag.create', 'schematic.component.place'}
    if rewire:
        supported.add('schematic.primitives.delete')
    require(action['action'] in supported, 'action lacks a supported execution contract; no dispatch')
    require(action['action'] == data['action'] and action.get('payload', {}) == data.get('payload', {}) and
            all(plan['route'].get(k) == data.get(k) for k in ('project', 'doc')), 'action/route/payload mismatch')
    packet = data.get('snapshot', {})
    require(w.read(w.resolve(root, data['snapshot_file'])) == packet, 'snapshot file does not match readback')
    freshness = stamp(packet, require)
    require(packet.get('context', {}).get('projectUuid') == data['project'] and packet.get('context', {}).get('documentUuid') == data['doc'], 'readback target mismatch')
    require(packet['result'].get('pagePrimitives') and packet['result'].get('pinNetsAvailable') is True and
            packet['result'].get('wiresAvailable') is True, 'full inventory required')
    journal = state.setdefault('execution', {'actions': {}})
    entries = journal['actions']
    previous = entries.get(key)
    operation = data['operation']
    expected, partial = postcondition(action, packet, require)
    if operation == 'prepare':
        require(not previous or previous['effective_state'] == 'NOT_APPLIED', 'readback recovery required; verified actions cannot replay')
        require(not expected, 'postcondition already satisfied; no write needed')
        require(not any(e['target'] == [data['project'], data['doc']] and e['effective_state'] in ('UNKNOWN', 'PARTIAL') for e in entries.values()), 'unresolved previous write; recover before dispatch')
        scope = requirements['execution_rules'].get('edit_scope', {})
        require(isinstance(scope.get('mutable_ids'), dict), 'confirmed edit scope required')
        scope = copy.deepcopy(scope)
        if action['action'] == 'schematic.component.place':
            require(scope.get('mode') == 'design', 'component creation requires confirmed design mode')
        if rewire:
            index = ids.index(key)
            require(all(entries.get(prior, {}).get('effective_state') in ('APPLIED_VERIFIED','APPLIED_INTERMEDIATE')
                        for prior in ids[:index]), 'rewire actions must execute in order; recover prior action first')
        for entry in entries.values():
            if entry['target'] == [data['project'], data['doc']] and entry['effective_state'] in ('APPLIED_VERIFIED','APPLIED_INTERMEDIATE'):
                for kind, values in entry.get('adopted_ids', {}).items():
                    scope['mutable_ids'][kind] = list(set(scope['mutable_ids'].get(kind, [])) | set(values))
        baseline_path = w.resolve(root, data['baseline'])
        require(w.file_hash(baseline_path) == scope.get('baseline_sha256'), 'baseline not bound to scope')
        baseline = w.read(baseline_path)
        require(baseline.get('context', {}).get('projectUuid') == data['project'] and baseline.get('context', {}).get('documentUuid') == data['doc'], 'baseline target mismatch')
        # In layout mode only declared geometry changes are allowed before write.
        if scope.get('mode', 'layout') == 'layout' and action['action'] == 'schematic.component.modify':
            payload = action.get('payload', {})
            require(payload.get('primitiveId') in scope['mutable_ids'].get('components', []) and
                    set(payload.get('patch', {})) <= {'x', 'y', 'rotation', 'mirror'}, 'layout identity/scope violation')
        if action['action'] == 'schematic.attribute.modify':
            require(action['payload'].get('primitiveId') in scope['mutable_ids'].get('attributes', []), 'attribute outside scope')
        if action['action'] == 'schematic.primitives.delete':
            require(set(action['payload']['primitiveIds']) <= set(scope['mutable_ids'].get('wires', [])), 'wire deletion outside scope')
        require(layout.compare_scope(layout.unwrap(baseline), layout.unwrap(packet), scope['mutable_ids'], preserve_identity=rewire or scope.get('mode', 'layout') == 'layout')['passed'], 'pre-write scope drift')
        if scope.get('mode', 'layout') == 'layout' and not rewire:
            require(layout.compare(layout.unwrap(baseline), layout.unwrap(packet))['passed'], 'pre-write electrical drift')
        before = w.resolve(root, data['snapshot_file'])
        require(w.read(before) == packet, 'snapshot file does not match readback')
        entries[key] = {'action_id': key, 'batch_id': plan['batch_id'], 'target': [data['project'], data['doc']],
            'action': copy.deepcopy(action), 'rule_binding': plan['rule_binding'], 'before': str(before),
            'before_hash': w.file_hash(before), 'freshness': freshness, 'effective_state': 'UNKNOWN',
            'transport_status': 'pending', 'apply_state': 'unknown', 'verification_state': 'unknown', 'at': w.now()}
        result = {'passed': True, 'dispatch': True, 'action_id': key, 'freshness': freshness}
    else:
        require(previous is not None, 'no prepared action receipt')
        require(previous['rule_binding'] == plan['rule_binding'], 'rules changed during recovery')
        old = previous['freshness']
        require(freshness['runtimeInstance'] == old['runtimeInstance'] and freshness['seq'] > old['seq'] and
                freshness['seqAbandoned'] == old['seqAbandoned'] and freshness['mutationSeq'] >= old['mutationSeq'], 'recovery ordering unproven; unresolved')
        require(w.file_hash(previous['before']) == previous['before_hash'], 'baseline receipt changed')
        before = w.read(previous['before'])
        unchanged = (before['result']['pagePrimitives'] == packet['result']['pagePrimitives'] and
                     before['result']['components'] == packet['result']['components'] and
                     before['result']['wires'] == packet['result']['wires'])
        scope = requirements['execution_rules']['edit_scope']
        allowed = creation_scope(action, before, packet, scope, require)
        scope_ok = layout.compare_scope(layout.unwrap(before), layout.unwrap(packet), allowed, preserve_identity=rewire or scope.get('mode', 'layout') == 'layout')['passed']
        final_rewire_action = rewire and key == ids[-1]
        if rewire and not final_rewire_action:
            electrical_ok = False  # A planned temporary disconnect is checked only after the final action.
        else:
            try:
                electrical_ok = scope.get('mode') == 'design' or layout.compare(layout.unwrap(baseline if rewire else before), layout.unwrap(packet))['passed']
            except ValueError:
                if not rewire:
                    raise
                electrical_ok = False
        verified = expected and scope_ok and electrical_ok
        intermediate = rewire and not final_rewire_action and expected and scope_ok
        # An unchanged page after a settled ordered read proves this action did
        # not apply. Abandoned handlers are refused above: they may still land.
        effective = 'APPLIED_VERIFIED' if verified else 'APPLIED_INTERMEDIATE' if intermediate else 'NOT_APPLIED' if unchanged else 'PARTIAL' if partial else 'UNKNOWN'
        previous.update(effective_state=effective, apply_state='applied' if verified or intermediate else 'not_applied' if unchanged else 'partial' if partial else 'unknown',
                        verification_state='verified' if verified else 'intermediate' if intermediate else 'unknown',
                        transport_status=data.get('transport_status', previous['transport_status']),
                        readback_hash=w.digest(packet), readback_file=data.get('snapshot_file'), receipt=data.get('receipt', previous.get('receipt')), at=w.now())
        if verified or intermediate:
            previous['adopted_ids'] = {kind: [i for i in values if i not in scope['mutable_ids'].get(kind, [])] for kind, values in allowed.items()}
        if verified and rewire:
            for prior in ids[:ids.index(key)]:
                if entries[prior]['effective_state'] == 'APPLIED_INTERMEDIATE':
                    entries[prior].update(effective_state='APPLIED_VERIFIED', verification_state='verified')
        result = {'passed': verified or intermediate or unchanged, 'action_id': key, 'snapshot_id': w.digest(packet), 'verified': verified,
                  **{k: previous[k] for k in ('effective_state', 'transport_status', 'apply_state', 'verification_state')}}
    state['generation'] += 1
    return result

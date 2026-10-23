"""Read Open stage evidence into the existing workflow result; no EDA calls."""
import math
import re
import json
import zipfile
import copy
import subprocess
from datetime import datetime
from pathlib import Path


def local_rule_facts(report):
    """A later read may supply current rules, but must bind the same declared edit."""
    if not report.get('designCheck'):
        return value(report.get('execution'))
    original, original_context, original_ok = value(report.get('execution'))
    rules = original.get('rules') if isinstance(original.get('rules'), dict) else {}
    unresolved = [item for item in rules.get('items', []) if isinstance(item, dict)
                  and item.get('severity') == 'error' and item.get('coverage') != 'not_applicable' and item.get('status') != 'pass']
    checked, context, ok = value(report['designCheck'])
    binding, edit = checked.get('designVerification'), report.get('edit')
    if (not original_ok or not native_target(original_context, report.get('target', {}))
            or original.get('ok') is not True or original.get('partial') is True or not unresolved
            or any(item.get('id') != 'pin_nets' or item.get('status') not in ('unknown', 'fail') for item in unresolved)
            or not isinstance(binding, dict) or not isinstance(edit, dict)
            or edit.get('mode') != 'design' or edit.get('complete') is False or binding.get('mode') != 'design'
            or any(binding.get(key) != edit.get(key) for key in ('primitiveIds', 'expectedPins', 'expectedIdentities'))
            or not native_target(context, report.get('target', {}))):
        return {}, context, False
    return checked, context, ok


def validate(require, binding, domain):
    require(domain == 'schematic', 'open_plan belongs only to a schematic item')
    require(isinstance(binding, dict) and set(binding) == {'path', 'sha256'}
            and isinstance(binding['path'], str) and binding['path'].strip()
            and isinstance(binding['sha256'], str)
            and re.fullmatch('[a-f0-9]{64}', binding['sha256']), 'invalid open_plan binding')


def route_scope_nets(plan, ids):
    semantic = plan.get('semanticTarget', {})
    mapping = semantic.get('idMap', {}) if isinstance(semantic, dict) else {}
    parts = semantic.get('components', []) if isinstance(semantic, dict) else []
    keys = {key for key, identifier in mapping.items() if identifier in ids} if isinstance(mapping, dict) else set()
    selected = [part for part in parts if isinstance(part, dict) and part.get('key') in keys]
    if (len(keys) != len(ids) or len(selected) != len(keys)
            or any(not isinstance(part.get('pins'), list) for part in selected)):
        return None
    nets = set()
    for part in selected:
        for pin in part['pins']:
            if not isinstance(pin, dict) or pin.get('state') not in ('connected', 'nc', 'unconnected'):
                return None
            if pin.get('state') == 'connected':
                if not isinstance(pin.get('net'), str) or not pin['net']:
                    return None
                nets.add(pin['net'])
    return nets


def plan_inputs(w, root, binding, hashes=None):
    validate(w.require, binding, 'schematic')
    path = w.resolve(root, binding['path'])
    w.require(path.is_file() and w.file_hash(path, hashes) == binding['sha256'], 'Open plan changed; review its new revision')
    plan = w.read(path)
    w.require(isinstance(plan, dict) and plan.get('schemaVersion') == 'easyeda.plan/1' and isinstance(plan.get('revision'), int)
              and not isinstance(plan['revision'], bool) and plan['revision'] > 0 and isinstance(plan.get('target'), dict)
              and all(isinstance(plan['target'].get(k), str) and plan['target'][k]
                      for k in ('projectUuid', 'documentUuid'))
              and isinstance(plan.get('stages'), list) and plan['stages'], 'Open receipt needs a staged schematic plan')
    acceptance = plan.get('acceptanceScope', 'page')
    w.require(acceptance in ('page', 'module', 'local', 'local_display'), 'Unsupported Open acceptance scope')
    stage_ids = [stage.get('id') for stage in plan['stages'] if isinstance(stage, dict)]
    w.require(len(stage_ids) == len(plan['stages']) and all(isinstance(identifier, str) and identifier
              for identifier in stage_ids) and len(stage_ids) == len(set(stage_ids)),
              'Open stages need unique IDs')
    if acceptance == 'local_display':
        w.require(all(isinstance(stage, dict) and display_stage(stage)
                      and isinstance(stage.get('primitiveIds'), list) and stage['primitiveIds']
                      and all(isinstance(identifier, str) and identifier.strip() for identifier in stage['primitiveIds'])
                      and len(stage['primitiveIds']) == len(set(stage['primitiveIds'])) for stage in plan['stages']),
                  'Local display acceptance needs only scoped display stages')
    sources = plan.get('sources')
    w.require(isinstance(sources, list) and sources and len({source.get('id') for source in sources if isinstance(source, dict)}) == len(sources),
              'Open plan has no unique pinned sources')
    paths = [str(path)]
    for source in sources:
        w.require(isinstance(source, dict) and all(isinstance(source.get(k), str) and source[k]
                  for k in ('id', 'path', 'sha256')), 'invalid Open source')
        dependency = w.resolve(path.parent, source['path'])
        w.require(dependency.is_file() and w.file_hash(dependency, hashes) == source['sha256'],
                  'Open source changed: ' + source['id'])
        paths.append(str(dependency))
    semantic = plan.get('semanticTarget', {})
    id_map = semantic.get('idMap', {}) if isinstance(semantic, dict) else {}
    hints = plan.get('layoutHints', {})
    if isinstance(hints, dict) and 'interfaceStubs' in hints:
        stubs = hints['interfaceStubs']
        w.require(isinstance(stubs, list), 'interfaceStubs must be an array')
        seen = set()
        for stub in stubs:
            w.require(isinstance(stub, dict) and set(stub) == {'componentKey', 'pinNumber', 'length'}
                      and isinstance(stub['componentKey'], str) and isinstance(stub['pinNumber'], str), 'Invalid interface stub fields')
            parts = [part for part in semantic.get('components', []) if part.get('key') == stub['componentKey']]
            pins = [pin for part in parts for pin in part.get('pins', []) if pin.get('number') == stub['pinNumber']]
            pair = (stub['componentKey'], stub['pinNumber'])
            net = next((net for net in semantic.get('nets', []) if len(pins) == 1 and net.get('name') == pins[0].get('net')), {})
            w.require(len(parts) == 1 and len(pins) == 1 and pins[0].get('state') == 'connected'
                      and type(stub['length']) in (int, float) and math.isfinite(stub['length']) and stub['length'] > 0
                      and pair not in seen and net.get('match') != 'members'
                      and pins[0].get('net') in hints.get('visibleNetNames', [])
                      and hints.get('netPolicies', {}).get(pins[0].get('net')) == 'direct', 'Interface stub lacks a visible named direct target')
            seen.add(pair)
    for index, stage in enumerate(plan['stages']):
        if stage.get('componentKeys'):
            keys = stage['componentKeys']
            w.require(isinstance(keys, list) and len(keys) == len(set(keys))
                      and isinstance(stage.get('primitiveIds'), list) and isinstance(id_map, dict)
                      and all(key in id_map for key in keys)
                      and {id_map[key] for key in keys} == set(stage['primitiveIds']),
                      'Logical stage identities must match the final observed native ID scope')
        route_from = stage.get('routeFrom')
        if route_from is None:
            continue
        predecessors = [candidate for candidate in plan['stages'][:index]
                        if candidate.get('id') == route_from and candidate.get('check') == 'geometry']
        hints = plan.get('layoutHints')
        receipts = plan.get('executionReceipts')
        ids = stage.get('primitiveIds')
        scope_nets = route_scope_nets(plan, ids) if isinstance(ids, list) else None
        w.require(isinstance(route_from, str) and route_from and len(predecessors) == 1
                  and stage.get('check') == 'geometry' and stage.get('steps') == []
                  and isinstance(ids, list) and ids and len(ids) == len(set(ids))
                  and isinstance(id_map, dict) and set(ids) <= set(id_map.values())
                  and isinstance(predecessors[0].get('primitiveIds'), list)
                  and len(predecessors[0]['primitiveIds']) == len(set(predecessors[0]['primitiveIds']))
                  and set(ids) == set(predecessors[0]['primitiveIds'])
                  and isinstance(hints, dict) and isinstance(hints.get('visibleNetNames'), list)
                  and all(isinstance(name, str) and name for name in hints['visibleNetNames'])
                  and len(hints['visibleNetNames']) == len(set(hints['visibleNetNames']))
                  and isinstance(hints.get('netPolicies'), dict) and hints['netPolicies']
                  and all(isinstance(name, str) and name and isinstance(policy, str)
                          for name, policy in hints['netPolicies'].items())
                  and set(hints['visibleNetNames']) <= set(hints['netPolicies'])
                  and scope_nets is not None and all(hints['netPolicies'].get(net) == 'direct' for net in scope_nets)
                  and isinstance(receipts, dict) and isinstance(receipts.get('directory'), str)
                  and receipts['directory'].strip(),
                  'Open routeFrom stage needs a same-scope prior geometry stage and explicit direct net policy')
    for index, stage in enumerate(plan['stages']):
        if not stage.get('labelsFrom'):
            continue
        previous = [s for s in plan['stages'][:index] if s.get('id') == stage['labelsFrom']]
        w.require(len(previous) == 1 and stage.get('check') == 'geometry' and stage.get('steps') == []
                  and not stage.get('routeFrom') and isinstance(stage.get('primitiveIds'), list)
                  and set(stage['primitiveIds']) == set(previous[0].get('primitiveIds', []))
                  and isinstance(plan.get('layoutHints', {}).get('visibleNetNames'), list)
                  and (previous[0].get('routeFrom') or any(s.get('action') == 'schematic.wire.create' for s in previous[0].get('steps', []))),
                  'Open labelsFrom needs the same observed scope and a prior wired stage')
    requirements = plan.get('requirements')
    if requirements is not None:
        w.require(isinstance(requirements, list), 'Open requirements must be an array')
        source_ids = {source['id'] for source in sources}
        stage_ids = {stage.get('id') for stage in plan['stages'] if isinstance(stage, dict)}
        w.require(isinstance(semantic, dict) and isinstance(semantic.get('components'), list)
                  and isinstance(semantic.get('nets'), list) and isinstance(semantic.get('idMap', {}), dict),
                  'Open requirements need semantic target references')
        component_keys = {part.get('key') for part in semantic['components'] if isinstance(part, dict)}
        net_names = {net.get('name') for net in semantic['nets'] if isinstance(net, dict)}
        w.require(len(component_keys) == len(semantic['components']) and None not in component_keys
                  and len(net_names) == len(semantic['nets']) and None not in net_names
                  and all(isinstance(part.get('key'), str) and part['key'] and isinstance(part.get('ref'), str) and part['ref']
                          and isinstance(part.get('pins'), list) for part in semantic['components'] if isinstance(part, dict))
                  and all(isinstance(net.get('name'), str) and net['name'] and isinstance(net.get('members'), list)
                          for net in semantic['nets'] if isinstance(net, dict))
                  and all(isinstance(key, str) and key in component_keys and isinstance(value, str) and value
                          for key, value in semantic.get('idMap', {}).items()),
                  'invalid Open semantic target references')
        bindings = set()
        for rule in requirements:
            w.require(isinstance(rule, dict) and all(isinstance(rule.get(k), str) and rule[k] for k in
                      ('id', 'sourceId', 'when', 'method', 'check', 'severity'))
                      and rule['sourceId'] in source_ids
                      and rule['when'] in ('before_write', 'after_edit', 'delivery')
                      and rule['method'] in ('native', 'checker', 'ai_review', 'manual_unknown')
                      and rule['severity'] in ('error', 'warn')
                      and all(isinstance(rule.get(k), list) and all(isinstance(v, str) and v for v in rule[k])
                              for k in ('componentKeys', 'netNames'))
                      and set(rule['componentKeys']) <= component_keys and set(rule['netNames']) <= net_names
                      and (rule.get('stageId') is None or rule.get('stageId') in stage_ids),
                      'invalid Open requirement declaration or reference')
            unique = (rule['id'], rule['when'], rule.get('stageId'))
            w.require(unique not in bindings, 'duplicate Open requirement declaration')
            bindings.add(unique)
            w.require(acceptance != 'local_display' or rule['when'] != 'delivery',
                      'Local display requirements cannot include delivery checks; visual review is independent')
    return path, plan, paths


def value(receipt):
    """Traverse response containers only; steps/payloads are never observations."""
    transport_ok, context = True, {}
    while isinstance(receipt, dict):
        if receipt.get('isError') is True or receipt.get('ok') is False:
            transport_ok = False
        if isinstance(receipt.get('context'), dict):
            context = receipt['context']
        child = receipt.get('structuredContent', receipt.get('result'))
        if not isinstance(child, dict):
            break
        receipt = child
    return receipt if isinstance(receipt, dict) else {}, context, transport_ok


def native_target(context, target):
    return all(context.get(k) == target[k] for k in ('projectUuid', 'documentUuid'))


def measured(part):
    box, pins = part.get('bbox', {}), part.get('pins', [])
    finite = lambda x: isinstance(x, (int, float)) and not isinstance(x, bool) and math.isfinite(x)
    return (part.get('pinsAvailable') is True and isinstance(box, dict)
            and all(finite(box.get(k)) for k in ('minX', 'minY', 'maxX', 'maxY'))
            and box['minX'] < box['maxX'] and box['minY'] < box['maxY']
            and isinstance(pins, list) and bool(pins)
            and all(isinstance(p, dict) and isinstance(p.get('pinNumber'), str) and p['pinNumber'].strip()
                    and all(finite(p.get(k)) for k in ('x', 'y', 'rotation')) for p in pins))


def display_stage(stage):
    fields = {'x', 'y', 'rotation', 'color', 'fontName', 'fontSize', 'bold', 'italic',
              'underLine', 'alignMode', 'fillColor', 'keyVisible', 'valueVisible'}
    steps = stage.get('steps')
    return (stage.get('check') == 'geometry' and isinstance(steps, list) and bool(steps)
            and all(isinstance(s, dict) and s.get('action') == 'schematic.attribute.modify'
                    and isinstance(s.get('payload'), dict) and set(s['payload']) <= {'primitiveId', 'props', 'fontSizeUnit'}
                    and isinstance(s['payload'].get('primitiveId'), str) and s['payload']['primitiveId'].strip()
                    and s['payload'].get('fontSizeUnit', 'native') in ('native', 'display')
                    and isinstance(s['payload'].get('props'), dict) and bool(s['payload']['props'])
                    and set(s['payload']['props']) <= fields for s in steps))


def display_attributes(steps, execution, ids, readback=None):
    results = execution.get('results', [])
    if not steps or not ids or not isinstance(results, list) or len(results) != len(steps):
        return False
    for step, result in zip(steps, results):
        actual, _, ok = value(result)
        attribute, payload = actual.get('attribute'), step['payload']
        if not (ok and result.get('ok') is True and result.get('action') == step['action']
                and actual.get('verified') is True and actual.get('partial') is not True
                and actual.get('primitiveId') == payload['primitiveId'] and isinstance(attribute, dict)
                and attribute.get('primitiveId') == payload['primitiveId'] and display_owner(attribute, ids, readback)
                and isinstance(attribute.get('Key'), str) and attribute['Key'].strip() and isinstance(attribute.get('Value'), str)):
            return False
        for key, expected in payload['props'].items():
            observed = attribute.get(key[0].upper() + key[1:])
            if expected is None or type(expected) is not type(observed) and not (type(expected) in (int, float) and type(observed) in (int, float)):
                return False
            if type(expected) in (int, float) and (not math.isfinite(expected) or not math.isfinite(observed)):
                return False
            if key == 'fontSize' and payload.get('fontSizeUnit') == 'display' and type(expected) in (int, float):
                if abs(observed - expected) > 1e-6:
                    return False
            elif observed != expected:
                return False
    return True


def display_owner(attribute, ids, readback):
    if attribute.get('ParentPrimitiveId') in ids:
        return True
    observed, _, ok = value(readback)
    scope = observed.get('readScope', {})
    if not (ok and attribute.get('Key') == 'Name' and observed.get('wiresAvailable') is True
            and isinstance(observed.get('wires'), list) and isinstance(observed.get('components'), list)
            and scope.get('concurrentChange') is False and scope.get('missingIds') == []):
        return False
    wires = [w for w in observed['wires'] if w.get('primitiveId') == attribute.get('ParentPrimitiveId')]
    pins = [p for c in observed['components'] if c.get('primitiveId') in ids and c.get('pinsAvailable') is True for p in c.get('pins', [])]
    def touches(w, p):
        coords = [w.get(k) for k in ('x0','y0','x1','y1')] + [p.get('x'),p.get('y')]
        if not all(type(v) in (float,int) and math.isfinite(v) for v in coords): return False
        x0,y0,x1,y1,x,y = coords
        return (math.hypot(x1-x0,y1-y0)>1e-6 and abs((x-x0)*(y1-y0)-(y-y0)*(x1-x0))<=1e-6
                and min(x0,x1)-1e-6<=x<=max(x0,x1)+1e-6 and min(y0,y1)-1e-6<=y<=max(y0,y1)+1e-6)
    return bool(wires) and all(w.get('net') == attribute.get('Value') for w in wires) and any(touches(w,p) for w in wires for p in pins)


def prefix_binding(w, root, plan, intended, packet):
    """Only unchanged stage/target/source/expression facts survive a plan extension."""
    semantic = {k: v for k, v in plan['semanticTarget'].items() if k != 'idMap'}
    rules = [r for r in plan.get('requirements', []) if r.get('stageId') in (None, intended['id'])
             and (r['when'] != 'delivery' or intended['check'] == 'final')]
    expected = {'semanticTargetSha256': w.digest(semantic), 'sourcesSha256': w.digest(packet['sources']),
                'stageSha256': w.digest(intended)}
    if rules:
        expected['requirementsSha256'] = w.digest(rules)
    if plan.get('layoutHints') is not None:
        expected['expressionSha256'] = w.digest(plan['layoutHints'])
    if plan.get('taskKind'):
        expected['acceptanceSha256'] = w.digest({'acceptanceScope': plan.get('acceptanceScope', 'page'),
                                               'taskKind': plan['taskKind']})
    if plan.get('acceptanceScope') == 'local_display':
        expected['acceptanceSha256'] = w.digest({'acceptanceScope': 'local_display',
            'stages': [{'id': stage['id'], 'primitiveIds': stage['primitiveIds']} for stage in plan['stages']]})
    observed = packet.get('binding', {})
    w.require(isinstance(observed, dict) and all(observed.get(k) == v for k, v in expected.items())
              and observed.get('requirementsSha256') == expected.get('requirementsSha256')
              and observed.get('expressionSha256') == expected.get('expressionSha256')
              and observed.get('acceptanceSha256') == expected.get('acceptanceSha256')
              and w.resolve(root, observed.get('receiptDirectory', '')) == w.resolve(root, plan.get('executionReceipts', {}).get('directory', ''))
              and isinstance(observed.get('idMap'), dict)
              and all(plan['semanticTarget'].get('idMap', {}).get(k) == v for k, v in observed['idMap'].items()),
              'Open predecessor immutable binding changed')
    return expected


def route_artifact(w, root, plan_path, plan, packet, report, intended, hashes):
    """Load only the compiler artifact pinned by a successful route-stage receipt."""
    stage_index = plan['stages'].index(intended)
    geometry_index = next(index for index, stage in enumerate(plan['stages'][:stage_index])
                          if stage.get('id') == intended['routeFrom'])
    directory = plan.get('executionReceipts', {}).get('directory', '')
    geometry_path = w.resolve(root, directory) / ('stage-%02d.json' % (geometry_index + 1))
    compilation = report.get('compilation')
    w.require(isinstance(compilation, dict) and set(compilation) == {'file', 'sha256'}
              and isinstance(compilation['file'], str) and compilation['file'].strip()
              and Path(compilation['file']).is_absolute()
              and isinstance(compilation['sha256'], str) and re.fullmatch('[a-f0-9]{64}', compilation['sha256']),
              'Successful Open route stage needs a pinned compilation artifact')
    artifact_path = w.resolve(root, compilation['file'])
    expected_artifact = w.resolve(root, directory) / ('stage-%02d.routes.json' % (stage_index + 1))
    w.require(artifact_path == expected_artifact and artifact_path.is_file()
              and w.file_hash(artifact_path, hashes) == compilation['sha256']
              and geometry_path.is_file(), 'Open route artifact file is missing, changed or unmanaged')
    artifact = w.read(artifact_path)
    geometry = plan['stages'][geometry_index]
    semantic = {key: value for key, value in plan['semanticTarget'].items() if key != 'idMap'}
    rules = [rule for rule in plan.get('requirements', []) if rule.get('stageId') in (None, geometry['id'])
             and (rule['when'] != 'delivery' or geometry['check'] == 'final')]
    scope_nets = route_scope_nets(plan, set(intended['primitiveIds']))
    hints = plan['layoutHints']
    scope_policies = {net: hints['netPolicies'][net] for net in sorted(scope_nets or ())}
    finite = lambda value: isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value)
    def wire_step(step):
        payload = step.get('payload') if isinstance(step, dict) else None
        return (isinstance(step, dict) and step.get('action') == 'schematic.wire.create'
                and isinstance(payload, dict) and set(payload) <= {'net', 'points'}
                and isinstance(payload.get('points'), list) and len(payload['points']) >= 2
                and all(isinstance(point, list) and len(point) == 2 and all(finite(value) for value in point)
                        for point in payload['points'])
                and ('net' not in payload or (isinstance(payload['net'], str) and payload['net'] in scope_nets
                                              and payload['net'] in hints['visibleNetNames'])))
    expected_binding = {'semanticTargetSha256': w.digest(semantic),
                        'sourcesSha256': w.digest(packet['sources']),
                        'geometryStageSha256': w.digest(geometry),
                        'idMap': plan['semanticTarget']['idMap']}
    if rules:
        expected_binding['requirementsSha256'] = w.digest(rules)
    if plan.get('layoutHints') is not None:
        expected_binding['expressionSha256'] = w.digest(plan['layoutHints'])
    observed_binding = artifact.get('binding', {}) if isinstance(artifact, dict) else {}
    w.require(isinstance(artifact, dict) and artifact.get('schemaVersion') == 'easyeda.route-artifact/1'
              and w.resolve(root, artifact.get('planFile', '')) == plan_path
              and artifact.get('planSha256') == packet['sha256'] and artifact.get('target') == plan['target']
              and artifact.get('stage') == {'id': intended['id'], 'check': intended['check']}
              and isinstance(artifact.get('primitiveIds'), list)
              and len(artifact['primitiveIds']) == len(set(artifact['primitiveIds']))
              and all(isinstance(identifier, str) and identifier for identifier in artifact['primitiveIds'])
              and set(artifact['primitiveIds']) == set(intended['primitiveIds'])
              and w.resolve(root, artifact.get('geometryReceiptFile', '')) == geometry_path
              and artifact.get('geometryReceiptSha256') == w.file_hash(geometry_path, hashes)
              and isinstance(observed_binding, dict)
              and all(observed_binding.get(key) == value for key, value in expected_binding.items())
              and observed_binding.get('requirementsSha256') == expected_binding.get('requirementsSha256')
              and observed_binding.get('expressionSha256') == expected_binding.get('expressionSha256')
              and isinstance(artifact.get('typedSteps'), list) and 1 <= len(artifact['typedSteps']) <= 499
              and all(wire_step(step) for step in artifact['typedSteps'])
              and artifact.get('netPolicies') == scope_policies
              and artifact.get('visibleNetNames') == hints['visibleNetNames']
              and artifact.get('uncoveredConnections') == [],
              'Open route artifact does not bind the current plan, geometry receipt and scope')
    if intended.get('componentKeys'):
        measured, _, read_ok = value(w.read(geometry_path).get('receipt', {}).get('readback'))
        w.require(read_ok, 'Instance property steps lack successful native measurement')
        actual = {part['primitiveId']: part for part in measured.get('components', [])}
        changes = []
        fields = {'name', 'manufacturer', 'manufacturerId', 'supplier', 'supplierId', 'addIntoBom', 'addIntoPcb', 'otherProperty'}
        for part in plan['semanticTarget']['components']:
            if part['key'] not in intended['componentKeys']:
                continue
            identifier = plan['semanticTarget']['idMap'][part['key']]
            props = part.get('properties', {})
            w.require(identifier in actual and set(props) <= fields, 'Instance values cannot replace geometry or identity')
            patch = {key: val for key, val in props.items() if key not in actual[identifier] or actual[identifier][key] != val}
            if patch:
                changes.append({'action': 'schematic.component.modify', 'payload': {'primitiveId': identifier, 'patch': patch}})
        artifact = {**artifact, 'typedSteps': changes + artifact['typedSteps']}
    return artifact, [str(artifact_path), str(geometry_path)]


def labels_artifact(w, root, plan_path, plan, packet, report, intended, hashes):
    index = plan['stages'].index(intended)
    wire_index = next(i for i, stage in enumerate(plan['stages'][:index]) if stage['id'] == intended['labelsFrom'])
    directory = w.resolve(root, plan['executionReceipts']['directory'])
    file, wire_file = directory / ('stage-%02d.labels.json' % (index + 1)), directory / ('stage-%02d.json' % (wire_index + 1))
    compilation = report.get('compilation', {})
    w.require(set(compilation) == {'file', 'sha256'} and w.resolve(root, compilation.get('file', '')) == file
              and file.is_file() and wire_file.is_file() and w.file_hash(file, hashes) == compilation['sha256'],
              'Labels need their unchanged managed compiler and wire receipt')
    artifact, wired = w.read(file), w.read(wire_file)
    wire_stage = plan['stages'][wire_index]
    source_report = wired.get('receipt', {})
    source_execution, _, source_ok = value(source_report.get('execution'))
    wire_steps = wire_stage['steps']
    paths = [str(file), str(wire_file)]
    if wire_stage.get('routeFrom'):
        compiled, route_paths = route_artifact(w, root, plan_path, plan, wired, source_report, wire_stage, hashes)
        wire_steps = compiled['typedSteps']
        paths += route_paths
    rows = source_execution.get('results', [])
    w.require(source_ok and wired.get('state') == 'settled' and source_report.get('ok') is True
              and source_report.get('partial') is False and wired.get('stage', {}).get('completed') is True
              and len(rows) == len(wire_steps), 'Labels depend on a successful complete wire stage')
    created = {row.get('result', {}).get('primitiveId'): step['payload'].get('net')
               for row, step in zip(rows, wire_steps) if row.get('ok') is True and step.get('action') == 'schematic.wire.create'}
    expected = prefix_binding(w, root, plan, wire_stage, wired)
    binding, step = artifact.get('binding', {}), artifact.get('typedStep', {})
    payload, visible = step.get('payload', {}), plan['layoutHints']['visibleNetNames']
    ids, labels = payload.get('wireIds', []), payload.get('labels', [])
    w.require(artifact.get('schemaVersion') == 'easyeda.labels/1' and w.resolve(root, artifact.get('planFile', '')) == plan_path
              and artifact.get('planSha256') == packet['sha256'] and artifact.get('target') == plan['target']
              and artifact.get('stage') == {'id': intended['id'], 'check': intended['check']}
              and w.resolve(root, artifact.get('wireReceiptFile', '')) == wire_file
              and artifact.get('wireReceiptSha256') == w.file_hash(wire_file, hashes)
              and all(binding.get(key) == expected.get(key) for key in ('semanticTargetSha256', 'sourcesSha256', 'expressionSha256'))
              and binding.get('wireStageSha256') == expected['stageSha256'] and artifact.get('visibleNetNames') == visible
              and artifact.get('typedSteps') == [step] and step.get('action') == 'schematic.wire.labels.apply'
              and set(payload) == {'wireIds', 'labels'} and created and all(isinstance(i, str) and i for i in created)
              and isinstance(ids, list) and len(ids) == len(set(ids)) and set(ids) == set(created)
              and isinstance(labels, list) and all(isinstance(label, dict) and set(label) == {'wireId'}
                  and label['wireId'] in created and created[label['wireId']] in visible for label in labels)
              and all(any(created[label['wireId']] == net for label in labels) for net in created.values() if net in visible),
              'Labels artifact differs from its native wire scope, visible names or plan binding')
    return artifact, paths


def requirement_scope(plan, requirement, stage_ids):
    """Resolve declared plan targets to live primitive IDs; never guess from refs."""
    target = plan.get('semanticTarget', {})
    component_keys = set(requirement.get('componentKeys', []))
    for net in target.get('nets', []) if isinstance(target, dict) else []:
        if isinstance(net, dict) and net.get('name') in requirement.get('netNames', []):
            component_keys.update(member.get('componentKey') for member in net.get('members', [])
                                  if isinstance(member, dict) and isinstance(member.get('componentKey'), str))
    page = not component_keys and not requirement.get('netNames') and not requirement.get('stageId')
    ids = []
    mapping = target.get('idMap', {}) if isinstance(target, dict) else {}
    mapped = isinstance(mapping, dict)
    if not page:
        for key in sorted(component_keys):
            value = mapping.get(key) if isinstance(mapping, dict) else None
            if not isinstance(value, str) or not value:
                mapped = False
            else:
                ids.append(value)
    # At a named stage an empty target means that stage's actual observed range.
    if not component_keys and requirement.get('stageId'):
        ids = [value for value in stage_ids if isinstance(value, str) and value]
        mapped = bool(ids)
    return {'kind': 'page' if page else 'local', 'primitiveIds': sorted(set(ids)),
            'componentKeys': sorted(component_keys)}, mapped


def observed_scope(plan, scope, readback):
    """A plan idMap names an expected instance; native readback proves it is that instance."""
    observed, context, read_ok = value(readback)
    read = observed.get('readScope')
    if not (read_ok and native_target(context, plan['target']) and observed.get('partial') is not True
            and isinstance(read, dict) and read.get('concurrentChange') is False
            and read.get('missingIds') == [] and isinstance(read.get('primitiveIds'), list)
            and isinstance(observed.get('components'), list)
            and all(identifier in read['primitiveIds'] for identifier in scope['primitiveIds'])):
        return False
    parts = observed['components']
    if any(not isinstance(part, dict) or not isinstance(part.get('primitiveId'), str) or not part['primitiveId'] for part in parts):
        return False
    components = {part['primitiveId']: part for part in parts}
    if len(components) != len(parts) or any(identifier not in components for identifier in scope['primitiveIds']):
        return False
    semantic = plan.get('semanticTarget', {})
    expected_parts = {part.get('key'): part for part in semantic.get('components', []) if isinstance(part, dict)}
    mapping = semantic.get('idMap', {})
    for key in scope.get('componentKeys', []):
        expected, actual = expected_parts.get(key), components.get(mapping.get(key)) if isinstance(mapping, dict) else None
        if not (isinstance(expected, dict) and isinstance(actual, dict) and actual.get('designator') == expected.get('ref')
                and (not expected.get('uniqueId') or actual.get('uniqueId') == expected.get('uniqueId'))):
            return False
    return True


def design_pins(plan, primitive_ids):
    semantic = plan.get('semanticTarget', {})
    mapping = semantic.get('idMap', {}) if isinstance(semantic, dict) else {}
    targets = {}
    for identifier in primitive_ids:
        parts = [part for part in semantic.get('components', []) if isinstance(part, dict) and mapping.get(part.get('key')) == identifier]
        if len(parts) != 1 or not isinstance(parts[0].get('pins'), list) or not parts[0]['pins']:
            return None
        for pin in parts[0]['pins']:
            if not isinstance(pin, dict) or not isinstance(pin.get('number'), str) or pin.get('state') not in ('connected', 'nc', 'unconnected'):
                return None
            value = None if pin['state'] == 'nc' else pin.get('net')
            if pin['state'] != 'nc' and not isinstance(value, str):
                return None
            net = next((net for net in semantic.get('nets', []) if net.get('name') == value), {})
            if pin['state'] == 'connected' and net.get('match') == 'members':
                members = []
                for member in net.get('members', []):
                    owners = [part for part in semantic.get('components', []) if part.get('key') == member.get('componentKey')]
                    if len(owners) != 1 or mapping.get(owners[0]['key']) not in primitive_ids:
                        return None
                    members.append(owners[0]['ref'] + '.' + member['pinNumber'])
                if len(members) < 2 or len(set(members)) != len(members):
                    return None
                value = {'members': sorted(members)}
            targets[identifier + '.' + pin['number']] = value
    return targets


def requirement_results(plan, stage, settled, executed, stage_valid, report, report_context, execution, execution_context):
    """Derive requirement evidence from native receipts, never from caller claims."""
    results = []
    requirements = plan.get('requirements', [])
    if not isinstance(requirements, list):
        return results
    local, local_context, local_ok = local_rule_facts(report)
    rules = local.get('rules') if local_ok else None
    rule_items = rules.get('items') if isinstance(rules, dict) else None
    target_check, target_context, target_ok = value(report.get('targetCheck'))
    drc, drc_context, drc_ok = value(report.get('drcCheck'))
    for requirement in requirements:
        stage_ids = report.get('primitiveIds') if isinstance(report.get('primitiveIds'), list) else stage.get('primitiveIds', [])
        scope, mapped = requirement_scope(plan, requirement, stage_ids)
        item = {'requirement': requirement, 'status': 'unknown', 'severity': requirement.get('severity'),
                'coverage': 'unavailable', 'scope': scope,
                'evidence': {'kind': 'unavailable'}, 'note': 'No matching native evidence.'}
        applies = requirement.get('stageId') in (None, stage.get('id'))
        due = applies and (requirement.get('when') != 'delivery' or stage.get('check') == 'final')
        if not due:
            item.update(coverage='deferred', note='Requirement is bound to another stage.')
            results.append(item)
            continue
        method, check, when = requirement.get('method'), requirement.get('check'), requirement.get('when')
        if method == 'checker' and check == 'source_binding' and when == 'before_write':
            item.update(status='pass', coverage='checked', evidence={'kind': 'source_binding', 'sourceId': requirement.get('sourceId')},
                        note='Pinned source hash was revalidated while loading the plan.')
        elif when == 'after_edit' and method == 'checker' and check in {'identity', 'pin_nets', 'valid_wires', 'pin_exit', 'text_overlap', 'network_scope', 'target'}:
            if isinstance(report.get('edit'), dict) and report['edit'].get('complete') is False:
                item.update(coverage='deferred', note='Incomplete modification unit; post-edit evidence is deferred.')
            elif not (settled and executed and local_ok and native_target(local_context, plan['target'])):
                item['note'] = 'Execution or native target context is incomplete.'
            elif scope['kind'] == 'page':
                item['note'] = 'A local post-edit rule cannot prove a page-scoped requirement.'
            elif not mapped:
                item['note'] = 'Requirement targets lack current semanticTarget.idMap bindings.'
            elif not isinstance(rules, dict) or not isinstance(rule_items, list) or not isinstance(rules.get('scope'), list):
                item['note'] = 'Connector local-rule receipt is unavailable.'
            elif not set(scope['primitiveIds']) <= set(rules['scope']):
                item['note'] = 'Connector local-rule scope does not cover every bound target.'
            elif not observed_scope(plan, scope, report.get('readback')):
                item['note'] = 'Native local readback does not prove the mapped target identity and fresh scope.'
            else:
                matches = [row for row in rule_items if isinstance(row, dict) and row.get('id') == check]
                if len(matches) != 1:
                    item['note'] = 'Connector local-rule item is missing or ambiguous.'
                else:
                    row = matches[0]
                    status = row.get('status') if row.get('status') in ('pass', 'fail', 'unknown') else 'unknown'
                    coverage = row.get('coverage') if row.get('coverage') in ('checked', 'unavailable', 'not_applicable') else 'unavailable'
                    if check == 'pin_nets' and rules.get('mode') == 'design':
                        edit = report.get('edit') if isinstance(report.get('edit'), dict) else {}
                        edit_ids = edit.get('primitiveIds')
                        expected = design_pins(plan, edit_ids) if isinstance(edit_ids, list) and all(isinstance(v, str) and v for v in edit_ids) else None
                        if not isinstance(expected, dict) or not set(scope['primitiveIds']) <= set(edit_ids) or edit.get('expectedPins') != expected:
                            item['note'] = 'Design pin targets do not exactly match the independent semantic target.'
                            results.append(item)
                            continue
                    item.update(status=status if coverage == 'checked' else 'unknown', coverage=coverage,
                                severity='error' if row.get('severity') == 'error' else item['severity'],
                                evidence={'kind': 'local_rule', 'id': check}, note='Connector local-rule receipt for the bound scope.')
        elif when == 'delivery' and method == 'checker' and check in {'schematic.target.check', 'identity', 'pin_nets'}:
            if not (settled and executed and stage.get('check') == 'final'
                    and native_target(target_context, plan['target'])):
                item['note'] = 'Final native target receipt is unavailable or incomplete.'
            elif stage_valid and target_check.get('status') == 'pass' and target_check.get('verified') is True and target_check.get('partial') is False:
                item.update(status='pass', coverage='checked', evidence={'kind': 'target_check'},
                            note='Independent full-page native target comparison passed.')
            elif target_check.get('status') == 'fail' and check == 'schematic.target.check':
                item.update(status='fail', coverage='checked' if target_ok and target_check.get('partial') is False else 'unavailable',
                            evidence={'kind': 'target_check'}, note='Independent native target comparison failed; incomplete coverage is retained.')
            elif target_check.get('status') == 'fail':
                item.update(coverage='checked' if target_ok and target_check.get('partial') is False else 'unavailable', evidence={'kind': 'target_check'},
                            note='A composite target failure cannot be attributed to this individual requirement.')
        elif when == 'delivery' and method == 'native' and check == 'drc':
            counts = drc.get('summary') if isinstance(drc, dict) else None
            if not (settled and executed and stage.get('check') == 'final' and stage_valid and drc_ok and drc.get('partial') is not True
                    and native_target(drc_context, plan['target']) and drc.get('countsAvailable') is True
                    and isinstance(counts, dict) and all(isinstance(counts.get(k), int) and not isinstance(counts[k], bool)
                                                        and counts[k] >= 0 for k in ('fatal', 'error', 'unknown'))):
                item['note'] = 'Native DRC receipt or explicit severity counts are unavailable.'
            elif counts['fatal'] or counts['error']:
                item.update(status='fail', severity='error', coverage='checked', evidence={'kind': 'drc'}, note='Native DRC reported fatal or error findings.')
            elif counts['unknown']:
                item.update(severity='error', coverage='unavailable', evidence={'kind': 'drc'}, note='Native DRC has unclassified findings.')
            else:
                item.update(status='pass', severity='error', coverage='checked', evidence={'kind': 'drc'}, note='Native DRC counts contain no blocking findings; warnings do not block.')
        results.append(item)
    return results


def requirement_check(results, final=False):
    applicable = [item for item in results if item['coverage'] != 'deferred' or final]
    errors = [item for item in applicable if item.get('severity') == 'error']
    return ('fail' if any(item['status'] == 'fail' for item in errors) else
            'unknown' if any(item['status'] != 'pass' for item in errors) else 'pass')


def symbol_source_evidence(w, root, plan, report, hashes):
    outer = report.get('targetCheck', {}).get('structuredContent', {})
    evidence = outer.get('nativeSymbolEvidence')
    if evidence is None:
        return []
    original, old_context, _ = value(evidence.get('originalTargetCheck'))
    current, context, ok = value(evidence.get('readback'))
    exported, export_context, export_ok = value(evidence.get('exportReceipt'))
    findings = original.get('findings', [])
    w.require(evidence.get('kind') == 'official-project-symbol-source' and original.get('status') == 'unknown'
              and findings and all(row.get('code') == 'library_identity_domain_unknown' and row.get('status') == 'unknown'
                  and row.get('field') == 'symbol' for row in findings)
              and ok and export_ok and all(native_target(c, plan['target']) for c in (context, old_context, export_context))
              and current.get('readScope', {}).get('complete') is True and current['readScope'].get('concurrentChange') is False,
              'Symbol-source recovery lacks bound original unknown and fresh native page/export')
    file = w.resolve(root, exported.get('artifactPath', ''))
    w.require(file.is_file() and 0 < file.stat().st_size <= 8 * 1024 * 1024
              and w.file_hash(file, hashes) == evidence.get('archiveSha256'), 'Native symbol source archive changed')
    with zipfile.ZipFile(file) as archive:
        entries = archive.infolist()
        sources = [e for e in entries if not e.is_dir() and re.fullmatch(r'[^/\\]+\.epru', e.filename, re.I)]
        w.require(len(entries) <= 2048 and len(sources) == 1 and sources[0].file_size <= 64 * 1024 * 1024,
                  'Native source archive scope or size invalid')
        with archive.open(sources[0]) as stream:
            raw = stream.read(64 * 1024 * 1024 + 1)
        w.require(len(raw) == sources[0].file_size, 'Native source length changed')
    rows = raw.decode('utf-8').splitlines()
    w.require(len(rows) <= 200000, 'Native source exceeds row budget')
    page_count, active, symbols = 0, None, {}
    for line in rows:
        if not line.strip():
            continue
        header_text, payload = line.strip().removesuffix('|').split('||', 1)
        header, data = json.loads(header_text), json.loads(payload)
        if header.get('type') == 'DOCHEAD':
            active = None
            if data.get('docType') == 'SCH_PAGE' and data.get('uuid') == plan['target']['documentUuid']:
                page_count += 1
            if data.get('docType') == 'SYMBOL':
                active = data.get('uuid')
                w.require(isinstance(active, str) and active not in symbols, 'Native symbol instance duplicated')
                symbols[active] = []
        elif active and header.get('type') == 'META':
            symbols[active].append(data.get('source'))
    w.require(page_count == 1, 'Native source does not contain exactly the target page')
    parts = current.get('components', [])
    mappings = evidence.get('mappings', [])
    lowered = copy.deepcopy(plan['semanticTarget'])
    w.require(isinstance(mappings, list) and len(mappings) == len(findings), 'Symbol mapping coverage differs')
    for finding in findings:
        wanted = next(p for p in plan['semanticTarget']['components'] if p['key'] == finding['component'])
        identifier = plan['semanticTarget']['idMap'][wanted['key']]
        matches = [p for p in parts if p.get('primitiveId') == identifier and p.get('designator') == wanted['ref']
                   and (not wanted.get('uniqueId') or p.get('uniqueId') == wanted['uniqueId'])]
        mapped = [m for m in mappings if m.get('primitiveId') == identifier]
        w.require(len(matches) == len(mapped) == 1, 'Symbol native owner or mapping ambiguous')
        actual, source = matches[0].get('symbol', {}), mapped[0]
        expected = wanted.get('deviceResolution', {}).get('symbolSource') or wanted['symbol']
        w.require(actual.get('uuid') == finding.get('actual') == source.get('instanceUuid')
                  and source.get('sourceKind') == 'project-epro2' and source.get('uuid') == expected.get('uuid')
                  and source.get('libraryUuid') == actual.get('libraryUuid') == expected.get('libraryUuid')
                  and re.fullmatch('[a-fA-F0-9]{32}', source['uuid'])
                  and symbols.get(source['instanceUuid']) == [source['uuid'] + '|' + source['libraryUuid']],
                  'Symbol identity is not proven by exact native DOCHEAD/META.source')
        bound_part = next(part for part in lowered['components'] if part['key'] == wanted['key'])
        bound_part['symbol']['uuid'] = source['instanceUuid']
        bound_part.get('deviceResolution', {}).pop('symbolSource', None)
    bound, bound_context, bound_ok = value(evidence.get('boundInstanceTargetCheck'))
    w.require(evidence.get('nativeTarget') == lowered and native_target(bound_context, plan['target'])
              and bound == outer.get('result') and bound_ok == (bound.get('status') == 'pass'),
              'Bound native target changed facts beyond source-proven symbol identities')
    return [str(file)]


def import_receipt(w, root, binding, filename, hashes=None, _prefix=False, _memo=None):
    validate(w.require, binding, 'schematic')
    hashes = {} if hashes is None else hashes
    _memo = {} if _memo is None else _memo
    memo_key = (str(w.resolve(root, filename)), binding['sha256'], _prefix)
    if memo_key in _memo:
        return _memo[memo_key]
    plan_path, plan, paths = plan_inputs(w, root, binding, hashes)
    path = w.resolve(root, filename)
    packet = w.read(path)
    w.require(isinstance(packet, dict) and packet.get('schemaVersion') == 'easyeda.execution/1'
              and packet.get('dryRun') is False, 'not a real Open execution receipt')
    w.require(w.resolve(root, packet.get('planFile', '')) == plan_path
              and ((_prefix and type(packet.get('revision')) is int and 0 < packet['revision'] <= plan['revision'])
                   or (packet.get('sha256') == binding['sha256'] and packet.get('revision') == plan['revision']))
              and packet.get('target') == plan['target'], 'Open receipt belongs to a different plan or target')
    sources = [{k: s[k] for k in ('id', 'path', 'sha256')} for s in plan['sources']]
    w.require(packet.get('sources') == sources, 'Open receipt source binding differs')
    stage = packet.get('stage', {})
    w.require(isinstance(stage, dict), 'Open receipt stage required')
    matches = [s for s in plan['stages'] if s.get('id') == stage.get('id')]
    w.require(len(matches) == 1 and matches[0].get('check') == stage.get('check'), 'Open receipt stage differs')
    w.require(stage.get('check') in ('geometry', 'final'), 'unsupported Open stage check')
    intended = matches[0]
    if packet.get('recheckOf') is not None:
        link = packet['recheckOf']
        index = plan['stages'].index(intended)
        original = w.resolve(root, plan['executionReceipts']['directory']) / ('stage-%02d.json' % (index + 1))
        w.require(index == len(plan['stages']) - 1 and intended.get('check') == 'final' and intended.get('steps') == []
                  and not intended.get('routeFrom') and not intended.get('labelsFrom')
                  and path == original.with_suffix('.recheck.json') and set(link) == {'file', 'sha256'}
                  and w.resolve(root, link['file']) == original and original.is_file()
                  and w.file_hash(original, hashes) == link['sha256'], 'Read-only final recheck lost its immutable failure')
        failed = w.read(original)
        failure = failed.get('receipt', {})
        w.require(failed.get('schemaVersion') == 'easyeda.execution/1' and failed.get('state') == 'settled'
                  and failed.get('dryRun') is False and failed.get('sha256') == packet['sha256']
                  and failed.get('revision') == packet['revision'] and failed.get('target') == plan['target']
                  and failed.get('sources') == packet['sources'] and failed.get('binding') == packet.get('binding')
                  and failed.get('stage') == {'id': intended['id'], 'check': 'final', 'completed': False}
                  and failure.get('execution') is None and failure.get('ok') is False and failure.get('partial') is True
                  and failure.get('targetCheck', {}).get('structuredContent', {}).get('result', {}).get('status') in ('fail', 'unknown'),
                  'Final recheck cannot supersede successful, mutating or uncertain execution')
        paths.append(str(original))
    if _prefix:
        prefix_binding(w, root, plan, intended, packet)
    report, report_context, report_ok = value(packet.get('receipt'))
    settled = False
    try:
        start, end = (datetime.fromisoformat(packet[k].replace('Z', '+00:00')) for k in ('startedAt', 'completedAt'))
        settled = packet.get('state') == 'settled' and start.tzinfo is not None and end.tzinfo is not None and end >= start
    except (ValueError, KeyError, TypeError, AttributeError):
        pass
    w.require(packet.get('state') in ('pending', 'settled', 'unknown'), 'invalid Open receipt state')
    if settled:
        w.require(report.get('target') == plan['target'] and report.get('sha256') == packet['sha256']
                  and report.get('stage') == stage, 'Open report binding differs')
    execution, execution_context, execution_ok = value(report.get('execution'))
    steps = intended.get('steps', [])
    artifact_paths = symbol_source_evidence(w, root, plan, report, hashes) if report.get('targetCheck') else []
    if (intended.get('routeFrom') and settled and stage.get('completed') is True
            and report_ok and execution_ok and execution.get('ok') is True):
        artifact, artifact_paths = route_artifact(w, root, plan_path, plan, packet, report, intended, hashes)
        steps = artifact['typedSteps']
    if (intended.get('labelsFrom') and settled and stage.get('completed') is True
            and report_ok and execution_ok and execution.get('ok') is True):
        artifact, artifact_paths = labels_artifact(w, root, plan_path, plan, packet, report, intended, hashes)
        steps = artifact['typedSteps']
    results = execution.get('results', [])
    executed = (not steps and stage.get('check') == 'final') or (
        execution_ok and execution.get('ok') is True and execution.get('partial') is not True
        and execution.get('completed') == len(steps) and execution.get('total') == len(steps)
        and isinstance(results, list) and len(results) == len(steps)
        and all(isinstance(r, dict) and r.get('ok') is True and r.get('action') == s.get('action')
                for r, s in zip(results, steps)))
    scope = report.get('primitiveIds', [])
    if stage.get('check') == 'geometry':
        observed, context, read_ok = value(report.get('readback'))
        read_scope, components = observed.get('readScope', {}), observed.get('components', [])
        ids = set(intended.get('primitiveIds', []))
        ids.update(s['payload']['primitiveId'] for s in steps if s.get('action') == 'schematic.component.modify'
                   and isinstance(s.get('payload', {}).get('primitiveId'), str))
        created_missing = False
        for r in results if isinstance(results, list) else []:
            if r.get('action') not in ('schematic.component.place', 'schematic.component.modify'):
                continue
            created, _, _ = value(r)
            id_value = created.get('component', {}).get('primitiveId') or created.get('primitiveId')
            if isinstance(id_value, str) and id_value:
                ids.add(id_value)
            elif r.get('action') == 'schematic.component.place':
                created_missing = True
        display = display_stage(intended)
        profile_ok = report.get('readbackProfile', 'geometry') == ('display' if display else 'geometry')
        observed_ids = {c.get('primitiveId') for c in components if isinstance(c, dict) and (display or measured(c))} if isinstance(components, list) else set()
        stage_valid = (bool(ids) and not created_missing and isinstance(scope, list) and len(scope) == len(ids) and set(scope) == ids
                       and read_ok and native_target(context, plan['target']) and observed.get('partial') is not True
                       and isinstance(read_scope, dict) and read_scope.get('concurrentChange') is False
                       and read_scope.get('missingIds') == [] and isinstance(read_scope.get('primitiveIds'), list)
                       and len(read_scope['primitiveIds']) == len(ids) and set(read_scope['primitiveIds']) == ids
                       and ids == observed_ids and len(components) == len(ids)
                       and profile_ok and (not display or display_attributes(steps, execution, ids, report.get('readback'))))
        coverage = {'scope': 'local', 'primitiveIds': scope, 'checked': ['native_display_attributes', 'scoped_instance_identity'] if display else ['measured_geometry'],
                    'notChecked': ['electrical_target', 'visual_review', 'persistence']}
    else:
        target, context, read_ok = value(report.get('targetCheck'))
        stage_valid = (read_ok and native_target(context, plan['target']) and target.get('status') == 'pass'
                       and target.get('verified') is True and target.get('partial') is False)
        if packet.get('recheckOf') is not None and stage.get('completed') is True:
            delivery, delivery_context, delivery_ok = value(report.get('deliveryCheck'))
            ids = list(plan['semanticTarget']['idMap'].values())
            identities = {plan['semanticTarget']['idMap'][part['key']]: {'designator': part['ref'],
                          **({'uniqueId': part['uniqueId']} if part.get('uniqueId') else {})}
                          for part in plan['semanticTarget']['components']}
            verification = delivery.get('designVerification', {})
            stage_valid = (stage_valid and delivery_ok and native_target(delivery_context, plan['target'])
                           and delivery.get('rules', {}).get('status') == 'pass'
                           and verification.get('primitiveIds') == ids
                           and verification.get('expectedPins') == design_pins(plan, ids)
                           and verification.get('expectedIdentities') == identities)
        coverage = {'scope': 'page', 'checked': ['independent_schematic_target'],
                    'notChecked': ['visual_review', 'design_suitability', 'persistence']}
    passed = settled and report_ok and executed and stage_valid and stage.get('completed') is True
    paths.extend(artifact_paths + [str(path)])
    def check(key, status, note, method, covered):
        return {'id': key, 'status': status, 'note': note, 'evidence': [str(path)],
                'requirement': {'id': key, 'stage': stage['id']},
                'binding': {'target': plan['target'], 'planRevision': plan['revision'], 'planSha256': binding['sha256']},
                'enforcement': {'kind': 'checker', 'method': method}, 'coverage': covered}
    checks = [check('open_stage_receipt', 'pass' if passed else 'unknown',
                    'Actual stage execution and declared readback only; no visual or saved claim.',
                    'Open typed batch + declared readback', coverage)]
    local, local_context, local_ok = local_rule_facts(report)
    rules = local.get('rules') if local_ok and native_target(local_context, plan['target']) else None
    if report.get('designCheck') and not isinstance(rules, dict):
        checks.append(check('open_local_rules', 'unknown', 'Independent design check is missing its original edit or target binding.',
                            'Connector local-rules', {'scope': 'local', 'primitiveIds': scope, 'checked': [], 'notChecked': ['design_readback']}))
    if isinstance(rules, dict):
        items = rules.get('items', [])
        required = [c for c in items if isinstance(c, dict) and c.get('severity') == 'error'
                    and c.get('coverage') != 'not_applicable'] if isinstance(items, list) else []
        rules_status = ('fail' if rules.get('status') == 'fail' or any(c.get('status') == 'fail' for c in required) else
                        'pass' if rules.get('status') == 'pass' and required and all(c.get('status') == 'pass' and c.get('coverage') == 'checked'
                                                  for c in required) and settled else 'unknown')
        checks.append(check('open_local_rules', rules_status, 'Hard local observations only; WARN candidates are retained separately.',
                            'Connector local-rules', {'scope': 'local', 'primitiveIds': rules.get('scope', []),
                            'checked': [c.get('id') for c in required if c.get('coverage') == 'checked'],
                            'notChecked': [c.get('id') for c in required if c.get('coverage') != 'checked']}))
    if stage.get('check') == 'final':
        result_status = target.get('status') if settled and native_target(context, plan['target']) else 'unknown'
        if result_status not in ('pass', 'fail', 'unknown') or (result_status == 'pass' and not stage_valid):
            result_status = 'unknown'
        checks.append(check('open_target_check', result_status,
                            'Independent native target comparison; does not prove design, visual or persistence.',
                            'schematic.target.check', coverage))
    requirement_rows = requirement_results(plan, stage, settled, executed, stage_valid, report, report_context,
                                           execution, execution_context)
    # Read only managed predecessor files. Recompute raw evidence; never trust saved requirementResults.
    predecessor_items = report.get('predecessors', [])
    stage_index = plan['stages'].index(intended)
    if predecessor_items:
        w.require(isinstance(predecessor_items, list) and len(predecessor_items) == stage_index
                  and isinstance(plan.get('executionReceipts'), dict), 'Open predecessor chain is incomplete')
        for index, link in enumerate(predecessor_items):
            expected_file = w.resolve(root, plan['executionReceipts']['directory']) / ('stage-%02d.json' % (index + 1))
            w.require(isinstance(link, dict) and w.resolve(root, link.get('file', '')) == expected_file
                      and expected_file.is_file() and w.file_hash(expected_file, hashes) == link.get('sha256'),
                      'Open predecessor file is missing, changed or out of order')
            previous_packet = w.read(expected_file)
            w.require(previous_packet.get('stage', {}).get('id') == plan['stages'][index]['id'], 'Open predecessor stage differs')
            previous = import_receipt(w, root, binding, str(expected_file), hashes, _prefix=True, _memo=_memo)
            w.require(any(c['id'] == 'open_stage_receipt' and c['status'] == 'pass' for c in previous['checks'])
                      and datetime.fromisoformat(previous_packet['completedAt'].replace('Z', '+00:00')) <= start,
                      'Open predecessor was not completed before this stage')
            paths.extend(previous['paths'])
            for row in requirement_rows:
                requirement = row['requirement']
                if row['coverage'] != 'deferred' or requirement.get('stageId') != plan['stages'][index]['id'] or requirement['when'] == 'delivery':
                    continue
                matching = [item for item in previous['summary']['requirementResults'] if item['requirement'] == requirement]
                if len(matching) != 1 or matching[0]['coverage'] == 'deferred':
                    continue
                old = matching[0]
                touched = requirement['when'] == 'after_edit' and any(stage_invalidates_requirement(
                    s, requirement['check'], report if s['id'] == intended['id'] else value(w.read(
                        w.resolve(root, plan['executionReceipts']['directory']) / ('stage-%02d.json' % (j + 1)))['receipt'])[0], plan['target'])
                    for j, s in enumerate(plan['stages'][index + 1:stage_index + 1], start=index + 1))
                if touched:
                    refreshed = False
                    for fresh_index in range(stage_index, index, -1):
                        candidate = plan['stages'][fresh_index]
                        current = fresh_index == stage_index
                        fresh_file = w.resolve(root, plan['executionReceipts']['directory']) / ('stage-%02d.json' % (fresh_index + 1))
                        observed = report if current else value(w.read(fresh_file)['receipt'])[0]
                        if not current and observed.get('ok') is not True:
                            continue
                        if any(stage_invalidates_requirement(later, requirement['check'], report if j == stage_index else value(w.read(
                                w.resolve(root, plan['executionReceipts']['directory']) / ('stage-%02d.json' % (j + 1)))['receipt'])[0], plan['target'])
                               for j, later in enumerate(plan['stages'][fresh_index + 1:stage_index + 1], start=fresh_index + 1)):
                            continue
                        fresh_plan = dict(plan, requirements=[dict(requirement, stageId=candidate['id'])])
                        actual, actual_context, _ = value(observed.get('execution'))
                        fresh = requirement_results(fresh_plan, dict(candidate, completed=True), settled if current else True,
                            executed if current else True, stage_valid if current else True, observed, plan['target'], actual, actual_context)[0]
                        old_scope, fresh_scope = old['scope'], fresh['scope']
                        if (fresh['coverage'] != 'checked' or old_scope.get('kind') != 'local' or not old_scope.get('primitiveIds')
                                or not set(old_scope['primitiveIds']) <= set(fresh_scope.get('primitiveIds', []))):
                            continue
                        row.update(fresh, requirement=requirement, scope=old_scope)
                        row['evidence'] = dict(fresh['evidence'], stageId=candidate['id'], validity='fresh_covering_check')
                        if not current:
                            row['evidence'].update(receiptFile=str(fresh_file), receiptSha256=w.file_hash(fresh_file, hashes))
                        row['note'] = 'The same predicate was re-evaluated on current native evidence covering the original scope; prior history was not rewritten.'
                        refreshed = True
                        break
                    if not refreshed:
                        row.update(coverage='unavailable', note='A later stage changes or cannot prove preservation of this check dependency; bind a fresh check.')
                    continue
                row.update(old)
                row['evidence'] = dict(old['evidence'], stageId=plan['stages'][index]['id'], receiptFile=str(expected_file),
                                       receiptSha256=link['sha256'], validity='post_stage')
                row['note'] += ' Recomputed from the completed predecessor; later stages preserve this predicate. This is historical stage evidence, not a new electrical read.'
    elif stage_index and plan.get('executionReceipts'):
        w.require(False, 'Open managed stage needs its original predecessor chain')
    if requirement_rows:
        requirement_status = requirement_check(requirement_rows, stage.get('check') == 'final')
        checks.append(check('open_requirements', requirement_status,
                            'Recomputed from bound sources and native receipts; declarations and caller statuses are not evidence.',
                            'Open requirement adapter', {'items': requirement_rows}))
    imported = {'checks': checks, 'paths': list(dict.fromkeys(paths)), 'summary': {'stage': stage, 'state': packet['state'],
            'target': plan['target'], 'planSha256': binding['sha256'], 'receiptFile': str(path),
            'acceptanceScope': plan.get('acceptanceScope', 'page'),
            'wholePageComplete': False, 'rules': rules,
            'requirementResults': requirement_rows,
            'limits': report.get('limits', ['Unknown or unfinished execution; read back before retrying.'])}}
    _memo[memo_key] = imported
    return imported


def stage_invalidates_requirement(stage, check, report, target):
    if not stage.get('steps') and not stage.get('routeFrom'):
        return False
    actual, _, report_ok = value(report)
    native, context, ok = value(actual.get('execution'))
    rules = native.get('rules') if isinstance(native.get('rules'), dict) else {}
    edit = actual.get('edit') if isinstance(actual.get('edit'), dict) else {}
    display_verified = (display_stage(stage) and report_ok and actual.get('stage', {}).get('completed') is True
                        and edit.get('complete') is not False and ok and native_target(context, target)
                        and rules.get('status') == 'pass'
                        and any(item.get('id') == 'identity' and item.get('status') == 'pass' and item.get('coverage') == 'checked'
                                for item in rules.get('items', []) if isinstance(item, dict))
                        and display_attributes(stage['steps'], native, stage.get('primitiveIds', []), actual.get('readback')))
    if display_verified:
        return check not in {'identity', 'pin_nets', 'valid_wires', 'pin_exit', 'network_scope', 'target'}
    # Remote members can share a changed net; declared local IDs are not an electrical boundary.
    return True


def check_visual(w, root, binding, stage_filename, check, hashes=None):
    """Bind the AI judgement to an actual current image and the saved runtime task."""
    _, plan, paths = plan_inputs(w, root, binding, hashes)
    w.require(plan.get('acceptanceScope') == 'local_display' and isinstance(check, dict)
              and check.get('status') == 'pass' and isinstance(check.get('note'), str) and check['note'].strip(),
              'Local visual review requires an observed judgement')
    imported = import_receipt(w, root, binding, stage_filename, hashes)
    w.require(any(c['id'] == 'open_stage_receipt' and c['status'] == 'pass' for c in imported['checks']),
              'Visual review needs the actual completed stage')
    report, _, _ = value(w.read(w.resolve(root, stage_filename))['receipt'])
    execution = report['execution'].get('structuredContent', report['execution'])
    files = {}
    for field in ('runtime_state', 'image_receipt'):
        w.require(isinstance(check.get(field), str) and check[field].strip(), 'Visual review needs ' + field)
        path = w.resolve(root, check[field])
        w.require(path.is_file(), 'Missing visual evidence: ' + field)
        files[field] = w.read(path)
        paths.append(str(path))
    state = files['runtime_state'].get('structuredContent', files['runtime_state'])
    image = files['image_receipt'].get('structuredContent', files['image_receipt'])
    w.require(state.get('source') == 'daemon-receipts' and isinstance(state.get('receipts'), list)
              and image.get('type') == 'response' and image.get('ok') is True
              and files['image_receipt'].get('isError') is not True, 'Incomplete native visual evidence')
    edits = [r for r in state['receipts'] if r.get('requestId') == execution.get('id')]
    captures = [r for r in state['receipts'] if r.get('requestId') == image.get('id')]
    w.require(len(edits) == len(captures) == 1, 'Visual evidence lacks a unique edit/image request')
    edit, capture = edits[0], captures[0]
    checks = edit.get('checks', {})
    review = checks.get('visualReview', {})
    generation = edit.get('generation')
    w.require(type(generation) is int and generation > 0 and edit.get('status') == 'saved'
              and checks.get('status') == 'pass' and review.get('generation') == generation
              and review.get('source') in ('ai', 'engineer') and review.get('screenshotRequestId') == image.get('id')
              and capture.get('generation') == capture.get('startedGeneration') == generation
              and capture.get('action') == 'schematic.export.image' and capture.get('imageAvailable') is True
              and capture.get('status') == 'action_finished' and not edit.get('historical') and not capture.get('historical')
              and isinstance(edit.get('windowId'), str) and edit['windowId']
              and edit['windowId'] == capture.get('windowId')
              and native_target(edit, plan['target']) and native_target(capture, plan['target'])
              and native_target(image.get('context', {}), plan['target']), 'Visual review targets a stale/different task')
    documents = [d for d in state.get('documents', []) if d.get('windowId') == edit['windowId']
                 and native_target(d.get('context', {}), plan['target'])]
    w.require(len(documents) == 1 and documents[0].get('generation') == generation
              and documents[0].get('mutationAttemptedSinceConfirmedSave') is False
              and documents[0].get('checks', {}).get('result', {}).get('visualReview') == review,
              'Visual runtime state changed after the reviewed modification')
    ids = {identifier for stage in plan['stages'] for identifier in stage['primitiveIds']}
    scope = image.get('result', {}).get('selectedPrimitiveIds')
    w.require(isinstance(scope, list) and len(scope) == len(set(scope)) and ids <= set(scope)
              and image['result'].get('scope') == 'selection'
              and image['result'].get('selectedCount') == len(scope)
              and set(capture.get('imageScope', [])) == set(scope), 'Actual image does not cover the local acceptance scope')
    stage_end = datetime.fromisoformat(w.read(w.resolve(root, stage_filename))['completedAt'].replace('Z', '+00:00'))
    image_at = datetime.fromisoformat(image['createdAt'].replace('Z', '+00:00'))
    reviewed_at = datetime.fromisoformat(review['at'].replace('Z', '+00:00'))
    saved_at = datetime.fromisoformat(documents[0]['savedAt'].replace('Z', '+00:00'))
    w.require(stage_end <= image_at <= reviewed_at < saved_at and reviewed_at.tzinfo is not None,
              'Image/review predates the completed modification')
    artifacts = [a for a in image.get('artifacts', []) if a.get('id') == image['result'].get('artifactId')]
    w.require(len(artifacts) == 1, 'Native image artifact is missing or ambiguous')
    artifact = artifacts[0]
    path = w.resolve(root, artifact.get('path', ''))
    w.require(artifact.get('kind') == 'schematic_export' and artifact.get('mimeType') == 'image/png'
              and path.is_file() and path.stat().st_size == artifact.get('size') == image['result'].get('bytes')
              and w.file_hash(path, hashes) == artifact.get('sha256'), 'Reviewed image changed or is missing')
    paths.extend(imported['paths'] + [str(path)])
    return list(dict.fromkeys(paths))


def import_delivery(w, root, binding, filename, hashes=None):
    """Import the existing Open verifier's result and its immutable delivery chain."""
    hashes = {} if hashes is None else hashes
    plan_path, plan, paths = plan_inputs(w, root, binding, hashes)
    path = w.resolve(root, filename)
    report = w.read(path)
    scope = plan.get('acceptanceScope', 'local' if plan.get('taskKind') == 'local_edit' else 'page')
    w.require(scope in ('page', 'module', 'local') and plan.get('confirmation', {}).get('status') == 'confirmed'
              and isinstance(report, dict) and report.get('schemaVersion') == 'easyeda.delivery-verification/1'
              and report.get('ok') is True and report.get('derivedOnly') is True and report.get('readOnly') is True
              and report.get('target') == plan['target'] and report.get('acceptanceScope') == scope
              and report.get('engineeringAccepted') is False
              and all(report.get(key) is expected for key, expected in (
                  ('wholePageComplete', scope == 'page'), ('moduleComplete', scope == 'module'), ('localComplete', scope == 'local'))),
              'Open delivery verification must pass for the confirmed plan, target and exact acceptance scope')

    def pinned_file(name, digest_value):
        w.require(isinstance(name, str) and name.strip(), 'Open delivery needs an original evidence file')
        dependency = w.resolve(root, name)
        w.require(dependency.is_file() and w.file_hash(dependency, hashes) == digest_value,
                  'Open delivery evidence changed: ' + name)
        paths.append(str(dependency))
        return dependency

    delivery_path = pinned_file(report.get('deliveryFile'), report.get('deliverySha256'))
    delivery = w.read(delivery_path)
    directory = w.resolve(root, plan.get('executionReceipts', {}).get('directory', ''))
    w.require(delivery_path == directory / 'delivery.json' and isinstance(delivery, dict)
              and delivery.get('schemaVersion') == 'easyeda.delivery/1' and delivery.get('state') == 'settled'
              and w.resolve(root, delivery.get('planFile', '')) == plan_path and delivery.get('sha256') == binding['sha256']
              and delivery.get('target') == plan['target'], 'Open delivery belongs to another plan or target, or is unsettled')
    receipt = delivery.get('receipt', {})
    w.require(isinstance(receipt, dict) and receipt.get('saved') is True and receipt.get('partial') is not True
              and receipt.get('target') == plan['target'] and receipt.get('sha256') == binding['sha256'],
              'Open delivery needs its confirmed native save; unknown saves cannot be imported')
    run_path = pinned_file(delivery.get('runResultFile'), delivery.get('runResultSha256'))
    image_path = pinned_file(delivery.get('imagePath'), delivery.get('imageSha256'))
    run, _, run_ok = value(w.read(run_path))
    completed = run.get('completedStages')
    w.require(run_ok and run.get('ok') is True and run.get('partial') is False and run.get('remainingStages') == []
              and isinstance(completed, list) and completed and completed[-1] == plan['stages'][-1]['id']
              and all(identifier in [stage['id'] for stage in plan['stages']] for identifier in completed)
              and w.resolve(root, run.get('planFile', '')) == plan_path and run.get('sha256') == binding['sha256']
              and run.get('target') == plan['target'], 'Open delivery run is incomplete or bound to another plan')
    review = run.get('review', {})
    visual_receipt = receipt.get('visualReviewReceipt', {})
    w.require(isinstance(review, dict) and w.resolve(root, review.get('imagePath', '')) == image_path
              and review.get('imageSha256') == delivery['imageSha256']
              and isinstance(review.get('visualReview'), dict)
              and isinstance(review.get('visualReview', {}).get('window'), str) and review['visualReview']['window']
              and isinstance(visual_receipt, dict) and visual_receipt.get('isError') is not True
              and isinstance(visual_receipt.get('structuredContent'), dict)
              and visual_receipt.get('structuredContent', {}).get('ok') is True and report.get('visualAccepted') is True,
              'Open delivery visual acceptance does not bind the unchanged run image')
    links = report.get('stages')
    w.require(isinstance(links, list) and len(links) == len(plan['stages']), 'Open delivery needs its complete stage chain')
    stage_path = None
    for index, (stage, link) in enumerate(zip(plan['stages'], links)):
        w.require(isinstance(link, dict), 'Open delivery stage link is invalid')
        stage_path = pinned_file(link.get('file'), link.get('sha256'))
        original = directory / ('stage-%02d.json' % (index + 1))
        w.require(stage_path == original or index == len(links) - 1 and stage_path == original.with_suffix('.recheck.json'),
                  'Open delivery stage files are out of order')
        w.require(w.read(stage_path).get('stage', {}).get('id') == stage['id'], 'Open delivery stage identity differs')
    imported = import_receipt(w, root, binding, str(stage_path), hashes)
    w.require(imported['summary']['stage'].get('check') == 'final'
              and all(check['status'] == 'pass' for check in imported['checks']),
              'Open delivery needs a passing original final receipt and all required stage evidence')
    paths.extend(imported['paths'])

    for diff in (receipt.get('preSaveDiff'), report.get('designDiff')):
        w.require(isinstance(diff, dict) and diff.get('status') == 'synced', 'Open delivery design changed or was not compared')
        checked, _, checked_ok = value(diff.get('checker'))
        w.require(checked_ok and checked.get('status') == 'synced' and checked.get('coverage', {}).get('drawingCompared') is True,
                  'Open delivery requires the existing full drawing difference checker')
    final_report, _, _ = value(w.read(stage_path).get('receipt'))
    snapshots = []
    for observation in (final_report.get('finalReadback'), receipt.get('preSaveReadback'), report.get('readback')):
        snapshot, context, read_ok = value(observation)
        inventory, read_scope = snapshot.get('pagePrimitives'), snapshot.get('readScope', {})
        w.require(read_ok and native_target(context, plan['target']) and snapshot.get('partial') is not True
                  and snapshot.get('wiresAvailable') is True and isinstance(snapshot.get('components'), list)
                  and isinstance(inventory, dict) and all(isinstance(inventory.get(key), list) for key in
                      ('components', 'wires', 'buses', 'arcs', 'circles', 'rectangles', 'polygons', 'texts', 'attributes', 'objects'))
                  and isinstance(read_scope, dict) and read_scope.get('complete') is True and read_scope.get('concurrentChange') is False
                  and not read_scope.get('allPages') and not read_scope.get('primitiveIds') and not read_scope.get('missingIds')
                  and all(isinstance(part, dict) and (part.get('componentType') != 'part'
                          or part.get('pinsAvailable') is True and part.get('netlistAvailable') is True)
                          for part in snapshot['components']), 'Open delivery requires complete fresh native page observations')
        snapshots.append(snapshot)
    # Match nativeDesignDiff's JavaScript number/string serialization, not Python's JSON formatting.
    hash_script = """const {createHash}=require('node:crypto');let input='';
process.stdin.setEncoding('utf8');process.stdin.on('data',chunk=>input+=chunk);
process.stdin.on('end',()=>{const {snapshots,target}=JSON.parse(input);
process.stdout.write(JSON.stringify(snapshots.map(snapshot=>createHash('sha256')
.update(JSON.stringify({...snapshot,...target})).digest('hex'))));});"""
    try:
        hashed = subprocess.run(['node', '-e', hash_script], input=json.dumps({'snapshots': snapshots, 'target': plan['target']}),
                                text=True, encoding='utf-8', capture_output=True, timeout=10, check=True)
        native_hashes = json.loads(hashed.stdout)
    except (OSError, subprocess.SubprocessError, ValueError) as error:
        raise ValueError('Open delivery input hashing requires the existing Node runtime: ' + str(error)) from error
    w.require(receipt['preSaveDiff'].get('inputSha256') == native_hashes[:2]
              and report['designDiff'].get('inputSha256') == native_hashes[1:],
              'Open delivery design difference proof does not bind its actual final, pre-save and reopened snapshots')
    persistence = report.get('persistence', {})
    w.require(isinstance(persistence, dict), 'Open delivery persistence proof is unavailable')
    saved = receipt.get('finishReceipt', {})
    saved_result, saved_context, saved_ok = value(saved)
    save_packet = saved.get('structuredContent', {}) if isinstance(saved, dict) else {}
    read_packet = report['readback'].get('structuredContent', {})
    request_ids = persistence.get('requestIds', {})
    w.require(persistence.get('status') == 'verified' and isinstance(request_ids, dict)
              and set(request_ids) == {'save', 'close', 'open', 'readback'}
              and all(isinstance(identifier, str) and identifier for identifier in request_ids.values())
              and len(set(request_ids.values())) == 4 and type(persistence.get('generation')) is int and persistence['generation'] >= 0
              and isinstance(persistence.get('instance'), str) and persistence['instance']
              and saved_ok and isinstance(save_packet, dict) and save_packet.get('ok') is True and saved_result.get('saved') is True
              and native_target(saved_context, plan['target'])
              and request_ids['save'] == save_packet.get('requestId', save_packet.get('id'))
              and request_ids['readback'] == read_packet.get('requestId', read_packet.get('id')),
              'Open delivery lacks the native verifier save/close/open/readback proof')
    try:
        saved_at, completed_at, read_at, verified_at = (datetime.fromisoformat(stamp.replace('Z', '+00:00')) for stamp in
            (save_packet['createdAt'], delivery['completedAt'], read_packet['createdAt'], report['verifiedAt']))
        ordered = all(stamp.tzinfo is not None for stamp in (saved_at, completed_at, read_at, verified_at)) and saved_at <= completed_at <= read_at <= verified_at
    except (KeyError, TypeError, ValueError, AttributeError):
        ordered = False
    w.require(ordered, 'Open delivery verification must follow its settled save and fresh reopened readback')
    delivery_rules = [rule for rule in plan.get('requirements', []) if rule['when'] == 'delivery']
    target, target_context, target_ok = value(report.get('targetCheck'))
    target_valid = (target_ok and native_target(target_context, plan['target']) and target.get('status') == 'pass'
                    and target.get('verified') is True and target.get('partial') is False)
    fresh = requirement_results(dict(plan, requirements=delivery_rules), plan['stages'][-1], True, True, target_valid,
                                report, plan['target'], {}, {})
    w.require(target_valid and fresh and requirement_check(fresh, final=True) == 'pass'
              and any(row['requirement']['check'] == 'drc' and row['status'] == 'pass' for row in fresh),
              'Open delivery reopened target and every required DRC check must pass; unknown is not pass')
    paths.extend(symbol_source_evidence(w, root, plan, report, hashes))
    paths = list(dict.fromkeys(paths + [str(path)]))
    coverage = {'scope': scope, 'checked': ['native_save_reopen', 'full_design_diff', 'reopened_target', 'native_drc', 'accepted_image'],
                'notChecked': ['engineering_acceptance']}
    checks = [{'id': key, 'status': 'pass', 'note': note, 'evidence': paths, 'coverage': coverage,
               'binding': {'target': plan['target'], 'planRevision': plan['revision'], 'planSha256': binding['sha256']}}
              for key, note in (('persistence', 'Original Open delivery and native verifier prove save/reopen within the declared scope.'),
                                ('visual_review', 'Original accepted image is unchanged and bound to the verified delivery.'))]
    return {'checks': checks, 'paths': paths, 'summary': {'target': plan['target'], 'planSha256': binding['sha256'],
            'acceptanceScope': scope, 'stageReceipt': str(stage_path), 'receiptFile': str(path), 'deliveryFile': str(delivery_path),
            'savedAt': save_packet['createdAt'], 'wholePageComplete': scope == 'page', 'moduleComplete': scope == 'module',
            'localComplete': scope == 'local', 'engineeringAccepted': False, 'coverage': coverage}}


def import_persistence(w, root, binding, filename, hashes=None):
    """Prove visible local display persistence from pinned native save/reopen receipts."""
    hashes = {} if hashes is None else hashes
    plan_path, plan, paths = plan_inputs(w, root, binding, hashes)
    path = w.resolve(root, filename)
    report = w.read(path)
    if isinstance(report, dict) and report.get('schemaVersion') == 'easyeda.delivery-verification/1':
        return import_delivery(w, root, binding, filename, hashes)
    w.require(isinstance(report, dict) and report.get('schemaVersion') == 'easyeda.persistence/1'
              and w.resolve(root, report.get('planFile', '')) == plan_path
              and report.get('planSha256') == binding['sha256'] and report.get('revision') == plan['revision']
              and report.get('target') == plan['target']
              and report.get('acceptanceScope') == plan.get('acceptanceScope') == 'local_display'
              and plan.get('confirmation', {}).get('status') == 'confirmed',
              'Open persistence requires the confirmed current local display plan and target')
    inputs = report.get('inputs')
    w.require(isinstance(inputs, dict) and inputs, 'Open persistence needs pinned native files')
    pinned = {}
    for name, digest in inputs.items():
        dependency = w.resolve(path.parent, name)
        w.require(dependency.is_file() and w.file_hash(dependency, hashes) == digest,
                  'Open persistence evidence changed: ' + name)
        pinned[dependency] = digest
        paths.append(str(dependency))
    w.require(pinned.get(plan_path) == binding['sha256'], 'Open persistence does not pin the current plan')

    def load(name):
        w.require(isinstance(report.get(name), str) and report[name].strip(), 'Open persistence needs ' + name)
        dependency = w.resolve(path.parent, report[name])
        w.require(dependency in pinned, 'Unpinned Open persistence file: ' + name)
        return dependency, w.read(dependency)

    def native(receipt):
        packet = receipt
        while isinstance(packet, dict) and packet.get('type') != 'response':
            packet = packet.get('structuredContent', packet.get('result'))
        result, context, ok = value(receipt)
        w.require(isinstance(packet, dict) and packet.get('type') == 'response' and packet.get('ok') is True
                  and isinstance(packet.get('id'), str) and packet['id'] and ok
                  and result.get('partial') is not True and result.get('writeState') != 'unknown'
                  and packet.get('unordered') is not True and type(packet.get('seq')) is int and packet['seq'] > 0
                  and type(packet.get('seqAbandoned')) is int and packet['seqAbandoned'] >= 0
                  and isinstance(packet.get('createdAt'), str), 'Native Open persistence receipt is incomplete or unknown')
        stamp = datetime.fromisoformat(packet['createdAt'].replace('Z', '+00:00'))
        w.require(stamp.tzinfo is not None, 'Native persistence time must include its timezone')
        return result, context, packet, stamp

    stage_path, stage_packet = load('stageReceipt')
    imported = import_receipt(w, root, binding, str(stage_path), hashes)
    stage = next(s for s in plan['stages'] if s.get('id') == imported['summary']['stage']['id'])
    index = plan['stages'].index(stage)
    directory = plan.get('executionReceipts', {}).get('directory')
    w.require(isinstance(directory, str) and directory
              and stage_path == w.resolve(root, directory) / ('stage-%02d.json' % (index + 1))
              and index == len(plan['stages']) - 1
              and display_stage(stage) and any(c['id'] == 'open_stage_receipt' and c['status'] == 'pass'
                                              for c in imported['checks']),
              'Open persistence requires a completed managed display stage')
    # shortcut: Scoped texts expose position/visibility only; add native attribute evidence before accepting other fields.
    supported = {'x', 'y', 'keyVisible', 'valueVisible'}
    w.require(all(set(step['payload']['props']) <= supported for step_stage in plan['stages'] for step in step_stage['steps']),
              'Local text readback cannot prove this display field; native attribute evidence is required')
    stage_reports, stage_reads = [], []
    for stage_index in range(len(plan['stages'])):
        proof = w.read(w.resolve(root, directory) / ('stage-%02d.json' % (stage_index + 1)))
        stage_report, _, _ = value(proof['receipt'])
        stage_reports.append(stage_report)
        stage_reads.append(native(stage_report.get('readback')))
    observations = [native(load(name)[1]) for name in
                    ('beforeSave', 'saveReceipt', 'closeReceipt', 'openReceipt', 'reopenedReadback')]
    reuse_before = observations[0][2] == stage_reads[-1][2]
    ordered = stage_reads + (observations[1:] if reuse_before else observations)
    w.require(all(a[2]['seq'] < b[2]['seq'] and a[3] < b[3] for a, b in zip(ordered, ordered[1:]))
              and len({item[2]['seqAbandoned'] for item in ordered}) == 1
              and (reuse_before or observations[0][3] >= datetime.fromisoformat(stage_packet['completedAt'].replace('Z', '+00:00')))
              and observations[1][3] > datetime.fromisoformat(stage_packet['completedAt'].replace('Z', '+00:00')),
              'Open save/close/open/readback sequence is stale or abandoned')
    for field in ('runtimeInstance', 'windowId'):
        if any(field in item[2] for item in ordered):
            w.require(all(isinstance(item[2].get(field), str) and item[2][field] == ordered[0][2].get(field)
                          for item in ordered), 'Open persistence runtime changed: ' + field)
    before, saved, closed, opened, after = observations
    w.require(all(native_target(item[1], plan['target']) and item[1].get('documentType') == 'schematic'
                  for item in stage_reads + [before, saved, opened, after])
              and saved[0].get('saved') is True and closed[0].get('closed') is True
              and closed[0].get('uuid') == plan['target']['documentUuid']
              and closed[1].get('projectUuid') == plan['target']['projectUuid'] and opened[0].get('ready') is True,
              'Open save/close/open/readback failed or targets another document')
    tab = before[1].get('tabId')
    w.require(isinstance(tab, str) and tab and saved[1].get('tabId') == tab and closed[0].get('tabId') == tab
              and isinstance(opened[0].get('tabId'), str) and opened[0]['tabId']
              and opened[1].get('tabId') == opened[0]['tabId'] == after[1].get('tabId'),
              'Open persistence document tab binding differs')
    ids = sorted({identifier for step_stage in plan['stages'] for identifier in step_stage['primitiveIds']})
    semantic = plan.get('semanticTarget', {})
    keys = [key for key, identifier in semantic.get('idMap', {}).items() if identifier in ids]
    identity_scope = {'primitiveIds': ids, 'componentKeys': keys}
    w.require(len(keys) == len(ids) and all(observed_scope(plan, identity_scope, item[2]) for item in (before, after)),
              'Open persistence scoped identity or fresh readback is unavailable')
    def identities(item):
        parts = item[0]['components']
        w.require(all(all(type(p.get(k)) in (int, float) and math.isfinite(p[k]) for k in ('x', 'y', 'rotation'))
                      and type(p.get('mirror')) is bool for p in parts), 'Open display owner pose is unavailable')
        return {p['primitiveId']: tuple(p.get(k) for k in ('designator', 'uniqueId', 'x', 'y', 'rotation', 'mirror')) for p in parts}
    wanted_identities = {}
    for step_stage, stage_read in zip(plan['stages'], stage_reads):
        stage_scope = {'primitiveIds': step_stage['primitiveIds'],
                       'componentKeys': [key for key in keys if semantic['idMap'][key] in step_stage['primitiveIds']]}
        w.require(observed_scope(plan, stage_scope, stage_read[2]), 'Open persisted stage identity is unavailable')
        wanted_identities.update(identities(stage_read))
    w.require(wanted_identities == identities(before) == identities(after), 'Open persistence component identity changed')
    expected = {}
    for step_stage, stage_report in zip(plan['stages'], stage_reports):
        execution, _, _ = value(stage_report.get('execution'))
        for step, result in zip(step_stage['steps'], execution['results']):
            actual, _, _ = value(result)
            attribute = actual['attribute']
            key = (attribute['ParentPrimitiveId'], attribute['Key'])
            entry = expected.setdefault(key, {'primitiveId': attribute['primitiveId'], 'value': attribute['Value'], 'props': {}})
            w.require(entry['primitiveId'] == attribute['primitiveId'] and entry['value'] == attribute['Value'],
                      'Open display attribute identity is ambiguous')
            entry['props'].update({field: attribute[field[0].upper() + field[1:]] for field in step['payload']['props']})
    for item in (before, after):
        texts = item[0].get('texts')
        w.require(item[0].get('textsAvailable') is True and isinstance(texts, list), 'Native scoped display text is unavailable')
        for (parent, key), expected_text in expected.items():
            matches = [t for t in texts if isinstance(t, dict) and t.get('parentId') == parent and t.get('key') == key]
            w.require(len(matches) == 1 and matches[0].get('value') == expected_text['value']
                      and (item is after or matches[0].get('primitiveId') == expected_text['primitiveId']),
                      'Saved/reopened display attribute is missing or ambiguous')
            for field, wanted in expected_text['props'].items():
                observed = matches[0].get(field)
                w.require((type(observed) is bool if type(wanted) is bool else
                           type(observed) in (int, float) and math.isfinite(observed)) and observed == wanted,
                          'Saved/reopened display field differs: ' + key + '.' + field)
    paths.extend(imported['paths'] + [str(path)])
    coverage = {'scope': 'local', 'primitiveIds': ids, 'checked': ['display_fields_saved_and_reopened'],
                'notChecked': ['electrical_target', 'visual_review', 'whole_page_persistence']}
    return {'checks': [{'id': 'persistence', 'status': 'pass', 'note': 'Pinned native save, close, open and local display readback agree.',
                        'evidence': list(dict.fromkeys(paths)), 'coverage': coverage,
                        'binding': {'target': plan['target'], 'planRevision': plan['revision'], 'planSha256': binding['sha256']}}],
            'paths': list(dict.fromkeys(paths)), 'summary': {'target': plan['target'], 'planSha256': binding['sha256'],
                'acceptanceScope': 'local_display',
                'stage': stage['id'], 'stageReceipt': str(stage_path), 'receiptFile': str(path), 'savedAt': saved[2]['createdAt'],
                'wholePageComplete': False, 'coverage': coverage}}

#!/usr/bin/env python3
"""Measured schematic audit + bounded local candidates; execute with existing typed tools.

No circuit names, library guesses, fixed zones, font estimates or whole-page rebuilds.
Raw coordinates are y-up. Flat wire records are independent segments, not a polyline.
"""
import argparse
import copy
import itertools
import json
import math
import re
from collections import Counter, defaultdict
from pathlib import Path

EPS = 1e-6


def unwrap(data):
    if 'ok' in data and not data['ok']:
        raise ValueError('EDA read failed: ' + str(data.get('error')))
    data = data.get('result', data)
    return data.get('value', data)


def load(path):
    return unwrap(json.loads(Path(path).read_text(encoding='utf-8-sig')))


def write(path, data):
    Path(path).write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding='utf-8')


def valid_box(b):
    return isinstance(b, dict) and all(isinstance(b.get(k), (int, float)) and
        math.isfinite(b[k]) for k in ('minX', 'minY', 'maxX', 'maxY')) and \
        b['maxX'] > b['minX'] and b['maxY'] > b['minY']


def overlap(a, b):
    return min(a['maxX'], b['maxX']) > max(a['minX'], b['minX']) + EPS and \
        min(a['maxY'], b['maxY']) > max(a['minY'], b['minY']) + EPS


def inside(a, b):
    return all(a[k] >= b[k] - EPS for k in ('minX', 'minY')) and \
        all(a[k] <= b[k] + EPS for k in ('maxX', 'maxY'))


def point_on(p, w):
    a, b = (w['x0'], w['y0']), (w['x1'], w['y1'])
    return abs((p[0]-a[0])*(b[1]-a[1]) - (p[1]-a[1])*(b[0]-a[0])) < EPS and \
        min(a[0], b[0])-EPS <= p[0] <= max(a[0], b[0])+EPS and \
        min(a[1], b[1])-EPS <= p[1] <= max(a[1], b[1])+EPS


def crossing(a, b):
    # Orthogonal native schematic segments only. Collinear overlaps are separate findings.
    if abs(a['x0']-a['x1']) < EPS and abs(b['y0']-b['y1']) < EPS:
        p = (a['x0'], b['y0'])
    elif abs(a['y0']-a['y1']) < EPS and abs(b['x0']-b['x1']) < EPS:
        p = (b['x0'], a['y0'])
    else:
        return None
    return p if point_on(p, a) and point_on(p, b) else None


def hits_box(w, b):
    # Open rectangle: touching a symbol edge/pin is not passing through its body.
    if abs(w['y0']-w['y1']) < EPS:
        return b['minY']+EPS < w['y0'] < b['maxY']-EPS and \
            min(w['x0'], w['x1']) < b['maxX']-EPS and max(w['x0'], w['x1']) > b['minX']+EPS
    if abs(w['x0']-w['x1']) < EPS:
        return b['minX']+EPS < w['x0'] < b['maxX']-EPS and \
            min(w['y0'], w['y1']) < b['maxY']-EPS and max(w['y0'], w['y1']) > b['minY']+EPS
    return False


def parts(s):
    return [c for c in s['components'] if c.get('componentType') == 'part']


def net_projection(s):
    out, names = {}, set()
    for c in parts(s):
        ref = c.get('designator')
        if not ref or c.get('pinsAvailable') is False or not c.get('pins'):
            raise ValueError('missing designator/pin read: ' + str(c.get('primitiveId')))
        for p in c['pins']:
            number = p.get('pinNumber', p.get('number'))
            if number is None:
                raise ValueError('missing pin number: ' + ref)
            key = ref + '.' + str(number)
            net, nc = p.get('net'), p.get('noConnected', False)
            if key in out or net is None or (not net and nc is not True) or (net and nc):
                raise ValueError('duplicate or unknown/conflicting pin-net/NC: ' + key)
            out[key] = {'name': p.get('pinName', p.get('name')), 'net': net, 'nc': nc}
            if net:
                names.add(net)
    return out, {'objects':len(parts(s)), 'nets':len(names), 'pinReferences':len(out)}


def compare(a, b):
    aa, ac = net_projection(a)
    bb, bc = net_projection(b)
    delta = [{'pin':k, 'before':aa.get(k), 'after':bb.get(k)} for k in sorted(aa.keys() | bb.keys())
             if aa.get(k) != bb.get(k)]
    return {'passed':not delta, 'before':ac, 'after':bc, 'differences':delta,
            'method':'fresh actual designator + pin number + function + net + explicit NC'}


def text_visibility(text, snapshot):
    """Prefer explicit observations; a zero box is never proof of invisibility."""
    if text.get('key') == 'free_text':
        return 'visible'
    key, value, visible = (text.get(k) for k in ('keyVisible', 'valueVisible', 'visible'))
    for attribute in snapshot.get('pagePrimitives', {}).get('attributes', []):
        if (attribute.get('primitiveId') == text.get('primitiveId') and
                attribute.get('ParentPrimitiveId') == text.get('parentId') and
                attribute.get('Key') == text.get('key') and
                str(attribute.get('Value')) == str(text.get('value', text.get('text')))):
            for old, field in ((key, 'KeyVisible'), (value, 'ValueVisible')):
                if isinstance(old, bool) and isinstance(attribute.get(field), bool) and old != attribute[field]:
                    return 'unknown'
            key = attribute.get('KeyVisible') if key is None else key
            value = attribute.get('ValueVisible') if value is None else value
            break
    if visible is False or (key is False and value is False):
        return 'hidden'
    if key is True or value is True or (text.get('visibilityKnown') is True and text.get('rendered') is True):
        return 'visible'
    return 'unknown'


def compare_scope(before, after, mutable_ids, preserve_identity=True):
    """Full native inventory, with an explicit rule-bound object allowlist.

    Creation/deletion is also a difference: IDs must be in the confirmed scope.
    Part identity cannot be waived by allowing movement of that component.
    """
    kinds = {'components', 'wires', 'buses', 'arcs', 'circles', 'rectangles',
             'polygons', 'texts', 'attributes', 'objects'}
    if not isinstance(mutable_ids, dict) or set(mutable_ids) - kinds:
        raise ValueError('explicit mutable_ids scope required')
    differences = []
    for kind in sorted(kinds):
        maps = []
        for snapshot in (before, after):
            rows = snapshot.get('pagePrimitives', {}).get(kind)
            if not isinstance(rows, list):
                raise ValueError('full page inventory missing: ' + kind)
            mapping = {r.get('primitiveId'): r for r in rows}
            if len(mapping) != len(rows) or any(not isinstance(k, str) or not k for k in mapping):
                raise ValueError('missing/duplicate primitive ID: ' + kind)
            maps.append(mapping)
        allowed = mutable_ids.get(kind, [])
        if not isinstance(allowed, list) or any(not isinstance(k, str) or not k for k in allowed) or len(set(allowed)) != len(allowed):
            raise ValueError('invalid mutable IDs: ' + kind)
        a, b = maps
        for key in sorted(a.keys() | b.keys()):
            old, new = a.get(key), b.get(key)
            if old == new:
                continue
            if key not in allowed:
                differences.append({'kind': kind, 'id': key, 'reason': 'outside_scope'})
            if preserve_identity and kind == 'components' and any(r and r.get('componentType') == 'part' for r in (old, new)):
                identity = lambda r: {k: v for k, v in (r or {}).items() if k not in ('x', 'y', 'rotation', 'mirror')}
                if identity(old) != identity(new):
                    differences.append({'kind': kind, 'id': key, 'reason': 'part_identity_changed'})
            elif kind == 'components' and old and new and old.get('componentType') == 'part':
                # Circuit changes do not authorize disabling an existing part or
                # rewriting its native/BOM metadata as a layout side effect.
                for field in ('uniqueId', 'addIntoBom', 'addIntoPcb', 'otherProperty'):
                    if old.get(field) != new.get(field):
                        differences.append({'kind': kind, 'id': key, 'reason': 'protected_property_changed', 'field': field})
    return {'passed': not differences, 'differences': differences}


def compare_target(target, snapshot):
    """Adapt the existing canonical 1.4 source to the same per-pin comparator.

    This checks source intent, not equality to the pre-edit circuit. Geometry,
    scope and rule checks remain the existing independent save preconditions.
    """
    if target.get('schemaVersion') != '1.4':
        raise ValueError('expected design must use existing canonical schemaVersion 1.4')
    def index(rows, key, label):
        if not isinstance(rows, list):
            raise ValueError('missing canonical ' + label)
        result = {r.get(key): r for r in rows}
        if len(rows) != len(result) or any(not isinstance(k, str) or not k for k in result):
            raise ValueError('duplicate/empty canonical ' + label)
        return result
    components = index(target.get('components'), 'id', 'components')
    nets = index(target.get('nets'), 'id', 'nets')
    if not components or len({c.get('ref') for c in components.values()}) != len(components):
        raise ValueError('empty design or duplicate designator')
    if any(not isinstance(n.get('name'), str) or not n['name'] for n in nets.values()) or len({n['name'] for n in nets.values()}) != len(nets):
        raise ValueError('net names missing/ambiguous')
    connections = {}
    for wire in target.get('connections', []):
        key = (wire.get('componentId'), wire.get('pinNumber'))
        if key in connections or key[0] not in components or wire.get('netId') not in nets:
            raise ValueError('duplicate or unresolved source connection')
        connections[key] = nets[wire['netId']]['name']
    observed = index(parts(snapshot), 'designator', 'observed designators')
    expected = {'components': []}
    differences, known_pins = [], set()
    for cid, component in components.items():
        ref, device = component.get('ref'), component.get('device', {})
        if not isinstance(ref, str) or not ref or not re.fullmatch(r'[0-9a-fA-F]{32}', str(device.get('deviceUuid', ''))):
            raise ValueError('source designator/exact library device identity missing')
        pins = index(component.get('pins'), 'number', 'pins')
        if not pins:
            raise ValueError('source pin inventory empty: ' + ref)
        row = {'componentType': 'part', 'designator': ref, 'pins': []}
        for number, pin in pins.items():
            key = (cid, number)
            known_pins.add(key)
            if pin.get('connectionState') or not isinstance(pin.get('noConnected', False), bool):
                raise ValueError('unconnected/unknown source pin is not final acceptance: ' + ref + '.' + number)
            row['pins'].append({'pinNumber': number, 'pinName': pin.get('name'),
                               'net': connections.get(key, ''), 'noConnected': pin.get('noConnected', False)})
        expected['components'].append(row)
        actual = observed.get(ref)
        if actual:
            identity = actual.get('device', {})
            if actual.get('deviceIdentityError') or identity.get('uuid') != device['deviceUuid'] or (
                    device.get('libraryUuid') and identity.get('libraryUuid') != device['libraryUuid']):
                differences.append({'component': ref, 'reason': 'source_library_identity_mismatch_or_unknown'})
            if device.get('name') and actual.get('name') != device['name']:
                differences.append({'component': ref, 'reason': 'source_part_name_mismatch'})
            binding = actual.get('otherProperty', {}).get('EasyEDA Agent Component ID')
            if binding is not None and binding != cid:
                differences.append({'component': ref, 'reason': 'canonical_component_binding_mismatch'})
    if set(connections) - known_pins:
        raise ValueError('source connection references nonexistent pin')
    electrical = compare(expected, snapshot)
    differences.extend(electrical['differences'])
    return {'passed': not differences, 'differences': differences,
            'method': 'confirmed canonical source identity and full pin/net/NC vs actual readback'}


def visible_texts(t, s):
    sheet_ids = {c['primitiveId'] for c in s['components'] if c.get('componentType') == 'sheet'}
    return [v for v in t.get('texts', []) if v.get('parentId') not in sheet_ids and
            text_visibility(v, s) == 'visible' and valid_box(v.get('bbox'))]


def audit(s, t):
    findings, unknown = [], list(t.get('errors', []))
    # Inventory is independent of measured rows: an empty measurement response
    # must not certify omitted visible/unknown text. Hidden needs explicit proof.
    inventory = s.get('pagePrimitives', {})
    expected = []
    for kind in ('attributes', 'texts'):
        rows = inventory.get(kind)
        if not isinstance(rows, list):
            unknown.append({'kind': 'text_inventory_unavailable', 'collection': kind})
            continue
        for row in rows:
            if kind == 'attributes' and row.get('KeyVisible') is False and row.get('ValueVisible') is False:
                continue
            expected.append(row.get('primitiveId'))
    measured = [v.get('primitiveId') for v in t.get('texts', [])]
    if any(not isinstance(i, str) or not i for i in expected + measured) or len(set(expected)) != len(expected) or len(set(measured)) != len(measured):
        unknown.append({'kind': 'text_inventory_ambiguous'})
    for key in set(expected) - set(measured):
        unknown.append({'kind': 'text_measurement_missing', 'id': key})
    complete_text_inventory = all(isinstance(inventory.get(k), list) for k in ('attributes', 'texts'))
    if complete_text_inventory:
        for key in set(measured) - {r.get('primitiveId') for kind in ('attributes', 'texts') for r in inventory[kind]}:
            unknown.append({'kind': 'text_measurement_unbound', 'id': key})
    sheets = [c for c in s['components'] if c.get('componentType') == 'sheet' and valid_box(c.get('bbox'))]
    if len(sheets) != 1:
        unknown.append({'kind':'sheet_bounds_unavailable'})
    if s.get('wiresAvailable') is not True:
        unknown.append({'kind':'wire_inventory_unavailable'})
    cs, ts, ws = parts(s), visible_texts(t, s), s.get('wires', [])
    owners = {p['primitiveId']:c['primitiveId'] for c in cs for p in c.get('pins', []) if p.get('primitiveId')}
    for v in t.get('texts', []):
        visibility = text_visibility(v, s)
        if visibility == 'unknown':
            unknown.append({'kind':'text_visibility_unknown', 'id':v.get('primitiveId')})
        elif visibility == 'visible' and not valid_box(v.get('bbox')):
            unknown.append({'kind':'visible_text_bbox_unavailable', 'id':v.get('primitiveId')})
    objects = [{'id':c['primitiveId'], 'kind':'body', 'ref':c['designator'], 'bbox':c.get('bbox')} for c in cs]
    objects += [{'id':v['primitiveId'], 'kind':'text', 'text':v['text'], 'parent':v.get('parentId'),
                 'key':v.get('key'), 'bbox':v['bbox']} for v in ts]
    for o in objects:
        if not valid_box(o['bbox']):
            unknown.append({'kind':'object_bbox_unavailable', 'id':o['id']})
            continue
        if sheets and not inside(o['bbox'], sheets[0]['bbox']):
            findings.append({'kind':'out_of_bounds', 'objects':[o], 'sheet':sheets[0]['bbox']})
    for a, b in itertools.combinations(objects, 2):
        if not valid_box(a['bbox']) or not valid_box(b['bbox']) or not overlap(a['bbox'], b['bbox']):
            continue
        # Pin names/numbers embedded in their own symbol are intentional.
        if any(x['kind']=='text' and y['kind']=='body' and owners.get(x.get('parent'))==y['id']
               for x,y in ((a,b),(b,a))):
            continue
        findings.append({'kind':'body_overlap' if a['kind']==b['kind']=='body' else 'text_overlap', 'objects':[a,b]})
    for w in ws:
        if sheets and not all(sheets[0]['bbox']['minX']-EPS <= w[x] <= sheets[0]['bbox']['maxX']+EPS and
            sheets[0]['bbox']['minY']-EPS <= w[y] <= sheets[0]['bbox']['maxY']+EPS for x,y in (('x0','y0'),('x1','y1'))):
            findings.append({'kind':'wire_out_of_bounds','objects':[w]})
        if w['x0'] != w['x1'] and w['y0'] != w['y1']:
            unknown.append({'kind':'diagonal_wire_obstacle_check_unsupported','id':w['primitiveId']})
        for o in objects:
            if o['kind']=='text' and o.get('parent')==w['primitiveId']:
                continue
            if valid_box(o['bbox']) and hits_box(w, o['bbox']):
                findings.append({'kind':'wire_through_body' if o['kind']=='body' else 'wire_through_text',
                                 'objects':[w,o]})
    for a,b in itertools.combinations(ws,2):
        p = crossing(a,b)
        if p and a['primitiveId'] != b['primitiveId'] and a.get('net') != b.get('net'):
            # This is a readability candidate, not a claim that native X crossings short nets.
            findings.append({'kind':'crossing_candidate','objects':[a,b],'at':p})
    try:
        _, counts = net_projection(s)
    except ValueError as e:
        counts = None
        unknown.append({'kind':'pin_net_unknown','message':str(e)})
    # Dedup identical wire/body reports where native rawLine is repeated per segment.
    unique = {json.dumps(f,sort_keys=True):f for f in findings}
    findings = list(unique.values())
    return {'passed':not findings and not unknown, 'counts':counts, 'findings':findings,
            'summary':dict(Counter(f['kind'] for f in findings)), 'unknown':unknown,
            'coverage':{'bodies':len(cs),'visibleTexts':len(ts),'wireSegments':len(ws),
                        'expectedTextObjects':len(expected) if complete_text_inventory else None,'measuredTextObjects':len(measured)},
            'limits':['orthogonal wires only', 'rectangle envelopes, not glyph-level shapes',
                      'crossings are candidates until a feasible local replacement is found',
                      'no automatic independent attribute move/rotation or branched-tree reroute',
                      'no inference of module ownership, symmetry or design intent']}


def audit_local(before, after, mutable_ids):
    """Known unchanged issues outside the affected closure remain explicit debt.

    New/changed findings, affected findings and global missing coverage still fail.
    Attribute identity is semantic because native reopen can regenerate its ID.
    """
    old, current = audit(before, before['textMeasurements']), audit(after, after['textMeasurements'])
    selected = {key for ids in mutable_ids.values() for key in ids}
    for snapshot in (before, after):
        selected.update(p['primitiveId'] for c in parts(snapshot) if c['primitiveId'] in selected
                        for p in c.get('pins', []) if p.get('primitiveId'))
    attributes = [a for s in (before, after) for a in s['pagePrimitives']['attributes']]
    while True:
        more = {a['primitiveId'] for a in attributes if a.get('ParentPrimitiveId') in selected}
        if more <= selected:
            break
        selected.update(more)
    def identities(snapshot):
        return {a['primitiveId']: 'attribute:' + json.dumps({k:v for k,v in a.items() if k != 'primitiveId'}, sort_keys=True)
                for a in snapshot['pagePrimitives']['attributes']}
    old_ids, current_ids = identities(before), identities(after)
    affected = {mapping.get(key, key) for mapping in (old_ids, current_ids) for key in selected}
    id_fields = {'id', 'primitiveId', 'parent', 'parentId', 'ParentPrimitiveId'}
    def normalize(value, mapping):
        if isinstance(value, list):
            return [normalize(v, mapping) for v in value]
        if isinstance(value, dict):
            return {k: mapping.get(v, v) if k in id_fields and isinstance(v, str) else normalize(v, mapping)
                    for k,v in value.items()}
        return value
    def touches(value, mapping):
        if isinstance(value, list):
            return any(touches(v, mapping) for v in value)
        if isinstance(value, dict):
            return any((k in id_fields and isinstance(v, str) and mapping.get(v, v) in affected) or touches(v, mapping)
                       for k,v in value.items())
        return False
    inherited = {}
    local_unknown = {'text_visibility_unknown', 'visible_text_bbox_unavailable',
                     'object_bbox_unavailable', 'diagonal_wire_obstacle_check_unsupported'}
    for field in ('findings', 'unknown'):
        known = Counter(json.dumps(normalize(v, old_ids), sort_keys=True) for v in old[field]
                        if not touches(v, old_ids) and (field == 'findings' or v.get('kind') in local_unknown))
        pending, carried = [], []
        for value in current[field]:
            key = json.dumps(normalize(value, current_ids), sort_keys=True)
            if not touches(value, current_ids) and known[key] > 0:
                known[key] -= 1
                carried.append(value)
            else:
                pending.append(value)
        current[field], inherited[field] = pending, carried
    current.update(passed=not current['findings'] and not current['unknown'], acceptanceScope='local',
                   inherited=inherited, fullPagePassed=not any(inherited.values()) and not current['findings'] and not current['unknown'])
    return current


def physical_groups(s):
    """Rigid move closure follows real contacts, NEVER all remote same-name nets."""
    cs, ws = parts(s), s.get('wires', [])
    adj = defaultdict(set)
    for c in cs:
        adj[c['primitiveId']]
        for w in ws:
            if any(point_on((p['x'],p['y']),w) for p in c.get('pins', [])):
                adj[c['primitiveId']].add(w['primitiveId'])
                adj[w['primitiveId']].add(c['primitiveId'])
    for a,b in itertools.combinations(ws,2):
        if a['primitiveId']==b['primitiveId']:
            continue
        # Native same-net contacting wires, including T contacts. Different-net X stays independent.
        if a.get('net')==b.get('net') and (crossing(a,b) or any(point_on((a[x],a[y]),b)
                                      for x,y in (('x0','y0'),('x1','y1')))):
            adj[a['primitiveId']].add(b['primitiveId']); adj[b['primitiveId']].add(a['primitiveId'])
    seen, groups = set(), []
    for c in cs:
        start=c['primitiveId']
        if start in seen:
            continue
        group, todo=set(),[start]
        while todo:
            k=todo.pop()
            if k in group:
                continue
            group.add(k); todo.extend(adj[k]-group)
        seen |= group
        groups.append(group)
    return groups


def translated(s,t,ids,dx,dy):
    s,t=copy.deepcopy(s),copy.deepcopy(t)
    pin_ids={p['primitiveId'] for c in parts(s) if c['primitiveId'] in ids
             for p in c.get('pins',[]) if p.get('primitiveId')}
    def shift_box(b):
        if valid_box(b):
            for k in ('minX','maxX'): b[k]+=dx
            for k in ('minY','maxY'): b[k]+=dy
    for c in s['components']:
        if c['primitiveId'] in ids:
            c['x']+=dx; c['y']+=dy; shift_box(c.get('bbox'))
            for p in c.get('pins',[]): p['x']+=dx; p['y']+=dy
    for w in s.get('wires',[]):
        if w['primitiveId'] in ids:
            for k in ('x0','x1'): w[k]+=dx
            for k in ('y0','y1'): w[k]+=dy
            # rawLine is retained only as provenance; this is a prediction, never a native snapshot.
    for v in t.get('texts',[]):
        if v.get('parentId') in ids | pin_ids:
            shift_box(v.get('bbox'))
            if v.get('x') is not None: v['x']+=dx
            if v.get('y') is not None: v['y']+=dy
    return s,t


def signature(f):
    return (f['kind'],tuple(sorted(str(o.get('primitiveId',o.get('id'))) for o in f['objects'])))


def candidates(s,t,step=5,radius=60,fixed=()):
    baseline=audit(s,t)
    if baseline['unknown']:
        return {'candidates':[], 'blocked':baseline['unknown']}
    prior={signature(f) for f in baseline['findings']}
    bad={str(o.get('id',o.get('primitiveId'))) for f in baseline['findings'] for o in f['objects']}
    ps={c['primitiveId']:c for c in parts(s)}
    out=[]
    for group in physical_groups(s):
        refs=sorted(ps[k]['designator'] for k in group if k in ps)
        if not group & bad or set(refs) & set(fixed):
            continue
        best=[]
        # ponytail: bounded axial translations; rotation and partial island surgery stay unsupported.
        for distance in range(step,radius+1,step):
            for dx,dy in ((distance,0),(-distance,0),(0,distance),(0,-distance)):
                ss,tt=translated(s,t,group,dx,dy)
                after=audit(ss,tt)
                current={signature(f) for f in after['findings']}
                if not after['unknown'] and current < prior:
                    best.append({'id':'move-'+str(len(out)+len(best)), 'action':'group_move',
                        'ids':sorted(group),'refs':refs,'dx':dx,'dy':dy,
                        'fixedFindings':len(prior-current),'remaining':after['summary'],
                        'distance':distance,'electricalPrediction':compare(s,ss)})
            if best:
                break
        out.extend(best)
    out.sort(key=lambda c:(-c['fixedFindings'],c['distance'],c['refs'],c['dx'],c['dy']))
    return {'candidates':out[:6], 'blocked':[], 'baseline':baseline['summary'],
            'unresolved':'Only complete physical assemblies can move automatically; inspect other findings.'}


def main():
    ap=argparse.ArgumentParser(description=__doc__)
    sub=ap.add_subparsers(dest='command',required=True)
    for name in ('audit','plan'):
        p=sub.add_parser(name); p.add_argument('--snapshot',required=True); p.add_argument('--texts',required=True)
        p.add_argument('--out',required=True)
        if name=='plan':
            p.add_argument('--step',type=int,default=5); p.add_argument('--radius',type=int,default=60)
            p.add_argument('--fixed',nargs='*',default=[])
    p=sub.add_parser('compare');p.add_argument('--before',required=True);p.add_argument('--after',required=True);p.add_argument('--out',required=True)
    a=ap.parse_args()
    if a.command=='compare': result=compare(load(a.before),load(a.after))
    else:
        s,t=load(a.snapshot),load(a.texts)
        if a.command=='audit': result=audit(s,t)
        else:
            if a.step<=0 or a.radius<0 or a.radius>500: ap.error('bounded positive grid/radius required')
            result=candidates(s,t,a.step,a.radius,a.fixed)
    write(a.out,result)
    summary = {k:result[k] for k in ('passed','counts','before','after','summary','coverage','limits') if k in result}
    summary.update(output=str(Path(a.out).resolve()), unknownCount=len(result.get('unknown', [])),
                   findingCount=len(result.get('findings', [])), differenceCount=len(result.get('differences', [])),
                   candidateCount=len(result.get('candidates', [])))
    for field in ('unknown','findings','differences'):
        if result.get(field):
            summary[field + 'Sample'] = result[field][:5]
    print(json.dumps(summary, ensure_ascii=False))
    return 1 if a.command=='compare' and not result['passed'] else 0


if __name__=='__main__':
    raise SystemExit(main())

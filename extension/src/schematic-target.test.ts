import test from 'node:test';
import assert from 'node:assert/strict';
import { checkSchematicTarget } from './schematic-target';

function fixture() {
  const component = { componentType: 'part', primitiveId: 'new-primitive', uniqueId: 'u1', designator: 'R1', name: '10k', device: { uuid: 'd'.repeat(32), libraryUuid: 'lib', name: 'R' }, symbol: { uuid: 'a'.repeat(16), name: 'R' }, footprint: { uuid: 'b'.repeat(16), name: '0603' }, otherProperty: { qualification: 'provisional' }, pins: [{ pinNumber: '1', pinName: 'A', net: 'VIN' as string | null, noConnected: false }, { pinNumber: '2', pinName: 'B', net: '', noConnected: true }] };
  const snapshot = { context: { projectUuid: 'p', documentUuid: 'd', documentType: 'schematic' }, result: { readScope: { complete: true, primitiveIds: [] as string[], netRead: 'document-netlist' }, components: [component], pinNetsAvailable: true } };
  const target = { schemaVersion: 1, scope: { kind: 'page', complete: true, projectUuid: 'p', documentUuid: 'd' }, components: [{ key: 'unique:u1', uniqueId: 'u1', ref: 'R1', sourcePrimitiveId: 'old-primitive', device: structuredClone(component.device), symbol: structuredClone(component.symbol), footprint: structuredClone(component.footprint), properties: { name: '10k', otherProperty: { qualification: 'provisional' } }, pins: [{ number: '1', name: 'A', state: 'connected', net: 'VIN', noConnected: false }, { number: '2', name: 'B', state: 'nc', net: '', noConnected: true }] }], nets: [{ name: 'VIN', scope: 'unknown', members: [{ componentKey: 'unique:u1', pinNumber: '1' }] }] };
  return { snapshot, target, component };
}

test('stable identity survives recreated primitive IDs while source qualification remains unchanged', () => {
  const { snapshot, target } = fixture(); const result = checkSchematicTarget(snapshot, target);
  assert.equal(result.status, 'pass'); assert.equal(result.verified, true);
  assert.equal(result.idMap['unique:u1'].primitiveId, 'new-primitive');
  assert.equal(target.components[0].properties.otherProperty.qualification, 'provisional');
});

test('exact pin and part inventory catches missing, extra, duplicate, changed net and changed metadata', () => {
  for (const change of [
    (f: ReturnType<typeof fixture>) => { f.component.pins[0].net = 'OTHER'; },
    (f: ReturnType<typeof fixture>) => { f.component.pins.pop(); },
    (f: ReturnType<typeof fixture>) => { f.component.pins.push({ ...f.component.pins[0] }); },
    (f: ReturnType<typeof fixture>) => { f.snapshot.result.components.push({ ...f.component }); },
    (f: ReturnType<typeof fixture>) => { f.component.name = '1k'; },
    (f: ReturnType<typeof fixture>) => { f.component.otherProperty.qualification = 'approved'; },
    (f: ReturnType<typeof fixture>) => { f.target.nets[0].members[0].pinNumber = '2'; },
  ]) { const f = fixture(); change(f); assert.equal(checkSchematicTarget(f.snapshot, f.target).status, 'fail'); }
});

test('partial or geometry snapshots and unknown electrical targets never verify', () => {
  const scoped = fixture(); scoped.snapshot.result.readScope.primitiveIds = ['new-primitive'];
  assert.equal(checkSchematicTarget(scoped.snapshot, scoped.target).status, 'unknown');
  const geometry = fixture(); geometry.component.pins[0].net = null; geometry.snapshot.result.readScope.netRead = 'not-requested';
  assert.equal(checkSchematicTarget(geometry.snapshot, geometry.target).verified, false);
  const unknown = fixture(); unknown.target.components[0].pins[0].state = 'unknown'; unknown.target.nets = [];
  assert.equal(checkSchematicTarget(unknown.snapshot, unknown.target).status, 'unknown');
  const crossPage = fixture(); crossPage.target.nets[0].scope = 'global';
  assert.equal(checkSchematicTarget(crossPage.snapshot, crossPage.target).status, 'unknown');
});

test('library source and instance identity domains are not falsely declared different or equivalent', () => {
  const f = fixture(); f.component.footprint.uuid = 'c'.repeat(32);
  const result = checkSchematicTarget(f.snapshot, f.target);
  assert.equal(result.status, 'unknown'); assert.ok(result.findings.some(x => x.code === 'library_identity_domain_unknown'));
  const g = fixture(); g.component.device.uuid = 'e'.repeat(32);
  assert.equal(checkSchematicTarget(g.snapshot, g.target).status, 'fail');
});

test('missing parts in a scoped read stay unknown and malformed boundary values return findings', () => {
  const f = fixture(); f.snapshot.result.components = []; f.snapshot.result.readScope.primitiveIds = ['other'];
  assert.equal(checkSchematicTarget(f.snapshot, f.target).status, 'unknown');
  assert.equal(checkSchematicTarget({ ...f.snapshot, result: { components: [null] } }, f.target).status, 'unknown');
  const malformed = fixture(); (malformed.target.nets[0].members as unknown[]) = [null];
  assert.equal(checkSchematicTarget(malformed.snapshot, malformed.target).status, 'fail');
});

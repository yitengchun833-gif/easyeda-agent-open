/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';
import { ActionQueue } from './action-queue';
import { failedActionResult } from './action-result';
import { sweepDeadlines } from './deadlines';

const turn = () => new Promise<void>(resolve => setImmediate(resolve));
const lineStep = { action: 'pcb.line.create', payload: { net: 'SIG', layer: 1, startX: 0, startY: 0, endX: 10, endY: 0 } };
const connectPin = { kind: 'net_label', net: 'SIG', pinX: 0, pinY: 0, direction: 'right', offset: 10 };
const wire = (id: string) => ({ getState_PrimitiveId: () => id });

function mockEda(t: TestContext, value: unknown) {
 const globals = globalThis as any;
 const previous = globals.eda, types = globals.EDMT_EditorDocumentType;
 globals.eda = value;
 globals.EDMT_EditorDocumentType = { HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, PCB: 3 };
 t.after(() => { globals.eda = previous; globals.EDMT_EditorDocumentType = types; });
}

for (const settlement of ['resolve', 'reject']) test(`timed out stub is not replayed and releases its queue after native ${settlement}`, async t => {
 let creates = 0, finish!: () => void;
 mockEda(t, { sch_PrimitiveWire: { create: () => {
  creates++;
  return new Promise((resolve, reject) => { finish = () => settlement === 'resolve' ? resolve(wire('late')) : reject(new Error('late failure')); });
 } } });
 const queue = new ActionQueue({ graceMs: 0 });
 const result = queue.submit({ id: 'connect', timeoutMs: 60000, run: execution => runAction('debug.batch', {
  stopOnError: false, steps: [{ action: 'schematic.power.connect_pin', payload: connectPin }, lineStep],
 }, false, execution) });
 await turn();
 assert.equal(creates, 1);
 sweepDeadlines(Date.now() + 8000);
 const timedOut = await result;
 assert.equal(timedOut.status, 'error');
 if (timedOut.status === 'error') assert.equal((timedOut.error as any).code, 'NATIVE_OPERATION_PENDING');
 assert.deepEqual(queue.control({}).inFlight, ['connect']);
 let followingWrites = 0;
 const refused = await queue.submit({ id: 'while-pending', run: async () => ++followingWrites });
 assert.equal(refused.status, 'error');
 assert.equal(creates, 1, 'the timed-out native create was not replayed');
 assert.equal(followingWrites, 0);
 assert.equal((await queue.submit({ id: 'status', bypass: true, run: async () => queue.control({}) })).status, 'ok');
 finish();
 await turn(); await turn();
 assert.deepEqual(queue.control({}).inFlight, []);
 assert.equal((await queue.submit({ id: 'after-settlement', run: async () => ++followingWrites })).status, 'ok');
 assert.equal(followingWrites, 1);
});

test('standalone timed-out stub stops even without queue execution metadata', async t => {
 let creates = 0, finish!: () => void;
 mockEda(t, { sch_PrimitiveWire: { create: () => {
  creates++;
  return new Promise(resolve => { finish = () => resolve(wire('late')); });
 } } });
 const pending = runAction('debug.batch', { stopOnError: false, steps: [
  { action: 'schematic.power.connect_pin', payload: connectPin }, lineStep,
 ] }, false);
 await turn(); sweepDeadlines(Date.now() + 8000);
 const response = await pending;
 assert.equal(creates, 1);
 assert.equal(response.result!.ok, false);
 const steps = response.result!.results as any[];
 assert.equal(steps.length, 1);
 assert.equal(steps[0].error.code, 'NATIVE_OPERATION_PENDING');
 finish(); await turn();
});

test('settled native rejection is a failure and does not leave the queue locked', async t => {
 let creates = 0;
 mockEda(t, { sch_PrimitiveWire: { create: async () => { creates++; throw new Error('native rejected'); } } });
 const queue = new ActionQueue({ graceMs: 0 });
 const failed = await queue.submit({ id: 'rejected', timeoutMs: 60000,
  run: execution => runAction('schematic.power.connect_pin', connectPin, false, execution) });
 assert.equal(failed.status, 'error');
 if (failed.status === 'error') assert.equal((failed.error as any).code, 'EDA_CALL_FAILED');
 assert.equal(creates, 2, 'the existing retry applies only to settled API rejection');
 assert.deepEqual(queue.control({}).inFlight, []);
 assert.equal((await queue.submit({ id: 'after-rejection', run: async () => 1 })).status, 'ok');
});

test('pending label creation preserves the created stub and its native in-flight ownership', async t => {
 let deletes = 0, finish!: () => void;
 mockEda(t, {
  dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p' }) },
  dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'A', tabId: 'tab', documentType: 1 }) },
  sch_PrimitiveWire: { create: async () => wire('stub'), delete: async () => { deletes++; return true; } },
  sch_PrimitiveAttribute: { getAll: async () => [], createNetLabel: () => new Promise(resolve => { finish = () => resolve(undefined); }) },
 });
 const queue = new ActionQueue({ graceMs: 0 });
 const pending = queue.submit({ id: 'label', timeoutMs: 60000,
  run: execution => runAction('schematic.power.connect_pin', connectPin, false, execution) });
 await turn(); assert.equal(typeof finish, 'function');
 sweepDeadlines(Date.now() + 8000);
 assert.equal((await pending).status, 'error');
 assert.equal(deletes, 0, 'unknown marker creation must not roll back its stub');
 assert.deepEqual(queue.control({}).inFlight, ['label']);
 finish(); await turn(); await turn();
 assert.deepEqual(queue.control({}).inFlight, []);
});

test('typed batch steps retain the outer target across a document switch', async t => {
 let active = 'A'; const writes: string[] = [];
 mockEda(t, {
  dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p' }) },
  dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: active }) },
  pcb_PrimitiveLine: { create: async () => { writes.push(active); active = 'B'; return wire('line'); } },
 });
 const result = await runAction('debug.batch', { _target: { projectUuid: 'p', documentUuid: 'A' }, steps: [lineStep, lineStep] });
 assert.deepEqual(writes, ['A']);
 assert.equal(result.result!.ok, false);
 assert.equal(result.result!.partial, true);
 assert.equal((result.result!.results as any[])[1].error.code, 'PRECONDITION_REFUSED');
});

test('batch target overrides fail before the first write and unchanged targets remain usable', async t => {
 let writes = 0;
 mockEda(t, {
  dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p' }) },
  dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'A' }) },
  pcb_PrimitiveLine: { create: async () => { writes++; return wire('line'); } },
 });
 const target = { projectUuid: 'p', documentUuid: 'A' };
 await assert.rejects(runAction('debug.batch', { _target: target, steps: [lineStep,
  { ...lineStep, payload: { ...lineStep.payload, _target: { documentUuid: 'B' } } },
 ] }), /cannot replace/);
 assert.equal(writes, 0);
 const response = await runAction('debug.batch', { _target: target, steps: [lineStep, lineStep] });
 assert.equal(response.result!.ok, true);
 assert.equal(writes, 2);
});

test('explicit native delete failure stops the default batch and is never reported as success', async t => {
 let writes = 0;
 mockEda(t, {
  pcb_PrimitiveComponent: { getAll: async () => [wire('c1')], delete: async () => false },
  pcb_PrimitiveLine: { create: async () => { writes++; return wire('line'); } },
 });
 const steps = [{ action: 'pcb.component.delete', payload: { primitiveIds: ['c1'] } }, lineStep];
 const stopped = await runAction('debug.batch', { steps }, false);
 assert.equal(stopped.result!.ok, false);
 assert.equal(stopped.result!.completed, 1);
 assert.equal(writes, 0);
 const continued = await runAction('debug.batch', { steps, stopOnError: false }, false);
 assert.equal(continued.result!.ok, false);
 assert.equal(writes, 1, 'explicit continuation may proceed after a settled failure');
});

test('result classification recognizes explicit failures without requiring positive proof', () => {
 for (const result of [{ deleted: false }, { disconnected: false }, { failures: ['t'] }, { notApplied: ['t'] },
  { survived: { wires: ['w'] } }, { survivedIds: ['w'] }, { survivedTotal: 1 }, { partial: true, writeState: 'unknown' }]) {
  assert.equal(failedActionResult(result), true, JSON.stringify(result));
 }
 for (const result of [{ deleted: true }, { failures: [] }, { notApplied: [] }, { survived: {} },
  { survived: { components: [], wires: [] } }, { survivedTotal: 0 }, { primitiveId: 'new' }]) {
  assert.equal(failedActionResult(result), false, JSON.stringify(result));
 }
});

test('verified absent constraints remain idempotent and do not stop a following batch step', async t => {
 let writes = 0;
 mockEda(t, {
  pcb_Drc: { getAllDifferentialPairs: async () => [], getAllEqualLengthNetGroups: async () => [] },
  pcb_PrimitiveLine: { create: async () => { writes++; return wire('line'); } },
 });
 for (const action of ['pcb.differential_pair.delete', 'pcb.equal_length_group.delete']) {
  const response = await runAction('debug.batch', { steps: [{ action, payload: { name: 'absent' } }, lineStep] });
  const result = response.result!;
  assert.equal(result.ok, true);
  assert.equal(result.completed, 2);
  assert.deepEqual((result.results as any[])[0].result, { name: 'absent', deleted: false, notFound: true, verified: true });
 }
 assert.equal(writes, 2);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { captureSnapshot, planTool } from '../src/plan-tools.mjs';
import { loadPlan } from '../src/project-files.mjs';
import { toMcpResult } from '../src/core.mjs';

const observation = { isError: false, structuredContent: { ok: true, context: { projectUuid: 'p', documentUuid: 'd', documentType: 'schematic' }, result: {
  readScope: { complete: true, concurrentChange: false }, components: [{ componentType: 'part', uniqueId: 'u', primitiveId: 'r', designator: 'R1', pins: [{ pinNumber: '1', net: 'VIN', noConnected: false }], footprint: { uuid: 'f', name: '0603' } }], wires: [], pagePrimitives: { attributes: [{ value: 'native' }] },
} } };
const catalog = new Map(['schematic.target.check', 'schematic.component.modify'].map(name => [name, { name }]));

test('snapshot file route archives native facts and returns compact paths; cached/failed reads do not become fresh files', async t => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-plan-route-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  let calls = 0, sent;
  const invoke = async (action, input) => { calls++; sent = input; return observation; };
  const outputFile = path.join(dir, 'snapshot.json');
  const result = await captureSnapshot({ domain: 'schematic', outputFile }, invoke);
  assert.equal(calls, 1); assert.equal(result.isError, false);
  assert.equal(sent.payload.steps[1].payload.includePagePrimitives, true);
  assert.equal(sent.payload.steps[1].payload.includeDeviceIdentity, true);
  assert.equal(sent.payload.steps[1].payload.includeTexts, true);
  assert.equal(result.structuredContent.counts.pins, 1);
  assert.equal(result.structuredContent.snapshot, undefined);
  assert.deepEqual(JSON.parse(await readFile(outputFile, 'utf8')).snapshot, observation.structuredContent.result);
  await assert.rejects(captureSnapshot({ domain: 'schematic', outputFile, cacheOnly: true }, invoke), /fresh/);
  assert.equal(calls, 1);
  await assert.rejects(captureSnapshot({ domain: 'schematic', outputFile: path.join(dir, 'wrong.json'), target: { projectUuid: 'other', documentUuid: 'd' } }, invoke), /changed/);
  assert.deepEqual(sent.payload.steps[1].payload._target, { projectUuid: 'other', documentUuid: 'd' });
  const error = { isError: true, content: [{ type: 'text', text: 'partial' }] };
  assert.equal(await captureSnapshot({ domain: 'schematic', outputFile }, async () => error), error);
});

test('plan prepare/inspect is offline; apply binds reviewed hash/target and checks the final batch once', async t => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-plan-apply-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const snapshotFile = path.join(dir, 'snapshot.json'), planFile = path.join(dir, 'plan.json');
  await captureSnapshot({ domain: 'schematic', outputFile: snapshotFile }, async () => observation);
  let calls = 0, actual;
  const invoke = async (action, input) => { calls++; actual = { action, input }; return { isError: true, content: [{ type: 'text', text: 'unknown readback' }] }; };
  await planTool({ operation: 'prepare', snapshotFile, outputFile: planFile, goal: 'recreate' }, catalog, invoke);
  const { plan } = await loadPlan(planFile);
  plan.steps = [{ action: 'schematic.component.modify', payload: { primitiveId: 'r', patch: { x: 10 }, preserveInstance: true } }];
  await writeFile(planFile, JSON.stringify(plan));
  const inspected = await planTool({ operation: 'inspect', planFile }, catalog, invoke);
  const expectedHash = inspected.structuredContent.sha256;
  assert.equal(calls, 0);
  await assert.rejects(planTool({ operation: 'apply', planFile, window: 'w' }, catalog, invoke), /expectedHash/);
  await assert.rejects(planTool({ operation: 'apply', planFile, window: 'w', expectedHash, target: { projectUuid: 'wrong', documentUuid: 'd' } }, catalog, invoke), /target/);
  assert.equal(calls, 0);
  const result = await planTool({ operation: 'apply', planFile, window: 'w', expectedHash }, catalog, invoke);
  assert.equal(calls, 1); assert.equal(result.isError, true);
  assert.equal(actual.action, 'debug.batch');
  assert.deepEqual(actual.input.target, plan.target);
  assert.equal(actual.input.payload.steps.length, 2);
  assert.equal(actual.input.payload.steps[1].action, 'schematic.target.check');
  assert.deepEqual(actual.input.payload.steps[0].payload._target, plan.target);
  assert.deepEqual(actual.input.payload.steps[1].payload._target, plan.target);
  assert.deepEqual(actual.input.payload.steps[1].payload.semanticTarget, plan.semanticTarget);
  assert.equal(actual.input.payload.stopOnError, true);
  const navigation = structuredClone(plan);
  navigation.steps = [{ action: 'document.open', payload: { uuid: 'other' } }];
  const navFile = path.join(dir, 'navigation.json');
  await writeFile(navFile, JSON.stringify(navigation));
  const { sha256: navHash } = await loadPlan(navFile);
  await assert.rejects(planTool({ operation: 'apply', planFile: navFile, window: 'w', expectedHash: navHash }, new Map([...catalog, ['document.open', {}]]), invoke), /navigate/);
  assert.equal(calls, 1);
  await writeFile(snapshotFile, '\n', { flag: 'a' });
  await assert.rejects(planTool({ operation: 'apply', planFile, window: 'w', expectedHash }, catalog, invoke), /Source hash changed/);
  assert.equal(calls, 1);
});

const stageCatalog = new Map([...catalog, ['schematic.component.place', {}], ['schematic.components.list', {}], ['schematic.wire.create', {}], ['debug.exec_js', {}]]);
async function stagedPlan(t, stages) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-plan-stages-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const snapshotFile = path.join(dir, 'snapshot.json'), planFile = path.join(dir, 'plan.json');
  await captureSnapshot({ domain: 'schematic', outputFile: snapshotFile }, async () => observation);
  await planTool({ operation: 'prepare', snapshotFile, outputFile: planFile }, stageCatalog);
  const { plan } = await loadPlan(planFile); plan.stages = stages;
  await writeFile(planFile, JSON.stringify(plan));
  const { sha256: expectedHash } = await loadPlan(planFile);
  return { operation: 'apply', planFile, expectedHash, window: 'w', stage: stages[0].id };
}
const batchReceipt = results => toMcpResult({ ok: true, result: { requestId: 'batch', result: {
  ok: results.every(r => r.ok), partial: results.some(r => !r.ok), completed: results.length, total: results.length, results,
} } });
const geometryReceipt = ids => toMcpResult({ ok: true, result: { requestId: 'geometry', context: { projectUuid: 'p', documentUuid: 'd' }, result: {
  components: ids.map(primitiveId => ({ primitiveId, pinsAvailable: true, bbox: { minX: 0, minY: 0, maxX: 20, maxY: 30 },
    pins: [{ pinNumber: '1', x: 10, y: 20, rotation: 0, net: null }] })),
  readScope: { complete: false, concurrentChange: false, primitiveIds: ids, missingIds: [], netRead: 'not-requested' },
} } });

test('selected geometry stage uses actual created IDs and exactly one scoped read, never the next stage or full target', async t => {
  const input = await stagedPlan(t, [
    { id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device', x: 10, y: 20 } }] },
    { id: 'wire', check: 'final', steps: [{ action: 'schematic.wire.create', payload: { net: 'VIN' } }] },
  ]);
  const calls = [], execution = batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'new-id' } }]);
  const readback = geometryReceipt(['new-id']);
  const result = await planTool(input, stageCatalog, async (action, input) => { calls.push({ action, input }); return action === 'debug.batch' ? execution : readback; });
  assert.equal(result.isError, false); assert.equal(result.structuredContent.stage.completed, true);
  assert.equal(result.structuredContent.wholePageComplete, false); assert.equal(result.structuredContent.targetCheck, null);
  assert.equal(result.structuredContent.execution, execution); assert.equal(result.structuredContent.readback, readback);
  assert.deepEqual(calls.map(c => c.action), ['debug.batch', 'schematic.components.list']);
  assert.equal(calls[0].input.payload.steps.length, 1);
  assert.deepEqual(calls[1].input.payload.primitiveIds, ['new-id']);
  assert.equal(calls[1].input.payload.includePinNets, false); assert.equal(calls[1].input.payload.includeDeviceIdentity, false);
  for (const call of calls) assert.deepEqual(call.input.target, { projectUuid: 'p', documentUuid: 'd' });
  assert.deepEqual(calls[0].input.payload.steps[0].payload._target, calls[1].input.payload._target);
});

test('geometry completion requires measured pins and bounding box even when the Connector does not flag partial', async t => {
  const input = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }]);
  const execution = batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'new' } }]);
  for (const [reason, mutate] of [
    ['pin API failure', part => { part.pinsAvailable = false; part.pins = []; }],
    ['missing pins evidence', part => delete part.pinsAvailable],
    ['empty pins', part => part.pins = []],
    ['missing pin rotation', part => delete part.pins[0].rotation],
    ['non-numeric coordinate', part => part.pins[0].x = '10'],
    ['missing pin number', part => part.pins[0].pinNumber = ''],
    ['missing bbox', part => delete part.bbox],
    ['invalid bbox', part => part.bbox.maxX = -1],
  ]) {
    const readback = geometryReceipt(['new']); mutate(readback.structuredContent.result.components[0]);
    assert.equal(readback.isError, false); assert.equal(readback.structuredContent.result.partial, undefined);
    const result = await planTool(input, stageCatalog, async action => action === 'debug.batch' ? execution : readback);
    assert.equal(result.structuredContent.stage.completed, false, reason); assert.equal(result.isError, true, reason);
    assert.equal(result.structuredContent.execution, execution); assert.equal(result.structuredContent.readback, readback);
  }
});

test('partial stage retains every receipt, reads known actual IDs and never replays or reports completion', async t => {
  const steps = [1, 2].map(x => ({ action: 'schematic.component.place', payload: { uuid: 'device', x, y: 20 } }));
  const input = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps }]);
  const execution = batchReceipt([
    { index: 0, action: steps[0].action, ok: true, result: { primitiveId: 'created' } },
    { index: 1, action: steps[1].action, ok: false, error: { message: 'timed out', executionState: 'unknown' } },
  ]);
  const calls = [];
  const result = await planTool(input, stageCatalog, async (action, input) => { calls.push({ action, input }); return action === 'debug.batch' ? execution : geometryReceipt(['created']); });
  assert.equal(result.isError, true); assert.equal(result.structuredContent.stage.completed, false);
  assert.equal(result.structuredContent.execution, execution); assert.equal(result.structuredContent.partial, true);
  assert.deepEqual(calls.map(c => c.action), ['debug.batch', 'schematic.components.list']);
  assert.deepEqual(calls[1].input.payload.primitiveIds, ['created']);
});

test('missing creation ID does not cause a full-page fallback; partial geometry cannot complete a stage', async t => {
  const input = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }]);
  let calls = 0;
  const execution = batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: {} }]);
  const missing = await planTool(input, stageCatalog, async () => { calls++; return execution; });
  assert.equal(calls, 1); assert.equal(missing.isError, true); assert.equal(missing.structuredContent.readback, null);
  const scoped = await stagedPlan(t, [{ id: 'place', check: 'geometry', primitiveIds: ['existing'], steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }]);
  const scopedResult = await planTool(scoped, stageCatalog, async action => action === 'debug.batch' ? execution : geometryReceipt(['existing']));
  assert.equal(scopedResult.isError, true); assert.equal(scopedResult.structuredContent.stage.completed, false);
  const changed = geometryReceipt(['new']); changed.structuredContent.result.readScope.concurrentChange = true;
  const result = await planTool(input, stageCatalog, async action => action === 'debug.batch'
    ? batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'new' } }]) : changed);
  assert.equal(result.isError, true); assert.equal(result.structuredContent.stage.completed, false);
});

test('final stage checks the independent full target after success and skips checking after partial writes', async t => {
  const steps = [{ action: 'schematic.component.modify', payload: { primitiveId: 'r', patch: { x: 2 } } }];
  const input = await stagedPlan(t, [{ id: 'finish', check: 'final', steps }]);
  const calls = [];
  const result = await planTool(input, stageCatalog, async (action, input) => {
    calls.push({ action, input }); return action === 'debug.batch'
      ? batchReceipt([{ index: 0, action: steps[0].action, ok: true, result: {} }])
      : toMcpResult({ ok: true, result: { result: { ok: true, status: 'pass', verified: true } } });
  });
  assert.deepEqual(calls.map(c => c.action), ['debug.batch', 'schematic.target.check']);
  assert.equal(result.structuredContent.stage.completed, true); assert.equal(result.structuredContent.wholePageComplete, false);
  assert.equal(calls[1].input.payload.semanticTarget.components[0].key, 'unique:u');
  let count = 0;
  const partial = await planTool(input, stageCatalog, async () => { count++; return batchReceipt([{ index: 0, action: steps[0].action, ok: false, error: { message: 'failed' } }]); });
  assert.equal(count, 1); assert.equal(partial.isError, true); assert.equal(partial.structuredContent.targetCheck, null);
  const checkOnly = await stagedPlan(t, [{ id: 'final', check: 'final', steps: [] }]);
  count = 0;
  await planTool(checkOnly, stageCatalog, async action => { count++; assert.equal(action, 'schematic.target.check'); return toMcpResult({ ok: true, result: { result: { status: 'pass' } } }); });
  assert.equal(count, 1);
});

test('stage selection, source/hash/target changes and navigation are rejected before any action', async t => {
  const input = await stagedPlan(t, [{ id: 'layout', check: 'geometry', steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'r' } }] }]);
  const never = async () => assert.fail('No EDA calls allowed');
  for (const stage of [undefined, 'missing']) await assert.rejects(planTool({ ...input, stage }, stageCatalog, never), /stage id/);
  await assert.rejects(planTool({ ...input, expectedHash: '0'.repeat(64) }, stageCatalog, never), /hash changed/);
  await assert.rejects(planTool({ ...input, target: { projectUuid: 'p', documentUuid: 'changed' } }, stageCatalog, never), /target/);
  const nav = await stagedPlan(t, [{ id: 'script', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'debug.exec_js', payload: { code: 'navigate()' } }] }]);
  await assert.rejects(planTool(nav, stageCatalog, never), /navigate/);
  const { plan } = await loadPlan(input.planFile);
  await writeFile(plan.sources[0].path, '\n', { flag: 'a' });
  await assert.rejects(planTool(input, stageCatalog, never), /Source hash changed/);
});

test('geometry stage accepts explicit wire-related component scope and dryRun never reads or reports completion', async t => {
  const input = await stagedPlan(t, [{ id: 'wire', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'schematic.wire.create', payload: { net: 'VIN' } }] }]);
  const calls = [];
  const result = await planTool({ ...input, dryRun: true }, stageCatalog, async (action, input) => {
    calls.push({ action, input }); return toMcpResult({ ok: true, result: { result: { dryRun: true } } });
  });
  assert.equal(calls.length, 1); assert.equal(calls[0].input.payload.dryRun, true);
  assert.equal(result.structuredContent.stage.completed, false); assert.equal(result.structuredContent.readback, null);
});

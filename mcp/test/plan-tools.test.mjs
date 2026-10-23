import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { captureSnapshot, planTool, planRunSummary } from '../src/plan-tools.mjs';
import { loadPlan } from '../src/project-files.mjs';
import { toMcpResult } from '../src/core.mjs';

test('failure summary exposes native mismatch without claiming a stopped write completed', () => {
  const result = { isError: true, structuredContent: { ok: false, stoppedStage: 'text', remainingStages: ['text', 'restore'],
    stoppedResult: { structuredContent: { readbackReason: 'Native field differs', execution: { structuredContent: { requestId: 'q', result: {
      results: [{ action: 'schematic.attribute.modify', index: 0, ok: false, result: { mismatch: ['x'], attribute: { X: 310 } } }] } } } } } } };
  const summary = planRunSummary(result, 'receipt.json', 25);
  assert.equal(summary.ok, false);
  assert.deepEqual(summary.remainingStages, ['text', 'restore']);
  assert.deepEqual(summary.failure.mismatch, ['x']);
  assert.equal(summary.failure.observedAttribute.X, 310);
  assert.equal(summary.failure.requestId, 'q');
});

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
  await captureSnapshot({ domain: 'schematic', outputFile: path.join(dir, 'electrical.json'), profile: 'electrical' }, invoke);
  assert.equal(sent.payload.steps[1].payload.includeTexts, false);
  assert.equal(sent.payload.steps[1].payload.includeWires, false);
  assert.equal(sent.payload.steps[1].payload.includeDeviceIdentity, false);
  assert.deepEqual(JSON.parse(await readFile(outputFile, 'utf8')).snapshot, observation.structuredContent.result);
  await assert.rejects(captureSnapshot({ domain: 'schematic', outputFile, cacheOnly: true }, invoke), /fresh/);
  assert.equal(calls, 2);
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
  components: ids.map(primitiveId => ({ primitiveId, ...(primitiveId === 'r' ? { uniqueId: 'u', designator: 'R1' } : {}), pinsAvailable: true, bbox: { minX: 0, minY: 0, maxX: 20, maxY: 30 },
    pins: [{ pinNumber: '1', x: 10, y: 20, rotation: 0, net: null }] })),
  readScope: { complete: false, concurrentChange: false, primitiveIds: ids, missingIds: [], netRead: 'not-requested' },
} } });

async function withRequirements(input, requirements) {
  const { plan } = await loadPlan(input.planFile);
  plan.requirements = requirements; plan.semanticTarget.idMap = { 'unique:u': 'r' };
  await writeFile(input.planFile, JSON.stringify(plan));
  return { ...input, expectedHash: (await loadPlan(input.planFile)).sha256 };
}
const plannedRule = patch => ({ id: 'test-rule', sourceId: 'snapshot', when: 'after_edit', method: 'checker', check: 'identity', severity: 'error', componentKeys: ['unique:u'], netNames: [], stageId: 'local', ...patch });

test('R2 preconditions reject unavailable required checks without sending any EDA call', async t => {
  let input = await stagedPlan(t, [{ id: 'local', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'r' } }] }]);
  input = await withRequirements(input, [plannedRule({ when: 'before_write', check: 'not-implemented' })]);
  let calls = 0;
  const result = await planTool(input, stageCatalog, async () => { calls++; throw new Error('must not call'); });
  assert.equal(calls, 0); assert.equal(result.structuredContent.requirementResults.blocking, true);
  assert.equal(result.structuredContent.execution, null);
  for (const patch of [{ check: 'unsupported' }, { method: 'ai_review', check: 'visual_review' }]) {
    input = await withRequirements(input, [plannedRule(patch)]);
    await assert.rejects(planTool(input, stageCatalog, async () => { calls++; }), /after-edit check is unsupported/);
  }
  input = await withRequirements(input, [plannedRule()]);
  await assert.rejects(planTool({ ...input, edit: { complete: false } }, stageCatalog, async () => { calls++; }), /complete modification stage/);
  assert.equal(calls, 0, 'unsupported or permanently deferred hard checks must be rejected before writing');
});

test('R2 derives one checked edit unit and retains WARN while mapping actual local evidence', async t => {
  let input = await stagedPlan(t, [{ id: 'local', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'r', preserveInstance: true, patch: { x: 20 } } }] }]);
  input = await withRequirements(input, [plannedRule(), plannedRule({ id: 'S10', check: 'text_overlap', severity: 'warn' })]);
  const execution = batchReceipt([{ action: 'schematic.component.modify', ok: true, result: { primitiveId: 'r' } }]);
  execution.structuredContent.context = { projectUuid: 'p', documentUuid: 'd' };
  execution.structuredContent.result.rules = { scope: ['r'], status: 'pass', items: [
    { id: 'identity', status: 'pass', coverage: 'checked', severity: 'error' },
    { id: 'text_overlap', status: 'fail', coverage: 'checked', severity: 'warn' },
  ] };
  const calls = [];
  const result = await planTool(input, stageCatalog, async (action, args) => {
    calls.push(action);
    if (action === 'debug.batch') { assert.deepEqual(args.edit, { mode: 'layout', primitiveIds: ['r'], expectedIdentities: { r: { designator: 'R1', uniqueId: 'u' } } }); return execution; }
    return geometryReceipt(['r']);
  });
  assert.deepEqual(calls, ['debug.batch', 'schematic.components.list']);
  assert.equal(result.structuredContent.ok, true);
  assert.deepEqual(result.structuredContent.requirementResults.items.map(item => item.status), ['pass', 'fail']);
  execution.structuredContent.result.rules.items[0].status = 'unknown';
  const unknown = await planTool(input, stageCatalog, async action => action === 'debug.batch' ? execution : geometryReceipt(['r']));
  assert.equal(unknown.structuredContent.ok, false);
  assert.equal(unknown.structuredContent.stage.completed, true, 'retain completed writes/readback; missing rule evidence never asks for replay');
  execution.structuredContent.result.rules.items[0].status = 'pass';
  execution.structuredContent.result.results[0].action = 'schematic.wire.create';
  const mismatched = await planTool(input, stageCatalog, async action => action === 'debug.batch' ? execution : geometryReceipt(['r']));
  assert.equal(mismatched.structuredContent.ok, false, 'a success receipt for a different action cannot prove this batch');
});

test('R2 final delivery runs one explicit DRC and preserves its error or transport failure', async t => {
  let input = await stagedPlan(t, [{ id: 'final', check: 'final', steps: [] }]);
  input = await withRequirements(input, [plannedRule({ stageId: 'final', when: 'delivery', method: 'native', check: 'drc' })]);
  const actions = new Map([...stageCatalog, ['schematic.drc.check', {}]]), calls = [];
  const result = await planTool(input, actions, async action => {
    calls.push(action);
    return toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: action === 'schematic.target.check'
      ? { status: 'pass', verified: true, partial: false } : { countsAvailable: true, summary: { error: 0, fatal: 0, unknown: 0, warn: 99 } } } });
  });
  assert.deepEqual(calls, ['schematic.target.check', 'schematic.drc.check']); assert.equal(result.structuredContent.ok, true);
  const failed = await planTool(input, actions, async action => action === 'schematic.drc.check'
    ? { isError: true, structuredContent: { error: 'unavailable' } }
    : toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: { status: 'pass', verified: true, partial: false } } }));
  assert.equal(failed.structuredContent.ok, false); assert.equal(failed.structuredContent.requirementResults.status, 'unknown');
});

test('geometry completion rejects missing/different scope and another page context', async t => {
  const input = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: {} }] }]);
  for (const mutate of [
    receipt => delete receipt.structuredContent.result.readScope.primitiveIds,
    receipt => receipt.structuredContent.result.readScope.primitiveIds = ['other'],
    receipt => receipt.structuredContent.context.documentUuid = 'other',
  ]) {
    const readback = geometryReceipt(['created']); mutate(readback);
    const result = await planTool(input, stageCatalog, async action => action === 'debug.batch'
      ? batchReceipt([{ action: 'schematic.component.place', ok: true, result: { primitiveId: 'created' } }]) : readback);
    assert.equal(result.structuredContent.stage.completed, false);
  }
});

test('managed dryRun creates no receipt and final writes check scoped geometry before sending a batch', async t => {
  const input = await stagedPlan(t, [
    { id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: {} }] },
    { id: 'connect', check: 'final', primitiveIds: ['created'], steps: [{ action: 'schematic.wire.create', payload: { net: 'VIN' } }] },
  ]);
  const { plan } = await loadPlan(input.planFile), directory = path.join(path.dirname(input.planFile), 'receipts');
  plan.executionReceipts = { directory }; await writeFile(input.planFile, JSON.stringify(plan));
  input.expectedHash = (await loadPlan(input.planFile)).sha256;
  await planTool({ ...input, dryRun: true }, stageCatalog, async () => toMcpResult({ ok: true, result: { result: { dryRun: true } } }));
  await assert.rejects(readFile(path.join(directory, 'stage-01.json')), { code: 'ENOENT' });
  await planTool(input, stageCatalog, async action => action === 'debug.batch'
    ? batchReceipt([{ action: 'schematic.component.place', ok: true, result: { primitiveId: 'created' } }]) : geometryReceipt(['created']));
  let calls = 0;
  await assert.rejects(planTool({ ...input, stage: 'connect' }, stageCatalog, async action => {
    calls++; assert.equal(action, 'schematic.components.list');
    const readback = geometryReceipt(['created']); readback.structuredContent.result.components[0].bbox.maxX = 50; return readback;
  }), /geometry changed/);
  assert.equal(calls, 1);
  const receipt = JSON.parse(await readFile(path.join(directory, 'stage-02.json'), 'utf8'));
  assert.equal(receipt.state, 'unknown'); assert.ok(receipt.receipt.preflightReadback);
});

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
      : toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: { ok: true, status: 'pass', verified: true } } });
  });
  assert.deepEqual(calls.map(c => c.action), ['debug.batch', 'schematic.target.check']);
  assert.equal(result.structuredContent.stage.completed, true); assert.equal(result.structuredContent.wholePageComplete, false);
  assert.equal(calls[1].input.payload.semanticTarget.components[0].key, 'unique:u');
  let count = 0;
  const partial = await planTool(input, stageCatalog, async () => { count++; return batchReceipt([{ index: 0, action: steps[0].action, ok: false, error: { message: 'failed' } }]); });
  assert.equal(count, 1); assert.equal(partial.isError, true); assert.equal(partial.structuredContent.targetCheck, null);
  const checkOnly = await stagedPlan(t, [{ id: 'final', check: 'final', steps: [] }]);
  count = 0;
  await planTool(checkOnly, stageCatalog, async action => { count++; assert.equal(action, 'schematic.target.check'); return toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: { status: 'pass', verified: true } } }); });
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

test('staged apply writes one receipt envelope before EDA and settles geometry, partial, final, and unknown outcomes', async t => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-plan-receipt-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const readReceipt = async file => JSON.parse(await readFile(file, 'utf8'));
  const hashFile = async file => createHash('sha256').update(await readFile(file)).digest('hex');

  const geometry = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }]);
  const geometryFile = path.join(dir, 'geometry.json');
  let existedBeforeCall = false;
  const geometryResult = await planTool({ ...geometry, receiptFile: geometryFile }, stageCatalog, async action => {
    existedBeforeCall = await readFile(geometryFile, 'utf8').then(() => true, () => false);
    return action === 'debug.batch'
      ? batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'created' } }])
      : geometryReceipt(['created']);
  });
  const geometryEnvelope = await readReceipt(geometryFile);
  assert.equal(existedBeforeCall, true); assert.equal(geometryEnvelope.state, 'settled');
  assert.equal(geometryEnvelope.schemaVersion, 'easyeda.execution/1'); assert.equal(geometryEnvelope.dryRun, false);
  assert.equal(geometryEnvelope.stage.completed, true); assert.equal(geometryEnvelope.receipt.execution.structuredContent.requestId, 'batch');
  assert.equal(geometryEnvelope.receipt.readback.structuredContent.requestId, 'geometry');
  assert.equal(geometryEnvelope.receipt.targetCheck, null); assert.deepEqual(geometryEnvelope.sources, (await loadPlan(geometry.planFile)).plan.sources);
  assert.equal(geometryResult.structuredContent.receiptFile, geometryFile);
  assert.equal(geometryResult.structuredContent.receiptSha256, await hashFile(geometryFile));

  const partial = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }]);
  const partialFile = path.join(dir, 'partial.json');
  await planTool({ ...partial, receiptFile: partialFile }, stageCatalog, async action => action === 'debug.batch'
    ? batchReceipt([{ index: 0, action: 'schematic.component.place', ok: false, error: { message: 'failed' } }]) : geometryReceipt([]));
  const partialEnvelope = await readReceipt(partialFile);
  assert.equal(partialEnvelope.state, 'settled'); assert.equal(partialEnvelope.receipt.partial, true);

  const final = await stagedPlan(t, [{ id: 'finish', check: 'final', steps: [] }]);
  const finalFile = path.join(dir, 'final.json');
  await planTool({ ...final, receiptFile: finalFile }, stageCatalog, async () => toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: { status: 'pass', verified: true } } }));
  const finalEnvelope = await readReceipt(finalFile);
  assert.equal(finalEnvelope.state, 'settled'); assert.equal(finalEnvelope.stage.check, 'final');
  assert.equal(finalEnvelope.receipt.targetCheck.structuredContent.result.status, 'pass');

  const unknown = await stagedPlan(t, [{ id: 'finish', check: 'final', steps: [] }]);
  const unknownFile = path.join(dir, 'unknown.json');
  await assert.rejects(planTool({ ...unknown, receiptFile: unknownFile }, stageCatalog, async () => { throw new Error('cancelled'); }), /cancelled/);
  const unknownEnvelope = await readReceipt(unknownFile);
  assert.equal(unknownEnvelope.state, 'unknown'); assert.equal(unknownEnvelope.receipt.error.message, 'cancelled');
});

test('receiptFile rejects invalid routes before EDA and preserves calls without the option', async t => {
  const input = await stagedPlan(t, [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }]);
  let calls = 0;
  const never = async () => { calls++; throw new Error('must not call EDA'); };
  await assert.rejects(planTool({ operation: 'inspect', planFile: input.planFile, receiptFile: path.join(os.tmpdir(), 'inspect.json') }, stageCatalog, never), /only to apply/);
  await assert.rejects(planTool({ ...input, dryRun: true, receiptFile: path.join(os.tmpdir(), 'dry.json') }, stageCatalog, never), /dryRun/);
  await assert.rejects(planTool({ ...input, receiptFile: 'relative.json' }, stageCatalog, never), /absolute/);
  const occupied = path.join(os.tmpdir(), `easyeda-plan-receipt-${Date.now()}.json`);
  await writeFile(occupied, '{}'); t.after(() => rm(occupied, { force: true }));
  await assert.rejects(planTool({ ...input, receiptFile: occupied }, stageCatalog, never), /EEXIST/);
  assert.equal(calls, 0);
  const result = await planTool(input, stageCatalog, async action => action === 'debug.batch'
    ? batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'created' } }]) : geometryReceipt(['created']));
  assert.equal(result.structuredContent.receiptFile, undefined);
});

test('managed staged receipts enforce predecessor completion, permit observed IDs in later revisions, and preserve drift readback', async t => {
  const input = await stagedPlan(t, [
    { id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] },
    { id: 'move', check: 'geometry', primitiveIds: ['created'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'created', patch: { x: 30 } } }] },
  ]);
  const { plan } = await loadPlan(input.planFile), receiptDir = path.join(path.dirname(input.planFile), 'receipts');
  plan.executionReceipts = { directory: receiptDir }; await writeFile(input.planFile, JSON.stringify(plan));
  input.expectedHash = (await loadPlan(input.planFile)).sha256;
  const second = { ...input, stage: 'move' };
  let calls = 0;
  await assert.rejects(planTool(second, stageCatalog, async () => { calls++; }), /Previous stage receipt is missing/);
  assert.equal(calls, 0);
  const first = await planTool(input, stageCatalog, async action => action === 'debug.batch'
    ? batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'created' } }]) : geometryReceipt(['created']));
  assert.match(first.structuredContent.receiptFile, /stage-01\.json$/);
  await assert.rejects(planTool(input, stageCatalog, async () => assert.fail('must not replay')), /EEXIST/);

  const revised = (await loadPlan(input.planFile)).plan;
  revised.revision++;
  revised.semanticTarget.idMap = { 'unique:u': 'created' };
  await writeFile(input.planFile, JSON.stringify(revised));
  second.expectedHash = (await loadPlan(input.planFile)).sha256;
  const callsAfterRevision = [];
  const completed = await planTool(second, stageCatalog, async action => {
    callsAfterRevision.push(action);
    if (callsAfterRevision.length === 1) return geometryReceipt(['created']);
    if (action === 'debug.batch') return batchReceipt([{ index: 0, action: 'schematic.component.modify', ok: true, result: { primitiveId: 'created' } }]);
    return geometryReceipt(['created']);
  });
  assert.equal(completed.structuredContent.stage.completed, true);
  assert.deepEqual(callsAfterRevision, ['schematic.components.list', 'debug.batch', 'schematic.components.list']);

  const driftInput = await stagedPlan(t, [
    { id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] },
    { id: 'move', check: 'geometry', primitiveIds: ['created'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'created', patch: { x: 30 } } }] },
  ]);
  const driftPlan = (await loadPlan(driftInput.planFile)).plan, driftDir = path.join(path.dirname(driftInput.planFile), 'receipts');
  driftPlan.executionReceipts = { directory: driftDir }; await writeFile(driftInput.planFile, JSON.stringify(driftPlan));
  driftInput.expectedHash = (await loadPlan(driftInput.planFile)).sha256;
  await planTool(driftInput, stageCatalog, async action => action === 'debug.batch'
    ? batchReceipt([{ index: 0, action: 'schematic.component.place', ok: true, result: { primitiveId: 'created' } }]) : geometryReceipt(['created']));
  const driftRevision = (await loadPlan(driftInput.planFile)).plan;
  driftRevision.revision++; driftRevision.semanticTarget.idMap = { 'unique:u': 'created' };
  await writeFile(driftInput.planFile, JSON.stringify(driftRevision));
  const driftSecond = { ...driftInput, stage: 'move', expectedHash: (await loadPlan(driftInput.planFile)).sha256 };
  let driftCalls = 0;
  await assert.rejects(planTool(driftSecond, stageCatalog, async action => {
    driftCalls++; assert.equal(action, 'schematic.components.list');
    const changed = geometryReceipt(['created']); changed.structuredContent.result.components[0].bbox.maxX = 99; return changed;
  }), /geometry changed/);
  assert.equal(driftCalls, 1);
  const driftReceipt = JSON.parse(await readFile(path.join(driftDir, 'stage-02.json'), 'utf8'));
  assert.equal(driftReceipt.state, 'unknown'); assert.ok(driftReceipt.receipt.preflightReadback);
});

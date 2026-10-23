import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { captureSnapshot } from '../src/plan-tools.mjs';
import { compileLabels } from '../src/label-tools.mjs';
import { loadPlan, managedStageReceiptFile, stageReceiptBinding } from '../src/project-files.mjs';

const observation = { isError: false, structuredContent: { ok: true, context: { projectUuid: 'p', documentUuid: 'd', documentType: 'schematic' }, result: {
  readScope: { complete: true, concurrentChange: false }, components: [{ componentType: 'part', uniqueId: 'u', primitiveId: 'r', designator: 'R1', pins: [{ pinNumber: '1', net: 'VIN', noConnected: false }], footprint: { uuid: 'f', name: '0603' } }], wires: [], pagePrimitives: { attributes: [] },
} } };

async function fixture(t) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-label-tools-')); t.after(() => rm(dir, { recursive: true, force: true }));
  const snapshotFile = path.join(dir, 'snapshot.json'), planFile = path.join(dir, 'plan.json');
  await captureSnapshot({ domain: 'schematic', outputFile: snapshotFile }, async () => observation);
  const { preparePlan } = await import('../src/project-files.mjs');
  await preparePlan(snapshotFile, planFile);
  const { plan } = await loadPlan(planFile);
  plan.confirmation = { status: 'confirmed', authoredBy: 'caller', evidence: 'Confirmed label compiler test fixture' };
  plan.stages = [
    { id: 'wire', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'schematic.wire.create', payload: { points: [0, 0, 10, 0] } }] },
    { id: 'labels', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'r', patch: { x: 2 } } }] },
  ];
  plan.executionReceipts = { directory: path.join(dir, 'receipts') };
  await writeFile(planFile, JSON.stringify(plan));
  const loaded = await loadPlan(planFile), receiptFile = managedStageReceiptFile(loaded.plan, 0);
  await mkdir(path.dirname(receiptFile), { recursive: true });
  const receipt = { schemaVersion: 'easyeda.execution/1', planFile: loaded.planFile, sha256: loaded.sha256, revision: loaded.plan.revision,
    target: loaded.plan.target, sources: loaded.plan.sources.map(({ id, path, sha256 }) => ({ id, path, sha256 })),
    stage: { id: 'wire', check: 'geometry', completed: true }, binding: stageReceiptBinding(loaded.plan, loaded.plan.stages[0]), state: 'settled', dryRun: false,
    startedAt: '2026-10-08T00:00:00.000Z', completedAt: '2026-10-08T00:00:01.000Z',
    receipt: { ok: true, partial: false, stage: { id: 'wire', check: 'geometry', completed: true }, execution: { structuredContent: { result: { ok: true, partial: false, completed: 1, total: 1, results: [
      { action: 'schematic.wire.create', ok: true, result: { primitiveId: 'actual-wire' }, payload: { primitiveId: 'forged-wire' } },
    ] } } } },
  };
  await writeFile(receiptFile, JSON.stringify(receipt));
  return { dir, planFile, receiptFile, receipt, expectedHash: loaded.sha256, outputFile: path.join(dir, 'labels.json') };
}

test('compileLabels honors explicit hiding of observed wire Names and never trusts payload IDs', async t => {
  const input = await fixture(t);
  const result = await compileLabels({ ...input, wireReceiptFile: input.receiptFile, stage: 'labels', labels: [] });
  assert.equal(result.wireCount, 1); assert.equal(result.labelCount, 0);
  const artifact = JSON.parse(await readFile(input.outputFile, 'utf8'));
  assert.deepEqual(artifact.typedStep, { action: 'schematic.wire.labels.apply', payload: { wireIds: ['actual-wire'], labels: [] } });
  assert.deepEqual(artifact.typedSteps, [artifact.typedStep]);
  assert.equal(artifact.wireReceiptFile, input.receiptFile);
});

test('compileLabels permits only explicit labels for observed wire IDs and rejects stale or overwrite paths', async t => {
  const input = await fixture(t);
  const labelled = path.join(input.dir, 'shown.json');
  const result = await compileLabels({ ...input, outputFile: labelled, wireReceiptFile: input.receiptFile, stage: 'labels', labels: [{ wireId: 'actual-wire', x: 10, y: 20, rotation: 0 }] });
  assert.equal(result.labelCount, 1);
  assert.deepEqual(JSON.parse(await readFile(labelled, 'utf8')).typedStep.payload.labels,
    [{ wireId: 'actual-wire', x: 10, y: 20, rotation: 0 }]);
  await assert.rejects(compileLabels({ ...input, outputFile: path.join(input.dir, 'forged.json'), wireReceiptFile: input.receiptFile, stage: 'labels', labels: [{ wireId: 'forged-wire' }] }), /outside/);
  await assert.rejects(compileLabels({ ...input, outputFile: labelled, wireReceiptFile: input.receiptFile, stage: 'labels', labels: [] }), /EEXIST/);
  await assert.rejects(compileLabels({ ...input, expectedHash: '0'.repeat(64), outputFile: path.join(input.dir, 'old.json'), wireReceiptFile: input.receiptFile, stage: 'labels', labels: [] }), /hash changed/);
  const wrong = structuredClone(input.receipt); wrong.target = { projectUuid: 'other', documentUuid: 'd' };
  await writeFile(input.receiptFile, JSON.stringify(wrong));
  await assert.rejects(compileLabels({ ...input, outputFile: path.join(input.dir, 'wrong.json'), wireReceiptFile: input.receiptFile, stage: 'labels', labels: [] }), /binding changed/);
});

test('compileLabels emits automatic positions from actual wire geometry without changing its net or ID', async t => {
  const input = await fixture(t);
  const { plan } = await loadPlan(input.planFile);
  plan.layoutHints = { ...plan.layoutHints, visibleNetNames: ['VIN'] };
  plan.stages[0].steps = [{ action: 'schematic.wire.create', payload: { net: 'VIN', points: [[0, 0], [200, 0]] } }];
  await writeFile(input.planFile, JSON.stringify(plan));
  const loaded = await loadPlan(input.planFile);
  input.receipt.sha256 = loaded.sha256;
  input.receipt.binding = stageReceiptBinding(loaded.plan, loaded.plan.stages[0]);
  input.receipt.receipt.execution.structuredContent.result.results = [
    { index: 0, action: 'schematic.wire.create', ok: true, result: { primitiveId: 'actual-wire', net: 'VIN', line: [[0, 0], [200, 0]] } },
  ];
  input.receipt.receipt.readback = { structuredContent: { result: { components: [
    { componentType: 'part', bbox: { minX: 0, minY: 1, maxX: 200, maxY: 50 } },
  ] } } };
  await writeFile(input.receiptFile, JSON.stringify(input.receipt));
  await compileLabels({ ...input, expectedHash: loaded.sha256, wireReceiptFile: input.receiptFile, stage: 'labels' });
  const artifact = JSON.parse(await readFile(input.outputFile, 'utf8'));
  assert.equal(artifact.typedStep.action, 'schematic.wire.labels.apply');
  assert.deepEqual(artifact.typedStep.payload.wireIds, ['actual-wire']);
  const [label] = artifact.typedStep.payload.labels;
  assert.equal(label.wireId, 'actual-wire'); assert.equal(label.rotation, 0); assert.ok(label.y < 0);
  assert.equal(Object.hasOwn(label, 'net'), false);
});

test('compileLabels rejects partial wire receipts and any target-stage receipt', async t => {
  const input = await fixture(t);
  const partial = structuredClone(input.receipt); partial.receipt.partial = true;
  await writeFile(input.receiptFile, JSON.stringify(partial));
  await assert.rejects(compileLabels({ ...input, outputFile: path.join(input.dir, 'partial.json'), wireReceiptFile: input.receiptFile, stage: 'labels', labels: [] }), /complete settled/);
  await writeFile(input.receiptFile, JSON.stringify(input.receipt));
  const { plan } = await loadPlan(input.planFile), targetReceipt = managedStageReceiptFile(plan, 1);
  await mkdir(path.dirname(targetReceipt), { recursive: true }); await writeFile(targetReceipt, '{}');
  await assert.rejects(compileLabels({ ...input, outputFile: path.join(input.dir, 'target.json'), wireReceiptFile: input.receiptFile, stage: 'labels', labels: [] }), /already has a receipt/);
});

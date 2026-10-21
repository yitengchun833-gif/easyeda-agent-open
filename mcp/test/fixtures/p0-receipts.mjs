// Reused from the P0 interruption validation; all EDA data below is synthetic.
import net from 'node:net';
net.Socket.prototype.connect = function () { throw new Error('P0_OFFLINE_SOCKET_FORBIDDEN'); };
globalThis.fetch = async () => { throw new Error('P0_OFFLINE_FETCH_FORBIDDEN'); };
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { captureSnapshot, planTool } from '../../src/plan-tools.mjs';
import { loadPlan, verifyStagePrefix } from '../../src/project-files.mjs';
import { toMcpResult } from '../../src/core.mjs';

export { planTool, loadPlan, verifyStagePrefix, toMcpResult };
export const sha = bytes => createHash('sha256').update(bytes).digest('hex');
export const catalog = new Map(['schematic.target.check', 'schematic.component.modify', 'schematic.components.list'].map(name => [name, { name, mutates: name === 'schematic.component.modify' }]));
const observation = { isError: false, structuredContent: { ok: true, context: { projectUuid: 'p', documentUuid: 'd', documentType: 'schematic' }, result: {
  readScope: { complete: true, concurrentChange: false }, components: [{ componentType: 'part', uniqueId: 'u', primitiveId: 'r', designator: 'R1',
    pins: [{ pinNumber: '1', net: 'VIN', noConnected: false }], footprint: { uuid: 'f', name: '0603' } }], wires: [], pagePrimitives: { attributes: [] },
} } };

export async function fixture(dir) {
  await mkdir(dir, { recursive: true });
  const snapshotFile = path.join(dir, 'snapshot.json'), planFile = path.join(dir, 'plan.json');
  await captureSnapshot({ domain: 'schematic', outputFile: snapshotFile }, async () => structuredClone(observation));
  await planTool({ operation: 'prepare', snapshotFile, outputFile: planFile }, catalog);
  const { plan } = await loadPlan(planFile);
  plan.confirmation = { status: 'confirmed', authoredBy: 'caller', evidence: 'OFFLINE SYNTHETIC FIXTURE ONLY' };
  plan.steps = [];
  plan.semanticTarget.idMap = { 'unique:u': 'r' };
  plan.stages = [
    { id: 'move', check: 'geometry', primitiveIds: ['r'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'r', patch: { x: 10 } } }] },
    { id: 'final', check: 'final', steps: [] },
  ];
  plan.executionReceipts = { directory: path.join(dir, 'receipts') };
  await writeFile(planFile, JSON.stringify(plan));
  const { sha256: expectedHash } = await loadPlan(planFile);
  const input = { operation: 'apply', planFile, expectedHash, window: 'offline-window', stage: 'move' };
  await writeFile(path.join(dir, 'input.json'), JSON.stringify(input));
  return input;
}

export const geometry = () => toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: {
  components: [{ primitiveId: 'r', uniqueId: 'u', designator: 'R1', pinsAvailable: true,
    bbox: { minX: 0, minY: 0, maxX: 20, maxY: 30 }, pins: [{ pinNumber: '1', x: 10, y: 20, rotation: 0, net: 'VIN' }] }],
  readScope: { complete: false, concurrentChange: false, primitiveIds: ['r'], missingIds: [], netRead: 'not-requested' },
} } });
export const batch = () => toMcpResult({ ok: true, result: { requestId: 'offline-edit', context: { projectUuid: 'p', documentUuid: 'd' }, result: {
  ok: true, partial: false, completed: 1, total: 1,
  results: [{ index: 0, action: 'schematic.component.modify', ok: true, result: { primitiveId: 'r' } }],
  rules: { status: 'pass', items: [] },
} } });
export const target = () => toMcpResult({ ok: true, result: { context: { projectUuid: 'p', documentUuid: 'd' }, result: { status: 'pass', verified: true } } });
export const invoke = async action => action === 'debug.batch' ? batch() : action === 'schematic.target.check' ? target() : geometry();

export async function readyFinish(dir) {
  const input = await fixture(dir);
  const first = await planTool(input, catalog, invoke);
  if (!first.structuredContent.stage.completed) throw new Error('Fixture did not complete move');
  const last = await planTool({ ...input, stage: 'final' }, catalog, invoke);
  if (!last.structuredContent.stage.completed) throw new Error('Fixture did not complete final');
  const imagePath = path.join(dir, 'synthetic-image.bin');
  await writeFile(imagePath, 'SYNTHETIC IMAGE HASH FIXTURE, NOT AN EDA SCREENSHOT');
  const runResultFile = path.join(dir, 'run-result.json');
  const run = { ok: true, partial: false, remainingStages: [], completedStages: ['move', 'final'], planFile: input.planFile, sha256: input.expectedHash,
    target: { projectUuid: 'p', documentUuid: 'd' }, review: { imagePath, imageSha256: sha(await readFile(imagePath)), saved: false,
      visualReview: { operation: 'visual_review', window: input.window, requestId: 'offline-edit', generation: 1, screenshotRequestId: 'offline-image', source: 'ai' },
      finish: { operation: 'finish', window: input.window, requestId: 'offline-edit' } } };
  await writeFile(runResultFile, JSON.stringify({ structuredContent: run }));
  return { operation: 'finish', window: input.window, runResultFile, expectedRunResultHash: sha(await readFile(runResultFile)), visualAccepted: true };
}

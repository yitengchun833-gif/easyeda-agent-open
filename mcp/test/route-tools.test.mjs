import test from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { compileRoutes } from '../src/route-tools.mjs';
import { loadPlan, managedStageReceiptFile, stageReceiptBinding } from '../src/project-files.mjs';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');

async function fixture(t, visibleNetNames) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-route-tools-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const source = path.join(dir, 'snapshot.json');
  await writeFile(source, '{}');
  const sourceHash = hash(await readFile(source));
  const planFile = path.join(dir, 'plan.json');
  const receipts = path.join(dir, 'receipts');
  const plan = {
    schemaVersion: 'easyeda.plan/1', revision: 2, goal: '', target: { projectUuid: 'p', documentUuid: 'd' },
    sources: [{ id: 'snapshot', path: source, sha256: sourceHash }],
    semanticTarget: { schemaVersion: 1, scope: { complete: true, kind: 'page', projectUuid: 'p', documentUuid: 'd', readScope: {} },
      idMap: { 'unique:a': 'a', 'unique:b': 'b' },
      components: [
        { key: 'unique:a', uniqueId: 'a', ref: 'R1', sourcePrimitiveId: 'obsolete-a', pins: [{ number: '1', name: '1', net: 'VIN', noConnected: false, state: 'connected' }, { number: '2', name: '2', net: '', noConnected: true, state: 'nc' }] },
        { key: 'unique:b', uniqueId: 'b', ref: 'R2', sourcePrimitiveId: 'obsolete-b', pins: [{ number: '1', name: '1', net: 'VIN', noConnected: false, state: 'connected' }, { number: '2', name: '2', net: '', noConnected: false, state: 'unconnected' }] },
      ],
      nets: [{ name: 'VIN', scope: visibleNetNames?.includes('VIN') === false ? 'local' : 'unknown',
        ...(visibleNetNames?.includes('VIN') === false ? { match: 'members' } : {}),
        members: [{ componentKey: 'unique:a', pinNumber: '1' }, { componentKey: 'unique:b', pinNumber: '1' }] }],
    },
    layoutHints: { mode: 'preserve', parts: [], ...(visibleNetNames === undefined ? {} : { netPolicies: { VIN: 'direct' }, visibleNetNames }) },
    confirmation: { status: 'confirmed', authoredBy: 'caller', evidence: 'Controlled test fixture' },
    executionReceipts: { directory: receipts }, steps: [],
    stages: [{ id: 'place', check: 'geometry', primitiveIds: ['a', 'b'], steps: [] }, { id: 'wire', check: 'final', primitiveIds: ['a', 'b'], steps: [] }],
    missing: [], limits: [],
  };
  await writeFile(planFile, JSON.stringify(plan));
  const loaded = await loadPlan(planFile);
  const stage = plan.stages[0], binding = stageReceiptBinding(loaded.plan, stage);
  const components = ['a', 'b'].map((primitiveId, index) => ({
    primitiveId, designator: `R${index + 1}`, x: index * 100, y: 0, rotation: 0, mirror: false, pinsAvailable: true,
    bbox: { minX: index * 100 - 10, minY: -10, maxX: index * 100 + 10, maxY: 10 },
    pins: [{ pinNumber: '1', pinName: '1', x: index === 0 ? 10 : 90, y: 0, rotation: index === 0 ? 0 : 180 }, { pinNumber: '2', pinName: '2', x: index === 0 ? -10 : 110, y: 0, rotation: index === 0 ? 180 : 0 }],
  }));
 const receipt = { schemaVersion: 'easyeda.execution/1', state: 'settled', planFile: loaded.planFile, target: plan.target,
   stage: { id: 'place', check: 'geometry', completed: true }, sources: plan.sources, binding,
    startedAt: '2026-10-08T00:00:00.000Z', completedAt: '2026-10-08T00:00:01.000Z', dryRun: false,
    receipt: { ok: true, partial: false, stage: { id: 'place', check: 'geometry', completed: true }, primitiveIds: ['a', 'b'],
      readback: { isError: false, structuredContent: { context: plan.target, result: { partial: false, components, readScope: { concurrentChange: false, primitiveIds: ['a', 'b'], missingIds: [] } } } } } };
  await mkdir(receipts, { recursive: true });
  const receiptFile = managedStageReceiptFile(plan, 0);
  await writeFile(receiptFile, JSON.stringify(receipt));
  return { dir, planFile, expectedHash: loaded.sha256, receiptFile };
}

test('compiles measured receipt and semantic target through route-only plus validation without changing plan', async t => {
  const fx = await fixture(t), output = path.join(fx.dir, 'routes.json'), calls = [];
  const run = async args => {
    calls.push(args);
    return { ok: true, result: { schemaVersion: 1, layoutMode: 'route-only', wires: [{ net: 'VIN', points: [[-10, 0], [90, 0]] }] } };
  };
  const result = await compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: output, netPolicies: { VIN: 'direct' } }, run);
  assert.equal(result.ok, true); assert.equal(result.steps, 1); assert.deepEqual(calls.map(args => args.slice(0, 3)), [['sch', 'layout-plan', '--route-only'], ['sch', 'layout-plan', '--route-only']]);
  assert.ok(calls[1].includes('--validate-wires'));
  const artifact = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(artifact.typedSteps, [{ action: 'schematic.wire.create', payload: { net: 'VIN', points: [[-10, 0], [90, 0]] } }]);
  assert.equal(artifact.geometryReceiptSha256, hash(await readFile(fx.receiptFile)));
  assert.match(artifact.limits.join(' '), /no EDA write/);
  assert.equal(hash(await readFile(fx.planFile)), fx.expectedHash);
  await assert.rejects(compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: output, netPolicies: { VIN: 'direct' } }, run), /EEXIST|already exists/);
});

test('joins same-net degree-two endpoints into one native polyline without adding anonymous names', async t => {
  const fx = await fixture(t, []), output = path.join(fx.dir, 'joined.json');
  const points = [[450, 450], [450, 455], [300, 455], [300, 450]];
  const wires = [{ net: 'VIN', points: points.slice(0, 2) }, { net: 'VIN', points: points.slice(1, 3).toReversed() }, { net: 'VIN', points: points.slice(2) }];
  const result = await compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: output }, async () => ({ ok: true, result: { wires } }));
  assert.equal(result.steps, 1);
  const artifact = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(artifact.typedSteps, [{ action: 'schematic.wire.create', payload: { points } }]);
  assert.deepEqual(wires[1].points, [[300, 455], [450, 455]], 'joining does not mutate validated route geometry');
});

test('preserves branch endpoints, interior branches and disconnected paths during native path joining', async t => {
  const cases = [
    ['branch', [{ net: 'VIN', points: [[0, 0], [10, 0]] }, { net: 'VIN', points: [[10, 0], [20, 0]] }, { net: 'VIN', points: [[10, 0], [10, 10]] }], 3],
    ['interior branch', [{ net: 'VIN', points: [[0, 0], [20, 0]] }, { net: 'VIN', points: [[20, 0], [30, 0]] }, { net: 'VIN', points: [[20, 0], [20, 10]] }], 3],
    ['T contact on segment', [{ net: 'VIN', points: [[0, 0], [20, 0]] }, { net: 'VIN', points: [[10, -10], [10, 0]] }, { net: 'VIN', points: [[10, 0], [10, 10]] }], 3],
    ['separate islands', [{ net: 'VIN', points: [[0, 0], [10, 0]] }, { net: 'VIN', points: [[20, 0], [30, 0]] }], 2],
  ];
  for (const [name, wires, steps] of cases) {
    const fx = await fixture(t), output = path.join(fx.dir, 'paths.json');
    const result = await compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
      stage: 'wire', outputFile: output, netPolicies: { VIN: 'direct' } }, async () => ({ ok: true, result: { wires } }));
    assert.equal(result.steps, steps, name);
    const artifact = JSON.parse(await readFile(output, 'utf8'));
    assert.deepEqual(artifact.typedSteps.map(step => step.payload.points), wires.map(wire => wire.points), name);
  }
});

test('removes rotated-pin floating tails before emitting a native polyline', async t => {
  const fx = await fixture(t, ['VIN']), output = path.join(fx.dir, 'rounded.json');
  const wires = [{ net: 'VIN', points: [[-10, 0], [40, 0], [40 + 1e-12, 0], [90, 0]] }];
  await compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: output }, async () => ({ ok: true, result: { wires } }));
  const artifact = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(artifact.typedSteps[0].payload.points, [[-10, 0], [40, 0], [90, 0]]);
  assert.equal(wires[0].points.length, 4, 'the measured source remains unchanged');
});

test('keeps the explicit visible-name seed on a joined route and rejects unanchored anonymous islands', async t => {
  for (const visible of [['VIN'], []]) {
    const fx = await fixture(t, visible), output = path.join(fx.dir, 'names.json');
    const wires = [{ net: 'VIN', points: [[10, 0], [50, 0]] }, { net: 'VIN', points: [[50, 0], [90, 0]] }];
    await compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
      stage: 'wire', outputFile: output }, async () => ({ ok: true, result: { wires } }));
    const artifact = JSON.parse(await readFile(output, 'utf8'));
    assert.deepEqual(artifact.typedSteps, [{ action: 'schematic.wire.create', payload: { ...(visible.length ? { net: 'VIN' } : {}), points: [[10, 0], [50, 0], [90, 0]] } }]);
  }
  const fx = await fixture(t, []);
  await assert.rejects(compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: path.join(fx.dir, 'islands.json') }, async () => ({ ok: true, result: { wires: [
      { net: 'VIN', points: [[10, 0], [40, 0]] }, { net: 'VIN', points: [[60, 0], [90, 0]] },
    ] } })), /route island/);
});

test('does not join different nets even when route endpoints coincide', async t => {
  const fx = await fixture(t), plan = JSON.parse(await readFile(fx.planFile, 'utf8'));
  for (const part of plan.semanticTarget.components) Object.assign(part.pins[1], { net: 'OTHER', noConnected: false, state: 'connected' });
  plan.semanticTarget.nets.push({ name: 'OTHER', scope: 'unknown', members: plan.semanticTarget.components.map(part => ({ componentKey: part.key, pinNumber: '2' })) });
  await writeFile(fx.planFile, JSON.stringify(plan));
  const loaded = await loadPlan(fx.planFile), receipt = JSON.parse(await readFile(fx.receiptFile, 'utf8'));
  receipt.binding = stageReceiptBinding(loaded.plan, loaded.plan.stages[0]);
  await writeFile(fx.receiptFile, JSON.stringify(receipt));
  const wires = [{ net: 'VIN', points: [[0, 0], [10, 0]] }, { net: 'OTHER', points: [[10, 0], [20, 0]] }], output = path.join(fx.dir, 'different-nets.json');
  const result = await compileRoutes({ planFile: fx.planFile, expectedHash: loaded.sha256, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: output, netPolicies: { VIN: 'direct', OTHER: 'direct' } }, async () => ({ ok: true, result: { wires } }));
  assert.equal(result.steps, 2);
  const artifact = JSON.parse(await readFile(output, 'utf8'));
  assert.deepEqual(artifact.typedSteps.map(step => step.payload), wires);
});

test('rejects partial measurement and sourcePrimitiveId fallback before the CLI runs', async t => {
  const fx = await fixture(t), receipt = JSON.parse(await readFile(fx.receiptFile, 'utf8'));
  receipt.receipt.readback.structuredContent.result.partial = true;
  await writeFile(fx.receiptFile, JSON.stringify(receipt));
  let called = false;
  await assert.rejects(compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: path.join(fx.dir, 'bad.json'), netPolicies: { VIN: 'direct' } }, async () => { called = true; }), /complete successful scoped measurement/);
  assert.equal(called, false);
  receipt.receipt.readback.structuredContent.result.partial = false;
  const { plan } = await loadPlan(fx.planFile); delete plan.semanticTarget.idMap['unique:b']; await writeFile(fx.planFile, JSON.stringify(plan));
  const current = await loadPlan(fx.planFile);
  await assert.rejects(compileRoutes({ planFile: fx.planFile, expectedHash: current.sha256, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: path.join(fx.dir, 'mapping.json'), netPolicies: { VIN: 'direct' } }, async () => { called = true; }), /primitiveIds|binding|current primitive ID/);
  assert.equal(called, false);
});

test('rejects unknown targets, policy gaps and non-authoritative geometry without invoking the CLI', async t => {
  const cases = [
    ['unknown semantic pin', async fx => { const { plan } = await loadPlan(fx.planFile); plan.semanticTarget.components[0].pins[0].state = 'unknown'; plan.semanticTarget.components[0].pins[0].net = null; plan.semanticTarget.nets[0].members = [{ componentKey: 'unique:b', pinNumber: '1' }]; await writeFile(fx.planFile, JSON.stringify(plan)); return { expectedHash: (await loadPlan(fx.planFile)).sha256, policies: { VIN: 'direct' } }; }],
    ['policy gap', async fx => ({ expectedHash: fx.expectedHash, policies: {} })],
    ['incomplete pin geometry', async fx => { const r = JSON.parse(await readFile(fx.receiptFile)); delete r.receipt.readback.structuredContent.result.components[0].pins[0].rotation; await writeFile(fx.receiptFile, JSON.stringify(r)); return { expectedHash: fx.expectedHash, policies: { VIN: 'direct' } }; }],
    ['wrong read context', async fx => { const r = JSON.parse(await readFile(fx.receiptFile)); r.receipt.readback.structuredContent.context = { projectUuid: 'wrong', documentUuid: 'd' }; await writeFile(fx.receiptFile, JSON.stringify(r)); return { expectedHash: fx.expectedHash, policies: { VIN: 'direct' } }; }],
    ['scope mismatch', async fx => { const r = JSON.parse(await readFile(fx.receiptFile)); r.receipt.readback.structuredContent.result.readScope.primitiveIds = ['a']; await writeFile(fx.receiptFile, JSON.stringify(r)); return { expectedHash: fx.expectedHash, policies: { VIN: 'direct' } }; }],
  ];
  for (const [name, mutate] of cases) {
    const fx = await fixture(t), input = await mutate(fx); let calls = 0;
    await assert.rejects(compileRoutes({ planFile: fx.planFile, expectedHash: input.expectedHash, geometryReceiptFile: fx.receiptFile, stage: 'wire', outputFile: path.join(fx.dir, name + '.json'), netPolicies: input.policies }, async () => { calls++; return { ok: true, result: { wires: [] } }; }), /unknown|policy|Geometry|scope|binding/i, name);
    assert.equal(calls, 0, name);
  }
});

test('uses the configured EasyEDA CLI for a valid two-resistor route-only case', { skip: !process.env.EASYEDA_BIN }, async t => {
  const fx = await fixture(t);
  const result = await compileRoutes({ planFile: fx.planFile, expectedHash: fx.expectedHash, geometryReceiptFile: fx.receiptFile,
    stage: 'wire', outputFile: path.join(fx.dir, 'actual.json'), netPolicies: { VIN: 'direct' } });
  assert.equal(result.ok, true);
  assert.ok(result.steps >= 1);
});


test('module interface stubs allow named remote members but reject multiple measured endpoints', async t => {
  const fx = await fixture(t, ['VIN']), plan = JSON.parse(await readFile(fx.planFile, 'utf8'));
  plan.semanticTarget.components[0].pins[1] = { number: '2', name: '2', state: 'connected', net: 'OUT', noConnected: false };
  plan.semanticTarget.nets.push({ name: 'OUT', members: [{ componentKey: 'unique:a', pinNumber: '2' }] });
  plan.layoutHints.visibleNetNames.push('OUT'); plan.layoutHints.netPolicies.OUT = 'direct';
  plan.layoutHints.interfaceStubs = [{ componentKey: 'unique:a', pinNumber: '2', length: 20 }];
  const remote = structuredClone(plan.semanticTarget.components[0]);
  remote.key = 'unique:remote'; remote.uniqueId = 'remote'; remote.ref = 'R3'; remote.sourcePrimitiveId = 'remote';
  remote.pins[0] = { number: '1', name: '1', state: 'unconnected', net: '', noConnected: false };
  plan.semanticTarget.components.push(remote); plan.semanticTarget.idMap[remote.key] = 'remote';
  plan.semanticTarget.nets.find(net => net.name === 'OUT').members.push({ componentKey: remote.key, pinNumber: '2' });
  await writeFile(fx.planFile, JSON.stringify(plan));
  const loaded = await loadPlan(fx.planFile), packet = JSON.parse(await readFile(fx.receiptFile, 'utf8'));
  packet.binding = stageReceiptBinding(loaded.plan, loaded.plan.stages[0]); await writeFile(fx.receiptFile, JSON.stringify(packet));
  const output = path.join(fx.dir, 'interfaces.json'); let calls = 0;
  await compileRoutes({ planFile: fx.planFile, expectedHash: loaded.sha256, geometryReceiptFile: fx.receiptFile, stage: 'wire', outputFile: output }, async args => {
    calls++;
    if (!args.includes('--validate-wires')) return { ok: true, result: { wires: [{ net: 'VIN', points: [[10, 0], [90, 0]] }] } };
    const packet = JSON.parse(await readFile(args.at(-1), 'utf8'));
    assert.deepEqual(packet.routes.find(route => route.net === 'OUT').points, [[-10, 0], [-30, 0]]);
    return { ok: true, result: { wires: packet.routes } };
  });
  assert.equal(calls, 2);
  assert.deepEqual(JSON.parse(await readFile(output, 'utf8')).typedSteps.find(step => step.payload.net === 'OUT').payload.points, [[-10, 0], [-30, 0]]);
  plan.layoutHints.interfaceStubs[0].pinNumber = '1'; await writeFile(fx.planFile, JSON.stringify(plan));
  const invalid = await loadPlan(fx.planFile); packet.binding = stageReceiptBinding(invalid.plan, invalid.plan.stages[0]); await writeFile(fx.receiptFile, JSON.stringify(packet));
  await assert.rejects(compileRoutes({ planFile: fx.planFile, expectedHash: invalid.sha256, geometryReceiptFile: fx.receiptFile, stage: 'wire', outputFile: path.join(fx.dir, 'invalid-scope.json') }), /one measured module endpoint/);
});

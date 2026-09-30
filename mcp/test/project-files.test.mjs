import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rm, mkdir, symlink } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { exportSnapshot, preparePlan, loadPlan, renderPlan, snapshotData, validatePlan } from '../src/project-files.mjs';

const fixture = () => ({ ok: true, requestId: 'observed', context: { projectUuid: 'p', documentUuid: 'd', documentType: 'schematic' }, result: {
  readScope: { complete: true, concurrentChange: false, allPages: false, primitiveIds: [], netRead: 'document-netlist' }, pinNetsAvailable: true,
  components: [{ componentType: 'part', primitiveId: 'old', uniqueId: 'u1', designator: 'R1', name: '10k', device: { uuid: 'd'.repeat(32), libraryUuid: 'lib', name: 'Resistor' }, symbol: { uuid: 's'.repeat(16), name: 'R' }, footprint: { uuid: 'f'.repeat(16), name: 'R0603' },
    otherProperty: { 'Pilot substitution': 'physical mapping unapproved' }, pins: [{ pinNumber: '1', pinName: '1', net: 'VIN', noConnected: false }, { pinNumber: '2', pinName: '2', net: '', noConnected: true }], x: 10, y: 20 }],
  pagePrimitives: { attributes: [{ primitiveId: 'attribute', value: 'original' }] }, wires: [{ primitiveId: 'wire', net: 'VIN' }]
} });

async function temporary(fn) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-project-files-'));
  try { await fn(dir); } finally { await rm(dir, { recursive: true, force: true }); }
}

test('native, audit and batch response archival preserves native references and never overwrites by default', () => temporary(async dir => {
  const data = fixture(), file = path.join(dir, 'snapshot.json');
  const output = await exportSnapshot({ ok: true, result: { results: [{ action: 'document.current', ok: true, result: {} }, data] } }, { outputFile: file });
  const saved = JSON.parse(await readFile(file, 'utf8'));
  assert.deepEqual(saved.snapshot, data.result);
  assert.deepEqual(saved.snapshot.components[0].footprint, data.result.components[0].footprint);
  assert.equal(output.scope.complete, true);
  assert.match(await readFile(output.markdownFile, 'utf8'), /R1/);
  await assert.rejects(exportSnapshot(data, { outputFile: file }), /overwrite/);
  await assert.rejects(exportSnapshot(data, { outputFile: 'relative.json' }), /absolute/);
  assert.throws(() => snapshotData({ ok: false, result: data.result }), /found 0/);
  assert.throws(() => snapshotData({ results: [data, data] }), /found 2/);
}));

test('plan retains independent pin target, provisional facts, exact source hash and caller-authored confirmation', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), planFile = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot });
  await preparePlan(snapshot, planFile, { goal: 'Recreate confirmed circuit' });
  const loaded = await loadPlan(planFile);
  assert.equal(loaded.plan.semanticTarget.components[0].key, 'unique:u1');
  assert.equal(loaded.plan.semanticTarget.components[0].pins[1].state, 'nc');
  assert.equal(loaded.plan.semanticTarget.components[0].properties.otherProperty['Pilot substitution'], 'physical mapping unapproved');
  assert.equal(loaded.plan.semanticTarget.nets[0].scope, 'unknown');
  assert.deepEqual(loaded.plan.steps, []);
  assert.equal(loaded.summary.humanApprovalVerified, false);
  await assert.rejects(loadPlan(planFile, { requireConfirmed: true }), /confirmation/);
  await assert.rejects(loadPlan(planFile, { expectedHash: '0'.repeat(64) }), /hash changed/);
  await writeFile(snapshot, '\n', { flag: 'a' });
  await assert.rejects(loadPlan(planFile), /Source hash changed/);
}));

test('local geometry patch stays partial, references immutable base and cannot become a full target', () => temporary(async dir => {
  const baseFile = path.join(dir, 'base.json'), patchFile = path.join(dir, 'patch.json');
  await exportSnapshot(fixture(), { outputFile: baseFile });
  const before = await readFile(baseFile, 'utf8'), patch = fixture();
  patch.result.readScope = { complete: true, primitiveIds: ['old'], netRead: 'not-requested' };
  patch.result.components[0].pins[0].net = null;
  const output = await exportSnapshot(patch, { outputFile: patchFile, baselineFile: baseFile });
  const value = JSON.parse(await readFile(patchFile, 'utf8'));
  assert.equal(output.scope.complete, false);
  assert.equal(value.baseline.mode, 'patch-reference');
  assert.equal(await readFile(baseFile, 'utf8'), before);
  assert.equal(value.snapshot.components[0].pins[0].net, null);
  await assert.rejects(preparePlan(patchFile, path.join(dir, 'plan.json')), /complete identified/);
  await assert.rejects(exportSnapshot(patch, { outputFile: baseFile, baselineFile: baseFile, overwrite: true }), /overwrite its baseline/);
}));

test('invalid target duplicate identities and contradictory net membership are rejected before execution', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan } = await loadPlan(file);
  const duplicate = structuredClone(plan); duplicate.semanticTarget.components.push(duplicate.semanticTarget.components[0]);
  assert.throws(() => validatePlan(duplicate), /duplicate/);
  const contradictory = structuredClone(plan); contradictory.semanticTarget.nets[0].members[0].pinNumber = '2';
  assert.throws(() => validatePlan(contradictory), /membership/);
  const unknown = structuredClone(plan); unknown.semanticTarget.components[0].pins[0].state = 'unknown'; unknown.semanticTarget.nets = [];
  assert.doesNotThrow(() => validatePlan(unknown));
}));

test('JSON and derived Markdown outputs cannot overwrite snapshots, baselines or additional sources', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json');
  await exportSnapshot(fixture(), { outputFile: snapshot });
  for (const suffix of ['json', 'md']) {
    const source = path.join(dir, `plan.${suffix}`), before = 'original engineering requirements';
    await writeFile(source, before);
    await assert.rejects(preparePlan(snapshot, path.join(dir, 'plan.json'), { overwrite: true, sources: [{ id: 'requirements', path: source }] }), /overwrite a source/);
    assert.equal(await readFile(source, 'utf8'), before);
    await rm(source);
  }
  const markdownSource = path.join(dir, 'drawing.md'), bytes = JSON.stringify(fixture());
  await writeFile(markdownSource, bytes);
  await assert.rejects(preparePlan(markdownSource, path.join(dir, 'drawing.json'), { overwrite: true }), /source snapshot/);
  await assert.rejects(exportSnapshot(fixture(), { outputFile: path.join(dir, 'drawing.json'), baselineFile: markdownSource, overwrite: true }), /overwrite its baseline/);
  assert.equal(await readFile(markdownSource, 'utf8'), bytes);
}));

test('Windows case aliases cannot overwrite a fixed source', { skip: process.platform !== 'win32' }, () => temporary(async dir => {
  const source = path.join(dir, 'baseline.json'), alias = path.join(dir, 'BASELINE.JSON');
  await exportSnapshot(fixture(), { outputFile: source });
  const before = await readFile(source, 'utf8');
  await assert.rejects(exportSnapshot(fixture(), { outputFile: alias, baselineFile: source, overwrite: true }), /overwrite its baseline/);
  await assert.rejects(preparePlan(source, alias, { overwrite: true }), /source snapshot/);
  assert.equal(await readFile(source, 'utf8'), before);
}));

test('directory links cannot bypass source overwrite protection', () => temporary(async dir => {
  const sourceDir = path.join(dir, 'source'), aliasDir = path.join(dir, 'alias');
  await mkdir(sourceDir);
  await symlink(sourceDir, aliasDir, process.platform === 'win32' ? 'junction' : 'dir');
  const source = path.join(sourceDir, 'baseline.json'), alias = path.join(aliasDir, 'baseline.json');
  await exportSnapshot(fixture(), { outputFile: source });
  const before = await readFile(source, 'utf8');
  await assert.rejects(exportSnapshot(fixture(), { outputFile: alias, baselineFile: source, overwrite: true }), /overwrite its baseline/);
  await assert.rejects(preparePlan(source, alias, { overwrite: true }), /source snapshot/);
  assert.equal(await readFile(source, 'utf8'), before);
}));

test('render refreshes the current plan view without changing JSON and protects source hashes and Markdown paths', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan, sha256: oldHash } = await loadPlan(file);
  plan.revision = 2; plan.confirmation = { status: 'confirmed', authoredBy: 'caller', evidence: 'record-2' };
  plan.layoutHints.mode = 'relayout'; plan.layoutHints.parts[0].x = 99;
  plan.steps = [{ action: 'schematic.component.modify', payload: { primitiveId: 'new-id', props: { x: 99 } } }];
  const bytes = JSON.stringify(plan, null, 2) + '\n'; await writeFile(file, bytes);
  await assert.rejects(renderPlan(file, { expectedHash: oldHash }), /Plan hash changed/);
  const { sha256 } = await loadPlan(file), rendered = await renderPlan(file, { expectedHash: sha256 });
  const markdown = await readFile(rendered.markdownFile, 'utf8');
  assert.equal(await readFile(file, 'utf8'), bytes); assert.equal(rendered.sha256, sha256);
  assert.match(markdown, /Revision: 2; confirmation: confirmed \(caller-authored; human approval not verified\)/);
  assert.match(markdown, /Layout: relayout; executable steps: 1/); assert.match(markdown, /"x": 99/);
  assert.match(markdown, /schematic\.component\.modify/); assert.match(markdown, /new-id/);
  plan.sources.push({ id: 'engineering-note', path: rendered.markdownFile, sha256: createHash('sha256').update(markdown).digest('hex') });
  await writeFile(file, JSON.stringify(plan));
  await assert.rejects(renderPlan(file), /Markdown cannot overwrite/);
  assert.equal(await readFile(rendered.markdownFile, 'utf8'), markdown);
  plan.sources.pop(); await writeFile(file, JSON.stringify(plan)); await writeFile(snapshot, '\n', { flag: 'a' });
  await assert.rejects(renderPlan(file), /Source hash changed/);
  assert.equal(await readFile(rendered.markdownFile, 'utf8'), markdown);
}));

test('optional stages are explicit, unambiguous and included in inspection and rendered review', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan } = await loadPlan(file);
  assert.equal(plan.stages, undefined); assert.deepEqual(plan.steps, []);
  plan.stages = [
    { id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] },
    { id: 'finish', check: 'final', steps: [] },
  ];
  await writeFile(file, JSON.stringify(plan));
  const loaded = await loadPlan(file), rendered = await renderPlan(file);
  assert.deepEqual(loaded.summary.stages.map(s => [s.id, s.check, s.steps]), [['place', 'geometry', 1], ['finish', 'final', 0]]);
  const markdown = await readFile(rendered.markdownFile, 'utf8');
  assert.match(markdown, /### place/); assert.match(markdown, /Stage completion is not whole-page delivery/);
  assert.match(markdown, /schematic\.component\.place/);
  for (const mutate of [
    p => p.stages.push(p.stages[0]),
    p => p.stages[0].check = 'final',
    p => p.stages[0].check = 'none',
    p => p.stages[0].primitiveIds = [],
    p => p.stages[0].primitiveIds = ['r', 'r'],
    p => p.stages[0].extra = true,
    p => p.steps.push({ action: 'schematic.component.place' }),
  ]) {
    const invalid = structuredClone(plan); mutate(invalid);
    assert.throws(() => validatePlan(invalid), /stage|Staged/);
  }
}));

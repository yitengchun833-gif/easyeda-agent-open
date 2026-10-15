import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rm, mkdir, symlink } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { exportSnapshot, preparePlan, loadPlan, managedStageReceiptFile, renderPlan, snapshotData, stageReceiptBinding, validatePlan, verifyStagePrefix, bindPlacedComponents } from '../src/project-files.mjs';

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

test('R1 declarations reuse source hashes, preserve expression intent and render as unexecuted', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json'), rules = path.join(dir, 'rules.md');
  await exportSnapshot(fixture(), { outputFile: snapshot });
  await writeFile(rules, '# Rules\nS10: readable labels\n');
  const requirement = { id: 'S10', sourceId: 'rules', when: 'delivery', method: 'ai_review', check: 'visual_review', severity: 'warn', componentKeys: ['unique:u1'], netNames: ['VIN'] };
  const layoutHints = { groups: [{ id: 'input', purpose: '输入支路', componentKeys: ['unique:u1'] }], netPolicies: { VIN: 'module_port' }, visibleNetNames: ['VIN'] };
  const prepared = await preparePlan(snapshot, file, { sources: [{ id: 'rules', path: rules, anchor: 'S10' }], requirements: [requirement], layoutHints });
  const loaded = await loadPlan(file, { knowledgeRead: () => { throw new Error('Ordinary rules must not query knowledge'); } });
  assert.deepEqual(loaded.summary.requirements, { status: 'declared_only', items: [requirement] });
  assert.deepEqual(loaded.summary.expression, { status: 'declared_only', ...layoutHints });
  assert.equal(loaded.plan.layoutHints.parts[0].x, 10);
  assert.deepEqual(loaded.plan.steps, []);
  const markdown = await readFile(prepared.markdownFile, 'utf8');
  assert.match(markdown, /Requirements \(declared_only\)/);
  assert.match(markdown, /S10 \/ rules/); assert.match(markdown, /ai_review \/ visual_review/);
  assert.match(markdown, /输入支路/); assert.match(markdown, /"visibleNetNames"/);
  const bytes = await readFile(file, 'utf8'); await renderPlan(file);
  assert.equal(await readFile(file, 'utf8'), bytes);
  loaded.plan.layoutHints.visibleNetNames = [];
  await writeFile(file, JSON.stringify(loaded.plan));
  await assert.rejects(loadPlan(file, { expectedHash: loaded.sha256 }), /Plan hash changed/);
  assert.deepEqual((await loadPlan(file)).summary.expression.visibleNetNames, []);
  delete loaded.plan.layoutHints.visibleNetNames; await writeFile(file, JSON.stringify(loaded.plan));
  assert.equal(Object.hasOwn((await loadPlan(file)).summary.expression, 'visibleNetNames'), false);
  await writeFile(rules, '\nchanged', { flag: 'a' });
  await assert.rejects(loadPlan(file), /Source hash changed: rules/);
}));

test('R1 rejects invalid declarations and accepts old plans without a new mandatory gate', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan, summary } = await loadPlan(file);
  assert.equal(Object.hasOwn(plan, 'requirements'), false); assert.equal(Object.hasOwn(summary, 'expression'), false);
  plan.stages = [{ id: 'place', check: 'geometry', steps: [] }, { id: 'finish', check: 'final', steps: [] }];
  const rule = { id: 'identity', sourceId: 'snapshot', when: 'after_edit', method: 'checker', check: 'identity', severity: 'error', componentKeys: ['unique:u1'], netNames: [], stageId: 'place' };
  plan.requirements = [rule, { ...rule, stageId: 'finish' }]; assert.equal(validatePlan(plan), plan);
  for (const patch of [{ sourceId: 'missing' }, { stageId: 'missing' }, { componentKeys: ['absent'] }, { netNames: ['absent'] }, { method: 'guess' }, { when: 'always' }, { check: ' ' }, { id: ' ' }, { severity: 'block' }, { componentKeys: ['unique:u1', 'unique:u1'] }, { result: 'pass' }, { toString: 'extra field' }]) {
    const bad = structuredClone(plan); Object.assign(bad.requirements[0], patch);
    assert.throws(() => validatePlan(bad), /Invalid requirement/);
  }
  const duplicate = structuredClone(plan); duplicate.requirements.push(structuredClone(rule));
  assert.throws(() => validatePlan(duplicate), /Duplicate requirement/);
  for (const hints of [{ groups: null }, { groups: [{ id: 'g', purpose: '', componentKeys: ['unique:u1'] }] }, { groups: [{ id: 'g', purpose: 'test', componentKeys: ['absent'] }] }, { netPolicies: { missing: 'direct' } }, { netPolicies: { VIN: 'hexagon' } }, { visibleNetNames: ['VIN', 'VIN'] }, { visibleNetNames: ['missing'] }]) {
    const bad = structuredClone(plan); bad.layoutHints = hints;
    assert.throws(() => validatePlan(bad), /expression|visible net|groups/);
  }
  plan.requirements = [{ ...rule, componentKeys: [], netNames: [] }]; assert.equal(validatePlan(plan), plan);
  await assert.rejects(preparePlan(snapshot, path.join(dir, 'bad.json'), { layoutHints: { parts: [] } }), /only accepts expression/);
}));

test('knowledge-bound plans verify pinned receipt, current conditions and explicit target adoption', () => temporary(async dir => {
  const snapshot=path.join(dir,'snapshot.json'),file=path.join(dir,'plan.json'),evidence=path.join(dir,'evidence.json');
  await exportSnapshot(fixture(),{outputFile:snapshot});await preparePlan(snapshot,file);
  const {plan}=await loadPlan(file);
  const context={evidenceRef:{schemaVersion:1,evidenceId:'ev-'+'a'.repeat(64),sourceId:'kb-fixture',sourceRevision:'r1',sourceHash:'b'.repeat(64),snapshotHash:'a'.repeat(64),selector:{chunkId:null,anchor:null}},identity:{models:['fixture']},scope:{documentVersion:'RevA'},applicability:{conditions:'fixture only',exclusions:'not hardware evidence'},verification:{measurement:'unknown'},states:{review:'approved',parse:'success'},content:{contextCompleteness:'complete',truncated:false,nextCursor:null}};
  await writeFile(evidence,JSON.stringify({readMode:'pinned',context}));
  plan.sources.push({id:'knowledge-fixture',path:evidence,sha256:createHash('sha256').update(await readFile(evidence)).digest('hex'),knowledge:{adoptedFor:'fixture pin allocation',conditions:'fixture only',componentKeys:['unique:u1'],netNames:['VIN'],checkMethod:'ai_review'}});
  await writeFile(file,JSON.stringify(plan));
  let calls=0;const knowledgeRead=async()=>{calls++;return {context:structuredClone(context)};};
  const loaded=await loadPlan(file,{knowledgeRead});assert.equal(calls,2);assert.equal(loaded.knowledgeChecks[0].checkResult,'unknown');
  assert.equal(loaded.summary.knowledgeBindings[0].adoptedFor,'fixture pin allocation');
  plan.semanticTarget.components.push({key:'unique:u2',uniqueId:'u2',ref:'R2',sourcePrimitiveId:'other',pins:[]});
  plan.stages=[{id:'local',check:'geometry',steps:[{action:'schematic.component.modify',payload:{primitiveId:'old',preserveInstance:true,patch:{x:20}}}]}];
  plan.sources.push({id:'unrelated-knowledge',path:path.join(dir,'unavailable.json'),sha256:'f'.repeat(64),knowledge:{adoptedFor:'other isolated part',conditions:'another module',componentKeys:['unique:u2'],netNames:[],checkMethod:'manual_unknown'}});
  await writeFile(file,JSON.stringify(plan));calls=0;
  const scoped=await loadPlan(file,{stage:'local',knowledgeRead});
  assert.equal(calls,2);assert.equal(scoped.knowledgeScope.kind,'stage');
  assert.equal(scoped.deferredKnowledgeSources[0].sourceId,'unrelated-knowledge');
  plan.requirements=[{id:'source-check',sourceId:'unrelated-knowledge',when:'before_write',method:'checker',check:'source_binding',severity:'error',componentKeys:[],netNames:[],stageId:'local'}];
  await writeFile(file,JSON.stringify(plan));
  await assert.rejects(loadPlan(file,{stage:'local',knowledgeRead}),/ENOENT/,'an explicitly required source cannot be deferred by its separate knowledge scope');
  delete plan.requirements;
  plan.stages[0].steps[0].payload.patch={name:'changed material'};await writeFile(file,JSON.stringify(plan));
  await assert.rejects(loadPlan(file,{stage:'local',knowledgeRead}),/ENOENT/,'unknown impact must expand to all dependencies');
  await assert.rejects(loadPlan(file,{knowledgeRead}),/ENOENT/);
  plan.sources.pop();delete plan.stages;await writeFile(file,JSON.stringify(plan));
  await assert.rejects(loadPlan(file,{knowledgeRead:async route=>({context:{...context,...(route.includes('id=')?{applicability:{conditions:'another condition'}}:{})}})}),/applicability changed/);
  await assert.rejects(loadPlan(file,{knowledgeRead:async()=>({context:{...context,evidenceRef:{...context.evidenceRef,sourceRevision:'r2'}}})}),/reference mismatch/);
  plan.sources.at(-1).knowledge.componentKeys=['missing'];assert.throws(()=>validatePlan(plan),/valid targets/);
  plan.sources.at(-1).knowledge.componentKeys=['unique:u1'];
  await writeFile(evidence,JSON.stringify({readMode:'pinned',context:{...context,content:{...context.content,contextCompleteness:'partial'}}}));
  plan.sources.at(-1).sha256=createHash('sha256').update(await readFile(evidence)).digest('hex');await writeFile(file,JSON.stringify(plan));
  await assert.rejects(loadPlan(file,{knowledgeRead}),/complete pinned/);
}));

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

test('managed stage receipts require staged plans and preserve only valid observed ID mappings', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan } = await loadPlan(file);
  plan.stages = [{ id: 'place', check: 'geometry', steps: [{ action: 'schematic.component.place', payload: { uuid: 'device' } }] }];
  plan.executionReceipts = { directory: path.join(dir, 'receipts') };
  assert.doesNotThrow(() => validatePlan(plan));
  const invalidDirectory = structuredClone(plan); invalidDirectory.executionReceipts = { directory: 'relative' };
  assert.throws(() => validatePlan(invalidDirectory), /executionReceipts/);
  const invalidKey = structuredClone(plan); invalidKey.semanticTarget.idMap = { missing: 'new' };
  assert.throws(() => validatePlan(invalidKey), /idMap/);
  const duplicate = structuredClone(plan); duplicate.semanticTarget.idMap = { 'unique:u1': 'same', another: 'same' };
  assert.throws(() => validatePlan(duplicate), /idMap/);
  delete plan.stages; assert.throws(() => validatePlan(plan), /executionReceipts/);
}));

test('managed prefix accepts only complete timed batch receipts with the exact planned actions', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan } = await loadPlan(file);
  plan.stages = [
    { id: 'wire', check: 'geometry', primitiveIds: ['old'], steps: [{ action: 'schematic.wire.create', payload: { points: [0, 0, 10, 0] } }] },
    { id: 'next', check: 'final', steps: [] },
  ];
  plan.executionReceipts = { directory: path.join(dir, 'receipts') };
  await writeFile(file, JSON.stringify(plan));
  const loaded = await loadPlan(file), receiptFile = managedStageReceiptFile(loaded.plan, 0);
  await mkdir(path.dirname(receiptFile), { recursive: true });
  const valid = () => ({ schemaVersion: 'easyeda.execution/1', planFile: loaded.planFile, target: loaded.plan.target,
    sources: loaded.plan.sources.map(({ id, path, sha256 }) => ({ id, path, sha256 })), state: 'settled', dryRun: false,
    startedAt: '2026-10-08T00:00:00.000Z', completedAt: '2026-10-08T00:00:01.000Z',
    stage: { id: 'wire', check: 'geometry', completed: true }, binding: stageReceiptBinding(loaded.plan, loaded.plan.stages[0]),
    receipt: { ok: true, partial: false, stage: { id: 'wire', check: 'geometry', completed: true }, execution: { structuredContent: { result: {
      ok: true, partial: false, completed: 1, total: 1, results: [{ action: 'schematic.wire.create', ok: true, result: { primitiveId: 'wire' } }],
    } } } },
  });
  for (const [name, mutate] of [
    ['pseudo complete', receipt => { receipt.receipt.execution.structuredContent.result.completed = 0; }],
    ['wrong action', receipt => { receipt.receipt.execution.structuredContent.result.results[0].action = 'schematic.component.modify'; }],
    ['missing time', receipt => { delete receipt.completedAt; }],
  ]) {
    const receipt = valid(); mutate(receipt); await writeFile(receiptFile, JSON.stringify(receipt));
    await assert.rejects(verifyStagePrefix(loaded, 1), /complete settled/, name);
  }
  await writeFile(receiptFile, JSON.stringify(valid()));
  await assert.doesNotReject(verifyStagePrefix(loaded, 1));
  const declaration = { id: 'pins', sourceId: 'snapshot', when: 'after_edit', method: 'checker', check: 'pin_nets', severity: 'error', componentKeys: ['unique:u1'], netNames: [], stageId: 'wire' };
  loaded.plan.requirements = [declaration];
  await assert.rejects(verifyStagePrefix(loaded, 1), /binding changed/, 'new applicable requirement cannot inherit an old receipt');
  await writeFile(receiptFile, JSON.stringify(valid()));
  await assert.doesNotReject(verifyStagePrefix(loaded, 1));
  loaded.plan.requirements.push({ ...declaration, stageId: 'next', when: 'delivery' });
  await assert.doesNotReject(verifyStagePrefix(loaded, 1), 'unexecuted future rules do not invalidate prior facts');
  loaded.plan.requirements[0].check = 'identity';
  await assert.rejects(verifyStagePrefix(loaded, 1), /binding changed/);
  loaded.plan.requirements = [];
  await assert.rejects(verifyStagePrefix(loaded, 1), /binding changed/, 'removing requirements cannot bypass the prior binding');
}));


test('bind derives IDs from complete placement receipts, rejects wrong pins and never rewrites executed stages', () => temporary(async dir => {
  const snapshot = path.join(dir, 'snapshot.json'), file = path.join(dir, 'plan.json');
  await exportSnapshot(fixture(), { outputFile: snapshot }); await preparePlan(snapshot, file);
  const { plan } = await loadPlan(file), part = plan.semanticTarget.components[0];
  const step = { action: 'schematic.component.place', payload: { designator: part.ref, libraryUuid: part.device.libraryUuid, uuid: part.device.uuid, x: 10, y: 20 } };
  plan.stages = [{ id: 'place', check: 'geometry', steps: [step] }, { id: 'measure', check: 'geometry', steps: [] }, { id: 'connect', check: 'geometry', steps: [] }];
  plan.executionReceipts = { directory: path.join(dir, 'receipts') }; plan.steps = [];
  plan.layoutHints.netPolicies = { VIN: 'direct' }; plan.layoutHints.visibleNetNames = ['VIN'];
  await writeFile(file, JSON.stringify(plan));
  const loaded = await loadPlan(file), packetStage = { id: 'place', check: 'geometry', completed: true };
  const record = { primitiveId: 'new', uniqueId: part.uniqueId, designator: part.ref, pinsAvailable: true, pins: part.pins.map(pin => ({ pinNumber: pin.number })) };
  const packet = { schemaVersion: 'easyeda.execution/1', state: 'settled', planFile: loaded.planFile, target: plan.target,
    sources: plan.sources, binding: stageReceiptBinding(plan, plan.stages[0]), dryRun: false,
    startedAt: '2026-10-12T00:00:00Z', completedAt: '2026-10-12T00:00:01Z', stage: packetStage,
    receipt: { ok: true, partial: false, stage: packetStage, execution: { structuredContent: { result: { ok: true, partial: false, total: 1, completed: 1,
      results: [{ action: step.action, ok: true, result: { primitiveId: 'new' } }] } } },
      readback: { structuredContent: { ok: true, context: plan.target, result: { components: [record], readScope: { concurrentChange: false, missingIds: [] } } } } } };
  const receipt = managedStageReceiptFile(plan, 0); await mkdir(path.dirname(receipt), { recursive: true });
  record.pins[0].pinNumber = 'wrong'; await writeFile(receipt, JSON.stringify(packet));
  const input = { planFile: file, expectedHash: loaded.sha256, stage: 'place', bindStages: ['measure', 'connect'], routeStages: { connect: 'measure' } };
  await assert.rejects(bindPlacedComponents(input), /pins differ/);
  assert.equal((await loadPlan(file)).sha256, loaded.sha256);
  record.pins[0].pinNumber = part.pins[0].number; await writeFile(receipt, JSON.stringify(packet));
  const bound = await bindPlacedComponents(input);
  assert.deepEqual(bound.primitiveIds, ['new']);
  const after = await loadPlan(file, { expectedHash: bound.sha256 });
  assert.equal(after.plan.semanticTarget.idMap[part.key], 'new');
  assert.equal(after.plan.stages[2].routeFrom, 'measure');
  await assert.rejects(bindPlacedComponents({ ...input, expectedHash: bound.sha256, bindStages: ['place'] }), /completed/);
}));

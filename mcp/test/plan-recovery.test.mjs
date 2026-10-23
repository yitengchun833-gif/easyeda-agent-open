import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, mkdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fixture, planTool, catalog, invoke, loadPlan, sha } from './fixtures/p0-receipts.mjs';
import { modulePlan, moduleCatalog, moduleInvoke } from './fixtures/module-plan.mjs';
import { bindPlacedComponents } from '../src/project-files.mjs';

async function setup(t) {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-recovery-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const input = await fixture(dir);
  const file = path.join(dir, 'receipts', 'stage-01.json');
  return { input, file, dir };
}

test('reconcile only reads and distinguishes a matching goal from an unknown write', async t => {
  const { input, file } = await setup(t);
  await assert.rejects(planTool(input, catalog, async () => { throw new Error('lost response'); }));
  const before = await readFile(file), calls = [];
  const result = await planTool({ ...input, operation: 'reconcile' }, catalog, async action => { calls.push(action); return invoke(action); });
  assert.equal(result.structuredContent.readOnly, true);
  assert.equal(result.structuredContent.goalSatisfied, true);
  assert.equal(result.structuredContent.ok, false);
  assert.deepEqual(result.structuredContent.blocking, ['move']);
  assert.equal(result.structuredContent.resumeInput, undefined);
  assert.deepEqual(calls, ['schematic.target.check']);
  assert.deepEqual(await readFile(file), before);
});

for (const [name, value] of [['pending', '{"state":"pending"}'], ['torn', '{'], ['empty', '']]) {
  test(`resume blocks ${name} receipt even if current goal matches`, async t => {
    const { input, file } = await setup(t);
    await mkdir(path.dirname(file)); await writeFile(file, value);
    const calls = [];
    const result = await planTool({ operation: 'resume', planFile:input.planFile, expectedHash:input.expectedHash, window:input.window, runStages: [{stage:'final'}] }, catalog,
      async action => { calls.push(action); return invoke(action); });
    assert.equal(result.structuredContent.ok, false);
    assert.deepEqual(calls, ['schematic.target.check']);
    assert.equal(await readFile(file, 'utf8'), value);
  });
}

test('resume consumes only the verified completed prefix; never repeats its mutation', async t => {
  const { input, file } = await setup(t);
  await planTool(input, catalog, invoke);
  const before = sha(await readFile(file));
  const reconciliation = await planTool({ ...input, operation: 'reconcile' }, catalog, invoke);
  assert.deepEqual(reconciliation.structuredContent.completedStages, ['move']);
  assert.deepEqual(reconciliation.structuredContent.remainingStages, ['final']);
  const calls = [];
  const resumed = await planTool(reconciliation.structuredContent.resumeInput, catalog, async action => { calls.push(action); return invoke(action); });
  assert.ok(!calls.includes('debug.batch'));
  assert.deepEqual(resumed.structuredContent.completedStages, ['move', 'final']);
  assert.equal(sha(await readFile(file)), before);
  assert.equal(JSON.parse(await readFile(file.replace('01','02'), 'utf8')).stage.completed, true);
});

test('changed or out-of-order receipts cannot authorize remaining work', async t => {
  const { input, file } = await setup(t);
  await planTool(input, catalog, invoke);
  const packet = JSON.parse(await readFile(file, 'utf8'));
  packet.binding.stageSha256 = '0'.repeat(64); await writeFile(file, JSON.stringify(packet));
  let result = await planTool({ ...input, operation:'reconcile' }, catalog, invoke);
  assert.equal(result.structuredContent.ok, false);
  await rm(file);
  await writeFile(file.replace('01','02'), JSON.stringify(packet));
  result = await planTool({ ...input, operation:'reconcile' }, catalog, invoke);
  assert.equal(result.structuredContent.ok, false);
});

test('any existing delivery receipt prevents another continuation or save attempt', async t => {
  const { input, dir } = await setup(t);
  await mkdir(path.join(dir,'receipts'));
  await writeFile(path.join(dir,'receipts','delivery.json'), JSON.stringify({ state:'settled', receipt:{saved:null} }));
  const result = await planTool({ ...input, operation:'reconcile' }, catalog, invoke);
  assert.equal(result.structuredContent.delivery.saved, null);
  assert.deepEqual(result.structuredContent.blocking, ['delivery']);
  assert.equal(result.structuredContent.resumeInput, undefined);
});

test('reconcile rejects changed sources and mismatched target before reading EDA', async t => {
  const { input } = await setup(t);
  await assert.rejects(planTool({ ...input, operation:'reconcile', target:{projectUuid:'wrong',documentUuid:'d'} },catalog,()=>assert.fail('no read')));
  const { plan } = await loadPlan(input.planFile);
  await writeFile(plan.sources[0].path,'changed');
  await assert.rejects(planTool({ ...input, operation:'reconcile' },catalog,()=>assert.fail('no read')));
});

for(const bound of [false,true]) test(`continuous resume after placement, bound=${bound}, reaches measurement without replacing parts`,async t=>{
  const {dir}=await setup(t), input=await modulePlan(path.join(dir,'module'));
  await planTool(input,moduleCatalog,moduleInvoke());
  let expectedHash=input.expectedHash;
  if(bound)expectedHash=(await bindPlacedComponents({planFile:input.planFile,expectedHash,stage:'place',bindStages:['measure','routes','labels']})).sha256;
  const controller=new AbortController(),calls=[];
  const mock=moduleInvoke(()=>controller.abort());
  const result=await planTool({operation:'resume',continuous:true,planFile:input.planFile,expectedHash,window:input.window,signal:controller.signal},moduleCatalog,async(action,args)=>{
    calls.push(args?.payload?.steps?.map(s=>s.action)??[action]);return mock(action,args);
  });
  assert.deepEqual(result.structuredContent.completedStages,['place','measure']);
  assert.equal(calls.flat().filter(action=>action==='schematic.component.place').length,0);
});

test('zero-prefix continuous resume retains the observed-placement ID guard',async t=>{
  const {dir}=await setup(t),input=await modulePlan(path.join(dir,'module'));
  const {plan}=await loadPlan(input.planFile);plan.semanticTarget.idMap={'unique:u1':'old1','unique:u2':'old2'};
  for(const stage of plan.stages.slice(1,4))stage.primitiveIds=['old1','old2'];
  await writeFile(input.planFile,JSON.stringify(plan));
  let writes=0;
  const mock=moduleInvoke();
  const result=await planTool({operation:'resume',continuous:true,planFile:input.planFile,expectedHash:(await loadPlan(input.planFile)).sha256,window:input.window},moduleCatalog,async(action,args)=>{
    if(action==='debug.batch')writes++;return mock(action,args);
  });
  assert.equal(writes,0);
  assert.equal(result.structuredContent.executionState,'not_executed');
  assert.ok(result.structuredContent.missing.some(row=>row.path.includes('idMap')));
});

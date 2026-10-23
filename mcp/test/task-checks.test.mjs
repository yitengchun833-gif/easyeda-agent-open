import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {modulePlan,moduleCatalog,moduleInvoke,measuredParts} from './fixtures/module-plan.mjs';
import {loadPlan} from '../src/project-files.mjs';
import {taskRequirements} from '../src/plan-rules.mjs';
import {planTool,toMcpResult} from './fixtures/p0-receipts.mjs';

const fullGeometry=()=>toMcpResult({ok:true,result:{ok:true,context:{projectUuid:'p',documentUuid:'d'},result:{components:[
  {componentType:'sheet',primitiveId:'sheet',bbox:{minX:0,minY:0,maxX:1188,maxY:840}},...measuredParts()],readScope:{complete:true,concurrentChange:false,missingIds:[]}}}});

test('module task requirements derive from actions and cannot be removed or downgraded',async t=>{
  const dir=await mkdtemp(path.join(os.tmpdir(),'easyeda-task-checks-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const input=await modulePlan(dir),{plan}=await loadPlan(input.planFile);plan.taskKind='new_module';
  await writeFile(input.planFile,JSON.stringify(plan));
  let loaded=await loadPlan(input.planFile);
  assert.ok(loaded.plan.requirements.some(rule=>rule.when==='delivery'&&rule.check==='drc'));
  assert.ok(loaded.plan.requirements.some(rule=>rule.stageId==='routes'&&rule.check==='valid_wires'));
  assert.ok(loaded.plan.requirements.some(rule=>rule.stageId==='labels'&&rule.check==='identity'));
  loaded.plan.requirements=[];await writeFile(input.planFile,JSON.stringify(loaded.plan));
  loaded=await loadPlan(input.planFile);assert.ok(loaded.plan.requirements.length>0);
  loaded.plan.requirements[0].severity='warn';await writeFile(input.planFile,JSON.stringify(loaded.plan));
  await assert.rejects(loadPlan(input.planFile),/cannot be downgraded/);
});

test('scoped local edits derive identity and pin checks, without introducing a parallel rule protocol',()=>{
  const plan={taskKind:'local_edit',sources:[{id:'snapshot'}],semanticTarget:{idMap:{key:'r'},components:[{key:'key'}]},
    stages:[{id:'move',check:'geometry',primitiveIds:['r'],steps:[{action:'schematic.component.modify'}]}]};
  assert.deepEqual(taskRequirements(plan).map(rule=>rule.check),['source_binding','identity','pin_nets']);
  assert.equal(taskRequirements({...plan,taskKind:undefined}),undefined,'legacy untyped plans retain their compatibility path');
  const display={...plan,stages:[{id:'display',check:'geometry',primitiveIds:['r'],steps:[{action:'schematic.attribute.modify',payload:{primitiveId:'attr',props:{x:5}}}]}]};
  assert.deepEqual(taskRequirements(display).map(rule=>rule.check),['source_binding','identity']);
});

for(const [real,pass] of [[false,false],[true,true],[true,false]])test(`module placement requires checked layout quality, actual CLI=${real}, pass=${pass}`,{skip:real&&!process.env.EASYEDA_BIN},async t=>{
  const dir=await mkdtemp(path.join(os.tmpdir(),'easyeda-quality-gate-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const input=await modulePlan(dir),{plan}=await loadPlan(input.planFile);plan.taskKind='new_module';await writeFile(input.planFile,JSON.stringify(plan));
  input.expectedHash=(await loadPlan(input.planFile)).sha256;
  const geometry=fullGeometry();
  if(real&&!pass){const parts=geometry.structuredContent.result.components;parts[2].x=parts[1].x;parts[2].bbox={...parts[1].bbox};parts[2].pins=structuredClone(parts[1].pins);}
  const mock=moduleInvoke(),invoke=async(action,args)=>action==='schematic.components.list'&&!args.payload.primitiveIds?geometry:mock(action,args);
  const result=real?await planTool(input,moduleCatalog,invoke):await planTool(input,moduleCatalog,invoke,async args=>{
    assert.deepEqual(args.slice(0,3),['sch','layout-lint','--from']);return {ok:false,result:{ok:false,strict:true}};
  });
  assert.equal(result.structuredContent.layoutQuality.status,pass?'pass':'fail');
  assert.equal(result.structuredContent.stage.completed,pass);
});

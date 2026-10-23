import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { guardedSchematicAction } from '../src/execution.mjs';
import { runtimeGate, snapshotStamp } from '../src/runtime.mjs';

const mcp={build:'build',capabilities:['execution.bound.v1']};
const expected={daemonVersion:'daemon',connectorVersion:'connector',mcpBuild:'build',capabilities:['execution.bound.v1','schematic.review.v2']};
const health={found:{raw:{version:'daemon',windows:[{windowId:'w',connectorVersion:'connector',capabilities:['schematic.review.v2'],context:{projectUuid:'p',documentUuid:'d'}}]}}};
const packet=()=>({ok:true,context:{projectUuid:'p',documentUuid:'d'},runtimeInstance:'r',mutationSeq:1,seq:2,seqAbandoned:0,result:{pagePrimitives:{components:[]},pinNetsAvailable:true,wiresAvailable:true}});

test('runtime and snapshot freshness reject old hosts, missing coverage identity and unordered reads',()=>{
 assert.equal(runtimeGate(health,expected,{project:'p',doc:'d'},mcp).passed,true);
 for(const change of [{mcpBuild:'old'},{connectorVersion:'old'},{daemonVersion:'old'}]) assert.equal(runtimeGate(health,{...expected,...change},{project:'p',doc:'d'},mcp).passed,false);
 assert.equal(runtimeGate(health,expected,{project:'p',doc:'other'},mcp).passed,false);
 assert.equal(snapshotStamp(packet()).freshness_available,true);
 assert.equal(snapshotStamp({...packet(),unordered:true}).freshness_available,false);
 assert.equal(snapshotStamp({...packet(),runtimeInstance:undefined}).freshness_available,false);
});

for (const mode of ['layout', 'design']) test(`controlled ${mode} dispatch records before write, reads after timeout, and never retries`,async()=>{
 const root=await mkdtemp(join(tmpdir(),'runeda-contract-'));
 try {
  await mkdir(join(root,'.workflow'));
  await writeFile(join(root,'.workflow','state.json'),'{}');
  const plan=join(root,'plan.json');await writeFile(plan,JSON.stringify({runtime:expected}));
  await writeFile(join(root,'req.json'),JSON.stringify({execution_rules:{edit_scope:{mode}}}));
  const input={project:'p',doc:'d',payload:{primitiveId:'c',patch:{x:1,y:2}},execution:{root,plan,requirements:join(root,'req.json'),baseline:join(root,'baseline.json'),action_id:'a'}};
  const calls=[];let writes=0;
  const run=async args=>{
   if(args[0]==='daemon')return {ok:true,result:health};
   const action=args[args.indexOf('call')+1];calls.push(action);
   if(action==='schematic.components.list'){
    assert.equal(JSON.parse(args[args.indexOf('--payload')+1]).includeDeviceIdentity,mode==='design');
    return {ok:true,result:packet()};
   }
   writes++;return {ok:false,error:{message:'timeout'}};
  };
  const check=async(_root,data)=>{
   if(data.operation==='prepare'){assert.equal(writes,0);return {action_id:'a',freshness:{runtimeInstance:'r',mutationSeq:1}};}
   assert.equal(writes,1);return {passed:true,verified:true,action_id:'a',snapshot_id:'s',effective_state:'APPLIED_VERIFIED'};
  };
  const result=await guardedSchematicAction({name:'schematic.component.modify',mutates:true},input,{mcp,run,check});
  assert.equal(result.isError,false);assert.equal(writes,1);
  assert.deepEqual(calls,['schematic.components.list','schematic.component.modify','schematic.components.list']);
  assert.equal(result.structuredContent.outcome.transport_status,'error');
  assert.equal(result.structuredContent.outcome.effective_state,'APPLIED_VERIFIED');
  assert.ok(!result.content[0].text.includes('pagePrimitives'));
 }finally{await rm(root,{recursive:true,force:true});}
});

for (const scenario of ['success', 'partial', 'timeout-landed', 'wire', 'wrong-target', 'changed-plan', 'rewire', 'design-rewire']) test(`geometry group: ${scenario}`, async () => {
 const root=await mkdtemp(join(tmpdir(),'runeda-group-'));
 try {
  await mkdir(join(root,'.workflow'));await writeFile(join(root,'.workflow','state.json'),'{}');
  const items=['a','b'].map((id,i)=>({action:'schematic.attribute.modify',payload:{primitiveId:id,patch:{x:i+1}}}));
  if(scenario==='wire')items[1].action='schematic.wire.create';
  if(scenario.endsWith('rewire')){
   items[0]={action:'schematic.primitives.delete',payload:{primitiveIds:['old-wire']}};
   items[1]={action:'schematic.wire.create',payload:{points:[0,0,10,0],net:'SIG'}};
  }
  const plan={runtime:expected,route:{project:'p',doc:'d'},actions:items,rewire:scenario.endsWith('rewire')};
  const planPath=join(root,'plan.json');await writeFile(planPath,JSON.stringify(plan));
  const requirements=join(root,'req.json');await writeFile(requirements,JSON.stringify({execution_rules:{edit_scope:{mode:scenario==='design-rewire'?'design':'layout',...(scenario.endsWith('rewire')?{rewire:{authorization:{source:'fixture'}}}:{})}}}));
  const input={project:'p',doc:scenario==='wrong-target'?'other':'d',payload:items[0].payload,
    execution:{root,plan:planPath,requirements,baseline:join(root,'base.json'),action_ids:['a','b']}};
  let reads=0,writes=0,healthCalls=0;const beforeFiles=[],afterFiles=[];
  const run=async args=>{
   if(args[0]==='daemon'){healthCalls++;return {ok:true,result:health};}
   const name=args[args.indexOf('call')+1];
   if(name==='schematic.components.list'){reads++;return {ok:true,result:{...packet(),seq:reads+writes,mutationSeq:writes}};}
   const payload=JSON.parse(args[args.indexOf('--payload')+1]);
   assert.equal(payload.expectedMutationSeq,writes);assert.equal(payload.expectedRuntimeInstance,'r');
   writes++;return scenario==='timeout-landed'?{ok:false,error:{message:'timeout'}}:{ok:true,result:{}};
  };
  const check=async(_root,data)=>{
   if(data.operation==='inspect')return {passed:true,actions:['a','b'],runtime:expected,route:plan.route};
   if(data.operation==='prepare'){beforeFiles.push(data.snapshot_file);return {action_id:data.action_id,freshness:{runtimeInstance:'r',mutationSeq:writes}};}
   afterFiles.push(data.snapshot_file);
   if(scenario==='changed-plan')await writeFile(planPath,JSON.stringify({...plan,batch_id:'changed'}));
   const verified=scenario!=='partial' && !(scenario.endsWith('rewire') && data.action_id==='a');
   const intermediate=scenario.endsWith('rewire') && data.action_id==='a';
   return {passed:verified||intermediate,verified,action_id:data.action_id,snapshot_id:'snapshot-'+reads,
    effective_state:verified?'APPLIED_VERIFIED':intermediate?'APPLIED_INTERMEDIATE':'PARTIAL'};
  };
  const call=()=>guardedSchematicAction({name:items[0].action,mutates:true},input,{mcp,run,check});
  if(['wire','wrong-target'].includes(scenario)){
   await assert.rejects(call);assert.equal(writes,0);assert.equal(reads,0);return;
  }
  const result=await call();const complete=['success','timeout-landed','rewire','design-rewire'].includes(scenario);
  assert.equal(result.isError,!complete,JSON.stringify(result.structuredContent));assert.equal(writes,complete?2:1);
  assert.equal(reads,complete?3:2);assert.equal(healthCalls,1);
  if(complete)assert.equal(beforeFiles[1],afterFiles[0]);
  assert.ok(!result.content[0].text.includes('pagePrimitives'));
 } finally {await rm(root,{recursive:true,force:true});}
});

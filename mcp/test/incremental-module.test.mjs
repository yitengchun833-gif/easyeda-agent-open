import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { captureSnapshot } from '../src/plan-tools.mjs';
import { preservationDiff, validateIncrementalRegion } from '../src/preservation.mjs';
import { modulePlan, moduleCatalog, moduleParts, moduleInvoke, measuredParts } from './fixtures/module-plan.mjs';
import { loadPlan, planTool, toMcpResult } from './fixtures/p0-receipts.mjs';

export const region={minX:100,minY:100,maxX:400,maxY:300};
const emptyInventory=()=>Object.fromEntries(['components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects'].map(key=>[key,[]]));
export const baseline=()=>({wiresAvailable:true,components:[{componentType:'part',primitiveId:'old',uniqueId:'old',designator:'R9',subPartName:'A',x:0,y:0,rotation:0,mirror:false,
  bbox:{minX:-10,minY:-10,maxX:10,maxY:10},pinsAvailable:true,netlistAvailable:true,
  device:moduleParts()[0].device,symbol:moduleParts()[0].symbol,footprint:moduleParts()[0].footprint,
  pins:[{pinNumber:'1',pinName:'A',x:10,y:0,rotation:0,net:'OLD',noConnected:false}]}],
  wires:[{primitiveId:'old-wire',net:'OLD',x0:10,y0:0,x1:40,y1:0}],pagePrimitives:emptyInventory(),
  readScope:{complete:true,concurrentChange:false,missingIds:[]}});
async function setup(t,empty=false){
  const dir=await mkdtemp(path.join(os.tmpdir(),'easyeda-incremental-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const seed=await modulePlan(path.join(dir,'seed')), {plan}=await loadPlan(seed.planFile);
  const draft={schemaVersion:plan.schemaVersion,goal:'Add resistor module',semanticTarget:plan.semanticTarget,layoutHints:{...plan.layoutHints,region},confirmation:plan.confirmation};
  const snapshotFile=path.join(dir,'before.json'),planFile=path.join(dir,'declaration.json'),outputFile=path.join(dir,'ready.json');
  const snapshot=empty?{...baseline(),components:[],wires:[]}:baseline();
  await captureSnapshot({domain:'schematic',outputFile:snapshotFile},async()=>toMcpResult({ok:true,result:{ok:true,context:{projectUuid:'p',documentUuid:'d'},result:snapshot}}));
  await writeFile(planFile,JSON.stringify(draft));
  return {dir,draft,snapshot,input:{operation:'prepare',snapshotFile,planFile,outputFile,window:'offline-window'}};
}

test('prepare retains empty-page entry and creates a protected nonempty module using the same five stages',async t=>{
  for(const empty of [true,false]){
    const {input}=await setup(t,empty), result=await planTool(input,moduleCatalog);
    assert.equal(result.structuredContent.ok,true);
    const {plan}=await loadPlan(input.outputFile);
    assert.deepEqual(plan.stages.map(stage=>stage.id),['place','measure','routes','labels','final']);
    assert.equal(plan.semanticTarget.components.length,empty?2:3);
    assert.equal(plan.acceptanceScope,empty?undefined:'module');
    assert.equal(plan.semanticTarget.idMap['unique:old'],empty?undefined:'old');
    assert.equal(plan.stages[0].steps.length,2);
  }
});

test('prepare refuses unknown original connectivity, intersecting region and duplicate original identity',async t=>{
  for(const mutate of [draft=>delete draft.layoutHints.region,draft=>{draft.layoutHints.parts[0].subPartName='B';},draft=>{draft.layoutHints.parts[0].rotationMode='native';},draft=>{draft.layoutHints.parts[0].x=0;},draft=>{draft.layoutHints.region={minX:-20,minY:-20,maxX:50,maxY:50};},
    draft=>{draft.semanticTarget.components[0].ref='R9';}]){
    const {input,draft}=await setup(t);mutate(draft);await writeFile(input.planFile,JSON.stringify(draft));
    await assert.rejects(planTool(input,moduleCatalog));
  }
  const snapshot=baseline();snapshot.components[0].netlistAvailable=false;
  assert.throws(()=>validateIncrementalRegion(snapshot,region),/unknown/);
});

test('preservation detects original moves, rewires, missing data and additions outside the reserved region',()=>{
  const before=baseline();assert.equal(preservationDiff(before,structuredClone(before),region).status,'pass');
  for(const mutate of [s=>s.components[0].x++,s=>s.components[0].pins[0].net='changed',s=>s.wires[0].x1++,
    s=>{s.components.push({...structuredClone(s.components[0]),primitiveId:'new',bbox:{minX:0,minY:0,maxX:5,maxY:5}});}]){
    const after=structuredClone(before);mutate(after);assert.equal(preservationDiff(before,after,region).status,'fail');
  }
  assert.throws(()=>preservationDiff(before,{...before,readScope:{complete:false}},region),/complete/);
});

test('host sheet update timestamps do not masquerade as outside-scope edits',()=>{
  const before=baseline();
  before.components.push({primitiveId:'sheet',componentType:'sheet',x:0,y:0,bbox:{minX:0,minY:0,maxX:1170,maxY:825},
    otherProperty:{'@Update Date':'2026-10-20','@Update Time':'17:59:33',Width:'1170',Company:'Original'}});
  // Native pagePrimitives already omit these automatic clock values.
  before.pagePrimitives.components.push({...structuredClone(before.components[1]),otherProperty:{Width:'1170',Company:'Original'}});
  for(const scope of [null,region]){
    const after=structuredClone(before);
    Object.assign(after.components[1].otherProperty,{'@Update Date':'2026-10-21','@Update Time':'18:03:02'});
    assert.equal(preservationDiff(before,after,scope).status,'pass');
    assert.equal(before.components[1].otherProperty['@Update Time'],'17:59:33');
    for(const mutate of [s=>s.components[1].otherProperty.Width='1200',s=>s.components[1].otherProperty.Company='Changed',
      s=>s.components[1].bbox.maxX++,s=>s.pagePrimitives.components[0].otherProperty.Company='Changed',
      s=>s.components[0].otherProperty={'@Update Time':'unexpected part property'}]){
      const changed=structuredClone(after);mutate(changed);
      assert.equal(preservationDiff(before,changed,scope).status,'fail');
    }
  }
});

test('native polyline segments preserve correctly and unknown inventories or occupied text regions never pass',()=>{
  const snapshot=baseline();
  snapshot.wires=[{primitiveId:'polyline',segmentIndex:0,net:'OLD',x0:0,y0:0,x1:20,y1:0},
    {primitiveId:'polyline',segmentIndex:1,net:'OLD',x0:20,y0:0,x1:20,y1:20}];
  assert.equal(preservationDiff(snapshot,structuredClone(snapshot),region).status,'pass');
  for(const mutate of [s=>s.wiresAvailable=false,s=>delete s.pagePrimitives,
    s=>s.pagePrimitives.texts.push({primitiveId:'text',bbox:{minX:190,minY:190,maxX:230,maxY:210}}),s=>s.pagePrimitives.buses.push({primitiveId:'bus',bbox:{minX:190,minY:200,maxX:230,maxY:200}})]){
    const bad=structuredClone(snapshot);mutate(bad);assert.throws(()=>validateIncrementalRegion(bad,region));
  }
});

test('new native pin attributes and measured non-drawing metadata stay with their new component',()=>{
  const before=baseline(),after=structuredClone(before);
  after.components.push({primitiveId:'new',componentType:'part',bbox:{minX:190,minY:190,maxX:210,maxY:210},
    pins:[{primitiveId:'new-pin',pinNumber:'1'}]});
  after.pagePrimitives.attributes.push(
    {primitiveId:'metadata',ParentPrimitiveId:'new',Key:'Supplier Part',KeyVisible:null,ValueVisible:null},
    {primitiveId:'pin-label',ParentPrimitiveId:'new-pin',Key:'Pin Number',KeyVisible:false,ValueVisible:true});
  after.wires.push({primitiveId:'new-wire',net:'',x0:200,y0:200,x1:240,y1:200});
  after.pagePrimitives.attributes.push({primitiveId:'empty-name',ParentPrimitiveId:'new-wire',Key:'Name',Value:'',KeyVisible:false,ValueVisible:true});
  after.pagePrimitiveBounds={metadata:{bbox:{minX:0,minY:0,maxX:0,maxY:0}},
    'pin-label':{bbox:{minX:195,minY:195,maxX:205,maxY:205}},'empty-name':{bbox:{minX:0,minY:0,maxX:0,maxY:0}}};
  assert.equal(preservationDiff(before,after,region).status,'pass');
  for(const mutate of [s=>s.pagePrimitives.attributes[0].ParentPrimitiveId='unknown',
    s=>delete s.pagePrimitiveBounds.metadata,s=>s.pagePrimitives.attributes[0].ValueVisible=true,
    s=>s.pagePrimitives.attributes[2].Value='visible-name',
    s=>s.pagePrimitiveBounds['pin-label'].bbox={minX:0,minY:0,maxX:10,maxY:10}]){
    const invalid=structuredClone(after);mutate(invalid);
    assert.equal(preservationDiff(before,invalid,region).status,'fail');
  }
  const ambiguous=structuredClone(after);ambiguous.components[0].pins.push({primitiveId:'new-pin',pinNumber:'2'});
  assert.throws(()=>preservationDiff(before,ambiguous,region),/Ambiguous native/);
});

test('scoped component edits cover only attributes of their exact native pins',()=>{
  const before=baseline();before.components[0].pins[0].primitiveId='old-pin';
  before.pagePrimitives.attributes.push({primitiveId:'pin-label',ParentPrimitiveId:'old-pin',Key:'Pin Number',X:10,Y:0});
  const after=structuredClone(before);after.pagePrimitives.attributes[0].X=20;
  assert.equal(preservationDiff(before,after,null,{components:['old']}).status,'pass');
  assert.equal(preservationDiff(before,after,null,{}).status,'fail');
});

test('original page drift stops before the first write and leaves the original baseline immutable',async t=>{
  const {input,snapshot}=await setup(t);await planTool(input,moduleCatalog);
  const loaded=await loadPlan(input.outputFile),before=await readFile(input.snapshotFile);let writes=0;
  const moved=structuredClone(snapshot);moved.components[0].x++;
  await assert.rejects(planTool({operation:'apply',planFile:input.outputFile,expectedHash:loaded.sha256,stage:'place',window:input.window},moduleCatalog,async action=>{
    if(action==='debug.batch')writes++;
    return toMcpResult({ok:true,result:{ok:true,context:{projectUuid:'p',documentUuid:'d'},result:moved}});
  }),/Preserved page changed/);
  assert.equal(writes,0);assert.deepEqual(await readFile(input.snapshotFile),before);
});

for(const drift of [false,true])test(`incremental placement checks original page after writing, drift=${drift}`,async t=>{
  const {input,snapshot}=await setup(t);await planTool(input,moduleCatalog);
  const loaded=await loadPlan(input.outputFile),mock=moduleInvoke();let applied=false;
  const result=await planTool({operation:'apply',planFile:input.outputFile,expectedHash:loaded.sha256,stage:'place',window:input.window},moduleCatalog,async(action,args)=>{
    if(action==='schematic.components.list'&&!args.payload.primitiveIds){
      const page=structuredClone(snapshot);
      if(applied){page.components.push(...measuredParts());if(drift)page.wires[0].net='changed';}
      return toMcpResult({ok:true,result:{ok:true,context:{projectUuid:'p',documentUuid:'d'},result:page}});
    }
    if(action==='debug.batch')applied=true;
    return mock(action,args);
  },async()=>({ok:true,result:{ok:true,strict:true,componentCount:2,functionalGroups:[{id:'module'}],groupOverlaps:[]}}));
  assert.equal(result.structuredContent.stage.completed,!drift);
  assert.equal(result.structuredContent.preservation.status,drift?'fail':'pass');
  const receipt=JSON.parse(await readFile(path.join(loaded.plan.executionReceipts.directory,'stage-01.json'),'utf8'));
  assert.equal(receipt.receipt.execution.structuredContent.result.completed,2);
});

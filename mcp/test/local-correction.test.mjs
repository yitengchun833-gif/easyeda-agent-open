import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp,writeFile,readFile,rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { planTool } from '../src/plan-tools.mjs';
import { loadPlan,verifyStagePrefix } from '../src/project-files.mjs';
import { toMcpResult } from '../src/core.mjs';
import { correctionScope,correctionPreservation } from '../src/local-correction.mjs';

const target={projectUuid:'p',documentUuid:'d'};
const catalog=new Map(['schematic.components.list','schematic.attribute.modify'].map(name=>[name,{name,mutates:name!=='schematic.components.list'}]));
const reply=result=>toMcpResult({ok:true,result:{ok:true,id:'read',context:{...target,documentType:'schematic'},result}});
function scene(){
  const components=['r1','r2'].map((id,index)=>({componentType:'part',primitiveId:id,uniqueId:id,designator:`R${index+1}`,
    x:index*100,y:0,rotation:0,mirror:false,bbox:{minX:index*100,minY:0,maxX:index*100+20,maxY:20},
    pinsAvailable:true,netlistAvailable:true,pins:[{pinNumber:'1',pinName:'1',net:'VIN',noConnected:false,x:index*100,y:0,rotation:0}],
    footprint:{uuid:'f'},device:{uuid:'v'},symbol:{uuid:'s'}}));
  const pagePrimitives=Object.fromEntries(['components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects'].map(kind=>[kind,[]]));
  pagePrimitives.components=structuredClone(components);
  pagePrimitives.attributes=components.flatMap(part=>['Designator','Value'].map((Key,i)=>({primitiveId:`${part.primitiveId}-${Key}`,ParentPrimitiveId:part.primitiveId,Key,Value:i?'10k':part.designator,X:part.x,Y:30+i*10})));
  const wires=[{primitiveId:'w',segmentIndex:0,net:'VIN',x0:0,y0:0,x1:100,y1:0}];
  pagePrimitives.wires=structuredClone(wires);
  return {components,wires,wiresAvailable:true,pagePrimitives,readScope:{complete:true,concurrentChange:false,missingIds:[]}};
}
async function fixture(t,options={}){
  const dir=await mkdtemp(path.join(os.tmpdir(),'easyeda-correction-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const snapshotFile=path.join(dir,'snapshot.json'),outputFile=path.join(dir,'correction.json'),snapshot=scene();
  await writeFile(snapshotFile,JSON.stringify(reply(snapshot)));
  const input={operation:'prepare_correction',window:'w',snapshotFile,outputFile,primitiveIds:['r1'],
    corrections:[{primitiveId:'r1-Designator',props:{y:60}},{primitiveId:'r1-Value',props:{x:25}}],...options};
  return {input,snapshot,dir};
}

test('correction prepare is offline and creates one scoped batch job with the unchanged complete pin target',async t=>{
  const {input,snapshot}=await fixture(t);
  const prepared=await planTool(input,catalog,()=>assert.fail('prepare contacted EDA'));
  assert.equal(prepared.structuredContent.executionState,'not_executed');
  const {plan}=await loadPlan(input.outputFile);
  assert.equal(plan.acceptanceScope,'local_display');assert.equal(plan.stages.length,1);
  assert.deepEqual(plan.stages[0].primitiveIds,['r1']);assert.equal(plan.stages[0].steps.length,2);
  assert.equal(plan.semanticTarget.components.length,2);assert.equal(plan.semanticTarget.nets[0].members.length,2);
  assert.equal(plan.stages[0].steps.every(step=>step.action==='schematic.attribute.modify'),true);
  assert.deepEqual(JSON.parse(await readFile(input.snapshotFile,'utf8')).structuredContent.result,snapshot);
  assert.equal(prepared.structuredContent.job.runStages[0].stage,'correct');
});

test('region selection excludes partially intersecting components and rejects outside/electrical edits',async t=>{
  const {input,snapshot}=await fixture(t);
  const region={minX:-5,minY:-5,maxX:110,maxY:25};
  const {primitiveIds,...regional}=input;
  assert.deepEqual(correctionScope(snapshot,{...regional,region}).componentIds,['r1']);
  for(const corrections of [[{primitiveId:'r2-Value',props:{y:60}}],[{primitiveId:'r1-Value',props:{value:'changed'}}],
    [{primitiveId:'r1-Value',props:{fontSize:-1}}],[{primitiveId:'r1-Value',props:{x:Infinity}}]]){
    await assert.rejects(planTool({...input,corrections},catalog),/scope|outside|display|property/);
  }
});

async function apply(t,{stale=false,outside=false,partial=false,collateral=false,unknown=false,finalY}={}){
  const {input,snapshot}=await fixture(t);
  const prepared=(await planTool(input,catalog)).structuredContent;
  const live=structuredClone(snapshot),calls=[];
  let fullReads=0;
  if(stale)live.components[1].x+=5;
  const invoke=async(action,args)=>{
    calls.push({action,args});
    if(action==='schematic.export.image'){
      const artifactPath=path.join(path.dirname(input.outputFile),'synthetic-image.bin');
      await writeFile(artifactPath,'OFFLINE TEST IMAGE, NOT AN EDA CAPTURE');
      return toMcpResult({ok:true,result:{ok:true,id:'image',context:target,result:{artifactPath}}});
    }
    if(action==='schematic.components.list'){
      if(!args.payload.primitiveIds && ++fullReads===2 && finalY!==undefined)live.pagePrimitives.attributes[0].Y=finalY;
      const value=structuredClone(live),ids=args.payload.primitiveIds;
      if(ids){value.components=value.components.filter(part=>ids.includes(part.primitiveId));value.readScope={...value.readScope,complete:false,primitiveIds:ids};}
      return reply(value);
    }
    assert.equal(action,'debug.batch');
    assert.deepEqual(args.edit.primitiveIds,['r1']);
    const results=args.payload.steps.map((step,index)=>{
      assert.equal(step.action,'schematic.attribute.modify');
      assert.deepEqual(step.payload._target,target);
      const attribute=live.pagePrimitives.attributes.find(attr=>attr.primitiveId===step.payload.primitiveId);
      for(const [key,value] of Object.entries(step.payload.props))attribute[key[0].toUpperCase()+key.slice(1)]=value;
      return {index,action:step.action,ok:true,result:{primitiveId:attribute.primitiveId,attribute:structuredClone(attribute),verified:true,partial:false}};
    });
    if(outside)live.wires[0].net='WRONG';
    if(collateral)live.pagePrimitives.attributes[0].Color='unexpected';
    if(unknown)return toMcpResult({ok:false,error:{id:'lost-write-response',executionState:'unknown',message:'Simulated response loss after dispatch'}});
    return reply({ok:!partial,partial,completed:partial?1:results.length,total:results.length,results,
      rules:{status:'pass',mode:'layout',scope:['r1'],items:[{id:'identity',status:'pass',severity:'error',coverage:'checked'}]}});
  };
  const request={operation:'apply',planFile:input.outputFile,expectedHash:prepared.sha256,window:'w',stage:'correct',edit:{mode:'layout',primitiveIds:['r1']}};
  return {request,calls,invoke,result:async()=>planTool(request,catalog,invoke),prepared};
}

test('display correction applies all edits in one batch, preserving outside primitives and all pin nets',async t=>{
  const run=await apply(t),result=await run.result();
  assert.equal(result.structuredContent.ok,true,JSON.stringify(result));
  assert.equal(result.structuredContent.preservedOutsideScope.status,'pass');
  assert.equal(result.structuredContent.wholePageComplete,false);
  assert.equal(run.calls.filter(call=>call.action==='debug.batch').length,1);
  assert.equal(run.calls.length,4);
  await assert.rejects(run.result(),/receipt|replay/);
  assert.equal(run.calls.filter(call=>call.action==='debug.batch').length,1);
});

test('stale baseline stops before sending any attribute write',async t=>{
  const run=await apply(t,{stale:true});
  await assert.rejects(run.result(),/baseline changed/);
  assert.equal(run.calls.some(call=>call.action==='debug.batch'),false);
});

test('a correct step receipt cannot certify a requested field that drifted by the final readback',async t=>{
  const run=await apply(t,{finalY:999}),result=await run.result(),report=result.structuredContent;
  assert.equal(report.execution.structuredContent.result.results[0].result.attribute.Y,60);
  assert.equal(report.execution.structuredContent.result.results[0].result.verified,true);
  assert.equal(report.ok,false);assert.equal(report.stage.completed,false);
  assert.equal(report.preservedOutsideScope.status,'fail');
  assert.deepEqual(report.preservedOutsideScope.requestedFieldMismatches,
    [{primitiveId:'r1-Designator',field:'Y',expected:60,actual:999}]);
  assert.equal(run.calls.length,4,'final field verification must reuse the existing complete readback');
  assert.equal(run.calls.filter(call=>call.action==='debug.batch').length,1);
});

test('final requested-field verification covers missing fields, style/visibility and display font tolerance',()=>{
  const before=scene(),after=structuredClone(before),attribute=after.pagePrimitives.attributes[0];
  const edits=[{primitiveId:attribute.primitiveId,props:{fontName:'Arial',valueVisible:true,fontSize:8},fontSizeUnit:'display'}];
  Object.assign(attribute,{FontName:'Arial',ValueVisible:true,FontSize:8+5e-7});
  assert.equal(correctionPreservation(before,after,edits).status,'pass');
  for(const [field,value] of [['FontName','wrong'],['ValueVisible',false],['FontSize',8.01],['FontName',undefined]]){
    const changed=structuredClone(after);
    changed.pagePrimitives.attributes[0][field]=value;
    const checked=correctionPreservation(before,changed,edits);
    assert.equal(checked.status,'fail');
    assert.equal(checked.requestedFieldMismatches[0].field,field);
  }
});

test('a completed correction cannot lose its protection metadata and inherit the old receipt',async t=>{
  const run=await apply(t);
  await run.result();
  const {plan}=await loadPlan(run.request.planFile);
  delete plan.localCorrection;
  await writeFile(run.request.planFile,JSON.stringify(plan));
  await assert.rejects(verifyStagePrefix(await loadPlan(run.request.planFile),1),/binding/);
});

test('the generated run job reaches the existing image review path without saving or redrawing',async t=>{
  const run=await apply(t),originalFetch=globalThis.fetch;
  globalThis.fetch=async(_url,options)=>{
    assert.equal(options.method,'GET');
    return new Response(JSON.stringify({documents:[{windowId:'w',context:target,generation:1}],
      receipts:[{requestId:'read',generation:1},{requestId:'image',generation:1}]}));
  };
  try{
    const result=await planTool(run.prepared.job,new Map([...catalog,['schematic.export.image',{mutates:false}]]),run.invoke);
    assert.equal(result.structuredContent.ok,true,JSON.stringify(result));
    assert.deepEqual(result.structuredContent.completedStages,['correct']);
    assert.ok(result.structuredContent.review.imagePath.endsWith('synthetic-image.bin'));
    assert.equal(result.structuredContent.wholePageComplete,false);
    assert.equal(run.calls.filter(call=>call.action==='debug.batch').length,1);
    assert.equal(run.calls.some(call=>/save|delete|place/.test(call.action)),false);
  }finally{globalThis.fetch=originalFetch;}
});

test('unexpected outside wire changes and partial writes remain failed with no replay',async t=>{
  for(const options of [{outside:true},{partial:true},{collateral:true},{unknown:true}]){
    const run=await apply(t,options),result=await run.result();
    assert.equal(result.structuredContent.ok,false,JSON.stringify(result));
    assert.equal(result.structuredContent.stage.completed,false);
    assert.equal(run.calls.filter(call=>call.action==='debug.batch').length,1);
    if(options.outside||options.collateral)assert.equal(result.structuredContent.preservedOutsideScope.status,'fail');
    await assert.rejects(run.result(),/receipt|replay/);
  }
});

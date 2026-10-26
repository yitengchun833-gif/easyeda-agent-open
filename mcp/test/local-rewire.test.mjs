import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {planTool} from '../src/plan-tools.mjs';
import {loadPlan} from '../src/project-files.mjs';
import {toMcpResult} from '../src/core.mjs';
import {planPinRewire} from '../src/local-rewire.mjs';

const target={projectUuid:'p',documentUuid:'d'};
const reply=result=>toMcpResult({ok:true,result:{ok:true,id:'read',context:{...target,documentType:'schematic'},result}});
const catalog=new Map(['schematic.primitives.delete','schematic.wire.create','schematic.components.list'].map(name=>[name,{name,mutates:name!=='schematic.components.list'}]));
const selection={primitiveId:'r1',pinNumber:'1',wireId:'wrong',targetNet:'NEW'};
function scene(){
  const components=['r1','r2','r3'].map((id,i)=>({primitiveId:id,componentType:'part',uniqueId:id,designator:`R${i+1}`,x:i*100,y:i?100:0,rotation:0,mirror:false,
    bbox:{minX:i*100-20,minY:i?90:-10,maxX:i*100-5,maxY:i?110:10},device:{uuid:'v'},symbol:{uuid:'s'},footprint:{uuid:'f'},pinsAvailable:true,netlistAvailable:true,
    pins:[{primitiveId:`${id}-pin`,pinNumber:'1',pinName:'1',x:i*100,y:i?100:0,rotation:0,net:i===2?'NEW':'OLD',noConnected:false}]}));
  const pagePrimitives=Object.fromEntries(['components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects'].map(kind=>[kind,[]]));
  pagePrimitives.components=components.map(({pins,...part})=>structuredClone(part));
  pagePrimitives.wires=[{primitiveId:'wrong',Line:[0,0,20,0],Net:'OLD',Color:null,LineWidth:1,LineType:0}];
  pagePrimitives.attributes=[{primitiveId:'old-name',ParentPrimitiveId:'wrong',Key:'Name',Value:'OLD',X:10,Y:10}];
  return {components,wires:[{primitiveId:'wrong',segmentIndex:0,x0:0,y0:0,x1:20,y1:0,net:'OLD'}],wiresAvailable:true,pagePrimitives,
    readScope:{complete:true,concurrentChange:false,missingIds:[]}};
}
async function prepared(t,mutate){
  const dir=await mkdtemp(path.join(os.tmpdir(),'easyeda-rewire-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const snapshot=scene();mutate?.(snapshot);
  const snapshotFile=path.join(dir,'snapshot.json'),outputFile=path.join(dir,'rewire.json');
  await writeFile(snapshotFile,JSON.stringify(reply(snapshot)));
  const result=await planTool({operation:'prepare_rewire',window:'w',snapshotFile,outputFile,...selection},catalog,()=>assert.fail('prepare contacted EDA'));
  return {snapshot,prepared:result.structuredContent,outputFile,snapshotFile};
}

test('rewire preview binds the exact leaf wire/pin, preserves geometry and changes only its independent pin target',async t=>{
  const fx=await prepared(t),{plan}=await loadPlan(fx.outputFile),preview=fx.prepared.preview;
  assert.deepEqual(preview.removedPrimitiveIds,['wrong','old-name']);
  assert.deepEqual(preview.replacement.points,[[0,0],[20,0]]);
  assert.equal(preview.replacement.net,'NEW');assert.equal(preview.affectedEndpoints[1].kind,'free-tip');
  assert.equal(plan.semanticTarget.components[0].pins[0].net,'NEW');
  assert.equal(plan.semanticTarget.components[1].pins[0].net,'OLD');
  assert.deepEqual(fx.prepared.job.edit.expectedPins,{'r1.1':'NEW'});
  assert.equal(fx.prepared.executionState,'not_executed');
});

test('branch, crossing, second pin, marker, polyline and unknown target are unsupported before any file/write',async t=>{
  const cases=[s=>s.wires.push({primitiveId:'branch',x0:10,y0:0,x1:10,y1:10,net:'OLD'}),
    s=>s.wires.push({primitiveId:'cross',x0:10,y0:-10,x1:10,y1:10,net:'OTHER'}),
    s=>{s.components[1].pins[0].x=20;s.components[1].pins[0].y=0;},
    s=>s.components.push({primitiveId:'marker',componentType:'netflag',x:20,y:0,bbox:{minX:19,minY:-1,maxX:21,maxY:1}}),
    s=>s.wires.push({...s.wires[0],segmentIndex:1,x0:20,y0:0,x1:20,y1:10}),
    s=>{s.components[2].pins[0].net='NOT_NEW';}];
  for(const mutate of cases)await assert.rejects(prepared(t,mutate),/Unsupported pin rewire/);
  const s=scene();s.pagePrimitives.wires[0].Line=[0,0,10,0,20,0];
  assert.throws(()=>planPinRewire(s,selection),/multi-segment/);
});

async function apply(t,{stale=false,deletedFalse=false,unknown=false,outside=false,wrongNet=false,wrongLabel=false,nativeLine}={}){
  const fx=await prepared(t),live=structuredClone(fx.snapshot),calls=[];
  if(stale)live.wires.push({primitiveId:'branch',x0:10,y0:0,x1:10,y1:5,net:'OLD'});
  const invoke=async(action,args)=>{
    calls.push({action,args});
    if(action==='schematic.components.list'){
      const value=structuredClone(live),ids=args.payload.primitiveIds;
      if(ids){value.components=value.components.filter(c=>ids.includes(c.primitiveId));value.readScope={...value.readScope,complete:false,primitiveIds:ids};}
      return reply(value);
    }
    assert.equal(action,'debug.batch');assert.equal(args.edit.mode,'design');
    assert.deepEqual(args.payload.steps.map(s=>s.action),['schematic.primitives.delete','schematic.wire.create']);
    assert.deepEqual(args.payload.steps[0].payload.primitiveIds,['wrong']);assert.equal(args.payload.steps[0].payload.requireDeleted,true);
    if(deletedFalse)return reply({ok:false,partial:true,completed:1,total:2,results:[{index:0,action:'schematic.primitives.delete',ok:false,result:{verified:false,partial:true}}]});
    live.wires=[];live.pagePrimitives.wires=[];live.pagePrimitives.attributes=[];live.components[0].pins[0].net='';
    if(unknown)return toMcpResult({ok:false,error:{executionState:'unknown',message:'Lost response after deletion; no creation'}});
    const points=args.payload.steps[1].payload.points;
    live.wires=[{primitiveId:'replacement',segmentIndex:0,x0:points[0][0],y0:points[0][1],x1:points[1][0],y1:points[1][1],net:'NEW'}];
    live.pagePrimitives.wires=[{primitiveId:'replacement',Line:nativeLine===undefined?points:nativeLine,Net:'NEW',Color:null,LineWidth:1,LineType:0}];
    live.pagePrimitives.attributes=[{primitiveId:'new-name',ParentPrimitiveId:'replacement',Key:'Name',Value:wrongLabel?'WRONG':'NEW'}];
    live.components[0].pins[0].net=wrongNet?'WRONG':'NEW';
    if(outside)live.components[1].pins[0].net='WRONG';
    return reply({ok:true,partial:false,completed:2,total:2,results:[
      {index:0,action:'schematic.primitives.delete',ok:true,result:{verified:true,deletedIds:{wires:['wrong']}}},
      {index:1,action:'schematic.wire.create',ok:true,result:{primitiveId:'replacement',net:'NEW',line:points}}],
      rules:{status:'pass',mode:'design',scope:['r1'],items:[{id:'pin_nets',severity:'error',status:'pass',coverage:'checked'}]}});
  };
  return {...fx,calls,invoke,run:()=>planTool(fx.prepared.job,catalog,invoke)};
}

test('one scoped write batch corrects the target pin and preserves all outside connections; no replay',async t=>{
  const fx=await apply(t),result=await fx.run();
  assert.equal(result.structuredContent.ok,true,JSON.stringify(result));
  assert.equal(result.structuredContent.preservedOutsideScope.status,'pass');
  assert.equal(result.structuredContent.preservedOutsideScope.targetPin.net,'NEW');
  assert.equal(fx.calls.filter(c=>c.action==='debug.batch').length,1);
  assert.equal(fx.calls.length,4);
  await assert.rejects(fx.run(),/receipt|replay/);
  assert.equal(fx.calls.filter(c=>c.action==='debug.batch').length,1);
});

test('preview and final native readback accept all three existing single-segment encodings',async t=>{
  for(const line of [[0,0,20,0],[[0,0,20,0]],[[0,0],[20,0]]]){
    const snapshot=scene();snapshot.pagePrimitives.wires[0].Line=line;
    assert.deepEqual(planPinRewire(snapshot,selection).metadata.points,[[0,0],[20,0]]);
    const fx=await apply(t,{nativeLine:line}),result=await fx.run();
    assert.equal(result.structuredContent.ok,true,JSON.stringify(result));
    assert.equal(result.structuredContent.preservedOutsideScope.status,'pass');
  }
});

test('extra, malformed or divergent final native Line cannot hide behind a correct projected segment',async t=>{
  const invalid=[null,[],[0,0,20,0,20,0,20,30],[[0,0,20,0],[20,0,20,30]],[[0,0],[20,0],[20,30]],
    [0,0,20,0,99],[0,0,20,0,null],[[0,0,20,0],null],[[0,0],[20,0,99]],
    [0,0,Infinity,0],[0,0,'20',0],[0,0,30,0],[0,0,0,0]];
  for(const line of invalid){
    const snapshot=scene();snapshot.pagePrimitives.wires[0].Line=line;
    assert.throws(()=>planPinRewire(snapshot,selection),/multi-segment|inconsistent/);
    const fx=await apply(t,{nativeLine:line}),result=await fx.run();
    assert.equal(result.structuredContent.ok,false,JSON.stringify({line,result}));
    assert.equal(result.structuredContent.partial,true);assert.equal(result.structuredContent.stage.completed,false);
    assert.equal(result.structuredContent.preservedOutsideScope.status,'fail');
    assert.ok(result.structuredContent.preservedOutsideScope.changed.includes('replacement'));
    await assert.rejects(fx.run(),/receipt|replay/);
    assert.equal(fx.calls.filter(c=>c.action==='debug.batch').length,1);
  }
});

test('a fresh branch or modified baseline stops before the deletion batch',async t=>{
  const fx=await apply(t,{stale:true});await assert.rejects(fx.run(),/baseline changed/);
  assert.equal(fx.calls.some(c=>c.action==='debug.batch'),false);
});

test('altered or missing independent identity/pin targets stop before any native call',async t=>{
  for(const change of [job=>delete job.edit.expectedIdentities,job=>job.edit.expectedIdentities.r1.designator='R99',
    job=>job.edit.expectedPins['r1.1']='OTHER']){
    const fx=await apply(t);change(fx.prepared.job);
    await assert.rejects(fx.run(),/independent identity\/pin targets/);
    assert.equal(fx.calls.length,0);
  }
});

test('failed deletion, unknown result, wrong final pin/Name or outside net change cannot pass or replay',async t=>{
  for(const options of [{deletedFalse:true},{unknown:true},{outside:true},{wrongNet:true},{wrongLabel:true}]){
    const fx=await apply(t,options),result=await fx.run();
    assert.equal(result.structuredContent.ok,false,JSON.stringify(result));assert.equal(result.structuredContent.stage.completed,false);
    assert.notEqual(result.structuredContent.preservedOutsideScope.status,'pass');
    await assert.rejects(fx.run(),/receipt|replay/);
    assert.equal(fx.calls.filter(c=>c.action==='debug.batch').length,1);
  }
});

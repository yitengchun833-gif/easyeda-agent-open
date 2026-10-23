// Offline EDA protocol simulation + the actual source-built Go layout/router/diff. No live editor or service.
import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,readFile,writeFile,rm} from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import {modulePlan,moduleCatalog,moduleParts,measuredParts} from './fixtures/module-plan.mjs';
import {planTool,loadPlan,sha,toMcpResult} from './fixtures/p0-receipts.mjs';

for(const interrupted of [false,true])test(`full offline module → review → save → reopen verification, interrupted=${interrupted}`,{skip:!process.env.EASYEDA_BIN},async t=>{
  const dir=await mkdtemp(path.join(os.tmpdir(),'easyeda-workflow-'));t.after(()=>rm(dir,{recursive:true,force:true}));
  const input=await modulePlan(dir),target={projectUuid:'p',documentUuid:'d'},window=input.window;
  const sheet={componentType:'sheet',primitiveId:'sheet',bbox:{minX:0,minY:0,maxX:1188,maxY:840}};
  let parts=[],wires=[],serial=0,saved=false;
  const rows=[],mutations=[],controls=[];
  const inventory=()=>Object.fromEntries(['components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects'].map(key=>[key,[]]));
  const snapshot=(ids)=>{
    const components=ids?parts.filter(part=>ids.includes(part.primitiveId)):[sheet,...parts];
    const pagePrimitives=inventory();pagePrimitives.components=structuredClone([sheet,...parts]);
    pagePrimitives.wires=wires.map(wire=>({primitiveId:wire.primitiveId,Line:wire.rawLine,Net:wire.net}));
    return {components:structuredClone(components),wires:structuredClone(wires),wiresAvailable:true,pagePrimitives,pagePrimitiveBounds:{},
      readScope:{complete:!ids,concurrentChange:false,missingIds:[],...(ids?{primitiveIds:ids}:{}),netRead:'compiled'}};
  };
  const response=(action,result)=>{
    const id=`q${++serial}`,createdAt=new Date().toISOString();
    rows.push({requestId:id,windowId:window,...target,action,status:'action_finished',startedAt:createdAt,durationMs:0,generation:1,
      contentWrite:action==='debug.batch'||action==='schematic.save'});
    return toMcpResult({ok:true,result:{ok:true,id,createdAt,context:{...target,documentType:'schematic'},result}});
  };
  const invoke=async(action,args={})=>{
    if(action==='schematic.components.list')return response(action,snapshot(args.payload?.primitiveIds));
    if(action==='schematic.target.check')return response(action,{status:'pass',verified:true,partial:false});
    if(action==='schematic.drc.check')return response(action,{nativePassed:true,passed:true,countsAvailable:true,summary:{error:0,fatal:0,unknown:0,warn:2}});
    if(action==='schematic.export.image'){
      const artifactPath=path.join(dir,'synthetic-image.bin');await writeFile(artifactPath,'SYNTHETIC IMAGE, NOT AN EDA SCREENSHOT');
      return response(action,{artifactPath});
    }
    if(action==='document.close'){
      const result=response(action,{closed:true,uuid:'d',tabId:'tab'});
      Object.assign(rows.at(-1),{startedContext:target,closedDocumentUuid:'d',documentUuid:'another-tab'});
      result.structuredContent.context={...target,documentUuid:'another-tab'};
      return result;
    }
    if(action==='document.open')return response(action,{ready:true,tabId:'tab'});
    assert.equal(action,'debug.batch');
    const results=args.payload.steps.map((step,index)=>{
      let result={};
      if(step.action==='schematic.component.place'){
        const i=Number(step.payload.designator.slice(1))-1;
        const part={...measuredParts()[i],...moduleParts()[i].properties,device:moduleParts()[i].device,symbol:moduleParts()[i].symbol,
          footprint:moduleParts()[i].footprint,subPartName:'A'};parts.push(part);result={primitiveId:part.primitiveId};
      }else if(step.action==='schematic.component.modify'){
        Object.assign(parts.find(part=>part.primitiveId===step.payload.primitiveId),step.payload.patch);result={primitiveId:step.payload.primitiveId};
      }else if(step.action==='schematic.components.list')result=snapshot(step.payload.primitiveIds);
      else if(step.action==='schematic.wire.create'){
        const id=`wire${wires.length+1}`,points=step.payload.points;
        for(let i=1;i<points.length;i++)wires.push({primitiveId:id,segmentIndex:i-1,net:'internal',x0:points[i-1][0],y0:points[i-1][1],x1:points[i][0],y1:points[i][1],rawLine:points,rawEncoding:'pairs'});
        result={primitiveId:id,net:'internal',line:points};
      }else if(step.action==='schematic.wire.labels.apply')result={verified:true,wireIds:step.payload.wireIds,labels:step.payload.labels};
      else assert.fail('Unexpected simulated mutation '+step.action);
      if(moduleCatalog.get(step.action)?.mutates!==false)mutations.push(step.action);
      return {index,action:step.action,ok:true,result};
    });
    return response(action,{ok:true,partial:false,total:results.length,completed:results.length,results,
      rules:{status:'pass',mode:args.edit?.mode??'layout',scope:parts.map(part=>part.primitiveId),items:['identity','pin_nets','valid_wires','pin_exit'].map(id=>({id,severity:'error',status:'pass',coverage:'checked'}))}});
  };
  const {plan}=await loadPlan(input.planFile);
  const original=toMcpResult({ok:true,result:{ok:true,context:target,result:snapshot()}});
  await writeFile(plan.sources[0].path,JSON.stringify(original));plan.sources[0].sha256=sha(await readFile(plan.sources[0].path));
  plan.taskKind='new_module';await writeFile(input.planFile,JSON.stringify(plan));input.expectedHash=(await loadPlan(input.planFile)).sha256;
  const oldFetch=globalThis.fetch;
  globalThis.fetch=async(_url,options)=>{
    if(options.method==='GET')return new Response(JSON.stringify({instance:'offline-mock',documents:[{windowId:window,context:target,generation:1,mutationAttemptedSinceConfirmedSave:!saved}],receipts:rows}));
    const command=JSON.parse(options.body);controls.push(command.operation);
    if(command.operation==='visual_review')return new Response(JSON.stringify({ok:true,generation:1}));
    assert.equal(command.operation,'finish');saved=true;return new Response(JSON.stringify(response('schematic.save',{saved:true}).structuredContent));
  };
  try{
    if(interrupted)await planTool(input,moduleCatalog,invoke);
    const result=await planTool({operation:interrupted?'resume':'run',continuous:true,planFile:input.planFile,expectedHash:input.expectedHash,window},moduleCatalog,invoke);
    assert.equal(result.structuredContent.ok,true,JSON.stringify(result.structuredContent));
    assert.equal(mutations.filter(action=>action==='schematic.component.place').length,2);
    assert.deepEqual(result.structuredContent.completedStages,['place','measure','routes','labels','final']);
    const runResultFile=path.join(dir,'run.json');await writeFile(runResultFile,JSON.stringify(result));
    const delivered=await planTool({operation:'finish',window,runResultFile,expectedRunResultHash:sha(await readFile(runResultFile)),visualAccepted:true},moduleCatalog,invoke);
    assert.equal(delivered.structuredContent.saved,true,JSON.stringify(delivered));
    assert.equal(delivered.structuredContent.persistence.status,'unverified');
    const deliveryFile=delivered.structuredContent.receiptFile,before=await readFile(deliveryFile);
    let verified=await planTool({operation:'verify_delivery',window,deliveryFile},moduleCatalog,invoke);
    assert.equal(verified.structuredContent.ok,false,'saved:true alone cannot establish persistence');
    const close=await invoke('document.close'),open=await invoke('document.open');
    const verification={operation:'verify_delivery',window,deliveryFile,closeRequestId:close.structuredContent.id,openRequestId:open.structuredContent.id};
    verified=await planTool(verification,moduleCatalog,invoke);
    assert.equal(verified.structuredContent.ok,true,JSON.stringify(verified));
    assert.equal(verified.structuredContent.wholePageComplete,true);
    assert.equal(verified.structuredContent.engineeringAccepted,false);
    assert.deepEqual(await readFile(deliveryFile),before);
    assert.deepEqual(controls,['visual_review','finish']);
    parts[0].name='unexpected post-save change';
    verified=await planTool(verification,moduleCatalog,invoke);
    assert.equal(verified.structuredContent.ok,false);
    assert.equal(verified.structuredContent.designDiff.status,'different');
  }finally{globalThis.fetch=oldFetch;}
});

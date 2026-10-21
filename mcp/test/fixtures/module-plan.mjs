import { writeFile } from 'node:fs/promises';
import { fixture, loadPlan, toMcpResult } from './p0-receipts.mjs';

export const moduleCatalog = new Map(['schematic.component.place','schematic.component.modify','schematic.components.list',
  'schematic.wire.create','schematic.wire.labels.apply','schematic.target.check','schematic.export.image','schematic.drc.check']
  .map(name => [name,{name,mutates:!['schematic.components.list','schematic.target.check','schematic.export.image','schematic.drc.check'].includes(name)}]));
export const moduleParts = () => [1,2].map(i => ({ key:`unique:u${i}`,uniqueId:`u${i}`,ref:`R${i}`,
  device:{uuid:'a'.repeat(32),libraryUuid:'b'.repeat(32)},symbol:{uuid:'c'.repeat(32),libraryUuid:'b'.repeat(32)},
  footprint:{uuid:'d'.repeat(32),libraryUuid:'b'.repeat(32)},properties:{name:'10k'},
  pins:[{number:'1',name:'A',state:'connected',net:'internal',noConnected:false}] }));
export async function modulePlan(dir) {
  const input = await fixture(dir), loaded = await loadPlan(input.planFile), plan = loaded.plan;
  const components = moduleParts(), keys = components.map(p=>p.key);
  plan.semanticTarget.components = components; plan.semanticTarget.idMap={};
  plan.semanticTarget.nets=[{name:'internal',match:'members',scope:'local',members:keys.map(componentKey=>({componentKey,pinNumber:'1'}))}];
  plan.missing=[];
  plan.layoutHints={mode:'preserve',parts:keys.map((key,i)=>({key,x:200+i*100,y:200,rotation:0,rotationMode:'stored',subPartName:'A',mirror:false})),
    groups:[{id:'module',purpose:'resistor module',componentKeys:keys}],netPolicies:{internal:'direct'},visibleNetNames:[]};
  plan.stages=[{id:'place',check:'geometry',steps:components.map((p,i)=>({action:'schematic.component.place',payload:{
    ...p.device,uniqueId:p.uniqueId,designator:p.ref,x:200+i*100,y:200,rotation:0,rotationMode:'stored',subPartName:'A',mirror:false}}))},
    {id:'measure',check:'geometry',componentKeys:keys,steps:[]},
    {id:'routes',check:'geometry',componentKeys:keys,routeFrom:'measure',steps:[]},
    {id:'labels',check:'geometry',componentKeys:keys,labelsFrom:'routes',steps:[]},
    {id:'final',check:'final',steps:[]}];
  await writeFile(input.planFile,JSON.stringify(plan));
  return {...input,stage:'place',expectedHash:(await loadPlan(input.planFile)).sha256};
}
export const measuredParts = () => moduleParts().map((p,i)=>({primitiveId:`r${i+1}`,uniqueId:p.uniqueId,designator:p.ref,
  componentType:'part',x:200+i*100,y:200,rotation:0,mirror:false,pinsAvailable:true,netlistAvailable:true,
  bbox:{minX:190+i*100,minY:190,maxX:210+i*100,maxY:210},
  pins:[{pinNumber:'1',pinName:'A',x:210+i*100,y:200,rotation:0,net:'internal',noConnected:false}]}));
export const moduleReadback = () => ({components:measuredParts(),readScope:{complete:false,concurrentChange:false,primitiveIds:['r1','r2'],missingIds:[]}});
export function moduleInvoke(abortMeasurement) {
  return async (action,args) => {
    if(action==='schematic.target.check')return toMcpResult({ok:true,result:{ok:true,context:{projectUuid:'p',documentUuid:'d'},result:{status:'pass',verified:true}}});
    if(action==='debug.batch'){
      const steps=args.payload.steps;
      const results=steps.map((step,i)=>({index:i,action:step.action,ok:true,result:step.action==='schematic.component.place'?{primitiveId:`r${i+1}`} : moduleReadback()}));
      if(steps[0].action==='schematic.components.list')abortMeasurement?.();
      return toMcpResult({ok:true,result:{ok:true,requestId:'edit',context:{projectUuid:'p',documentUuid:'d'},result:{ok:true,partial:false,total:steps.length,completed:steps.length,results,rules:{status:'pass',items:[]}}}});
    }
    if(action==='schematic.components.list')return toMcpResult({ok:true,result:{ok:true,context:{projectUuid:'p',documentUuid:'d'},result:moduleReadback()}});
    throw new Error('Unexpected offline action: '+action);
  };
}

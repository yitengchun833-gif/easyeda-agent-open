import {isDeepStrictEqual} from 'node:util';

// shortcut: compare explicitly bound source records; changed geometry needs a new review, not inferred rigid-body alignment.
export function inspectCopperAttachment(check,snapshot,decoded) {
  const out={id:check.id,type:check.type,status:'unknown',reason:null,actual:{},objectIds:[],
    coverage:'Declared board-level copper/mask dependency persistence only; no contour, footprint-library or Gerber validation'};
  const stop=reason=>({...out,reason});
  if(decoded?.status!=='complete')return stop('Current source coverage is unknown');
  const base=check.baseline;
  if(typeof check.componentId!=='string'||!check.componentId||!base?.component||
    !Array.isArray(base.objects)||base.objects.length<2||
    !['x','y','angle','layerId'].every(k=>Number.isFinite(base.component[k]))||
    typeof base.component.footprintUuid!=='string'||!base.component.footprintUuid)
    return stop('Explicit component pose/footprint and copper plus mask baseline required');
  const records=decoded.records;
  const owners=records.filter(r=>r.meta.type==='COMPONENT'&&r.meta.id===check.componentId);
  const footprint=records.filter(r=>r.meta.type==='ATTR'&&r.data.parentId===check.componentId&&r.data.key==='Footprint');
  if(owners.length!==1||footprint.length!==1)return stop('Component or footprint identity absent/ambiguous');
  out.objectIds.push(check.componentId);
  out.actual.component=Object.fromEntries(['x','y','angle','layerId'].map(k=>[k,owners[0].data[k]]));
  out.actual.component.footprintUuid=footprint[0].data.value;
  out.actual.objects=[];
  const ids=new Set();let copper=false,mask=false;
  for(const obj of base.objects) {
    if(!['FILL','POLY'].includes(obj.type)||typeof obj.id!=='string'||!obj.id||ids.has(obj.id)||!obj.data||typeof obj.data!=='object')
      return stop('Invalid or duplicate explicit copper/mask baseline');
    ids.add(obj.id);out.objectIds.push(obj.id);
    const found=records.filter(r=>r.meta.type===obj.type&&r.meta.id===obj.id);
    if(found.length!==1)return stop('Bound copper/mask object missing or ambiguous: '+obj.id);
    const data=found[0].data;
    out.actual.objects.push({type:obj.type,id:obj.id,data});
    const layers=records.filter(r=>r.meta.type==='LAYER'&&r.data.layerId===data.layerId);
    if(obj.type==='FILL') {
      if(data.fillStyle!=='SOLID'||!Array.isArray(data.path)||!data.path.length||layers.length!==1||
        !['TOP','BOTTOM'].includes(layers[0].data.layerType))
        return stop('Copper baseline outside supported solid outer-layer FILL scope');
      copper=true;
    }else {
      if(layers.length!==1||!['TOP_SOLDER_MASK','BOT_SOLDER_MASK'].includes(layers[0].data.layerType))
        return stop('Bound POLY is not a confirmed solder-mask layer');
      if(!Array.isArray(data.path)||!data.path.length)return stop('Mask path missing');
      mask=true;
    }
    if(!isDeepStrictEqual(data,obj.data))return stop('Bound copper/mask changed; dependency requires recheck: '+obj.id);
  }
  if(!copper||!mask)return stop('Both declared copper and solder-mask dependencies required');
  if(!isDeepStrictEqual(out.actual.component,base.component))
    return stop('Component pose or footprint changed; copper/mask dependency requires recheck');
  return {...out,status:'pass',reason:'Explicit component/copper/mask dependency records unchanged'};
}

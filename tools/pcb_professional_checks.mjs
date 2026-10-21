import {inspectCopperAttachment} from './pcb_copper_attachment.mjs';

const MM_PER_MIL=0.0254;
const finite=n=>typeof n==='number'&&Number.isFinite(n);
const text=s=>typeof s==='string'&&s.trim().length>0;
const aggregate=checks=>checks.some(c=>c.status==='fail')?'fail':checks.some(c=>c.status!=='pass')||!checks.length?'unknown':'pass';

export function decodeBoardSource(source) {
  const records=[],keys=new Set();
  if(!text(source))return {status:'unknown',records,reason:'Native document source is missing'};
  try {
    for(const line of source.split(/\r?\n/).filter(s=>s.trim())) {
      const split=line.indexOf('||');
      if(split<0)throw Error('Missing source record separator');
      const meta=JSON.parse(line.slice(0,split));
      const tail=line.slice(split+2);
      const data=JSON.parse(tail.endsWith('|')?tail.slice(0,-1):tail);
      if(!meta||typeof meta!=='object'||Array.isArray(meta)||!text(meta.type)||!data||typeof data!=='object'||Array.isArray(data))throw Error('Unrecognized source record');
      const key=JSON.stringify([meta.type,meta.id??null]);
      if(keys.has(key))throw Error('Repeated source record identity; current document coverage is ambiguous');
      keys.add(key);
      records.push({meta,data});
    }
    if(records.filter(r=>r.meta.type==='DOCHEAD').length!==1)throw Error('Expected one current document header');
    return {status:'complete',records};
  } catch(error) {return {status:'unknown',records,reason:error.message};}
}

function pinLookup(selector,snapshot,decoded) {
  if(!text(selector?.designator)||!text(selector?.padNumber))return {reason:'Explicit designator and padNumber are required'};
  const attrs=decoded.records.filter(r=>r.meta.type==='ATTR'&&r.data.key==='Designator'&&r.data.value===selector.designator);
  if(attrs.length!==1)return {reason:'Designator is absent or ambiguous in native source'};
  const componentId=attrs[0].data.parentId;
  if(decoded.records.filter(r=>r.meta.type==='COMPONENT'&&r.meta.id===componentId).length!==1)return {reason:'Component source binding is absent or ambiguous'};
  const mappings=[];
  for(const r of decoded.records.filter(r=>r.meta.type==='PAD_NET')) {
    let id;try{id=JSON.parse(r.meta.id);}catch{continue;}
    if(Array.isArray(id)&&id.length===4&&id[0]==='PAD_NET'&&id[1]===componentId&&id[2]===selector.padNumber&&text(id[3]))mappings.push({id,r});
  }
  if(mappings.length!==1)return {reason:'Pad-to-component mapping is absent or ambiguous'};
  const {id,r}=mappings[0];
  const pads=snapshot.pads?.filter(p=>p.primitiveId===componentId+id[3]&&p.padNumber===selector.padNumber);
  if(pads?.length!==1||pads[0].net!==r.data.padNet)return {reason:'Native pad and source mapping disagree or are unavailable'};
  return {pad:pads[0],componentId,selector};
}

function inspectPinRole(check,snapshot,decoded) {
  const pin=pinLookup(check.pin,snapshot,decoded);
  if(pin.reason)return {status:'unknown',reason:pin.reason};
  const objectIds=[pin.pad.primitiveId];
  const actual={role:check.role,net:pin.pad.net};
  const coverage=['Explicit pin-to-Net assignment; role is declared, never inferred from Net names'];
  if(!text(check.expectedNet))return {status:'unknown',reason:'Expected Net is missing',actual,objectIds,coverage};
  if(pin.pad.net!==check.expectedNet)return {status:'fail',reason:'Specified pin has a different Net',actual,objectIds,coverage};
  if(!check.decoupling)return {status:'pass',reason:'Specified pin-to-Net assignment matches',actual,objectIds,coverage};
  const d=check.decoupling;
  if(d.metric!=='pad_center_euclidean'||!finite(d.maxDistanceMm)||d.maxDistanceMm<0||!text(d.expectedReturnNet))
    return {status:'unknown',reason:'Decoupling requires an explicit supported metric, distance limit and return Net',actual,objectIds,coverage};
  const power=pinLookup(d.powerPin,snapshot,decoded),ret=pinLookup(d.returnPin,snapshot,decoded);
  if(power.reason||ret.reason)return {status:'unknown',reason:power.reason||ret.reason,actual,objectIds,coverage};
  objectIds.push(power.pad.primitiveId,ret.pad.primitiveId);
  actual.decoupling={powerNet:power.pad.net,returnNet:ret.pad.net,metric:d.metric,maxDistanceMm:d.maxDistanceMm};
  if(power.pad.net!==check.expectedNet||ret.pad.net!==d.expectedReturnNet)
    return {status:'fail',reason:'Specified decoupling endpoints have unexpected Nets',actual,objectIds,coverage};
  if(![pin.pad.x,pin.pad.y,power.pad.x,power.pad.y].every(finite))return {status:'unknown',reason:'Native pad coordinates are unavailable',actual,objectIds,coverage};
  actual.decoupling.distanceMm=Math.hypot(pin.pad.x-power.pad.x,pin.pad.y-power.pad.y)*MM_PER_MIL;
  coverage.push('Declared pad-center distance only; capacitor identity/value, return loop, coupling and performance are not established');
  return {status:actual.decoupling.distanceMm<=d.maxDistanceMm?'pass':'fail',reason:'Compared the declared pad-center distance limit',actual,objectIds,coverage};
}

const pointDistance=(p,a,b)=>{
  const dx=b.x-a.x,dy=b.y-a.y,l=dx*dx+dy*dy;
  const t=l?Math.max(0,Math.min(1,((p.x-a.x)*dx+(p.y-a.y)*dy)/l)):0;
  return Math.hypot(p.x-a.x-t*dx,p.y-a.y-t*dy);
};
const cross=(a,b)=>a.x*b.y-a.y*b.x;
function segmentDistance(a,b) {
  const u={x:a.b.x-a.a.x,y:a.b.y-a.a.y},v={x:b.b.x-b.a.x,y:b.b.y-b.a.y},w={x:b.a.x-a.a.x,y:b.a.y-a.a.y};
  const den=cross(u,v);
  if(den!==0) {
    const t=cross(w,v)/den,s=cross(w,u)/den;
    if(t>=0&&t<=1&&s>=0&&s<=1)return 0;
  }
  return Math.min(pointDistance(a.a,b.a,b.b),pointDistance(a.b,b.a,b.b),pointDistance(b.a,a.a,a.b),pointDistance(b.b,a.a,a.b));
}

function lineTouchesPad(line,pad,tolerance,layers) {
  const through=pad.metallization===true&&Array.isArray(pad.hole)&&pad.hole.length>1;
  if(!(through?layers.includes(line.layer):line.layer===pad.layer))return false;
  if(Array.isArray(pad.specialPad)&&pad.specialPad.length)return null;
  const c={x:pad.x,y:pad.y};
  if(pointDistance(c,line.a,line.b)<=tolerance)return true;
  const shape=pad.pad;
  if(!Array.isArray(shape)||!['RECT','ELLIPSE'].includes(shape[0])||![shape[1],shape[2],pad.rotation].every(finite)||shape[1]<=0||shape[2]<=0||(shape[0]==='RECT'&&shape[3]!==undefined&&shape[3]!==0))return null;
  const angle=-pad.rotation*Math.PI/180;
  const local=p=>({x:((p.x-c.x)*Math.cos(angle)-(p.y-c.y)*Math.sin(angle))/(shape[1]/2),y:((p.x-c.x)*Math.sin(angle)+(p.y-c.y)*Math.cos(angle))/(shape[2]/2)});
  const a=local(line.a),b=local(line.b);
  if(shape[0]==='ELLIPSE')return pointDistance({x:0,y:0},a,b)<=1;
  if((Math.abs(a.x)<=1&&Math.abs(a.y)<=1)||(Math.abs(b.x)<=1&&Math.abs(b.y)<=1))return true;
  const corners=[{x:-1,y:-1},{x:-1,y:1},{x:1,y:1},{x:1,y:-1}];
  return corners.some((p,i)=>segmentDistance({a,b},{a:p,b:corners[(i+1)%4]})===0);
}

function widestPath(nodes,edges,start,end,excluded=-1) {
  const score=Array(nodes.length).fill(-1),previous=Array(nodes.length).fill(-1),done=new Set();score[start]=Infinity;
  for(let step=0;step<nodes.length;step++) {
    let at=-1;for(let i=0;i<nodes.length;i++)if(i!==excluded&&!done.has(i)&&score[i]>=0&&(at<0||score[i]>score[at]))at=i;
    if(at<0)break;if(at===end) {const path=[];for(let n=end;n!==-1;n=previous[n])path.unshift(n);return {width:score[end],path};}
    done.add(at);
    for(const next of edges[at])if(next!==excluded&&!done.has(next)) {
      const candidate=Math.min(score[at],nodes[next].width);
      if(candidate>score[next]){score[next]=candidate;previous[next]=at;}
    }
  }
  return null;
}

function inspectPowerPath(check,snapshot,decoded) {
  if(!text(check.net)||!finite(check.minWidthMm)||check.minWidthMm<=0||!finite(check.toleranceMm)||check.toleranceMm<0)
    return {status:'unknown',reason:'Explicit Net, positive minimum width and nonnegative connection tolerance are required'};
  const from=pinLookup(check.from,snapshot,decoded),to=pinLookup(check.to,snapshot,decoded);
  if(from.reason||to.reason)return {status:'unknown',reason:from.reason||to.reason};
  if(from.pad.primitiveId===to.pad.primitiveId)return {status:'unknown',reason:'Power-path endpoints must be distinct explicit pads'};
  const objectIds=[from.pad.primitiveId,to.pad.primitiveId];
  const actual={net:check.net,minWidthMm:check.minWidthMm,toleranceMm:check.toleranceMm};
  const coverage=['Same-Net straight-line geometry and explicit endpoint pads; native mil coordinates converted by 0.0254 mm/mil',
    'No current/temperature guarantee, pad neck/thermal relief, plated-hole capacity or polygon current-distribution calculation'];
  if(from.pad.net!==check.net||to.pad.net!==check.net)return {status:'fail',reason:'Power-path endpoints have unexpected Nets',actual,objectIds,coverage};
  const layers=decoded.records.filter(r=>r.meta.type==='LAYER'&&r.data.use===true&&['TOP','BOTTOM','INNER'].includes(r.data.layerType)).map(r=>r.data.layerId);
  const lineRecords=decoded.records.filter(r=>r.meta.type==='LINE'&&r.data.netName===check.net);
  if(lineRecords.some(r=>![r.data.startX,r.data.startY,r.data.endX,r.data.endY,r.data.width,r.data.layerId].every(finite)||r.data.width<=0||(r.data.startX===r.data.endX&&r.data.startY===r.data.endY)))
    return {status:'unknown',reason:'Native line geometry is incomplete',actual,objectIds,coverage};
  if(!layers.length||lineRecords.some(r=>!layers.includes(r.data.layerId)))
    return {status:'unknown',reason:'Line layer is not confirmed as an active copper layer in source',actual,objectIds,coverage};
  const lines=lineRecords.map(r=>({id:r.meta.id,layer:r.data.layerId,a:{x:r.data.startX,y:r.data.startY},b:{x:r.data.endX,y:r.data.endY},width:r.data.width*MM_PER_MIL}));
  const padNodes=(snapshot.pads||[]).filter(p=>p.net===check.net);
  if(padNodes.some(p=>![p.x,p.y,p.layer].every(finite)))return {status:'unknown',reason:'Same-Net native pad geometry is incomplete',actual,objectIds,coverage};
  for(const r of decoded.records.filter(r=>r.meta.type==='PAD_NET'&&r.data.padNet===check.net)) {
    let id;try{id=JSON.parse(r.meta.id);}catch{return {status:'unknown',reason:'Same-Net pad source mapping is unrecognized',actual,objectIds,coverage};}
    if(!Array.isArray(id)||id.length!==4||id[0]!=='PAD_NET'||padNodes.filter(p=>p.primitiveId===id[1]+id[3]&&p.padNumber===id[2]).length!==1)
      return {status:'unknown',reason:'Full same-Net pad observations do not match source',actual,objectIds,coverage};
  }
  const nodes=[...lines,...padNodes.map(p=>({id:p.primitiveId,width:Infinity,pad:p}))];
  const edges=nodes.map(()=>[]),join=(a,b)=>{edges[a].push(b);edges[b].push(a);};
  let unresolvedOverlap=false;
  const tol=check.toleranceMm/MM_PER_MIL;
  for(let i=0;i<lines.length;i++)for(let k=0;k<i;k++)if(lines[i].layer===lines[k].layer) {
    const distance=segmentDistance(lines[i],lines[k]);
    if(distance<=tol)join(i,k);
    else if(distance*MM_PER_MIL<=(lines[i].width+lines[k].width)/2)unresolvedOverlap=true;
  }
  for(let i=0;i<lines.length;i++)for(let k=0;k<padNodes.length;k++) {
    const touch=lineTouchesPad(lines[i],padNodes[k],tol,layers);
    if(touch)join(i,lines.length+k);else if(touch===null)unresolvedOverlap=true;
  }
  const start=nodes.findIndex(n=>n.id===from.pad.primitiveId),end=nodes.findIndex(n=>n.id===to.pad.primitiveId);
  const best=widestPath(nodes,edges,start,end);
  actual.connectedLinePath=Boolean(best);
  actual.lineBottleneckMm=best&&Number.isFinite(best.width)?best.width:null;
  actual.pathObjectIds=best?best.path.map(i=>nodes[i].id):[];
  actual.selectedLineLengthMm=best?best.path.filter(i=>i<lines.length).reduce((sum,i)=>sum+Math.hypot(lines[i].a.x-lines[i].b.x,lines[i].a.y-lines[i].b.y)*MM_PER_MIL,0):null;
  if(best&&best.width>=check.minWidthMm)return {status:'pass',reason:'An explicit line path meets the declared minimum width',actual,objectIds:actual.pathObjectIds,coverage};
  // shortcut: copper contours and via barrels are not solved here; use geometry/DFM evidence before failing their possible paths.
  const possibleOthers=decoded.records.filter(r=>['FILL','POLY','POUR','ARC','VIA'].includes(r.meta.type)&&(r.data.netName===check.net||r.data.networkList?.includes(check.net)));
  actual.unresolvedObjectIds=possibleOthers.map(r=>r.meta.id);
  const parallel=best&&best.path.some(i=>i<lines.length&&widestPath(nodes,edges,start,end,i));
  actual.parallelLinePath=Boolean(parallel);
  if(possibleOthers.length||unresolvedOverlap||parallel)return {status:'unknown',reason:'Other copper/via geometry, overlap or parallel thin paths require a separate bottleneck assessment',actual,objectIds,coverage};
  return {status:'fail',reason:best?'Every recorded single line path contains a narrower mandatory section':'No recorded straight-line path connects the explicit endpoints',actual,objectIds:best?actual.pathObjectIds:objectIds,coverage};
}

export function inspectProfessionalChecks(spec,snapshot,baseline) {
  const limitations=['Checks evaluate only explicit design assertions and observed source geometry; evidenceRef is a reference for caller review, not an automatically certified source.'];
  if(spec?.version!=='pcb-professional-checks/v0'||spec.unit!=='mm'||!Array.isArray(spec.checks)||!spec.checks.length||!text(spec.target?.projectUuid)||!text(spec.target?.documentUuid))
    return {status:'unknown',checks:[],reason:'Expected nonempty pcb-professional-checks/v0 specification with unit mm',limitations};
  const decoded=decodeBoardSource(snapshot?.source);
  const head=decoded.records.find(r=>r.meta.type==='DOCHEAD');
  if(decoded.status==='complete'&&(head.data.docType!=='PCB'||head.data.uuid!==spec.target.documentUuid)) {
    decoded.status='unknown';decoded.reason='Specification target does not match the observed PCB document header';
  }
  const ids=new Set();
  const checks=spec.checks.map(check=>{
    const common={id:check?.id,type:check?.type,role:check?.role,evidenceRef:check?.evidenceRef};
    if(!text(check?.id)||ids.has(check.id)||!text(check.role)||!text(check.evidenceRef))return {...common,status:'unknown',reason:'Each check requires a unique id, declared role and evidenceRef'};
    ids.add(check.id);
    if(decoded.status!=='complete')return {...common,status:'unknown',reason:decoded.reason};
    if(check.type==='copper_attachment')return {...common,...inspectCopperAttachment(check,snapshot,decoded,baseline)};
    if(check.type==='pin_role')return {...common,...inspectPinRole(check,snapshot,decoded)};
    if(check.type==='power_path')return {...common,...inspectPowerPath(check,snapshot,decoded)};
    return {...common,status:'unknown',reason:'Unsupported professional check type'};
  });
  return {status:aggregate(checks),checks,limitations};
}

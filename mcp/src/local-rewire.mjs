import { isDeepStrictEqual } from 'node:util';
import { fullInventory, wirePoints, preservationDiff } from './preservation.mjs';

const eps=1e-6;
const same=(a,b)=>Math.abs(a[0]-b[0])<=eps&&Math.abs(a[1]-b[1])<=eps;
const sameSegment=(a,b)=>(same(a[0],b[0])&&same(a[1],b[1]))||(same(a[0],b[1])&&same(a[1],b[0]));
// Single-segment subset of schematic-wire-topology.ts parseObservedWireLine:
// flat segment records, nested segment records, or nested polyline vertices.
// Both preview and final native verification use this exact decoder. Never
// truncate/filter malformed or extra records into an apparently valid segment.
function singleObservedSegment(raw){
  const finiteRow=(row,length)=>Array.isArray(row)&&row.length===length&&Array.from(row).every(Number.isFinite);
  let segment;
  if(finiteRow(raw,4))segment=raw;
  else if(Array.isArray(raw)&&raw.length===1&&finiteRow(raw[0],4))segment=raw[0];
  else if(Array.isArray(raw)&&raw.length===2&&Array.from(raw).every(row=>finiteRow(row,2)))segment=[...raw[0],...raw[1]];
  return segment?[[segment[0],segment[1]],[segment[2],segment[3]]]:null;
}
const on=(p,[a,b])=>Math.abs((p[0]-a[0])*(b[1]-a[1])-(p[1]-a[1])*(b[0]-a[0]))<=eps
  &&p[0]>=Math.min(a[0],b[0])-eps&&p[0]<=Math.max(a[0],b[0])+eps&&p[1]>=Math.min(a[1],b[1])-eps&&p[1]<=Math.max(a[1],b[1])+eps;
const cross=(a,b,c)=>(b[0]-a[0])*(c[1]-a[1])-(b[1]-a[1])*(c[0]-a[0]);
const touches=([a,b],[c,d])=>on(a,[c,d])||on(b,[c,d])||on(c,[a,b])||on(d,[a,b])
  ||cross(a,b,c)*cross(a,b,d)<0&&cross(c,d,a)*cross(c,d,b)<0;
const refuse=reason=>{throw new Error(`Unsupported pin rewire: ${reason}; no EDA write sent`);};
function ownedWireName(attributes, net) {
  const names=attributes.filter(attr=>attr.Key==='Name');
  const relevance=attributes.filter(attr=>attr.Key==='Relevance');
  return names.length===1&&names[0].Value===net&&relevance.length<=1
    &&relevance.every(attr=>attr.Value==='[]')&&attributes.length===names.length+relevance.length;
}

// A deliberately small topology slice: replacing one isolated named pin stub
// with identical geometry. A wire primitive is never split or partly deleted.
export function planPinRewire(snapshot,input){
  fullInventory(snapshot);
  for(const key of ['primitiveId','pinNumber','wireId','targetNet'])if(typeof input[key]!=='string'||!input[key].trim())refuse(`missing ${key}`);
  if(snapshot.pagePrimitives.buses.length)refuse('bus topology is not supported');
  const parts=snapshot.components.filter(part=>part.componentType==='part');
  if(parts.some(part=>part.pinsAvailable!==true||part.netlistAvailable!==true||!part.pins?.length
    ||part.pins.some(pin=>![pin.x,pin.y,pin.rotation].every(Number.isFinite)||typeof pin.net!=='string'||typeof pin.noConnected!=='boolean'||pin.noConnected&&pin.net!=='')))
    refuse('complete measured pin geometry and networks are required');
  const owners=parts.filter(part=>part.primitiveId===input.primitiveId),part=owners[0];
  const pins=part?.pins.filter(pin=>pin.pinNumber===input.pinNumber)??[],pin=pins[0];
  if(owners.length!==1||pins.length!==1||pin.noConnected||!pin.net)refuse('source must be one known connected, non-NC pin');
  if(input.targetNet===pin.net)refuse('pin already has the requested net');
  const targets=parts.flatMap(owner=>owner.pins.filter(p=>p.net===input.targetNet&&p.noConnected!==true).map(p=>({primitiveId:owner.primitiveId,pinNumber:p.pinNumber})));
  if(!targets.length)refuse('targetNet must already exist on a measured pin; new-net inference is not supported');
  const rows=snapshot.wires.filter(wire=>wire.primitiveId===input.wireId),wire=rows[0];
  if(rows.length!==1||wire.net!==pin.net)refuse('wrong wire must be one measured segment on the source net');
  const raw=snapshot.pagePrimitives.wires.filter(record=>record.primitiveId===input.wireId);
  if(raw.length!==1)refuse('native wire identity is ambiguous');
  const rawPoints=singleObservedSegment(raw[0].Line);
  const points=wirePoints(wire),anchor=[pin.x,pin.y];
  if(!rawPoints||points.length!==2||!points.flat().every(Number.isFinite)||same(points[0],points[1])
    ||points[0][0]!==points[1][0]&&points[0][1]!==points[1][1]
    ||!sameSegment(points,rawPoints))refuse('multi-segment, nonorthogonal or inconsistent wire geometry');
  if(!same(anchor,points[0])&&!same(anchor,points[1]))refuse('source pin must be an endpoint');
  const tip=same(anchor,points[0])?points[1]:points[0],quarter=((pin.rotation%360)+360)%360/90;
  if(Math.abs(quarter-Math.round(quarter))>eps)refuse('pin outward direction is not cardinal');
  const [dx,dy]=[[1,0],[0,1],[-1,0],[0,-1]][Math.round(quarter)%4];
  if((tip[0]-pin.x)*dx+(tip[1]-pin.y)*dy<=eps||Math.abs((tip[0]-pin.x)*dy-(tip[1]-pin.y)*dx)>eps)refuse('wire does not follow the measured outward pin direction');
  if(snapshot.wires.some(other=>{
    if(other.primitiveId===input.wireId)return false;
    const vertices=wirePoints(other);
    if(!vertices.flat().every(Number.isFinite))refuse('other wire geometry is incomplete');
    return vertices.slice(1).some((vertex,i)=>touches(points,[vertices[i],vertex]));
  }))refuse('wire touches/crosses another wire; branch deletion is not supported');
  for(const owner of parts)for(const other of owner.pins)if(!(owner===part&&other===pin)&&on([other.x,other.y],points))refuse('wire also touches another pin');
  for(const marker of snapshot.components.filter(owner=>!['part','sheet'].includes(owner.componentType))){
    const box=marker.bbox;
    if(![marker.x,marker.y].every(Number.isFinite)||!box||![box.minX,box.minY,box.maxX,box.maxY].every(Number.isFinite))refuse('marker geometry is incomplete');
    if(on([marker.x,marker.y],points)||points.some(p=>p[0]>=box.minX-eps&&p[0]<=box.maxX+eps&&p[1]>=box.minY-eps&&p[1]<=box.maxY+eps)
      ||[[[box.minX,box.minY],[box.maxX,box.minY]],[[box.maxX,box.minY],[box.maxX,box.maxY]],
      [[box.maxX,box.maxY],[box.minX,box.maxY]],[[box.minX,box.maxY],[box.minX,box.minY]]].some(edge=>touches(points,edge)))refuse('wire touches a marker or its measured extent');
  }
  const attributes=snapshot.pagePrimitives.attributes.filter(attr=>attr.ParentPrimitiveId===input.wireId);
  if(attributes.some(attr=>!attr.primitiveId)||!ownedWireName(attributes,pin.net))
    refuse('wire must have native Name and only empty Relevance metadata');
  const metadata={primitiveId:input.primitiveId,pinNumber:input.pinNumber,wireId:input.wireId,oldNet:pin.net,targetNet:input.targetNet,
    points:[anchor,tip],attributeIds:attributes.map(attr=>attr.primitiveId)};
  const payload={points:metadata.points,net:input.targetNet};
  for(const [source,key] of [['Color','color'],['LineWidth','lineWidth'],['LineType','lineType']])if(raw[0][source]!=null)payload[key]=raw[0][source];
  return {metadata,steps:[{action:'schematic.primitives.delete',payload:{primitiveIds:[input.wireId],requireDeleted:true}},
    {action:'schematic.wire.create',payload}],preview:{sourcePin:{primitiveId:part.primitiveId,designator:part.designator,pinNumber:pin.pinNumber,x:pin.x,y:pin.y},
      oldNet:pin.net,targetNet:input.targetNet,removedPrimitiveIds:[input.wireId,...metadata.attributeIds],replacement:{...payload},
      affectedEndpoints:[{x:pin.x,y:pin.y,kind:'specified-pin'},{x:tip[0],y:tip[1],kind:'free-tip'}],targetNetMembers:targets,
      limits:['One isolated single-segment named pin stub only; no bus, branch, crossing, other pin or marker contact',
        'Old wire ID/owned Name attributes are replaced; new native Name placement is not promised; no save or redesign']}};
}

export function verifyRewirePlan(plan,snapshot){
  const planned=planPinRewire(snapshot,plan.localRewire),stage=plan.stages?.[0];
  if(plan.taskKind!=='local_edit'||plan.acceptanceScope!=='local'||plan.stages.length!==1||stage?.id!=='rewire'
    ||!isDeepStrictEqual(stage.primitiveIds,[planned.metadata.primitiveId])||!isDeepStrictEqual(stage.steps,planned.steps)
    ||!isDeepStrictEqual(plan.localRewire,planned.metadata))throw new Error('Rewire plan differs from its bound isolated pin/wire scope');
}

export function rewirePreservation(before,after,metadata,execution){
  const batch=execution?.structuredContent?.result;
  const deletion=batch?.results?.[0],creation=batch?.results?.[1],newId=creation?.result?.primitiveId;
  if(deletion?.ok!==true||creation?.ok!==true||typeof newId!=='string'||!newId
    ||deletion.result?.verified!==true||deletion.result?.partial===true
    ||!deletion.result?.deletedIds?.wires?.includes(metadata.wireId))throw new Error('Rewire lacks verified deletion/creation evidence; do not replay');
  const expected=structuredClone(before),source=expected.components.find(part=>part.primitiveId===metadata.primitiveId);
  source.pins.find(pin=>pin.pinNumber===metadata.pinNumber).net=metadata.targetNet;
  const preserved=preservationDiff(expected,after,null,{wires:[metadata.wireId],attributes:metadata.attributeIds,newWires:[newId]});
  if(after.wires.some(wire=>wire.primitiveId===metadata.wireId)
    ||after.pagePrimitives.attributes.some(attr=>attr.ParentPrimitiveId===metadata.wireId))preserved.changed.push(metadata.wireId);
  const rows=after.wires.filter(wire=>wire.primitiveId===newId);
  const points=rows.length===1?wirePoints(rows[0]):[];
  const original=before.pagePrimitives.wires.find(wire=>wire.primitiveId===metadata.wireId);
  const native=after.pagePrimitives.wires.filter(wire=>wire.primitiveId===newId);
  const nativePoints=native.length===1?singleObservedSegment(native[0].Line):null;
  const attributes=after.pagePrimitives.attributes.filter(attr=>attr.ParentPrimitiveId===newId);
  if(rows.length!==1||rows[0].net!==metadata.targetNet||points.length!==2
    ||!sameSegment(points,metadata.points)||!nativePoints||!sameSegment(nativePoints,metadata.points)||!sameSegment(nativePoints,points)
    ||native.length!==1||native[0].Net!==metadata.targetNet||['Color','LineWidth','LineType'].some(key=>!isDeepStrictEqual(original[key],native[0][key]))
    ||attributes.some(attr=>!attr.primitiveId)||!ownedWireName(attributes,metadata.targetNet))preserved.changed.push(newId);
  preserved.changed=[...new Set(preserved.changed)];preserved.status=preserved.changed.length?'fail':'pass';
  return {...preserved,replacedWireId:metadata.wireId,newWireId:newId,targetPin:{primitiveId:metadata.primitiveId,pinNumber:metadata.pinNumber,net:metadata.targetNet}};
}

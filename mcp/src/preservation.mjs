import { isDeepStrictEqual } from 'node:util';

const finiteBox = box => box && ['minX','minY','maxX','maxY'].every(key=>Number.isFinite(box[key]))
  && box.minX < box.maxX && box.minY < box.maxY;
const fields = ['primitiveId','componentType','uniqueId','designator','x','y','rotation','mirror','bbox','device','symbol','footprint',
  'name','manufacturer','manufacturerId','supplier','supplierId','addIntoBom','addIntoPcb','otherProperty'];
const pinFields = ['pinNumber','pinName','x','y','rotation','net','noConnected'];
const project = (value, keys) => {
  const state=Object.fromEntries(keys.filter(key=>Object.hasOwn(value,key)).map(key=>[key,value[key]]));
  if(state.componentType==='sheet'&&state.otherProperty){
    // Match the native inventory's clock normalization; all other sheet data stays protected.
    state.otherProperty={...state.otherProperty};
    delete state.otherProperty['@Update Date']; delete state.otherProperty['@Update Time'];
  }
  return state;
};
function sameRecord(before,after,keys) {
  return after && isDeepStrictEqual(project(before,keys),project(after,keys));
}
export function wirePoints(wire) {
  if (Array.isArray(wire.points) && wire.points.length>=2 && wire.points.every(p=>Array.isArray(p)&&p.length===2&&p.every(Number.isFinite))) return wire.points;
  if (['x0','y0','x1','y1'].every(key=>Number.isFinite(wire[key]))) return [[wire.x0,wire.y0],[wire.x1,wire.y1]];
  throw new Error('Existing wire geometry is incomplete');
}
export function contains(region, points) {
  return points.every(([x,y])=>x>region.minX && x<region.maxX && y>region.minY && y<region.maxY);
}
const kinds=['components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects'];
export function fullInventory(snapshot) {
  if(snapshot.wiresAvailable!==true || !snapshot.pagePrimitives || kinds.some(key=>!Array.isArray(snapshot.pagePrimitives[key])))
    throw new Error('Complete wiresAvailable and pagePrimitives inventory required; unknown is not empty');
}
export function measuredPlacementBounds(part,layout,snapshot,region) {
  if(layout.rotationMode!=='stored'||!layout.subPartName)throw new Error('Incremental geometry requires explicit stored rotation and subPartName');
  const candidates=snapshot.components.filter(c=>c.componentType==='part'&&c.device?.uuid===part.device?.uuid&&c.device?.libraryUuid===part.device?.libraryUuid
    && c.subPartName===layout.subPartName&&c.rotation===layout.rotation&&c.mirror===layout.mirror&&finiteBox(c.bbox)&&Number.isFinite(c.x)&&Number.isFinite(c.y));
  if(!candidates.length)throw new Error('Incremental placement needs source-measured device geometry in its declared orientation');
  const bounds=candidates.map(c=>({minX:c.bbox.minX+layout.x-c.x,maxX:c.bbox.maxX+layout.x-c.x,minY:c.bbox.minY+layout.y-c.y,maxY:c.bbox.maxY+layout.y-c.y}));
  if(bounds.some(box=>!isDeepStrictEqual(box,bounds[0]))||!contains(region,[[bounds[0].minX,bounds[0].minY],[bounds[0].maxX,bounds[0].maxY]]))
    throw new Error('Measured new component bounds are ambiguous or outside the incremental region');
  return bounds[0];
}
export function validateIncrementalRegion(snapshot,region) {
  fullInventory(snapshot);
  if (!finiteBox(region)) throw new Error('Incremental prepare needs an explicit empty layoutHints.region');
  const overlaps = box => !(box.maxX<region.minX || box.minX>region.maxX || box.maxY<region.minY || box.minY>region.maxY);
  for (const part of snapshot.components) {
    if (!part.primitiveId) throw new Error('Preserved objects need native IDs');
    if (part.componentType==='sheet') continue;
    if (!finiteBox(part.bbox)) throw new Error('Preserved components need complete bounding boxes');
    if (overlaps(part.bbox)) throw new Error('Incremental region intersects existing component geometry');
    if (part.componentType==='part' && (!part.pinsAvailable || part.netlistAvailable!==true || !part.pins?.length
      || part.pins.some(pin=>!pin.pinNumber||typeof pin.net!=='string'||typeof pin.noConnected!=='boolean')))
      throw new Error('Preserved pin networks are unknown; capture complete electrical evidence');
  }
  for (const wire of snapshot.wires) {
    if (!wire.primitiveId) throw new Error('Preserved wires need native IDs');
    const points=wirePoints(wire);
    for(let i=1;i<points.length;i++) {
      const [a,b]=[points[i-1],points[i]];
      if(overlaps({minX:Math.min(a[0],b[0]),maxX:Math.max(a[0],b[0]),minY:Math.min(a[1],b[1]),maxY:Math.max(a[1],b[1])}))
        throw new Error('Incremental region intersects existing wire geometry');
    }
  }
  for(const [kind,records] of Object.entries(snapshot.pagePrimitives))if(!['components','wires'].includes(kind))for(const record of records){
    if(!record.primitiveId)throw new Error('Page primitives need native IDs');
    if(kind==='attributes'&&record.KeyVisible===false&&record.ValueVisible===false)continue;
    const box=snapshot.pagePrimitiveBounds?.[record.primitiveId]?.bbox??record.bbox;
    if(!box||!['minX','minY','maxX','maxY'].every(key=>Number.isFinite(box[key])))throw new Error('Independent page primitive bounds are unknown');
    if(overlaps(box))throw new Error('Incremental region intersects existing page primitives');
  }
}

// Compare immutable baseline objects; new module IDs are checked separately by the full target predicate.
export function preservationDiff(before,after,region,allowed={}) {
  if(region)validateIncrementalRegion(before,region);else fullInventory(before);
  fullInventory(after);
  if (!Array.isArray(after.components)||!Array.isArray(after.wires)||after.readScope?.complete!==true
    || after.readScope.concurrentChange!==false || after.readScope.primitiveIds?.length || after.readScope.missingIds?.length || after.partial===true)
    throw new Error('Preservation needs a fresh complete single-page readback');
  const componentOwners=new Map();
  for(const part of [...before.components,...after.components])for(const id of [part.primitiveId,...(part.pins??[]).map(pin=>pin.primitiveId)].filter(Boolean)){
    if(componentOwners.has(id)&&componentOwners.get(id)!==part.primitiveId)throw new Error('Ambiguous native component/pin ownership');
    componentOwners.set(id,part.primitiveId);
  }
  const attributeOwner=record=>componentOwners.get(record.ParentPrimitiveId)??record.ParentPrimitiveId;
  const changed=[];
  for(const part of before.components) {
    if(allowed.components?.includes(part.primitiveId))continue;
    const matches=after.components.filter(actual=>actual.primitiveId===part.primitiveId),actual=matches[0];
    if(matches.length!==1||!sameRecord(part,actual,fields)) {changed.push(part.primitiveId);continue;}
    if(part.pins) {
      const pins=value=>value?.pins?.map(pin=>project(pin,pinFields)).sort((a,b)=>String(a.pinNumber).localeCompare(String(b.pinNumber)));
      if(!isDeepStrictEqual(pins(part),pins(actual)))changed.push(part.primitiveId);
    }
  }
  for(const wire of before.wires) {
    if(allowed.wires?.includes(wire.primitiveId))continue;
    const matches=after.wires.filter(actual=>actual.primitiveId===wire.primitiveId&&actual.segmentIndex===wire.segmentIndex);
    if(matches.length!==1||!sameRecord(wire,matches[0],['primitiveId','segmentIndex','net','points','x0','y0','x1','y1','rawLine','rawEncoding','lineWidth','lineType']))changed.push(wire.primitiveId);
  }
  for(const [kind,records] of Object.entries(before.pagePrimitives??{}))if(Array.isArray(records))for(const record of records){
    const id=record.primitiveId;
    if(!id)throw new Error('Preserved page primitives need native IDs');
    if(kind==='components'&&allowed.components?.includes(id)||kind==='wires'&&allowed.wires?.includes(id)
      ||kind==='attributes'&&(allowed.attributes?.includes(id)||allowed.components?.includes(attributeOwner(record))))continue;
    const matches=(after.pagePrimitives?.[kind]??[]).filter(actual=>actual.primitiveId===id);
    if(matches.length!==1||!isDeepStrictEqual(record,matches[0]))changed.push(id);
  }
  const oldComponents=new Set(before.components.map(part=>part.primitiveId)),oldWires=new Set(before.wires.map(wire=>wire.primitiveId));
  for(const part of after.components)if(region&&!oldComponents.has(part.primitiveId)){
    if(!finiteBox(part.bbox)||!contains(region,[[part.bbox.minX,part.bbox.minY],[part.bbox.maxX,part.bbox.maxY]]))changed.push(part.primitiveId);
  }
  for(const wire of after.wires)if(region&&!oldWires.has(wire.primitiveId)&&!contains(region,wirePoints(wire)))changed.push(wire.primitiveId);
  if(!region){
    for(const part of after.components)if(!oldComponents.has(part.primitiveId)&&!allowed.newComponents?.includes(part.primitiveId))changed.push(part.primitiveId);
    for(const wire of after.wires)if(!oldWires.has(wire.primitiveId)&&!allowed.newWires?.includes(wire.primitiveId))changed.push(wire.primitiveId);
  }
  for(const [kind,records] of Object.entries(after.pagePrimitives))if(Array.isArray(records))for(const record of records){
    if((before.pagePrimitives[kind]??[]).some(old=>old.primitiveId===record.primitiveId))continue;
    if(kind==='components'&&(region||allowed.newComponents?.includes(record.primitiveId)))continue;
    if(kind==='wires'&&(region||allowed.newWires?.includes(record.primitiveId)))continue;
    if(kind==='attributes'){
      const owner=attributeOwner(record);
      const owned=region?after.components.some(part=>!oldComponents.has(part.primitiveId)&&part.primitiveId===owner)
        ||after.wires.some(wire=>!oldWires.has(wire.primitiveId)&&wire.primitiveId===owner):[...(allowed.components??[]),...(allowed.newComponents??[]),...(allowed.wires??[]),...(allowed.newWires??[])].includes(owner);
      if(owned){
        if(!region||record.KeyVisible===false&&record.ValueVisible===false)continue;
        const box=after.pagePrimitiveBounds?.[record.primitiveId]?.bbox;
        // Native non-drawing metadata has unknown visibility and a measured zero-area box.
        if(record.KeyVisible!==true&&(record.ValueVisible!==true||record.KeyVisible===false&&record.Value==='')&&box
          &&['minX','minY','maxX','maxY'].every(key=>Number.isFinite(box[key]))&&box.minX===box.maxX&&box.minY===box.maxY)continue;
        if(box&&contains(region,[[box.minX,box.minY],[box.maxX,box.maxY]]))continue;
      }
    }
    changed.push(record.primitiveId);
  }
  return {status:changed.length?'fail':'pass',changed:[...new Set(changed)],preservedComponents:before.components.length,preservedWires:before.wires.length};
}

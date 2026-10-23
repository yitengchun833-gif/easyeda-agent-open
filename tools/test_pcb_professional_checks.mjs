import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {decodeBoardSource,inspectProfessionalChecks} from './pcb_professional_checks.mjs';

const row=(type,id,data)=>({meta:{type,...(id===undefined?{}:{id})},data});
const spec=checks=>({version:'pcb-professional-checks/v0',unit:'mm',target:{projectUuid:'project',documentUuid:'board'},checks});
const source=records=>records.map(r=>JSON.stringify(r.meta)+'||'+JSON.stringify(r.data)+'|').join('\n');
const pin=(designator,padNumber='1')=>({designator,padNumber});
const line=(id,x1,y1,x2,y2,width=40,net='P')=>row('LINE',id,{netName:net,layerId:1,startX:x1,startY:y1,endX:x2,endY:y2,width});
function fixture(lines=[line('main',0,0,100,0)]) {
  const records=[row('DOCHEAD',undefined,{docType:'PCB',uuid:'board'}),row('LAYER','top',{layerId:1,layerType:'TOP',use:true}),row('LAYER','bottom',{layerId:2,layerType:'BOTTOM',use:true}),...lines];
  const pads=[];
  for(const [designator,x,y,net,number]of [['J1',0,0,'P','1'],['J2',100,0,'P','1'],['C1',20,0,'P','1'],['C1',20,20,'G','2']]) {
    const id=designator+'-component';
    if(!records.some(r=>r.meta.type==='COMPONENT'&&r.meta.id===id))records.push(row('COMPONENT',id,{}),row('ATTR',designator+'-attr',{parentId:id,key:'Designator',value:designator}));
    const suffix='-pad'+number;
    records.push(row('PAD_NET',JSON.stringify(['PAD_NET',id,number,suffix]),{padNet:net}));
    pads.push({primitiveId:id+suffix,padNumber:number,net,x,y,layer:1,rotation:0,pad:['RECT',10,10],metallization:true,hole:null});
  }
  return {snapshot:{source:source(records),pads},records};
}
const path=extra=>({id:'supply',type:'power_path',role:'load_supply',evidenceRef:'reviewed-design.md#supply',net:'P',from:pin('J1'),to:pin('J2'),minWidthMm:0.8,toleranceMm:0,...extra});
const role=extra=>({id:'input-pin',type:'pin_role',role:'supply_input',evidenceRef:'datasheet.pdf#pin-table',pin:pin('J1'),expectedNet:'P',...extra});
const one=(check,f=fixture())=>inspectProfessionalChecks(spec([check]),f.snapshot).checks[0];

test('native parser preserves real records and rejects missing or multiple headers',()=>{
  const f=fixture();assert.equal(decodeBoardSource(f.snapshot.source).status,'complete');
  assert.equal(decodeBoardSource('broken').status,'unknown');
  assert.equal(decodeBoardSource(source([...f.records,f.records[0]])).status,'unknown');
  assert.equal(decodeBoardSource(source([...f.records,f.records.find(r=>r.meta.type==='LINE')])).status,'unknown');
});
test('wide path converts mil to mm and does not penalize a thin dead-end branch',()=>{
  const f=fixture([line('wide',0,0,100,0),line('signal-branch',50,0,50,100,5)]);
  const out=one(path(),f);assert.equal(out.status,'pass');assert.equal(out.actual.lineBottleneckMm,1.016);
  assert.deepEqual(out.actual.pathObjectIds,['J1-component-pad1','wide','J2-component-pad1']);
});
test('mandatory narrow series section and an entirely missing path fail',()=>{
  const f=fixture([line('wide',0,0,50,0),line('narrow',50,0,100,0,10)]);
  const out=one(path(),f);assert.equal(out.status,'fail');assert.equal(out.actual.lineBottleneckMm,0.254);
  assert.equal(one(path(),fixture([])).status,'fail');
});
test('wide alternate route passes while parallel narrow paths require assessment',()=>{
  const f=fixture([line('direct-narrow',0,0,100,0,10),line('alt1',0,0,0,100),line('alt2',0,100,100,100),line('alt3',100,100,100,0)]);
  assert.equal(one(path(),f).status,'pass');
  for(const r of f.records.filter(r=>r.meta.type==='LINE'))r.data.width=10;
  f.snapshot.source=source(f.records);
  const out=one(path(),f);assert.equal(out.status,'unknown');assert.equal(out.actual.parallelLinePath,true);
});
test('same-Net pour boundary, fill, arc or via cannot be ignored when lines are insufficient',()=>{
  for(const type of ['POUR','FILL','ARC','VIA']) {
    const f=fixture([line('narrow',0,0,100,0,10)]);f.records.push(row(type,'possible-copper',{netName:'P'}));f.snapshot.source=source(f.records);
    const out=one(path(),f);assert.equal(out.status,'unknown',type);assert.deepEqual(out.actual.unresolvedObjectIds,['possible-copper']);
  }
  const f=fixture([line('narrow',0,0,100,0,10)]);f.records.push(row('POUR','other-net',{netName:'G'}));f.snapshot.source=source(f.records);
  assert.equal(one(path(),f).status,'fail');
});
test('crossing line centers form a same-layer path; other layers do not',()=>{
  const f=fixture([line('horizontal',0,0,150,0),line('vertical',100,-10,100,10)]);
  assert.equal(one(path(),f).status,'pass');
  f.records.find(r=>r.meta.id==='horizontal').data.layerId=2;f.snapshot.source=source(f.records);
  assert.equal(one(path(),f).status,'fail');
});
test('unmodeled physical overlap remains unknown rather than a false disconnection',()=>{
  const f=fixture([line('first',0,0,45,0),line('second',55,0,100,0)]);
  assert.equal(one(path(),f).status,'unknown');
});
test('missing same-Net pads, ambiguous selectors and mismatched source observations stay unknown',()=>{
  const f=fixture();f.snapshot.pads=f.snapshot.pads.filter(p=>p.primitiveId!=='C1-component-pad1');assert.equal(one(path(),f).status,'unknown');
  assert.equal(one(role({pin:pin('MISSING')})).status,'unknown');
  const g=fixture();g.snapshot.pads[0].net='different';assert.equal(one(role(),g).status,'unknown');
});
test('pin role assignment uses an explicit designator/pad and expected Net',()=>{
  assert.equal(one(role()).status,'pass');
  assert.equal(one(role({expectedNet:'G'})).status,'fail');
  assert.equal(one(path({net:'G'})).status,'fail');
});
test('decoupling compares explicit power/return Nets and declared pad-center distance',()=>{
  const d={powerPin:pin('C1'),returnPin:pin('C1','2'),expectedReturnNet:'G',metric:'pad_center_euclidean',maxDistanceMm:0.6};
  const out=one(role({decoupling:d}));assert.equal(out.status,'pass');assert.equal(out.actual.decoupling.distanceMm,0.508);
  assert.equal(one(role({decoupling:{...d,maxDistanceMm:0.4}})).status,'fail');
  assert.equal(one(role({decoupling:{...d,expectedReturnNet:'P'}})).status,'fail');
  assert.equal(one(role({decoupling:{...d,metric:'route_length'}})).status,'unknown');
});
test('missing basis or thresholds, document mismatch, duplicate ids and unsupported types are unknown',()=>{
  assert.equal(one(path({minWidthMm:undefined})).status,'unknown');
  assert.equal(one(path({to:pin('J1')})).status,'unknown');
  assert.equal(one(role({evidenceRef:''})).status,'unknown');
  const s=spec([role()]);s.target.documentUuid='different';assert.equal(inspectProfessionalChecks(s,fixture().snapshot).status,'unknown');
  assert.equal(inspectProfessionalChecks(spec([role(),role()]),fixture().snapshot).status,'unknown');
  assert.equal(one(role({type:'thermal_rating'})).status,'unknown');
});


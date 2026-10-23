import {test} from 'node:test';
import assert from 'node:assert/strict';
import {inspectCopperAttachment} from './pcb_copper_attachment.mjs';

function fixture() {
  const component={x:10,y:20,angle:0,layerId:1,footprintUuid:'fp'};
  const objects=[{type:'FILL',id:'copper',data:{layerId:1,fillStyle:'SOLID',path:[[1,2,'L',3,4]],netName:'P'}},
    {type:'POLY',id:'mask',data:{layerId:5,path:[1,2,'L',3,4],width:10}}];
  return {check:{id:'attachment',type:'copper_attachment',componentId:'c',baseline:{component,objects}},
    decoded:{status:'complete',records:[{meta:{type:'COMPONENT',id:'c'},data:{x:10,y:20,angle:0,layerId:1}},
      {meta:{type:'ATTR',id:'fpattr'},data:{parentId:'c',key:'Footprint',value:'fp'}},
      {meta:{type:'LAYER',id:'5'},data:{layerId:5,layerType:'TOP_SOLDER_MASK'}},
      {meta:{type:'LAYER',id:'1'},data:{layerId:1,layerType:'TOP'}},
      ...objects.map(o=>({meta:{type:o.type,id:o.id},data:structuredClone(o.data)}))]}};
}
test('explicit copper/mask dependency is reusable only while its exact observed inputs match',()=>{
  const f=fixture();assert.equal(inspectCopperAttachment(f.check,{},f.decoded).status,'pass');
  f.decoded.records[0].data.x+=1;
  assert.equal(inspectCopperAttachment(f.check,{},f.decoded).status,'unknown');
  f.decoded.records[0].data.x-=1;f.decoded.records.at(-1).data.width+=1;
  assert.equal(inspectCopperAttachment(f.check,{},f.decoded).status,'unknown');
});
test('missing or wrong layer objects and unsupported coverage cannot pass',()=>{
  for(const mutate of [f=>f.decoded.records.pop(),f=>f.decoded.records[2].data.layerType='TOP_PASTE_MASK',
    f=>f.decoded.records[3].data.layerType='TOP_PASTE_MASK',
    f=>f.decoded.status='unknown',f=>f.check.baseline.objects.pop(),f=>f.decoded.records[1].data.value='other']) {
    const f=fixture();mutate(f);assert.equal(inspectCopperAttachment(f.check,{},f.decoded).status,'unknown');
  }
});

import test from 'node:test';
import assert from 'node:assert/strict';
import {reopenProof} from '../src/delivery-verification.mjs';
import {preservationDiff} from '../src/preservation.mjs';

function proof(){
  const target={projectUuid:'p',documentUuid:'d'},window='w';
  const row=(requestId,action,second)=>({requestId,action,windowId:window,...target,status:'action_finished',startedAt:`2026-10-19T00:00:0${second}.000Z`,durationMs:10,generation:4});
  const state={instance:'daemon',documents:[{windowId:window,context:target,generation:4,mutationAttemptedSinceConfirmedSave:false}],
    receipts:[row('save','schematic.save',1),row('close','document.close',2),row('open','document.open',3),row('read','schematic.components.list',4)]};
  Object.assign(state.receipts[1],{documentUuid:'another-tab',startedContext:target,closedDocumentUuid:'d'});
  const delivery={receipt:{saved:true,finishReceipt:{structuredContent:{ok:true,id:'save',createdAt:'2026-10-19T00:00:01.050Z',context:target,result:{saved:true}}}}};
  return {target,window,state,delivery,readback:{structuredContent:{id:'read'}},closeRequestId:'close',openRequestId:'open'};
}

test('persistence requires actual ordered current native save/close/open/read receipts',()=>{
  assert.equal(reopenProof(proof()).status,'verified');
  for(const change of [p=>p.delivery.receipt.saved=null,p=>p.state.receipts[1].historical=true,p=>p.state.receipts[2].action='schematic.components.list',
    p=>p.state.receipts[2].documentUuid='other',p=>p.state.receipts[2].startedAt='2026-10-19T00:00:01.000Z',
    p=>p.state.documents[0].generation++,p=>p.state.documents[0].mutationAttemptedSinceConfirmedSave=true,
    p=>delete p.state.receipts[1].closedDocumentUuid,
    p=>p.state.receipts.push({windowId:'w',contentWrite:true,status:'running'}),
    p=>p.state.receipts.push({...p.state.receipts[3],requestId:'later-write',contentWrite:true,status:'action_finished'})]){
    const p=proof();change(p);assert.equal(reopenProof(p).status,'unknown');
  }
});

test('scoped edits cannot hide new independent graphics outside their actual declared creations',()=>{
  const before={components:[],wires:[],wiresAvailable:true,readScope:{complete:true,concurrentChange:false,missingIds:[]},
    pagePrimitives:Object.fromEntries(['components','wires','buses','arcs','circles','rectangles','polygons','texts','attributes','objects'].map(key=>[key,[]]))};
  const after=structuredClone(before);after.pagePrimitives.texts.push({primitiveId:'unapproved',Content:'new text'});
  assert.equal(preservationDiff(before,after,null,{}).status,'fail');
});

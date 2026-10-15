import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os'; import path from 'node:path';
import { KnowledgeStore } from '../src/store.mjs';
import {keywordScore,isEvidence} from '../src/search-index.mjs';

test('strict model identity does not mix RS2251 with RS22510 and pending remains hidden', async () => {
  const root=await mkdtemp(path.join(os.tmpdir(),'easyeda-kb-')); const store=new KnowledgeStore(root);
  const one=store.saveSource({name:'rs2251.md',sourceHash:'a',contentPath:'a',metadata:{models:['RS2251'],pins:['VCC']}});store.replaceChunks(one,[{anchor:'#/1',text:'RS2251 模拟开关 VCC',contentHash:'1',metadata:{}}]);
  const two=store.saveSource({name:'rs22510.md',sourceHash:'b',contentPath:'b',metadata:{models:['RS22510'],pins:['VCCIO']}});store.replaceChunks(two,[{anchor:'#/1',text:'RS22510 开关 VCCIO',contentHash:'2',metadata:{}}]);
  const pending=store.saveSource({name:'candidate.md',sourceHash:'c',contentPath:'c',status:'pending',metadata:{models:['RS2251']}});store.replaceChunks(pending,[{anchor:'#/1',text:'pending RS2251',contentHash:'3',metadata:{}}]);
  const result=store.search('RS2251'); assert.deepEqual(result.map(x=>x.sourceId),[one.sourceId]); assert.equal(store.propose({problem:'x'}).status,'pending');store.close();await rm(root,{recursive:true,force:true});
});

test('model identity alone cannot turn a heading or unrelated fragment into evidence',async()=>{
  assert.equal(keywordScore('RON VCC','<table><tr><td>R ON</td><td>V CC</td></tr></table>'),1);
  assert.equal(keywordScore('table','<table><tr><td>ENABLE</td></tr></table>'),0);
  assert.equal(keywordScore('VINPUT','<td>V</td><td>INPUT</td>'),0);
  assert.equal(isEvidence('PIN CONFIGURATIONS',{label:'section_header'},'ENABLE pin truth table'),false);
  assert.equal(isEvidence('Vcc',{label:'text'},'RON conditions VCC RL CL'),false);
  const root=await mkdtemp(path.join(os.tmpdir(),'kb-relevance-')),store=new KnowledgeStore(root);
  try{
    const source=store.publish({name:'RS2251.pdf',sourceHash:'fixture',contentPath:'fixture',metadata:{models:['RS2251']}},[
      {anchor:'#/heading',text:'PIN CONFIGURATIONS',contentHash:'1',metadata:{label:'section_header'}},
      {anchor:'#/supply',text:'Vcc',contentHash:'2',metadata:{label:'text'}},
      {anchor:'#/description',text:'Single Supply Operation 2.5V',contentHash:'3',metadata:{}},
      {anchor:'#/table',text:'<table><tr><td>R ON</td><td>5V</td><td>+25°C</td></tr></table>',contentHash:'4',metadata:{label:'table'}},
    ]);
    const hits=store.search('RS2251 RON conditions VCC RL CL');
    assert.deepEqual(hits.map(hit=>hit.anchor),['#/table']);assert.equal(hits[0].sourceId,source.sourceId);
  }finally{store.close();await rm(root,{recursive:true,force:true});}
});

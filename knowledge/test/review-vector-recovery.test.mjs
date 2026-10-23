import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {KnowledgeStore} from '../src/store.mjs';
import {chunkText} from '../src/ingest.mjs';

test('committed lesson index resumes with the installed local model and persists without duplicate vectors',{skip:!process.env.EASYEDA_KNOWLEDGE_TEST_MODELS},async()=>{
  const root=await mkdtemp(path.join(tmpdir(),'kb-review-vector-'));let service,store;
  try{
    store=new KnowledgeStore(root);
    const text='# Recovery fixture\n\n## 可复用结论\n\nSynthetic recovery evidence only.\n',file=path.join(root,'lesson.md');await writeFile(file,text);
    const source=store.publish({name:'lesson.md',type:'lesson',contentPath:file,sourceHash:createHash('sha256').update(text).digest('hex'),metadata:{validation:{measurement:'unknown'}}},chunkText(text));
    const jobId=store.job('index-lesson','running',{sourceId:source.sourceId,sourceRevision:source.revision});store.close();store=null;
    process.env.EASYEDA_KNOWLEDGE_ROOT=root;process.env.EASYEDA_KNOWLEDGE_PORT='60947';process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR='1';process.env.EASYEDA_KNOWLEDGE_LOCAL_MODEL_PATH=process.env.EASYEDA_KNOWLEDGE_TEST_MODELS;
    const {startKnowledgeService}=await import('../src/service.mjs');service=await startKnowledgeService();assert.equal(service.vector.ready,true);await service.drain();
    assert.equal(service.store.getJob(jobId).status,'completed');assert.equal(service.store.get(source.sourceId).indexStatus,'ready');
    const hits=await service.vector.search('Synthetic recovery evidence',[source.sourceId]);assert(hits.length);assert(hits.every(item=>item.revision===source.revision));
    const {connect}=await import('@lancedb/lancedb');const db=await connect(path.join(root,'index','lancedb'));const table=await db.openTable('vectors_'+service.vector.signature.slice(0,16));
    const count=service.store.chunks(source.sourceId).length;assert.equal(await table.countRows(),count);
    service.store.indexed(source.sourceId,'indexing',undefined,source.revision);service.store.updateJob(jobId,'running',{sourceId:source.sourceId,sourceRevision:source.revision});
    await new Promise(r=>service.server.close(r));service.store.close();service=null;
    service=await startKnowledgeService();await service.drain();assert.equal(service.store.getJob(jobId).status,'completed');assert.equal(service.store.jobs().length,1);
    const refreshed=await db.openTable('vectors_'+service.vector.signature.slice(0,16));assert.equal(await refreshed.countRows(),count);assert.equal(service.store.get(source.sourceId).metadata.validation.measurement,'unknown');
  }finally{if(service){await new Promise(r=>service.server.close(r));await service.drain();service.store.close();}store?.close();await rm(root,{recursive:true,force:true});}
});

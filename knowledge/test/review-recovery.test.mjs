import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,rm,writeFile,readdir} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {KnowledgeStore} from '../src/store.mjs';
import {chunkText} from '../src/ingest.mjs';

const payload={problem:'review recovery fixture',cause:'test only',solution:'fixture conclusion',conditions:'isolated fixture',evidence:'synthetic evidence',validation:{measurement:'unknown',engineerReview:'unknown'}};
const sha=text=>createHash('sha256').update(text).digest('hex');
async function addSource(store,root){
  const text='# Fixture\n\nSynthetic source for review tests.\n',file=path.join(root,'fixture.md');await writeFile(file,text);
  return store.publish({name:'fixture.md',contentPath:file,sourceHash:sha(text),metadata:{version:'v1'}},chunkText(text));
}

test('source binding detects content, conditions, disabled/deleted and unpinned legacy sources',async()=>{
  const root=await mkdtemp(path.join(tmpdir(),'kb-review-source-'));const store=new KnowledgeStore(root);
  try{
    let source=await addSource(store,root);
    const body={...payload,sourceIds:[source.sourceId],sourceVersionSet:[{sourceId:source.sourceId,sourceRevision:source.revision,sourceHash:source.sourceHash}]};
    const candidate=store.propose(body);assert.equal(candidate.sourceVersions[0].basis,'read-version');store.checkCandidateSources(candidate);
    store.db.prepare("UPDATE sources SET parse_status='partial' WHERE source_id=?").run(source.sourceId);store.checkCandidateSources(candidate);
    store.db.prepare("UPDATE sources SET parse_status='success' WHERE source_id=?").run(source.sourceId);
    store.indexed(source.sourceId,'ready','new-vector-signature');store.checkCandidateSources(candidate);
    store.patchSource(source.sourceId,{version:'v2'});assert.throws(()=>store.checkCandidateSources(candidate),{code:'REVIEW_SOURCES_STALE'});
    store.patchSource(source.sourceId,{version:'v1',enabled:false});assert.throws(()=>store.checkCandidateSources(candidate),{code:'SOURCE_UNAVAILABLE'});
    store.patchSource(source.sourceId,{enabled:true});store.checkCandidateSources(candidate);
    store.db.prepare('UPDATE candidates SET source_versions_json=NULL WHERE candidate_id=?').run(candidate.candidateId);
    assert.throws(()=>store.checkCandidateSources(store.candidate(candidate.candidateId)),{code:'REVIEW_SOURCES_UNPINNED'});
    source=store.saveSource({...source,sourceId:source.sourceId,sourceHash:sha('updated'),metadata:{version:'v2'}});
    assert.throws(()=>store.propose(body),{code:'SOURCE_VERSION_STALE'});
    assert.throws(()=>store.propose({...body,sourceVersionSet:[]}),{code:'SOURCE_VERSION_INVALID'});
    store.deleteSource(source.sourceId);assert.throws(()=>store.propose({...payload,sourceIds:[source.sourceId]}),{code:'SOURCE_UNAVAILABLE'});
  }finally{store.close();await rm(root,{recursive:true,force:true});}
});

test('HTTP review commits receipt/publication/job atomically, retries and restart recovery',async()=>{
  const root=await mkdtemp(path.join(tmpdir(),'kb-review-recovery-'));
  process.env.EASYEDA_KNOWLEDGE_ROOT=root;process.env.EASYEDA_KNOWLEDGE_PORT='60946';process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR='0';
  const {startKnowledgeService}=await import('../src/service.mjs');let service=await startKnowledgeService();
  const call=async(route,body)=>{const r=await fetch(service.baseUrl+route,{method:body?'POST':'GET',headers:{'X-Knowledge-Token':service.token,Origin:service.baseUrl,'content-type':'application/json'},body:body&&JSON.stringify(body)});return {status:r.status,data:await r.json()};};
  try{
    const source=await addSource(service.store,root);
    const pinned={...payload,sourceIds:[source.sourceId],sourceVersionSet:[{sourceId:source.sourceId,sourceRevision:source.revision,sourceHash:source.sourceHash}]};
    assert.equal((await call('/api/candidates',{...pinned,sourceVersionSet:[{sourceId:source.sourceId,sourceRevision:source.revision,sourceHash:'bad'}]})).status,400);
    const c=(await call('/api/candidates',pinned)).data.candidate,route='/api/candidates/'+c.candidateId+'/review',input={decision:'approved',expectedCandidateRevision:1};
    service.store.patchSource(source.sourceId,{version:'v2'});
    assert.equal((await call(route,input)).data.code,'REVIEW_SOURCES_STALE');assert.equal(service.store.candidate(c.candidateId).status,'pending');
    service.store.patchSource(source.sourceId,{version:'v1'});
    service.store.db.exec("CREATE TRIGGER receipt_failure BEFORE INSERT ON candidate_reviews BEGIN SELECT RAISE(ABORT,'fixture receipt failure'); END");
    assert.equal((await call(route,input)).status,400);
    assert.equal(service.store.candidate(c.candidateId).status,'pending');assert.equal(service.store.list({type:'lesson'}).length,0);assert.equal(service.store.jobs().length,0);
    assert.deepEqual(await readdir(path.join(root,'lessons')),[]);service.store.db.exec('DROP TRIGGER receipt_failure');
    const concurrent=await Promise.all([call(route,input),call(route,input)]);
    assert.deepEqual(concurrent.map(x=>x.status),[200,200]);
    const receipt=concurrent[0].data;assert.equal(receipt.sourceId,concurrent[1].data.sourceId);assert.equal(receipt.jobId,concurrent[1].data.jobId);
    assert.equal(service.store.list({type:'lesson'}).length,1);assert.equal((await readdir(path.join(root,'lessons'))).length,1);assert.equal(service.store.jobs().length,1);
    assert.equal((await call(route,{...input,decision:'rejected'})).data.code,'REVIEW_CONFLICT');
    assert.equal((await call(route,input)).data.replayed,true);
    await service.drain();assert.equal(service.store.getJob(receipt.jobId).status,'deferred');
    const detail=service.store.getJob(receipt.jobId).detail;assert.equal(detail.sourceRevision,service.store.get(receipt.sourceId).revision);
    // Seed all durable recovery states before a real service/database restart.
    const jobs=['queued','running','interrupted','failed'].map(status=>service.store.job('index-lesson',status,detail));
    const bad=service.store.job('index-lesson','queued',{...detail,sourceRevision:'stale'});
    const unrelated=service.store.job('import','queued',{sourceId:source.sourceId});
    await new Promise(r=>service.server.close(r));service.store.close();service=await startKnowledgeService();await service.drain();
    assert.equal((await call(route,input)).data.jobId,receipt.jobId);
    for(const id of [...jobs,receipt.jobId])assert.equal(service.store.getJob(id).status,'deferred');
    assert.equal(service.store.getJob(bad).status,'failed');assert.equal(service.store.getJob(unrelated).status,'interrupted');
    const results=(await call('/api/search',{query:'fixture conclusion',filters:{type:'lesson'}})).data.items;assert(results.length);
    const read=(await call('/api/get?'+new URLSearchParams({evidenceRef:JSON.stringify(results[0].evidenceRef),scope:'source'}))).data;
    assert.equal(read.readMode,'pinned');assert.equal(read.context.verification.measurement,'unknown');assert(read.text.includes('sourceRevision'));
    assert.equal(service.store.list({type:'lesson'}).length,1);
    const rejected=(await call('/api/candidates',payload)).data.candidate,rejectRoute='/api/candidates/'+rejected.candidateId+'/review';
    const rejection={decision:'rejected',expectedCandidateRevision:1};assert.equal((await call(rejectRoute,rejection)).status,200);assert.equal((await call(rejectRoute,rejection)).data.replayed,true);
  }finally{await new Promise(r=>service.server.close(r));await service.drain();service.store.close();await rm(root,{recursive:true,force:true});}
});

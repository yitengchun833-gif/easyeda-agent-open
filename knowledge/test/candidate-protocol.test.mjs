import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, readdir, readFile } from 'node:fs/promises';
import { runInNewContext } from 'node:vm';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import { KnowledgeStore } from '../src/store.mjs';

const payload={problem:'fixture-only candidate protocol',cause:'test',solution:'fixture conclusion',conditions:'isolated test only',evidence:'fixture, not hardware evidence',validation:{connectivity:'unknown',measurement:'unknown',engineerReview:'unknown'},tags:['test','fixture']};

test('embedded UI uses the displayed candidate revision and never auto-approves after refresh',async()=>{
  const script=await readFile(new URL('../ui/app.js',import.meta.url),'utf8');
  const embedded=await readFile(new URL('../../extension/iframe/knowledge/index.html',import.meta.url),'utf8');
  assert(embedded.includes(script.replace(/<\/script/gi,'<\\/script')),'extension HTML must contain the current UI script');
  const start=script.indexOf('  async function review(candidate,');
  const end=script.indexOf('  async function toBase64',start);
  assert(start>=0&&end>start);
  const calls=[],buttons=[{disabled:false}];let refreshes=0,stale=false;
  const review=runInNewContext('('+script.slice(start,end)+')',{
    api:async(route,input)=>{calls.push({route,body:JSON.parse(input.body)});if(stale)throw Object.assign(new Error('changed'),{code:'REVIEW_STALE'});},
    refresh:async()=>{refreshes++;},toast:()=>{},errorMessage:error=>error.message,
  });
  const actions={querySelectorAll:()=>buttons};
  await review({candidateId:'fixture',revision:4},'approved',actions);
  assert.deepEqual(calls[0].body,{decision:'approved',expectedCandidateRevision:4});assert.equal(refreshes,1);
  stale=true;await review({candidateId:'fixture',revision:4},'approved',actions);
  assert.equal(calls.length,2);assert.equal(refreshes,2);assert.equal(buttons[0].disabled,false);
});

test('additive migration preserves legacy candidates and can reopen',async()=>{
  const root=await mkdtemp(path.join(tmpdir(),'kb-candidate-migrate-'));
  let store;
  try {
    const db=new DatabaseSync(path.join(root,'catalog.sqlite'));
    db.exec('CREATE TABLE candidates(candidate_id TEXT PRIMARY KEY,payload_json TEXT NOT NULL,status TEXT NOT NULL,review_json TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL)');
    db.prepare('INSERT INTO candidates VALUES(?,?,?,?,?,?)').run('legacy',JSON.stringify(payload),'approved',JSON.stringify({reviewer:'human'}),'old','old');db.close();
    store=new KnowledgeStore(root);assert.equal(store.candidate('legacy').revision,1);assert.equal(store.candidate('legacy').status,'approved');assert.deepEqual(store.candidate('legacy').payload,payload);
    store.close();store=new KnowledgeStore(root);assert.equal(store.candidate('legacy').review.reviewer,'human');assert.equal(store.propose(payload).revision,1);
  }finally{store?.close();await rm(root,{recursive:true,force:true});}
});

test('submission receipts are atomic, canonical, durable and do not replay old payloads',async()=>{
  const root=await mkdtemp(path.join(tmpdir(),'kb-candidate-cas-'));let store;
  try {
    store=new KnowledgeStore(root);
    const first=store.submitCandidate(payload,{submissionId:'create',expectedCandidateRevision:0});
    const reordered=Object.fromEntries(Object.entries(payload).reverse());reordered.validation=Object.fromEntries(Object.entries(payload.validation).reverse());
    assert.equal(store.submitCandidate(reordered,{submissionId:'create',expectedCandidateRevision:0}).candidate.candidateId,first.candidate.candidateId);
    assert.throws(()=>store.submitCandidate({...payload,tags:['fixture','test']},{submissionId:'create',expectedCandidateRevision:0}),{code:'IDEMPOTENCY_CONFLICT'});
    const candidateId=first.candidate.candidateId;
    const update={submissionId:'edit',candidateId,expectedCandidateRevision:1};
    assert.equal(store.submitCandidate({...payload,solution:'revised'},update).candidate.revision,2);
    assert.throws(()=>store.submitCandidate(payload,{...update,submissionId:'stale'}),error=>error.code==='CANDIDATE_STALE'&&error.currentCandidate.revision===2);
    const replay=store.submitCandidate(payload,{submissionId:'create',expectedCandidateRevision:0});
    assert.equal(replay.candidate.revision,1);assert.equal(replay.currentRevision,2);assert.equal(store.candidate(candidateId).payload.solution,'revised');
    store.db.exec("CREATE TRIGGER fail_receipt BEFORE INSERT ON candidate_submissions BEGIN SELECT RAISE(ABORT,'receipt failure'); END");
    assert.throws(()=>store.submitCandidate({...payload,solution:'lost'},{...update,submissionId:'rollback',expectedCandidateRevision:2}),/receipt failure/);
    assert.equal(store.candidate(candidateId).revision,2);store.db.exec('DROP TRIGGER fail_receipt');
    store.close();store=new KnowledgeStore(root);
    assert.equal(store.submitCandidate({...payload,solution:'revised'},update).replayed,true);
    assert.equal(store.candidates().length,1);
    assert.throws(()=>store.reviewCandidate(candidateId,'approved','human',1),{code:'REVIEW_STALE'});
    assert.equal(store.reviewCandidate(candidateId,'approved','human',2).revision,3);
    assert.throws(()=>store.submitCandidate(payload,{...update,submissionId:'formal',expectedCandidateRevision:3}),{code:'CANDIDATE_NOT_PENDING'});
    assert.throws(()=>store.submitCandidate(payload,{submissionId:'missing-version'}),{code:'CANDIDATE_REQUEST_INVALID'});
  }finally{store?.close();await rm(root,{recursive:true,force:true});}
});

test('MCP to HTTP candidate revise, stale recovery, human publication and evidence readback',async()=>{
  const root=await mkdtemp(path.join(tmpdir(),'kb-candidate-api-'));
  process.env.EASYEDA_KNOWLEDGE_ROOT=root;process.env.EASYEDA_KNOWLEDGE_PORT='60945';process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR='0';
  const {startKnowledgeService}=await import('../src/service.mjs');const service=await startKnowledgeService();
  const {knowledgeTool,validateKnowledgeProposal}=await import('../../mcp/src/knowledge-tools.mjs');
  const call=async(route,body)=>{const response=await fetch(service.baseUrl+route,{method:body?'POST':'GET',headers:{'X-Knowledge-Token':service.token,Origin:service.baseUrl,'content-type':'application/json'},body:body&&JSON.stringify(body)});return {status:response.status,data:await response.json()};};
  const proposal={...payload,submissionId:'mcp-create',expectedCandidateRevision:0};
  try {
    assert.throws(()=>validateKnowledgeProposal({...proposal,candidateId:'x',expectedCandidateRevision:0}),/Invalid/);
    const created=await knowledgeTool('knowledge_propose',proposal);assert.equal(created.isError,false);
    const first=created.structuredContent?.candidate||JSON.parse(created.content[0].text).candidate;
    assert(first?.candidateId);
    const retry=await knowledgeTool('knowledge_propose',proposal);assert.equal(retry.structuredContent.replayed,true);
    const edit={...proposal,submissionId:'mcp-edit',candidateId:first.candidateId,expectedCandidateRevision:1,solution:'updated fixture conclusion'};
    assert.equal((await knowledgeTool('knowledge_propose',edit)).structuredContent.candidate.revision,2);
    const stale=await knowledgeTool('knowledge_propose',{...edit,submissionId:'mcp-stale',solution:'must not overwrite'});
    assert.equal(stale.isError,true);const error=JSON.parse(stale.content[0].text);assert.equal(error.code,'CANDIDATE_STALE');assert.equal(error.currentCandidate.revision,2);
    const races=await Promise.all(['a','b'].map(suffix=>call('/api/candidates',{...edit,submissionId:'race-'+suffix,expectedCandidateRevision:2,solution:'race '+suffix})));
    assert.deepEqual(races.map(result=>result.status).sort(),[200,409]);
    assert.equal(service.store.candidate(first.candidateId).revision,3);
    const corrected=await call('/api/candidates',{...edit,submissionId:'restore-fixture',expectedCandidateRevision:3});assert.equal(corrected.data.candidate.revision,4);
    const route='/api/candidates/'+first.candidateId+'/review';
    assert.equal((await call(route,{decision:'approved'})).data.code,'REVIEW_VERSION_REQUIRED');
    assert.equal((await call(route,{decision:'approved',expectedCandidateRevision:1})).data.code,'REVIEW_STALE');
    assert.equal(service.store.list().length,0);
    const approved=await call(route,{decision:'approved',expectedCandidateRevision:4});assert.equal(approved.status,200);
    assert.equal(approved.data.candidate.review.candidateRevision,4);
    const replay=await call(route,{decision:'approved',expectedCandidateRevision:4});assert.equal(replay.status,200);assert.equal(replay.data.replayed,true);assert.equal(replay.data.sourceId,approved.data.sourceId);assert.equal(replay.data.jobId,approved.data.jobId);
    assert.equal(service.store.list().length,1);assert.equal((await readdir(path.join(root,'lessons'))).length,1);
    const results=(await call('/api/search',{query:'updated fixture',filters:{type:'lesson'}})).data;
    assert(results.items.length);
    const ref=results.items[0].context.evidenceRef;
    const read=(await call('/api/get?'+new URLSearchParams({evidenceRef:JSON.stringify(ref)}))).data;
    assert(read.text.includes('updated fixture conclusion'));assert.equal(read.context.verification.measurement,'unknown');
    assert.equal(read.context.content.readScope,'source');
    const job=service.store.getJob(approved.data.jobId);assert(['queued','running','completed','deferred'].includes(job.status));
  }finally{await new Promise(resolve=>service.server.close(resolve));await service.drain();service.store.close();await rm(root,{recursive:true,force:true});}
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { resolve } from 'node:path';
import { guardedSchematicSave } from '../src/save.mjs';

const input = { project: 'p', doc: 'd', payload: { verificationReport: resolve('review.json') } };
const fingerprint = 'a'.repeat(64);
const snapshot = { ok: true, result: { ok: true, result: { saveGuardVersion: 1, pageStateSha256: fingerprint } } };
const proof = { passed: true, phase: 'before_save', project: 'p', doc: 'd', pageStateSha256: fingerprint };

test('missing report blocks before reading or saving', async () => {
  await assert.rejects(guardedSchematicSave({ project:'p', doc:'d' }, { run:()=>{throw Error('must not run');} }),/verificationReport/);
});

for (const mode of ['valid','old_connector','failed_read','stale_report','wrong_target','cancelled','unknown_save']) test(`actual save orchestration: ${mode}`, async () => {
  const calls=[];
  const controller=new AbortController();
  const result = guardedSchematicSave(input, { signal:controller.signal, runtimeCheck:async()=>({mode:mode==='valid'?'layout':'design',measureTextIds:['reviewed-label']}),
    run:async args=>{
      calls.push(args);
      if(calls.length===1) {
        assert.equal(JSON.parse(args[args.indexOf('--payload')+1]).includeDeviceIdentity,mode!=='valid');
        assert.deepEqual(JSON.parse(args[args.indexOf('--payload')+1]).measureTextIds,['reviewed-label']);
        if(mode==='failed_read')return {ok:false};
        const reply=structuredClone(snapshot);
        if(mode==='old_connector')delete reply.result.result.saveGuardVersion;
        return reply;
      }
      return mode==='unknown_save'?{ok:false,error:{code:'TIMEOUT'}}:{ok:true,result:{ok:true,result:{saved:true}}};
    },
    verify:async()=>{
      if(mode==='stale_report')throw Error('page changed');
      if(mode==='cancelled')controller.abort();
      return {...proof,...(mode==='wrong_target'?{doc:'other'}:{})};
    }
  });
  if(['valid','unknown_save'].includes(mode)) {
    const reply=await result;assert.equal(calls.length,2);
    const payload=JSON.parse(calls[1][calls[1].indexOf('--payload')+1]);
    assert.deepEqual(payload,{expectedPageStateSha256:fingerprint,expectedProjectUuid:'p',expectedDocumentUuid:'d'});
    if(mode==='unknown_save')assert.equal(reply.ok,false); // no automatic retry
  } else {await assert.rejects(result);assert.equal(calls.length,1);}
});

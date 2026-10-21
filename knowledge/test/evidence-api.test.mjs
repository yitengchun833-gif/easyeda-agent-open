import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rename, rm, unlink, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { AjvJsonSchemaValidator } from '../../mcp/node_modules/@modelcontextprotocol/sdk/dist/esm/validation/ajv-provider.js';
import { knowledgeSearchOutputSchema, knowledgeGetOutputSchema } from '../src/context-contract.mjs';

test('HTTP evidence versions, cursor binding, compatibility and explicit failures', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'kb-evidence-api-'));
  const probe = createServer();
  await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  process.env.EASYEDA_KNOWLEDGE_ROOT = root;
  process.env.EASYEDA_KNOWLEDGE_PORT = String(port);
  process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR = '0';
  const { startKnowledgeService } = await import('../src/service.mjs');
  let service = await startKnowledgeService();
  const validator = new AjvJsonSchemaValidator();
  const schemas = {
    search: validator.getValidator(knowledgeSearchOutputSchema),
    get: validator.getValidator(knowledgeGetOutputSchema)
  };
  const call = async (route, method = 'GET', body) => {
    const response = await fetch(service.baseUrl + route, { method,
      headers: { 'X-Knowledge-Token': service.token, 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body) });
    return { status: response.status, data: await response.json() };
  };
  const valid = (name, value) => { const checked = schemas[name](value); assert.equal(checked.valid, true, checked.errorMessage); };
  const wait = async jobId => {
    for (let i = 0; i < 100; i++) {
      const job = (await call('/api/jobs')).data.items.find(item => item.jobId === jobId);
      if (job.status === 'completed') return job.detail;
      if (job.status === 'failed') throw Error(JSON.stringify(job.detail));
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    throw Error('Import timed out');
  };
  const importText = async (text, sourceId) => wait((await call('/api/sources', 'POST', {
    name: 'fixture.md', sourceId, contentBase64: Buffer.from(text).toString('base64'),
    metadata: { models: ['PX1000'], type: 'reference', project: 'old-project', version: 'RevA', domain: 'both' }
  })).data.jobId);
  const get = (evidenceRef, options = {}) => call('/api/get?' + new URLSearchParams({
    evidenceRef: JSON.stringify(evidenceRef), ...options }));
  const search = async () => {
    const result = await call('/api/search', 'POST', { query: 'PX1000 evidence', filters: { model: 'PX1000' } });
    assert.equal(result.status, 200); valid('search', result.data); return result.data;
  };
  try {
    const { sourceId } = await importText('# Original\nPX1000 original evidence ' + 'A'.repeat(900) + '\n# Tail\nPX1000 original tail evidence.');
    const firstSearch = await search(), ref = firstSearch.items[0].context.evidenceRef;
    const oldRead = await get(ref, { scope: 'source', maxChars: '30000' });
    assert.equal(oldRead.status, 200); valid('get', oldRead.data);
    assert(oldRead.data.text.includes('original tail'));
    assert.equal(oldRead.data.readMode, 'pinned');
    const firstPage = await get(ref, { scope: 'source', maxChars: '200' });
    assert(firstPage.data.nextCursor && firstPage.data.nextCursor.length <= 500);
    valid('get', firstPage.data);
    const mismatch = await get(ref, { scope: 'source', maxChars: '201', cursor: firstPage.data.nextCursor });
    assert.equal(mismatch.data.code, 'CURSOR_MISMATCH');
    assert.equal((await get(ref, { cursor: '3' })).data.code, 'CURSOR_MISMATCH');
    assert.equal((await get(ref, { id: sourceId })).status, 400);
    assert.equal((await get({ ...ref, selector: { chunkId: 'not-in-snapshot', anchor: null } })).data.code, 'EVIDENCE_SELECTOR_INVALID');
    const section = await get(ref, { scope: 'section' });
    assert.equal(section.status, 200);valid('get',section.data);
    assert.equal(section.data.context.content.contextExpansion.basis,'markdown_headings');
    assert.equal((await get({ ...ref, sourceHash: 'f'.repeat(64) })).data.code, 'EVIDENCE_INTEGRITY');
    await call('/api/sources/' + sourceId, 'PATCH', { project: 'new-project', version: 'RevB', title: 'New title' });
    const changedMeta = await search(), metaRef = changedMeta.items[0].context.evidenceRef;
    assert.equal(ref.sourceRevision, metaRef.sourceRevision);
    assert.notEqual(ref.evidenceId, metaRef.evidenceId);
    const stillOld = await get(ref, { scope: 'source' });
    assert.equal(stillOld.data.context.scope.project, 'old-project');
    assert.equal(stillOld.data.context.scope.documentVersion, 'RevA');
    assert.equal(stillOld.data.source.metadata.project, 'old-project');
    assert.equal(stillOld.data.citation.documentVersion, 'RevA');
    service.store.indexed(sourceId, 'ready', 'a-different-vector-signature');
    assert.equal((await search()).items[0].context.evidenceRef.evidenceId, metaRef.evidenceId);
    const separated = await get(ref);
    assert.equal(separated.data.source.indexStatus, undefined);
    assert.equal(separated.data.source.enabled, undefined);
    assert.equal(separated.data.runtime.indexStatus, 'ready');
    await importText('# Replaced\nPX1000 replacement evidence.', sourceId);
    const newRef = (await search()).items[0].context.evidenceRef;
    assert.notEqual(newRef.sourceRevision, ref.sourceRevision);
    assert.equal((await get(ref, { scope: 'source' })).data.text, oldRead.data.text);
    assert((await get(newRef, { scope: 'source' })).data.text.includes('replacement'));
    assert.equal((await get(newRef, { scope: 'source', maxChars: '200', cursor: firstPage.data.nextCursor })).data.code, 'CURSOR_MISMATCH');
    const legacy = await call('/api/get?id=' + sourceId);
    assert.equal(legacy.data.readMode, 'current_unpinned'); valid('get', legacy.data);
    assert.equal(legacy.data.source.type, 'reference');
    const prefix = await call('/api/get?' + new URLSearchParams({ id: sourceId, anchor: '#/text' }));
    assert.equal(prefix.status, 200); valid('get', prefix.data);
    assert.equal((await get(prefix.data.evidenceRef)).data.text, prefix.data.text);
    const numeric = await call('/api/get?' + new URLSearchParams({ id: sourceId, cursor: '3' }));
    assert.equal(numeric.data.text, legacy.data.text.slice(3)); valid('get', numeric.data);
    const token = service.token;
    await new Promise(resolve => service.server.close(resolve)); service.store.close();
    service = await startKnowledgeService(); assert.equal(service.token, token);
    const next = await get(ref, { scope: 'source', maxChars: '200', cursor: firstPage.data.nextCursor });
    assert.equal(next.status, 200);
    assert.equal(next.data.text, oldRead.data.text.slice(200, 400));
    await call('/api/sources/' + sourceId, 'PATCH', { enabled: false });
    assert.equal((await get(ref)).data.code, 'SOURCE_DISABLED');
    await call('/api/sources/' + sourceId, 'PATCH', { enabled: true });
    const file = path.join(root, 'evidence', ref.evidenceId + '.json'), original = await readFile(file);
    await writeFile(file, '{bad snapshot}');
    assert.equal((await get(ref)).data.code, 'EVIDENCE_INTEGRITY');
    await writeFile(file, original); await unlink(file);
    assert.equal((await get(ref)).data.code, 'EVIDENCE_UNAVAILABLE');
    const current = service.store.get(sourceId);
    service.store.indexed(sourceId, 'ready', 'race-model');
    service.vector.ready = true; service.vector.signature = 'race-model';
    service.vector.search = async () => { service.store.patchSource(sourceId, { project: 'race-new' }); return []; };
    const raced = await call('/api/search', 'POST', { query: 'PX1000 evidence', filters: { project: current.metadata.project } });
    assert.equal(raced.data.items.length, 0, 'async vector work must not bypass current project filters');
    service.vector.ready = false;
    const rawMetadata = service.store.db.prepare('SELECT metadata_json FROM sources WHERE source_id=?').get(sourceId).metadata_json;
    service.store.db.prepare('UPDATE sources SET metadata_json=? WHERE source_id=?').run('{broken metadata}',sourceId);
    assert.equal((await get(newRef)).data.code, 'EVIDENCE_INTEGRITY');
    assert.equal((await call('/api/get?id='+sourceId)).data.code, 'EVIDENCE_INTEGRITY');
    assert.equal((await call('/api/search','POST',{query:'PX1000'})).data.code, 'EVIDENCE_INTEGRITY');
    service.store.db.prepare('UPDATE sources SET metadata_json=? WHERE source_id=?').run(rawMetadata,sourceId);
    const evidenceDir=path.join(root,'evidence'), preservedDir=path.join(root,'test-preserved-evidence');
    await rename(evidenceDir,preservedDir);await writeFile(evidenceDir,'test-only publication fault');
    try {
      const before=service.store.list({limit:1000}).length;
      const failed=await call('/api/sources','POST',{name:'rollback.md',contentBase64:Buffer.from('PX2000 failure-only fixture').toString('base64')});
      await assert.rejects(wait(failed.data.jobId));
      assert.equal(service.store.list({limit:1000}).length,before,'failed evidence publication must rollback source and chunks');
    }finally{await unlink(evidenceDir);await rename(preservedDir,evidenceDir);}
    await call('/api/sources/' + sourceId, 'DELETE');
    assert.equal((await get(newRef)).data.code, 'EVIDENCE_UNAVAILABLE');
  } finally {
    await new Promise(resolve => service.server.close(resolve)); service.store.close();
    await rm(root, { recursive: true, force: true });
  }
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';

test('shared import queue reports exact jobs and preserves source identity, metadata and evidence', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'kb-import-'));
  const probe = createServer();
  await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  Object.assign(process.env, { EASYEDA_KNOWLEDGE_ROOT: root, EASYEDA_KNOWLEDGE_PORT: String(port), EASYEDA_KNOWLEDGE_ENABLE_VECTOR: '0' });
  const { startKnowledgeService } = await import('../src/service.mjs');
  const service = await startKnowledgeService();
  const call = async (route, method = 'GET', body) => {
    const response = await fetch(service.baseUrl + route, { method, headers: { 'X-Knowledge-Token': service.token, 'content-type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) });
    return { status: response.status, data: await response.json() };
  };
  const wait = async jobId => {
    for (let i = 0; i < 150; i++) {
      const { status, data } = await call('/api/jobs/' + jobId);
      assert.equal(status, 200, 'each submitted job has a durable exact status endpoint');
      if (['completed', 'failed', 'interrupted'].includes(data.job.status)) return data;
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.fail('Import did not finish');
  };
  const submit = async (text, extra = {}) => {
    const result = await call('/api/sources', 'POST', { name: 'intake.txt', contentBase64: Buffer.from(text).toString('base64'), ...extra });
    assert.equal(result.status, 202);
    return { jobId: result.data.jobId, ...await wait(result.data.jobId) };
  };
  try {
    const metadata = { title: 'Private input note', type: 'company_rule', domain: 'schematic', project: 'intake-demo', models: ['KB1000'], version: 'A' };
    const original = await submit('KB1000 initial input requirement.', { metadata });
    assert.equal(original.job.status, 'completed');
    assert.equal(original.source.indexStatus, 'keyword_ready', 'vector unavailable is distinct from fully indexed');
    assert.equal(original.job.detail.vector, 'unavailable');
    const id = original.source.sourceId;
    const search = await call('/api/search', 'POST', { query: 'KB1000 initial input', filters: { sourceIds: [id] } });
    const ref = search.data.items[0].context.evidenceRef;
    const oldText = (await call('/api/get?' + new URLSearchParams({ evidenceRef: JSON.stringify(ref), scope: 'source' }))).data.text;
    const duplicate = await submit('KB1000 initial input requirement.');
    assert.equal(duplicate.source.sourceId, id);
    assert.equal(duplicate.source.revision, original.source.revision);
    assert.equal(duplicate.source.metadata.project, metadata.project);
    assert.equal((await call('/api/sources')).data.total, 1);
    const updated = await submit('KB1000 changed input requirement.', { sourceId: id, metadata: { version: 'B' } });
    assert.equal(updated.job.status, 'completed');
    assert.notEqual(updated.source.revision, original.source.revision);
    for (const field of ['title', 'type', 'domain']) assert.equal(updated.source[field], original.source[field], field + ' survives an update');
    assert.equal(updated.source.metadata.project, metadata.project);
    assert.equal(updated.source.metadata.version, 'B');
    assert.equal((await call('/api/get?' + new URLSearchParams({ evidenceRef: JSON.stringify(ref), scope: 'source' }))).data.text, oldText);
    assert.match((await call('/api/get?id=' + id)).data.text, /changed input/);
    const invalid = await call('/api/sources', 'POST', { name: 'bad.txt', contentBase64: Buffer.from([255, 254]).toString('base64') });
    const failed = await wait(invalid.data.jobId);
    assert.equal(failed.job.status, 'failed');
    assert(failed.job.detail.error);
    assert.equal(failed.source, undefined);
    assert.equal((await call('/api/sources')).data.total, 1);
    assert.equal((await call('/api/jobs/job-missing')).status, 404);
    for (let i = 0; i < 101; i++) service.store.job('import', 'completed');
    assert.equal((await call('/api/jobs/' + original.jobId)).data.job.jobId, original.jobId, 'older jobs are not limited to the latest 100');
    const siblingText = 'KB1000 another registered version.';
    const sibling = await submit(siblingText);
    const beforeConflict = service.store.get(id);
    const conflict = await submit(siblingText, { sourceId: id });
    assert.equal(conflict.job.status, 'failed', 'an explicit update must not duplicate another registered identity');
    assert.equal(service.store.get(id).revision, beforeConflict.revision);
    assert.equal(service.store.get(sibling.source.sourceId).revision, sibling.source.revision);

    // Simulate only the publishing boundary; this does not claim a real model test.
    let publishAttempts = 0;
    Object.assign(service.vector, { ready: true, signature: 'test-publisher', publish: async () => { publishAttempts++; throw Error('simulated vector publish failure'); } });
    const interruptedText = 'KB1000 recoverable vector publication.';
    const indexFailure = await submit(interruptedText);
    assert.equal(indexFailure.job.status, 'failed');
    assert.equal(indexFailure.job.detail.sourceId, indexFailure.source.sourceId);
    assert.equal(indexFailure.source.indexStatus, 'failed');
    service.vector.publish = async () => { publishAttempts++; };
    const retry = await submit(interruptedText);
    assert.equal(retry.job.status, 'completed');
    assert.equal(retry.source.sourceId, indexFailure.source.sourceId);
    assert.equal(retry.source.revision, indexFailure.source.revision);
    assert.equal(retry.source.indexStatus, 'ready');
    assert.equal(publishAttempts, 2);
    service.vector.ready = false;
    const lesson = service.store.saveSource({ name: 'approved.txt', type: 'lesson', sourceHash: 'a'.repeat(64), contentPath: 'unused', metadata: {} });
    const bypass = await call('/api/sources', 'POST', { name: 'replacement.txt', sourceId: lesson.sourceId, contentBase64: Buffer.from('should not publish').toString('base64') });
    if (bypass.status === 202) assert.equal((await wait(bypass.data.jobId)).job.status, 'failed');
    else assert.equal(bypass.status, 400);
    assert.equal(service.store.get(lesson.sourceId).type, 'lesson');
    assert.equal(service.store.get(lesson.sourceId).revision, lesson.revision);
  } finally {
    // An early assertion must not close SQLite while the import queue is writing.
    for (let i = 0; i < 200 && service.store.jobs().some(job => ['queued', 'running'].includes(job.status)); i++) await new Promise(resolve => setTimeout(resolve, 10));
    await new Promise(resolve => service.server.close(resolve));
    service.store.close();
    await rm(root, { recursive: true, force: true });
  }
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { apiReference, callAction, control, imageResult, screenshotCode, snapshotSteps } from '../src/open.mjs';
import { DOMAIN_NAMES, toMcpResult } from '../src/core.mjs';

test('one HTTP request per batch, no replay on connection failure, errors stay errors', async () => {
  const requests = [];
  const server = createServer(async (req, res) => {
    assert.equal(req.headers['x-easyeda-response'], 'public-v1');
    let body = ''; for await (const chunk of req) body += chunk;
    requests.push(JSON.parse(body));
    if (requests.at(-1).action === 'debug.exec_js') { req.socket.destroy(); return; }
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ ok: true, result: { ok: false, partial: true, results: [{ ok: true }, { ok: false }] } }));
  }).listen(0, '127.0.0.1');
  await once(server, 'listening');
  try {
    const url = `http://127.0.0.1:${server.address().port}`;
    const result = await callAction('debug.batch', { window: 'w', target: { documentUuid: 'd' }, payload: { steps: snapshotSteps('pcb', true) } }, url);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].payload.steps.length, 7);
    assert.equal(requests[0].payload._target.documentUuid, 'd');
    assert.equal(result.isError, true);
    const failure = await callAction('debug.exec_js', { payload: { code: 'return 1' } }, url);
    assert.equal(requests.length, 2);
    assert.equal(failure.isError, true);
    assert.match(failure.content[0].text, /unknown/);
  } finally { server.closeAllConnections(); server.close(); }
});

test('inline image, exact API method and domain discovery', async () => {
  const base64 = Buffer.from('pixels').toString('base64');
  const image = imageResult(toMcpResult({ ok: true, result: { ok: true, result: { value: { base64, mimeType: 'image/png', size: 6 } } } }));
  assert.equal(image.content[0].type, 'image');
  assert.equal(image.content[0].data, base64);
  assert.ok(!image.content[1].text.includes(base64));
  assert.match(screenshotCode(true), /zoomToAllPrimitives/);
  assert.ok(DOMAIN_NAMES.includes('library') && DOMAIN_NAMES.includes('debug'));
  const doc = await apiReference('DMT_EditorControl', 'getCurrentRenderedAreaImage');
  assert.match(doc.markdown, /getCurrentRenderedAreaImage/);
  await assert.rejects(apiReference('../secrets', 'read'));
  assert.equal(toMcpResult({ ok: true, result: { ok: false, error: { code: 'NATIVE_ERROR' } } }).isError, true);
});

test('instruction revisions and cancellation travel through HTTP without replaying writes', async () => {
 const requests = []; let started; const entered = new Promise(r => { started = r; });
 const server = createServer(async (req, res) => {
  let body = ''; for await (const chunk of req) body += chunk;
  const request = JSON.parse(body); requests.push(request);
  if (request.action === 'debug.exec_js' && request.payload.code === 'slow') { started(); return; }
  res.setHeader('content-type', 'application/json'); res.end(JSON.stringify({ ok: true, result: {} }));
 }).listen(0, '127.0.0.1');
 await once(server, 'listening');
 try {
  const url = `http://127.0.0.1:${server.address().port}`;
  await control({ window: 'revision-window', operation: 'supersede' }, url);
  const abort = new AbortController();
  const pending = callAction('debug.exec_js', { window: 'revision-window', signal: abort.signal, payload: { code: 'slow' } }, url);
  await entered; abort.abort(); const result = await pending;
  assert.equal(result.isError, true);
  assert.equal(requests[1].payload._execution.revision, requests[0].payload.revision);
  assert.equal(requests[2].action, 'debug.control');
  assert.equal(requests[2].payload.requestId, requests[1].id);
  assert.equal(requests.filter(r => r.action === 'debug.exec_js').length, 1);
  assert.equal(JSON.parse(result.content[0].text).cancellationRequested, true);
  const snapshot = snapshotSteps('schematic'); assert.equal(snapshot[1].payload.includeCheck, false);
  const rich = snapshotSteps('schematic', false, true); assert.equal(rich[1].payload.includeWires, true);
 } finally { server.closeAllConnections(); server.close(); }
});

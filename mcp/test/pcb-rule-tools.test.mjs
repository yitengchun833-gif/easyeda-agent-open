import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import os from 'node:os';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

test('stdio rule files reuse Go parsing, preserve native targeting, and confirm preview/apply outcomes', async () => {
  assert.ok(process.env.EASYEDA_BIN, 'EASYEDA_BIN must point to the matching candidate CLI');
  const temp = await mkdtemp(path.join(os.tmpdir(), 'easyeda-rule-import-'));
  const file = path.join(temp, 'rules.json'), invalid = path.join(temp, 'invalid.json');
  const ruleConfiguration = { Spacing: { unit: 'mil', clearance: 6 }, NetCase: { USB_DP: 1, usb_dp: 2 } };
  await writeFile(file, JSON.stringify({ schema: 'easyeda-eda-rule-config-set', schemaVersion: 1,
    modules: ['ruleConfig', 'netRules'], data: { ruleConfig: ruleConfiguration, netRules: [{ name: 'ignored' }] } }));
  await writeFile(invalid, JSON.stringify({ schema: 'other-format', schemaVersion: 1, modules: ['ruleConfig'], data: { ruleConfig: ruleConfiguration } }));
  const calls = [];
  let mode = 'preview';
  const outcome = payload => payload.dryRun ? { dryRun: true,
    before: { ruleConfiguration: { ...ruleConfiguration, Spacing: { unit: 'mil', clearance: 4 } } },
    requested: { ruleConfiguration: payload.ruleConfiguration } }
    : mode === 'verified' ? { verified: true, actual: { ruleConfiguration: payload.ruleConfiguration } }
    : mode === 'partial' ? { verified: false, partial: true, rolledBack: true } : {};
  const mock = createServer(async (req, res) => {
    let text = ''; for await (const chunk of req) text += chunk;
    const request = JSON.parse(text); calls.push(request);
    res.setHeader('content-type', 'application/json');
    if (mode === 'unknown') { res.statusCode = 503; res.end(JSON.stringify({ ok: false, executionState: 'unknown', retryable: false })); return; }
    const result = request.action === 'debug.batch'
      ? { ok: true, results: request.payload.steps.map((step, index) => ({ index, action: step.action, ok: true, result: outcome(step.payload) })) }
      : outcome(request.payload);
    res.end(JSON.stringify({ ok: true, result }));
  }).listen(0, '127.0.0.1');
  await once(mock, 'listening');
  const packageDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
  const transport = new StdioClientTransport({ command: process.execPath, args: [path.join(packageDir, 'src/server.mjs')], cwd: packageDir,
    env: { ...process.env, EASYEDA_DAEMON_URL: `http://127.0.0.1:${mock.address().port}` } });
  const client = new Client({ name: 'rule-file-test', version: '1.0.0' });
  const target = { projectUuid: 'project', documentUuid: 'pcb' };
  const payload = { fromFile: file, module: 'ruleConfig', dryRun: true };
  const invoke = p => client.callTool({ name: 'easyeda_pcb', arguments: { action: 'pcb.drc.rules.set', window: 'w', target, payload: p } });
  try {
    await client.connect(transport);
    assert.match((await client.listTools()).tools.find(tool => tool.name === 'easyeda_pcb').description, /fromFile/);
    const preview = await invoke(payload);
    assert.equal(preview.isError, false);
    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0].payload.ruleConfiguration, ruleConfiguration);
    assert.deepEqual(calls[0].payload._target, target);
    assert.equal(calls[0].payload.fromFile, undefined);
    assert.equal(calls[0].payload.netRules, undefined);
    assert.equal(preview.structuredContent.ruleImports[0].status, 'previewed');
    assert.deepEqual(preview.structuredContent.ruleImports[0].unprocessedModules, ['netRules']);
    assert.deepEqual(preview.structuredContent.ruleImports[0].changes, [{ path: '/ruleConfiguration/Spacing/clearance', operation: 'replace', before: 4, after: 6 }]);
    for (const expected of ['verified', 'missing', 'partial', 'unknown']) {
      mode = expected;
      const count = calls.length;
      const applied = await invoke({ ...payload, dryRun: false });
      assert.equal(calls.length, count + 1, 'one native request without replay');
      assert.equal(applied.isError, expected !== 'verified');
      if (expected !== 'unknown') assert.equal(applied.structuredContent.ruleImports[0].status, expected === 'verified' ? 'verified' : 'unconfirmed');
      if (expected === 'partial') assert.equal(applied.structuredContent.result.rolledBack, true, 'native failed receipt is preserved');
      if (expected === 'unknown') assert.match(JSON.stringify(applied), /unknown/);
    }
    mode = 'preview';
    const batch = await client.callTool({ name: 'easyeda_batch', arguments: { window: 'w', target,
      steps: [{ action: 'pcb.drc.rules.set', payload }] } });
    assert.equal(batch.isError, false);
    assert.equal(calls.at(-1).action, 'debug.batch');
    assert.equal(calls.at(-1).payload.steps[0].payload.dryRun, true);
    assert.deepEqual(calls.at(-1).payload._target, target);
    assert.equal(batch.structuredContent.ruleImports[0].status, 'previewed');
    mode = 'missing';
    const missingBatch = await client.callTool({ name: 'easyeda_batch', arguments: { window: 'w',
      steps: [{ action: 'pcb.drc.rules.set', payload: { ...payload, dryRun: false } }] } });
    assert.equal(missingBatch.isError, true, 'batch also requires positive apply evidence');
    const count = calls.length;
    for (const args of [
      { window: 'w', dryRun: true, steps: [{ action: 'pcb.drc.rules.set', payload }] },
      { window: 'w', steps: [{ action: 'pcb.drc.rules.set', payload }, { action: 'pcb.drc.rules.set', payload: { ...payload, fromFile: invalid } }] },
    ]) {
      assert.equal((await client.callTool({ name: 'easyeda_batch', arguments: args })).isError, true);
    }
    assert.equal((await invoke({ ...payload, module: undefined })).isError, true, 'multi-module selection remains mandatory');
    assert.equal((await invoke({ ...payload, ruleConfiguration })).isError, true, 'inline and file payloads cannot mix');
    assert.equal(calls.length, count, 'invalid file inputs fail before every native batch step');
  } finally {
    await client.close();
    mock.closeAllConnections(); mock.close();
    await rm(temp, { recursive: true, force: true });
  }
});

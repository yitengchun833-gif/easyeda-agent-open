import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:net';
import { once } from 'node:events';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

test('shared daemon survives either MCP client closing; explicit stop owns shutdown', async () => {
  const probe = createServer().listen(0, '127.0.0.1');
  await once(probe, 'listening');
  const port = probe.address().port;
  await new Promise(resolve => probe.close(resolve));
  const url = `http://127.0.0.1:${port}`;
  const makeTransport = () => new StdioClientTransport({
    command: process.execPath, args: [process.env.EASYEDA_OPEN_LAUNCHER || fileURLToPath(new URL('../src/launch.mjs', import.meta.url))],
    env: { ...process.env, EASYEDA_DAEMON_URL: url,
      EASYEDA_RUNTIME_DIR: fileURLToPath(new URL('../../.runtime/launcher-test', import.meta.url)),
      EASYEDA_AUDIT_DIR: fileURLToPath(new URL('../../.runtime/launcher-test/audit', import.meta.url)) },
  });
  const client = new Client({ name: 'open-launch-test', version: '1.0.0' });
  const borrower = new Client({ name: 'open-launch-borrower', version: '1.0.0' });
  try {
    await client.connect(makeTransport());
    await borrower.connect(makeTransport());
	const listed = await client.listTools();
	const runtime = listed.tools.find(tool => tool.name === 'easyeda_runtime');
	assert.ok(runtime.inputSchema.properties.operation.enum.includes('task_update'));
	assert.ok(runtime.inputSchema.properties.expectedRevision);
  assert.ok(runtime.inputSchema.properties.operation.enum.includes('restore'));
  assert.ok(runtime.inputSchema.properties.sourceWindow);
    const result = await client.callTool({ name: 'easyeda_health', arguments: {} });
    assert.equal(result.isError, false);
    assert.equal(result.structuredContent.version, 'v1.8.1-open.9.4');
    assert.equal(result.structuredContent.windows.length, 0, 'no real editor connected to the test port');
    const pid = result.structuredContent.pid;
    await client.close();
    const remaining = await borrower.callTool({ name: 'easyeda_health', arguments: {} });
    assert.equal(remaining.isError, false, 'closing the starter must not kill the shared daemon');
    assert.equal(remaining.structuredContent.pid, pid);
    await borrower.close();
    assert.equal((await (await fetch(`${url}/health`)).json()).pid, pid);
  } finally {
    await client.close();
    await borrower.close();
    execFileSync(process.env.EASYEDA_OPEN_BIN || fileURLToPath(new URL('../../bin/easyeda.exe', import.meta.url)),
      ['--ports', `${port}-${port}`, 'daemon', 'stop'], { windowsHide: true, stdio: 'pipe' });
  }
  for (let attempt = 0; attempt < 20; attempt++) {
    try { await fetch(`${url}/health`, { signal: AbortSignal.timeout(1000) }); }
    catch { return; }
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  assert.fail('explicit stop left the isolated daemon running');
});

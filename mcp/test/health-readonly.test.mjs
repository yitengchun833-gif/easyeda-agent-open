import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

const root = process.env.EASYEDA_TEST_PACKAGE_ROOT || fileURLToPath(new URL('../../', import.meta.url));
const serverURL = pathToFileURL(path.join(root, 'mcp/src/server.mjs')).href;
const version = `v${JSON.parse(await readFile(path.join(root, 'mcp/package.json'), 'utf8')).version}`;

test('real MCP health handler reads one configured endpoint and never manages a daemon', async t => {
  const runtimeDir = await mkdtemp(path.join(os.tmpdir(), 'eda-health-readonly-'));
  const pidFile = path.join(runtimeDir, 'daemon.pid');
  await writeFile(pidFile, String(process.pid));
  const requests = [];
  let info = { service: 'easyeda-agent', version, pid: process.pid, windows: [] };
  let status = 200;
  const mock = createServer((req, res) => {
    requests.push({ method: req.method, path: req.url });
    res.writeHead(status, { 'content-type': 'application/json' });
    res.end(JSON.stringify(info));
  });
  await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
  let decoyRequests = 0;
  const decoy = createServer((req, res) => {
    decoyRequests++;
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify(info));
  });
  await new Promise(resolve => decoy.listen(0, '127.0.0.1', resolve));
  // Stub only CLI catalog loading; every lifecycle spawn/stop/restart is forbidden.
  const bootstrap = `
    import childProcess from 'node:child_process';
    import { syncBuiltinESMExports } from 'node:module';
    childProcess.spawn = () => { throw new Error('forbidden daemon spawn'); };
    process.kill = () => { throw new Error('forbidden daemon stop'); };
    childProcess.execFile = () => { throw new Error('forbidden CLI lifecycle call'); };
    childProcess.execFile[Symbol.for('nodejs.util.promisify.custom')] = async (_file, args) => {
      if (args.length !== 1 || args[0] !== 'actions') throw new Error('forbidden CLI lifecycle call');
      return { stdout: '[]', stderr: '' };
    };
    syncBuiltinESMExports();
    await import(${JSON.stringify(new URL('./open.mjs', serverURL).href)});
    process.env.EASYEDA_DAEMON_URL = ${JSON.stringify(`http://127.0.0.1:${decoy.address().port}`)};
    await import(${JSON.stringify(serverURL)});
  `;
  const client = new Client({ name: 'health-readonly-test', version: '1.0.0' });
  const transport = new StdioClientTransport({
    command: process.execPath, args: ['--input-type=module', '--eval', bootstrap],
    env: { ...process.env, EASYEDA_DAEMON_URL: `http://127.0.0.1:${mock.address().port}`, EASYEDA_RUNTIME_DIR: runtimeDir },
  });
  const call = () => client.callTool({ name: 'easyeda_health', arguments: {} });
  try {
    await client.connect(transport);
    await t.test('valid identity comes from the configured route in one GET', async () => {
      const result = await call();
      assert.equal(result.isError, false);
      assert.equal(result.structuredContent.pid, process.pid);
      assert.deepEqual(requests.splice(0), [{ method: 'GET', path: '/health' }]);
      assert.equal(decoyRequests, 0, 'health must not re-resolve its endpoint from mutable environment');
    });
    await t.test('explicit connect reuses a healthy daemon without lifecycle work or actions', async () => {
      const tools = await client.listTools();
      assert.equal(tools.tools.find(tool => tool.name === 'easyeda_connect').annotations.readOnlyHint, false);
      for (const windows of [[], [{ windowId: 'test-window' }]]) {
        info = { ...info, windows };
        const result = await client.callTool({ name: 'easyeda_connect', arguments: {} });
        assert.equal(result.isError, false);
        assert.equal(result.structuredContent.pid, process.pid);
        assert.equal(result.structuredContent.connectionState, windows.length ? 'connected' : 'waiting_for_connector');
        assert.equal(result.structuredContent.automaticReplay, false);
        assert.deepEqual(requests.splice(0), [{ method: 'GET', path: '/health' }]);
      }
      const invalid = await client.callTool({ name: 'easyeda_connect', arguments: { force: true } });
      assert.equal(invalid.isError, true);
      assert.match(invalid.content[0].text, /accepts no arguments/);
      assert.deepEqual(requests, []);
      assert.equal(decoyRequests, 0);
    });
    for (const [field, value] of [['service', 'foreign-app'], ['version', 'v0.0.0'], ['pid', process.pid + 1]]) {
      await t.test(`${field} mismatch fails without replacement`, async () => {
        const original = info;
        info = { ...info, [field]: value };
        const result = await call();
        assert.equal(result.isError, true);
        assert.match(result.content[0].text, /different process\/version/);
        assert.match(result.content[0].text, new RegExp(field));
        assert.deepEqual(requests.splice(0), [{ method: 'GET', path: '/health' }]);
        const connect = await client.callTool({ name: 'easyeda_connect', arguments: {} });
        assert.equal(connect.isError, true);
        assert.match(connect.content[0].text, /different process\/version/);
        assert.deepEqual(requests.splice(0), [{ method: 'GET', path: '/health' }]);
        info = original;
      });
    }
    await t.test('missing PID ownership evidence is an identity failure', async () => {
      await rm(pidFile);
      const result = await call();
      assert.equal(result.isError, true);
      assert.match(result.content[0].text, /pid/);
      assert.deepEqual(requests.splice(0), [{ method: 'GET', path: '/health' }]);
      await writeFile(pidFile, String(process.pid));
    });
    await t.test('unhealthy response is rejected without lifecycle work', async () => {
      status = 503;
      const result = await call();
      assert.equal(result.isError, true);
      assert.deepEqual(requests.splice(0), [{ method: 'GET', path: '/health' }]);
    });
    await t.test('connection refusal never starts a daemon or writes runtime files', async () => {
      await new Promise(resolve => mock.close(resolve));
      await new Promise(resolve => decoy.close(resolve));
      const result = await call();
      assert.equal(result.isError, true);
      assert.match(result.content[0].text, /unavailable/);
      assert.doesNotMatch(result.content[0].text, /forbidden/);
      assert.deepEqual(await readdir(runtimeDir), ['daemon.pid']);
    });
  } finally {
    await client.close();
    mock.closeAllConnections();
    decoy.closeAllConnections();
    await Promise.all([new Promise(resolve => mock.close(resolve)), new Promise(resolve => decoy.close(resolve))]);
    await rm(runtimeDir, { recursive: true, force: true });
  }
});

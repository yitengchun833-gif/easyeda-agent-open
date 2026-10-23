import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

const root = process.env.EASYEDA_TEST_PACKAGE_ROOT || fileURLToPath(new URL('../../', import.meta.url));
const launcher = pathToFileURL(process.env.EASYEDA_OPEN_LAUNCHER || path.join(root, 'mcp/src/launch.mjs')).href;
const version = 'v' + JSON.parse(await readFile(path.join(root, 'mcp/package.json'), 'utf8')).version;

async function connect(url, runtimeDir) {
  const bootstrap = `
    import childProcess from 'node:child_process';
    import { syncBuiltinESMExports } from 'node:module';
    childProcess.spawn = () => { throw new Error('forbidden service spawn'); };
    process.kill = () => { throw new Error('forbidden service stop'); };
    childProcess.execFile = () => { throw new Error('forbidden CLI lifecycle call'); };
    childProcess.execFile[Symbol.for('nodejs.util.promisify.custom')] = async (_file, args) => {
      if (args.length !== 1 || args[0] !== 'actions') throw new Error('forbidden CLI lifecycle call');
      return { stdout: '[]', stderr: '' };
    };
    syncBuiltinESMExports();
    await import(${JSON.stringify(launcher)});
  `;
  const transport = new StdioClientTransport({ command: process.execPath,
    args: ['--input-type=module', '--eval', bootstrap], stderr: 'pipe',
    env: { ...process.env, EASYEDA_DAEMON_URL: url, EASYEDA_RUNTIME_DIR: runtimeDir, EASYEDA_AUDIT_DIR: path.join(runtimeDir, 'audit') },
  });
  let stderr = '';
  transport.stderr.on('data', data => { stderr += data; });
  const client = new Client({ name: 'launch-readonly-test', version: '1.0.0' });
  try { await client.connect(transport); }
  catch (error) { await client.close(); throw error; }
  return { client, stderr: () => stderr };
}

test('default MCP launch lists tools with no daemon and creates no runtime or knowledge service', async () => {
  const base = await mkdtemp(path.join(os.tmpdir(), 'eda-launch-readonly-'));
  const reserve = createServer();
  await new Promise(resolve => reserve.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${reserve.address().port}`;
  await new Promise(resolve => reserve.close(resolve));
  let connected;
  try {
    connected = await connect(url, path.join(base, 'runtime'));
    const listed = await connected.client.listTools();
    assert.ok(listed.tools.some(tool => tool.name === 'easyeda_health'));
    const result = await connected.client.callTool({ name: 'easyeda_health', arguments: {} });
    assert.equal(result.isError, true);
    assert.match(result.content[0].text, /unavailable/);
    await connected.client.close();
    assert.equal(connected.stderr(), '', 'MCP connection must not try to start a knowledge service');
    assert.deepEqual(await readdir(base), [], 'no runtime directory, PID, audit or stderr log was created');
  } finally {
    await connected?.client.close();
    await rm(base, { recursive: true, force: true });
  }
});

test('two default MCP clients borrow an existing service without lifecycle calls or replacing PID evidence', async () => {
  const base = await mkdtemp(path.join(os.tmpdir(), 'eda-launch-shared-'));
  const runtimeDir = path.join(base, 'runtime');
  await mkdir(runtimeDir);
  await writeFile(path.join(runtimeDir, 'daemon.pid'), String(process.pid));
  const requests = [];
  const mock = createServer((req, res) => {
    requests.push(`${req.method} ${req.url}`);
    res.writeHead(200, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ service: 'easyeda-agent', version, pid: process.pid, windows: [] }));
  });
  await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${mock.address().port}`;
  let first, second;
  try {
    first = await connect(url, runtimeDir);
    second = await connect(url, runtimeDir);
    assert.deepEqual(requests, [], 'connecting clients must not probe/start a daemon');
    const result = await first.client.callTool({ name: 'easyeda_health', arguments: {} });
    assert.equal(result.isError, false);
    await first.client.close();
    const remaining = await second.client.callTool({ name: 'easyeda_health', arguments: {} });
    assert.equal(remaining.isError, false);
    assert.equal(remaining.structuredContent.pid, process.pid);
    await second.client.close();
    assert.deepEqual(requests, ['GET /health', 'GET /health']);
    assert.ok(mock.listening);
    assert.equal(await readFile(path.join(runtimeDir, 'daemon.pid'), 'utf8'), String(process.pid));
    assert.deepEqual(await readdir(runtimeDir), ['daemon.pid']);
    assert.equal(first.stderr() + second.stderr(), '');
  } finally {
    await first?.client.close();
    await second?.client.close();
    mock.closeAllConnections();
    await new Promise(resolve => mock.close(resolve));
    await rm(base, { recursive: true, force: true });
  }
});

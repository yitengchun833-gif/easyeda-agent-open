import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

const root = process.env.EASYEDA_TEST_PACKAGE_ROOT || fileURLToPath(new URL('../../', import.meta.url));
const serverURL = pathToFileURL(path.join(root, 'mcp/src/server.mjs')).href;
const version = `v${JSON.parse(await readFile(path.join(root, 'mcp/package.json'), 'utf8')).version}`;

// Real MCP handler and lifecycle helper, with only process creation replaced.
// This test cannot start/stop a real daemon or send an EDA action.
for (const scenario of ['start', 'spawn-error', 'exit']) {
  test(`explicit MCP connect: ${scenario}`, async () => {
    const runtimeDir = await mkdtemp(path.join(os.tmpdir(), 'eda-connect-'));
    const reserve = createServer();
    await new Promise(resolve => reserve.listen(0, '127.0.0.1', resolve));
    const port = reserve.address().port;
    await new Promise(resolve => reserve.close(resolve));
    const traceFile = path.join(runtimeDir, 'trace.json');
    const bootstrap = `
      import childProcess from 'node:child_process';
      import { syncBuiltinESMExports } from 'node:module';
      import { createServer } from 'node:http';
      import { writeFileSync } from 'node:fs';
      const trace = { starts: [], requests: [], stops: 0 };
      const record = () => writeFileSync(${JSON.stringify(traceFile)}, JSON.stringify(trace));
      record();
      process.kill = () => { throw new Error('real process stop forbidden'); };
      childProcess.spawn = (binary, args, options) => {
        trace.starts.push({binary, args, windowsHide:options.windowsHide, runtime:options.env.EASYEDA_RUNTIME_DIR}); record();
        if (${JSON.stringify(scenario)} === 'spawn-error') return {exitCode:null,unref(){},on(event,fn){if(event==='error')queueMicrotask(()=>fn(new Error('test spawn denied')))}};
        if (${JSON.stringify(scenario)} === 'exit') return {exitCode:1,unref(){},on(){}};
        const server = createServer((req,res) => {
          trace.requests.push(req.method+' '+req.url); record();
          if(req.method!=='GET'||req.url!=='/health') {res.writeHead(400);res.end();return;}
          res.setHeader('content-type','application/json');
          res.end(JSON.stringify({service:'easyeda-agent',version:${JSON.stringify(version)},pid:process.pid,windows:[]}));
        });
        writeFileSync(${JSON.stringify(path.join(runtimeDir, 'daemon.pid'))},String(process.pid));
        server.listen(${port},'127.0.0.1'); server.unref();
        return {exitCode:null,unref(){},on(){}};
      };
      childProcess.execFile = () => { throw new Error('CLI lifecycle forbidden'); };
      childProcess.execFile[Symbol.for('nodejs.util.promisify.custom')] = async (_file,args) => {
        if(args.length!==1||args[0]!=='actions')throw new Error('CLI lifecycle forbidden');
        return {stdout:'[]',stderr:''};
      };
      syncBuiltinESMExports();
      await import(${JSON.stringify(serverURL)});
    `;
    const client = new Client({ name: 'connect-test', version: '1.0.0' });
    const transport = new StdioClientTransport({ command: process.execPath,
      args: ['--input-type=module', '--eval', bootstrap],
      env: { ...process.env, EASYEDA_DAEMON_URL: `http://127.0.0.1:${port}`, EASYEDA_RUNTIME_DIR: runtimeDir,
        EASYEDA_OPEN_BIN: path.join(runtimeDir, 'companion.exe') },
    });
    try {
      // An unrelated evidence file must not be modified by connection management.
      const evidence = path.join(runtimeDir, 'old-unknown.json');
      await writeFile(evidence, '{"state":"unknown","requestId":"keep"}');
      await client.connect(transport);
      assert.deepEqual(JSON.parse(await readFile(traceFile, 'utf8')).starts, []);
      const call = () => client.callTool({ name: 'easyeda_connect', arguments: {} });
      const results = scenario === 'start' ? await Promise.all([call(), call(), call()]) : [await call()];
      if (scenario === 'start') {
        assert.ok(results.every(result => result.isError === false));
        assert.equal(new Set(results.map(result => result.structuredContent.pid)).size, 1);
        assert.equal(results[0].structuredContent.connectionState, 'waiting_for_connector');
        assert.equal((await call()).structuredContent.pid, results[0].structuredContent.pid);
      } else {
        assert.equal(results[0].isError, true);
        assert.match(results[0].content[0].text, scenario === 'exit' ? /exited during recovery/ : /test spawn denied/);
      }
      const trace = JSON.parse(await readFile(traceFile, 'utf8'));
      assert.equal(trace.starts.length, 1);
      assert.equal(trace.stops, 0);
      assert.ok(trace.requests.every(request => request === 'GET /health'));
      assert.equal(trace.starts[0].binary, path.join(runtimeDir, 'companion.exe'));
      assert.equal(trace.starts[0].runtime, runtimeDir);
      assert.equal(trace.starts[0].windowsHide, true);
      assert.deepEqual(trace.starts[0].args, ['--ports', `${port}-${port}`, 'daemon', 'start', '--auto-update-skill=false', '--autosave-debounce', '0']);
      assert.equal(await readFile(evidence, 'utf8'), '{"state":"unknown","requestId":"keep"}');
    } finally {
      await client.close();
      await rm(runtimeDir, { recursive: true, force: true });
    }
  });
}

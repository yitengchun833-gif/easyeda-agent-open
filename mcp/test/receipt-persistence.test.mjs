import test from 'node:test';
import assert from 'node:assert/strict';
import { fork, spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, mkdir, readdir, readFile, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { fixture, readyFinish, planTool, catalog, loadPlan, verifyStagePrefix, sha, invoke } from './fixtures/p0-receipts.mjs';
import { callAction } from '../src/open.mjs';

const json = async file => JSON.parse(await readFile(file, 'utf8'));
async function setup(t, kind = 'stage') {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'easyeda-p0-a-'));
  assert.ok(path.resolve(dir).startsWith(path.resolve(os.tmpdir()) + path.sep));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const input = await (kind === 'stage' ? fixture : readyFinish)(dir);
  await writeFile(path.join(dir, 'input.json'), JSON.stringify(input));
  return { dir, input, file: path.join(dir, 'receipts', kind === 'stage' ? 'stage-01.json' : 'delivery.json') };
}

async function worker(t, mode, dir, kind, point) {
  const child = fork(fileURLToPath(new URL('./fixtures/p0-crash-worker.mjs', import.meta.url)), [mode, dir, kind], {
    execArgv: [], env: { ...process.env, EASYEDA_DAEMON_URL: 'http://p0-offline.invalid' }, silent: true,
  });
  let stderr = '';
  child.stderr.on('data', bytes => { stderr += bytes; });
  const exited = once(child, 'exit');
  t.after(() => { if (child.exitCode === null && child.signalCode === null) child.kill(); });
  const message = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { child.kill(); reject(new Error('Fault point timeout: ' + stderr)); }, 10000);
    child.once('message', message => { clearTimeout(timer); resolve(message); });
    child.once('exit', code => { clearTimeout(timer); reject(new Error(`Worker exited ${code}: ${stderr}`)); });
    child.once('error', error => { clearTimeout(timer); reject(error); });
  });
  if (point) {
    assert.equal(message.point, point);
    assert.equal(child.kill(), true);
    const [, signal] = await exited;
    assert.equal(signal, 'SIGTERM');
  } else {
    assert.equal((await exited)[0], 0, stderr);
  }
  return message;
}

async function blocked(input, file, kind = 'stage') {
  const before = await readFile(file);
  let calls = 0;
  const old = globalThis.fetch;
  globalThis.fetch = async () => { calls++; throw new Error('MUST_NOT_SEND'); };
  try {
    await assert.rejects(planTool(input, catalog, async () => { calls++; }), /EEXIST/);
    if (kind === 'stage') {
      await assert.rejects(planTool({ operation: 'run', planFile: input.planFile, expectedHash: input.expectedHash,
        window: input.window, runStages: [{ stage: 'move' }] }, catalog, async () => { calls++; }), /already has a receipt/);
      await assert.rejects(planTool({ ...input, stage: 'final' }, catalog, async () => { calls++; }));
    }
    assert.equal(calls, 0);
    assert.deepEqual(await readFile(file), before);
  } finally { globalThis.fetch = old; }
}

test('P0 explicit cancellation before send is not_executed', async () => {
  let calls = 0;
  const old = globalThis.fetch;
  globalThis.fetch = async () => { calls++; throw new Error('MUST_NOT_SEND'); };
  try {
    const result = await callAction('schematic.component.modify', { window: 'offline-window', signal: AbortSignal.abort(), payload: {} });
    assert.equal(calls, 0);
    assert.equal(result.isError, true);
    assert.equal(JSON.parse(result.content[0].text).executionState, 'not_executed');
  } finally { globalThis.fetch = old; }
});

for (const kind of ['stage', 'delivery']) {
  for (const [mode, point] of [
    ['before-dispatch', 'pending-synced-before-dispatch'],
    [kind === 'stage' ? 'after-native' : 'save-crash', kind === 'stage' ? 'native-applied-before-reply' : 'saved-before-reply'],
    ['torn-settle', 'settle-write-torn'], ['temp-synced', 'temporary-synced'], ['temp-closed', 'temporary-closed'],
  ]) test(`P0-A ${kind}: killed at ${mode} preserves complete pending and blocks replay`, async t => {
    const { dir, input, file } = await setup(t, kind);
    const { pendingBytes } = await worker(t, mode, dir, kind, point);
    assert.deepEqual(await readFile(file), Buffer.from(pendingBytes, 'base64'));
    const pending = await json(file);
    assert.equal(pending.state, 'pending');
    assert.equal(pending.receipt, null);
    assert.equal(pending.completedAt, null);
    const marker = path.join(dir, kind === 'stage' ? 'mock-eda-state.json' : 'mock-saved-state.json');
    if (mode === 'before-dispatch') await assert.rejects(readFile(marker), { code: 'ENOENT' });
    else assert.equal((await json(marker))[kind === 'stage' ? 'applied' : 'saved'], true);
    await blocked(input, file, kind);
  });

  test(`P0-A ${kind}: replacement is valid after process termination`, async t => {
    const { dir, input, file } = await setup(t, kind);
    await worker(t, 'after-rename', dir, kind, 'replacement-complete');
    const settled = await json(file);
    assert.equal(settled.state, 'settled');
    if (kind === 'stage') {
      assert.equal(settled.stage.completed, true);
      assert.equal((await verifyStagePrefix(await loadPlan(input.planFile), 1)).receipts.length, 1);
    } else assert.equal(settled.receipt.saved, true);
    assert.deepEqual((await readdir(path.dirname(file))).filter(name => name.endsWith('.tmp')), []);
  });

  for (const phase of ['write', 'sync', 'close', 'rename']) test(`P0-A ${kind}: ${phase} failure is explicit and preserves pending bytes`, async t => {
    const { dir, input, file } = await setup(t, kind);
    const message = await worker(t, `fail-${phase}`, dir, kind);
    const error = kind === 'stage' ? message.error : message.result.structuredContent.reason;
    assert.match(error, new RegExp(`INJECTED_${phase.toUpperCase()}_FAILURE`));
    assert.match(error, /Receipt persistence|receipt persistence/);
    assert.deepEqual(await readFile(file), Buffer.from(message.pendingBytes, 'base64'));
    assert.equal((await json(file)).state, 'pending');
    if (kind === 'delivery') {
      assert.equal(message.result.structuredContent.ok, false);
      assert.equal(message.result.structuredContent.partial, true);
      assert.deepEqual(message.operations, ['visual_review', 'finish']);
    }
    assert.deepEqual((await readdir(path.dirname(file))).filter(name => name.endsWith('.tmp')), []);
    await blocked(input, file, kind);
  });

  for (const phase of ['write', 'sync', 'close']) test(`P0-A ${kind}: initial pending ${phase} failure prevents sending`, async t => {
    const { dir, input, file } = await setup(t, kind);
    const message = await worker(t, `initial-fail-${phase}`, dir, kind);
    assert.match(message.error, new RegExp(`INJECTED_${phase.toUpperCase()}_FAILURE`));
    assert.deepEqual(message.operations, []);
    await blocked(input, file, kind);
  });
}

for (const [name, bytes] of [['empty', ''], ['truncated', '{"schemaVersion":']]) {
  test(`P0 malformed ${name} receipt still fails closed`, async t => {
    const { input, file } = await setup(t);
    await mkdir(path.dirname(file));
    await writeFile(file, bytes);
    await blocked(input, file);
  });
}

test('P0-A unknown stage receipt preserves existing semantics', async t => {
  const { dir, input, file } = await setup(t);
  const message = await worker(t, 'unknown', dir, 'stage');
  assert.match(message.error, /INJECTED_NATIVE_REPLY_LOST/);
  assert.equal((await json(file)).state, 'unknown');
  await blocked(input, file);
});

for (const phase of ['write', 'sync', 'close', 'rename']) test(`P0-A unknown update ${phase} failure is explicit and preserves pending`, async t => {
  const { dir, input, file } = await setup(t);
  const message = await worker(t, `unknown-fail-${phase}`, dir, 'stage');
  assert.match(message.error, /Receipt persistence failed/);
  assert.match(message.error, new RegExp(`INJECTED_${phase.toUpperCase()}_FAILURE`));
  assert.deepEqual(await readFile(file), Buffer.from(message.pendingBytes, 'base64'));
  await blocked(input, file);
});

test('P0-A Windows reader denying delete sharing makes replacement fail without losing pending', { skip: process.platform !== 'win32' }, async t => {
  const { input, file } = await setup(t);
  let locked, before;
  const script = "$s = [IO.File]::Open($env:P0_RECEIPT_LOCK_PATH, 'Open', 'Read', 'Read'); [Console]::WriteLine('LOCKED'); [Console]::Out.Flush(); [Console]::ReadLine() | Out-Null; $s.Dispose()";
  await assert.rejects(planTool(input, catalog, async action => {
    if (action === 'debug.batch') {
      before = await readFile(file);
      locked = spawn(path.join(process.env.SystemRoot, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe'),
        ['-NoProfile', '-NonInteractive', '-Command', script], { env: { ...process.env, P0_RECEIPT_LOCK_PATH: file }, windowsHide: true });
      t.after(() => { if (locked.exitCode === null) locked.kill(); });
      await new Promise((resolve, reject) => {
        const timer = setTimeout(() => { locked.kill(); reject(new Error('Windows lock timeout')); }, 10000);
        let output = '';
        locked.stdout.on('data', chunk => { output += chunk; if (output.includes('LOCKED')) { clearTimeout(timer); resolve(); } });
        locked.once('error', error => { clearTimeout(timer); reject(error); });
        locked.once('exit', code => { clearTimeout(timer); reject(new Error(`Windows lock process exited ${code}`)); });
      });
    }
    return invoke(action);
  }), error => /Receipt persistence failed \(rename\)/.test(error.message) && /EPERM|EACCES|EBUSY/.test(error.message));
  assert.deepEqual(await readFile(file), before);
  const exited = once(locked, 'exit');
  locked.stdin.end('\n');
  assert.equal((await exited)[0], 0);
  await blocked(input, file);
});

test('P0 successful mock save with lost response stays saved:null and never retries', async t => {
  const { dir, input, file } = await setup(t, 'delivery');
  const { result, operations } = await worker(t, 'save-response-lost', dir, 'delivery');
  assert.equal((await json(path.join(dir, 'mock-saved-state.json'))).saved, true);
  assert.equal(result.structuredContent.saved, null);
  assert.equal(result.structuredContent.ok, false);
  assert.equal(result.structuredContent.partial, true);
  const settled = await json(file);
  assert.equal(settled.state, 'settled');
  assert.equal(JSON.parse(settled.receipt.finishReceipt.content[0].text).executionState, 'unknown');
  assert.equal(result.structuredContent.receiptSha256, sha(await readFile(file)));
  assert.deepEqual(operations, ['visual_review', 'finish']);
  await blocked(input, file, 'delivery');
});

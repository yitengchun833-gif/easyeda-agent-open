import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdir, open, readFile } from 'node:fs/promises';
import path from 'node:path';

const expectedVersion = `v${JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8')).version}`;
export const recoveryRevision = 'health-readonly-20261021';
let starting;

// A missing service is an observation, never permission to start or replace it.
export async function readDaemonHealth(options = {}) {
  const url = options.url || process.env.EASYEDA_DAEMON_URL || 'http://127.0.0.1:60932';
  const endpoint = new URL(url);
  if (endpoint.protocol !== 'http:' || endpoint.hostname !== '127.0.0.1' || !endpoint.port) throw new Error('Daemon recovery supports explicit local loopback ports only');
  const runtimeDir = options.runtimeDir || process.env.EASYEDA_RUNTIME_DIR || fileURLToPath(new URL('../../.runtime', import.meta.url));
  let response;
  try { response = await fetch(new URL('/health', endpoint), { signal: AbortSignal.timeout(1500) }); }
  catch (error) { if (error.cause?.code === 'ECONNREFUSED') return null; throw error; }
  const info = await response.json();
  const pidFile = path.join(runtimeDir, 'daemon.pid');
  const ownedPid = Number(await readFile(pidFile, 'utf8').catch(() => ''));
  const expected = { service: 'easyeda-agent', version: expectedVersion, pid: ownedPid || null };
  if (!response.ok || info?.service !== expected.service || info?.version !== expected.version || !Number.isInteger(info?.pid) || info.pid <= 0 || info.pid !== ownedPid) {
    throw new Error(`Daemon endpoint belongs to a different process/version; endpoint=${endpoint.origin}; pidFile=${pidFile}; expected=${JSON.stringify(expected)}; observed=${JSON.stringify({ service: info?.service, version: info?.version, pid: info?.pid, httpStatus: response.status })}; no process was replaced`);
  }
  return info;
}

// Explicit startup only. Never replay a failed action or replace a live daemon.
export async function ensureDaemon(options = {}) {
  const url = options.url || process.env.EASYEDA_DAEMON_URL || 'http://127.0.0.1:60932';
  const endpoint = new URL(url);
  const runtimeDir = options.runtimeDir || process.env.EASYEDA_RUNTIME_DIR || fileURLToPath(new URL('../../.runtime', import.meta.url));
  const binary = options.binary || process.env.EASYEDA_OPEN_BIN || process.env.EASYEDA_BIN || fileURLToPath(new URL('../../bin/easyeda.exe', import.meta.url));
  const probe = () => readDaemonHealth({ url, runtimeDir });
  const existing = await probe();
  if (existing) return existing;
  if (starting) return starting;
  starting = (async () => {
    await mkdir(runtimeDir, { recursive: true });
    const log = await open(path.join(runtimeDir, 'daemon-stderr.log'), 'a');
    let child, startupError;
    try {
      child = spawn(binary, ['--ports', `${endpoint.port}-${endpoint.port}`, 'daemon', 'start', '--auto-update-skill=false', '--autosave-debounce', '0'], {
        cwd: runtimeDir, env: { ...process.env, EASYEDA_RUNTIME_DIR: runtimeDir },
        windowsHide: true, detached: true, stdio: ['ignore', log.fd, log.fd],
      });
      child.on('error', error => { startupError = error; });
      child.unref();
    } finally { await log.close(); }
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      if (startupError) throw startupError;
      const info = await probe();
      if (info) return info;
      if (child.exitCode !== null) throw new Error('Daemon exited during recovery; inspect daemon-stderr.log');
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    throw new Error('Daemon recovery timed out; inspect daemon-stderr.log');
  })();
  try { return await starting; } finally { starting = undefined; }
}

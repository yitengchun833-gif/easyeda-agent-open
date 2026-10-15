import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdir, open, readFile } from 'node:fs/promises';
import path from 'node:path';

const expectedVersion = `v${JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8')).version}`;
export const recoveryRevision = 'health-recovery-20261014';
let starting;

// Called before health reads only. Never replay a failed action or restart a live daemon.
export async function ensureDaemon(options = {}) {
  const url = options.url || process.env.EASYEDA_DAEMON_URL || 'http://127.0.0.1:60932';
  const endpoint = new URL(url);
  if (endpoint.protocol !== 'http:' || endpoint.hostname !== '127.0.0.1' || !endpoint.port) throw new Error('Daemon recovery supports explicit local loopback ports only');
  const runtimeDir = options.runtimeDir || process.env.EASYEDA_RUNTIME_DIR || fileURLToPath(new URL('../../.runtime', import.meta.url));
  const binary = options.binary || process.env.EASYEDA_OPEN_BIN || process.env.EASYEDA_BIN || fileURLToPath(new URL('../../bin/easyeda.exe', import.meta.url));
  async function probe() {
    let response;
    try { response = await fetch(new URL('/health', endpoint), { signal: AbortSignal.timeout(1500) }); }
    catch (error) { if (error.cause?.code === 'ECONNREFUSED') return null; throw error; }
    const info = await response.json();
    const ownedPid = Number(await readFile(path.join(runtimeDir, 'daemon.pid'), 'utf8').catch(() => ''));
    if (!response.ok || info.service !== 'easyeda-agent' || info.version !== expectedVersion || info.pid !== ownedPid) throw new Error('Daemon endpoint belongs to a different process/version; no process was replaced');
    return info;
  }
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

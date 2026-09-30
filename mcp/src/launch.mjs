import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdir, open, readFile } from 'node:fs/promises';
import path from 'node:path';
import { daemonURL } from './open.mjs';

const expectedVersion = `v${JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8')).version}`;

process.env.EASYEDA_BIN = process.env.EASYEDA_OPEN_BIN || fileURLToPath(new URL('../../bin/easyeda.exe', import.meta.url));
process.env.EASYEDA_RUNTIME_DIR ||= fileURLToPath(new URL('../../.runtime', import.meta.url));
process.env.EASYEDA_AUDIT_DIR ||= path.join(process.env.EASYEDA_RUNTIME_DIR, 'audit');
await mkdir(process.env.EASYEDA_RUNTIME_DIR, { recursive: true });
async function health() {
  try {
    const response = await fetch(`${daemonURL}/health`, { signal: AbortSignal.timeout(1000) });
    const data = await response.json();
    if (!response.ok || data.service !== 'easyeda-agent' || data.version !== expectedVersion) throw new Error(`Port is occupied by daemon ${data.version ?? 'unknown'}; expected ${expectedVersion}. No process was replaced.`);
    return true;
  } catch (error) {
    if (error.cause?.code === 'ECONNREFUSED') return false;
    throw error;
  }
}
if (!await health()) {
  const endpoint = new URL(daemonURL);
  if (endpoint.hostname !== '127.0.0.1') throw new Error('Automatic startup supports local loopback only');
  // The daemon is shared by MCP clients and the Connector. Only explicit daemon
  // stop/restart owns its lifetime; no inherited pipe may keep an MCP alive.
  const log = await open(path.join(process.env.EASYEDA_RUNTIME_DIR, 'daemon-stderr.log'), 'a');
  let daemon;
  let startupError;
  try {
    daemon = spawn(process.env.EASYEDA_BIN, ['--ports', `${endpoint.port}-${endpoint.port}`, 'daemon', 'start', '--auto-update-skill=false', '--autosave-debounce', '0'], {
      windowsHide: true, detached: true, stdio: ['ignore', log.fd, log.fd], env: process.env,
      cwd: process.env.EASYEDA_RUNTIME_DIR,
    });
    daemon.on('error', error => { startupError = error; });
  } finally { await log.close(); }
  daemon.unref();
  const deadline = Date.now() + 10000;
  while (!await health()) {
    if (startupError) throw startupError;
    if (daemon.exitCode !== null || Date.now() > deadline) throw new Error('Open daemon did not start; inspect daemon-stderr.log');
    await new Promise(resolve => setTimeout(resolve, 100));
  }
}
await import('./server.mjs');

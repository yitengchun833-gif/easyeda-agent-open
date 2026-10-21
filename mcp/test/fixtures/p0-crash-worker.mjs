// Child-process faults adapted from the original P0 interruption tests.
import fs from 'node:fs/promises';
import { syncBuiltinESMExports } from 'node:module';
import path from 'node:path';
import { planTool, catalog, invoke, batch } from './p0-receipts.mjs';

const [mode, dir, kind = 'stage'] = process.argv.slice(2);
const file = path.join(dir, 'receipts', kind === 'stage' ? 'stage-01.json' : 'delivery.json');
const original = { open: fs.open, rename: fs.rename, readFile: fs.readFile };
const operations = [];
let pendingBytes;
const hold = async point => {
  setInterval(() => {}, 1000);
  process.send({ point, pendingBytes });
  await new Promise(() => {});
};
const fail = phase => { throw new Error(`INJECTED_${phase.toUpperCase()}_FAILURE`); };
fs.open = async (...args) => {
  const handle = await original.open(...args);
  const destination = String(args[0]);
  if (destination !== file && !(path.dirname(destination) === path.dirname(file) && destination.endsWith('.tmp'))) return handle;
  let state;
  const temporary = destination !== file;
  const selected = () => mode.startsWith('initial-') ? !temporary && state === 'pending' : state && state !== 'pending';
  return new Proxy(handle, { get(object, key) {
    if (key === 'write') return async (bytes, ...rest) => {
      state = JSON.parse(bytes.toString()).state;
      if (selected() && mode.endsWith('fail-write')) fail('write');
      if (state !== 'pending' && mode === 'torn-settle') {
        const cut = bytes.indexOf(Buffer.from('"completedAt":')) + 20;
        await object.write(bytes, 0, cut, 0);
        await object.sync();
        return hold('settle-write-torn');
      }
      return object.write(bytes, ...rest);
    };
    if (key === 'sync') return async () => {
      if (selected() && mode.endsWith('fail-sync')) fail('sync');
      await object.sync();
      if (!temporary && state === 'pending') pendingBytes = (await original.readFile(file)).toString('base64');
      if (mode === 'before-dispatch' && state === 'pending') return hold('pending-synced-before-dispatch');
      if (mode === 'temp-synced' && state !== 'pending') return hold('temporary-synced');
    };
    if (key === 'close') return async () => {
      if (selected() && mode.endsWith('fail-close')) fail('close');
      await object.close();
      if (mode === 'temp-closed' && state !== 'pending') return hold('temporary-closed');
    };
    const value = object[key];
    return typeof value === 'function' ? value.bind(object) : value;
  } });
};
fs.rename = async (from, to) => {
  if (to === file && mode.endsWith('fail-rename')) fail('rename');
  await original.rename(from, to);
  if (to === file && mode === 'after-rename') return hold('replacement-complete');
};
syncBuiltinESMExports();

const input = JSON.parse(await fs.readFile(path.join(dir, 'input.json'), 'utf8'));
if (kind === 'delivery') globalThis.fetch = async (_url, options) => {
  const command = JSON.parse(options.body);
  operations.push(command.operation);
  if (command.operation === 'visual_review') return new Response(JSON.stringify({ ok: true, generation: 1 }));
  if (command.operation !== 'finish') throw new Error('Unexpected mock request');
  await fs.writeFile(path.join(dir, 'mock-saved-state.json'), JSON.stringify({ saved: true }));
  if (mode === 'save-crash') return hold('saved-before-reply');
  if (mode === 'save-response-lost') throw new Error('INJECTED_SAVE_RESPONSE_LOST');
  return new Response(JSON.stringify({ ok: true, result: { saved: true } }));
};
try {
  const result = await planTool(input, catalog, async action => {
    operations.push(action);
    if (action === 'debug.batch') {
      await fs.writeFile(path.join(dir, 'mock-eda-state.json'), JSON.stringify({ applied: true }));
      if (mode === 'after-native') return hold('native-applied-before-reply');
      if (mode.startsWith('unknown')) throw new Error('INJECTED_NATIVE_REPLY_LOST');
      return batch();
    }
    return invoke(action);
  });
  process.send({ result, operations, pendingBytes });
} catch (error) {
  process.send({ error: error.message, operations, pendingBytes });
}
process.disconnect();

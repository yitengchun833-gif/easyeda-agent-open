import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { toMcpResult } from './core.mjs';

export const daemonURL = process.env.EASYEDA_DAEMON_URL || 'http://127.0.0.1:60932';

const clientId = `open-mcp:${randomUUID()}`;
const revisions = new Map();
export async function control(input = {}, url = daemonURL) {
  if (!input.window) throw new Error('Control requires an explicit window from health');
  const revision = input.operation === 'supersede' ? (revisions.get(input.window) ?? 0) + 1 : revisions.get(input.window) ?? 0;
  if (input.operation === 'supersede') revisions.set(input.window, revision);
  return callAction('debug.control', { window: input.window, timeoutMs: 5000,
    payload: { operation: input.operation ?? 'status', clientId: input.allClients ? undefined : clientId, requestId: input.requestId, revision } }, url);
}

// One request, no automatic replay: a transport timeout cannot cancel an EDA edit.
export async function callAction(action, input = {}, url = daemonURL) {
  const timeoutMs = input.timeoutMs ?? 60_000;
  if (!Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 600_000) throw new Error('timeoutMs must be 1000..600000');
  const payload = { ...input.payload };
  if (input.edit) payload._edit = input.edit;
  if (action !== 'debug.control') payload._execution = { clientId, revision: revisions.get(input.window) ?? 0 };
  if (input.target) payload._target = input.target;
  const id = randomUUID();
  let cancellation;
  const cancel = () => {
    if (!cancellation && action !== 'debug.control' && input.window) cancellation = callAction('debug.control', {
      window: input.window, timeoutMs: 3000, payload: { operation: 'cancel', clientId, requestId: id }
    }, url);
    return cancellation;
  };
  if (input.signal?.aborted) return toMcpResult({ ok: false, error: { id, executionState: 'not_executed', message: 'Request cancelled before send' } });
  input.signal?.addEventListener('abort', cancel, { once: true });
  try {
    const response = await fetch(`${url}/action`, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id, type: 'request', version: 'v1', action, windowId: input.window,
        timeoutMs, clientId: `open-mcp:${process.pid}`, payload }),
      signal: AbortSignal.any([AbortSignal.timeout(timeoutMs + 2000), ...(input.signal ? [input.signal] : [])]),
    });
    const chunks = [];
    let length = 0;
    for await (const chunk of response.body) {
      length += chunk.length;
      if (length > 32 * 1024 * 1024) throw new Error('Response exceeds 32 MiB; request a smaller projection');
      chunks.push(chunk);
    }
    const result = JSON.parse(Buffer.concat(chunks).toString('utf8'));
    result.requestId = id;
    return toMcpResult(response.ok ? { ok: true, result: publicReceipt(result) } : { ok: false, error: publicReceipt(result) });
  } catch (error) {
    const cancellationResult = await cancel();
    return toMcpResult({ ok: false, error: { id, message: error.message, executionState: 'unknown',
      cancellationRequested: !!cancellationResult && !cancellationResult.isError, retryable: false, recovery: 'Read current state before deciding whether to send another write.' } });
  } finally { input.signal?.removeEventListener('abort', cancel); }
}

// Full readbacks remain in the daemon/audit, not in the model's context.
export function publicReceipt(value) {
  if (Array.isArray(value)) return value.map(publicReceipt);
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value)
    .filter(([key]) => !['_readback','_baseline','_resumeBaseline','_netIndex','_scene'].includes(key))
    .map(([key,item])=>[key,key==='otherProperty'?item:publicReceipt(item)]));
  return value;
}
export async function runtimeRequest(input = {}, url = daemonURL) {
  const operation = input.operation ?? 'state';
  const timeoutMs=input.timeoutMs??65_000;
  if (!Number.isInteger(timeoutMs)||timeoutMs<1000||timeoutMs>600_000) throw new Error('timeoutMs must be 1000..600000');
  const endpoint = operation === 'state' ? `/runtime/state?${new URLSearchParams({window:input.window ?? '',primitiveIds:(input.primitiveIds ?? []).join(','),summary:input.includeObjects===true||input.primitiveIds?.length?'0':'1'})}` : '/runtime/control';
  try {
    const response = await fetch(`${url}${endpoint}`, { method:operation==='state'?'GET':'POST',
      headers:{'content-type':'application/json','X-Easyeda-Control':'1'},
      body:operation==='state'?undefined:JSON.stringify(input),
      signal:AbortSignal.any([AbortSignal.timeout(timeoutMs),...(input.signal?[input.signal]:[])]) });
    const chunks=[];let length=0;
    for await(const chunk of response.body){length+=chunk.length;if(length>32*1024*1024)throw new Error('Response exceeds 32 MiB; request primitiveIds');chunks.push(chunk)}
    const raw = Buffer.concat(chunks).toString('utf8');
    let result;try{result=JSON.parse(raw)}catch{result={message:raw}}
    return toMcpResult(response.ok?{ok:true,result:publicReceipt(result)}:{ok:false,error:publicReceipt(result)});
  } catch(error) { return toMcpResult({ok:false,error:{message:error.message,executionState:'unknown',retryable:false,recovery:'Inspect the task receipt; a control timeout is not proof that no continuation was sent.'}}); }
}

export async function apiReference(className, method) {
  if (!/^[A-Za-z][A-Za-z0-9_]*$/.test(className) || (method && !/^[A-Za-z][A-Za-z0-9_]*$/.test(method))) throw new Error('Invalid class or method name');
  const source = await readFile(new URL(`../../api-reference/classes/${className}.md`, import.meta.url), 'utf8');
  if (!method) return { source: 'easyeda/easyeda-api-skill@122fb27e', className, markdown: source };
  const section = source.split(/(?=^### )/m).find(s => s.split('\n')[0].trim().toLowerCase() === `### ${method.toLowerCase()}`);
  if (!section) throw new Error(`Method ${method} not found in ${className}`);
  return { source: 'easyeda/easyeda-api-skill@122fb27e', className, method, markdown: section };
}

export function screenshotCode(fit = false) {
  return `${fit ? 'await eda.dmt_EditorControl.zoomToAllPrimitives(); await new Promise(r => setTimeout(r, 200));' : ''}
const image = await eda.dmt_EditorControl.getCurrentRenderedAreaImage();
if (!(image instanceof Blob) || !image.size) throw new Error('Editor returned no image');
return image;`;
}

export function imageResult(result) {
  if (result.isError) return result;
  const envelope = result.structuredContent;
  const value = envelope?.result?.value;
  if (!value?.base64 || !value.mimeType?.startsWith('image/')) {
    return toMcpResult({ ok: false, error: { message: 'No image returned', response: envelope } });
  }
  const { base64, ...metadata } = value;
  return { content: [
    { type: 'image', data: base64, mimeType: value.mimeType },
    { type: 'text', text: JSON.stringify({ ...envelope, result: { value: metadata } }) },
  ], isError: false };
}

export function snapshotSteps(domain, routing = false, detail = false, allPages = false, options = {}) {
  const names = domain === 'schematic' ? ['document.current', detail || options.primitiveIds ? 'schematic.components.list' : 'schematic.read']
    : ['document.current', 'pcb.components.list', 'pcb.layers.list', 'pcb.nets.list',
      ...(routing ? ['pcb.line.list', 'pcb.via.list', 'pcb.pour.list'] : [])];
  return names.map(action => ({ action, payload: action === 'schematic.read' ? { includeCheck: false, allPages } : action === 'schematic.components.list' ? { allPages, includePins: true, includeDeviceIdentity: true, includeBBox: true, includeWires: true, includeNetIndex:true,includeTexts:options.includeTexts===true, ...(options.primitiveIds?{primitiveIds:options.primitiveIds}:{includePagePrimitives:!allPages}) } : {} }));
}

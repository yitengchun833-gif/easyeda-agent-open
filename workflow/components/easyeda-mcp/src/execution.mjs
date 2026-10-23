import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { isAbsolute, join } from 'node:path';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { buildActionCallArgs, buildCallArgs, runEasyeda, toMcpResult } from './core.mjs';
import { runtimeGate, snapshotStamp } from './runtime.mjs';

const verifier = fileURLToPath(new URL('../../../skills/evidence-to-delivery/scripts/workflow.py', import.meta.url));
export function executionCheck(root, input, signal) {
  return new Promise((resolve, reject) => {
    const child = execFile(process.env.PYTHON_BIN || 'python', ['-X', 'utf8', '-B', verifier, '--root', root, 'execution'],
      { windowsHide: true, encoding: 'utf8', timeout: 60_000, maxBuffer: 2 * 1024 * 1024, signal },
      (error, stdout, stderr) => {
        if (error) return reject(new Error(stderr.trim() || error.message));
        try { resolve(JSON.parse(stdout)); } catch { reject(new Error('Invalid execution check response')); }
      });
    child.stdin.on('error', () => {});
    child.stdin.end(JSON.stringify(input));
  });
}

export const snapshotPayload = { includePins: true, includePinNets: true, includeBBox: true,
  includeWires: true, includePagePrimitives: true, includeDeviceIdentity: true };

// One MCP request, existing per-action journal; never cache across requests.
export async function guardedSchematicAction(action, input, options = {}) {
  if (input.execution?.action_ids === undefined) return guardedAction(action, input, options);
  const { action_ids: ids, ...contract } = input.execution;
  if (!Array.isArray(ids) || !ids.length || new Set(ids).size !== ids.length ||
      ids.some(id => typeof id !== 'string' || !id) || contract.action_id || contract.recover) {
    throw new Error('Use distinct action_ids or single-action recovery, not both');
  }
  for (const key of ['root', 'requirements', 'plan', 'baseline']) {
    if (typeof contract[key] !== 'string' || !isAbsolute(contract[key])) throw new Error('Absolute execution paths required');
  }
  buildActionCallArgs(action, input);
  const plan = JSON.parse((await readFile(contract.plan, 'utf8')).replace(/^\uFEFF/, ''));
  const requirements = JSON.parse((await readFile(contract.requirements, 'utf8')).replace(/^\uFEFF/, ''));
  if (requirements.execution_rules?.edit_scope?.mode !== 'layout' &&
      !(plan.rewire === true && requirements.execution_rules?.edit_scope?.mode === 'design')) throw new Error('Grouped edits require layout or confirmed design rewire scope');
  const rewire = plan.rewire === true && requirements.execution_rules?.edit_scope?.rewire;
  const check = options.check ?? executionCheck;
  const inspected = await check(contract.root, { ...contract, operation: 'inspect' }, options.signal);
  if (!inspected.passed || !isDeepStrictEqual(inspected.runtime, plan.runtime) ||
      !isDeepStrictEqual(inspected.route, plan.route) ||
      ['project', 'doc'].some(k => plan.route?.[k] !== input[k])) throw new Error('Group plan/route mismatch');
  const selected = ids.map(id => plan.actions[inspected.actions.indexOf(id)]);
  for (const item of selected) {
    const fields = item?.action === 'schematic.component.modify' ? ['x','y','rotation','mirror'] :
      item?.action === 'schematic.attribute.modify' ? ['x','y','rotation'] : [];
    if (fields.length) {
      if (!item.payload?.primitiveId || !Object.keys(item.payload.patch ?? {}).length ||
          Object.keys(item.payload.patch).some(k => !fields.includes(k))) throw new Error('Invalid geometry group action');
    } else if (!rewire || !['schematic.primitives.delete','schematic.wire.create','schematic.netflag.create'].includes(item?.action)) {
      throw new Error('Group supports geometry only unless confirmed whole-page rewire');
    }
  }
  if (rewire && ids.some((id, i) => i && inspected.actions.indexOf(id) !== inspected.actions.indexOf(ids[i-1]) + 1)) {
    throw new Error('Rewire group must use contiguous plan actions');
  }
  if (selected[0].action !== action.name || !isDeepStrictEqual(selected[0].payload, input.payload)) throw new Error('First group action/payload mismatch');
  const session = { plan, requirements }, results = [];
  for (const [index, item] of selected.entries()) {
    try {
      const response = await guardedAction({ name: item.action, mutates: true }, { ...input,
        payload: item.payload, execution: { ...contract, action_id: ids[index] } }, options, session);
      results.push(response.structuredContent);
      const interim = rewire && response.structuredContent?.reconciliation?.effective_state === 'APPLIED_INTERMEDIATE' &&
        response.structuredContent?.outcome?.transport_status === 'ok' && !response.structuredContent?.recoveryError;
      if ((response.isError && !interim) || !['APPLIED_VERIFIED', ...(rewire ? ['APPLIED_INTERMEDIATE'] : [])]
          .includes(response.structuredContent?.reconciliation?.effective_state)) break;
    } catch (error) {
      results.push({ action_id: ids[index], error: error.message, recovery_required: true });
      break;
    }
  }
  const applied = results.filter(r => ['APPLIED_VERIFIED', ...(rewire ? ['APPLIED_INTERMEDIATE'] : [])]
    .includes(r.reconciliation?.effective_state)).length;
  const complete = applied === ids.length && results.at(-1)?.reconciliation?.effective_state === 'APPLIED_VERIFIED';
  const result = toMcpResult({ ok: true, result: { status: complete ? 'verified' : applied === ids.length && rewire ? 'intermediate' : 'stopped',
    applied, requested: ids.length, actions: results, unattempted: ids.slice(results.length) } });
  result.isError = !complete;
  return result;
}

// Extends the existing typed dispatch; workflow.py owns all durable state.
async function guardedAction(action, input, { mcp, signal, run = runEasyeda, check = executionCheck } = {}, session) {
  buildActionCallArgs(action, input); // Preserve routing rejection before any work.
  const contract = input.execution;
  if (!contract || !['root', 'requirements', 'plan', 'baseline'].every(k => typeof contract[k] === 'string' && isAbsolute(contract[k]))) {
    throw new Error('Schematic writes require execution {root,requirements,plan,baseline,action_id}; all paths absolute. Reuse existing workflow state.');
  }
  if (Object.keys(contract).some(k => !['root','requirements','plan','baseline','action_id','recover'].includes(k))) throw new Error('Unknown execution contract fields');
  const plan = JSON.parse((await readFile(contract.plan, 'utf8')).replace(/^\uFEFF/, ''));
  const requirements = JSON.parse((await readFile(contract.requirements, 'utf8')).replace(/^\uFEFF/, ''));
  // Native identity is compared in full; layout does not need online library replay resolution.
  const payload = { ...snapshotPayload, includeDeviceIdentity: requirements.execution_rules?.edit_scope?.mode !== 'layout' };
  if (session && (!isDeepStrictEqual(plan, session.plan) || !isDeepStrictEqual(requirements, session.requirements))) throw new Error('Group inputs changed; stop and replan');
  const health = session?.health ?? await run(['daemon', 'health'], 30_000, signal);
  const runtime = runtimeGate(health.result, plan.runtime, input, mcp);
  if (!health.ok || !runtime.passed) throw new Error('Runtime gate: ' + runtime.issues.join(', '));
  const route = { project: input.project, doc: input.doc, window: runtime.window.windowId };
  const directory = join(contract.root, '.workflow', 'evidence');
  // No new project initialization or second journal.
  await readFile(join(contract.root, '.workflow', 'state.json'));
  await mkdir(directory, { recursive: true });
  const attempt = randomUUID(), timings = {}, started = performance.now();
  const capture = async phase => {
    const t = performance.now();
    const execution = await run(buildCallArgs('schematic.components.list', { ...route, payload }), undefined, signal);
    timings[phase] = performance.now() - t;
    const file = join(directory, attempt + '-' + phase + '.json');
    const packet = execution.ok ? execution.result : execution.error;
    await writeFile(file, JSON.stringify(packet), { flag: 'wx' });
    if (!snapshotStamp(packet).freshness_available) throw new Error('Fresh ordered snapshot unavailable; evidence retained at ' + file);
    return { packet, file };
  };
  const before = session?.after ?? await capture('before');
  if (session) { session.health = health; delete session.after; }
  const data = { ...contract, ...route, action: action.name, payload: input.payload ?? {}, snapshot: before.packet, snapshot_file: before.file };
  if (contract.recover === true) {
    const recovered = await check(contract.root, { ...data, operation: 'recover' }, signal);
    const result = toMcpResult({ ok: true, result: { recovery: recovered, runtime, timings, mutations: 0 } });
    result.isError = !recovered.passed;
    return result;
  }
  const prepared = await check(contract.root, { ...data, operation: 'prepare' }, signal);
  if (signal?.aborted) throw new Error('Cancelled before dispatch; prepared action remains unknown until readback');
  const t = performance.now();
  const execution = await run(buildActionCallArgs(action, { ...route, payload: { ...input.payload,
    expectedRuntimeInstance: prepared.freshness.runtimeInstance, expectedMutationSeq: prepared.freshness.mutationSeq,
  } }), undefined, signal);
  execution.requiresReconciliation = true;
  timings.write = performance.now() - t;
  const receipt = join(directory, attempt + '-receipt.json');
  await writeFile(receipt, JSON.stringify(execution), { flag: 'wx' });
  // Even transport failure can have landed. Cancellation leaves durable UNKNOWN;
  // the next recover call reads first and never replays a verified action.
  try {
    const after = await capture('after');
    const result = await check(contract.root, { ...data, operation: 'record', snapshot: after.packet,
      snapshot_file: after.file, receipt, transport_status: execution.ok ? 'ok' : 'error' }, signal);
    execution.reconciliation = result;
    execution.executionResult = result;
    if (session && result.passed && ['APPLIED_VERIFIED','APPLIED_INTERMEDIATE'].includes(result.effective_state)) session.after = after;
  } catch (error) {
    execution.recoveryError = error.message;
  }
  timings.total = performance.now() - started;
  const response = toMcpResult(execution, true);
  const summary = { outcome: response.structuredContent?.outcome, action_id: prepared.action_id,
    reconciliation: execution.executionResult, recoveryError: execution.recoveryError,
    receipt, timings, runtime: { mcpBuild: mcp.build, window: route.window } };
  response.structuredContent = summary;
  response.content = [{ type: 'text', text: JSON.stringify(summary) }];
  return response;
}

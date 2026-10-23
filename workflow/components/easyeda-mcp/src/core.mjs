import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { open, readFile } from 'node:fs/promises';
import { isAbsolute } from 'node:path';
import { createHash } from 'node:crypto';
import { snapshotStamp } from './runtime.mjs';

const execFileAsync = promisify(execFile);

export const DOMAIN_NAMES = [
  'artifact',
  'board',
  'document',
  'pcb',
  'project',
  'schematic',
  'system',
];

export function easyedaBinary() {
  return process.env.EASYEDA_BIN || 'easyeda';
}

export async function runEasyeda(args, timeoutMs = 300_000, signal) {
  try {
    const { stdout, stderr } = await execFileAsync(easyedaBinary(), args, {
      encoding: 'utf8',
      maxBuffer: 32 * 1024 * 1024,
      timeout: timeoutMs,
      signal,
      windowsHide: true,
    });
    return {
      ok: true,
      result: parseOutput(stdout),
      stderr: stderr.trim() || undefined,
    };
  }
  catch (error) {
    return {
      ok: false,
      error: {
        message: error.message,
        code: error.code ?? null,
        stdout: parseOutput(error.stdout || ''),
        stderr: String(error.stderr || '').trim() || undefined,
      },
    };
  }
}

export function parseOutput(stdout) {
  const text = String(stdout).trim();
  if (!text) return null;
  try {
    return JSON.parse(text);
  }
  catch {
    return text;
  }
}

export function filterActions(actions, { domain, search, mutates } = {}) {
  const needle = String(search || '').trim().toLowerCase();
  return actions.filter((action) => {
    if (domain && action.domain !== domain) return false;
    if (typeof mutates === 'boolean' && action.mutates !== mutates) return false;
    if (!needle) return true;
    return [action.name, action.description, ...(action.inputs || [])]
      .join(' ')
      .toLowerCase()
      .includes(needle);
  });
}

// Project creation targets a connector window, not a document that does not exist yet.
// Project transfer uses explicit window/UUID guards; other mutations retain document pinning.
export function buildActionCallArgs(action, input = {}) {
  if (action.name === 'project.create') {
    if (typeof input.window !== 'string' || !input.window.trim()) {
      throw new Error('project.create requires an explicit window from easyeda_health');
    }
    if (input.project || input.doc) {
      throw new Error('project.create does not accept project or doc routing; use payload.friendlyName for the new project');
    }
  }
  else if (action.name === 'project.open' || action.name === 'project.export') {
    if (typeof input.window !== 'string' || !input.window.trim() || input.project || input.doc) {
      throw new Error('Project transfer requires an explicit window and payload.projectUuid; omit project/doc routing');
    }
    if (typeof input.payload?.projectUuid !== 'string' || !input.payload.projectUuid.trim()) {
      throw new Error('payload.projectUuid is required');
    }
    if (action.name === 'project.open' && input.payload.allowDiscardUnsaved !== true) {
      throw new Error('Save documents first; payload.allowDiscardUnsaved must be true');
    }
  }
  else if (action.mutates && (!input.project || !input.doc)) {
    throw new Error(`mutating action ${action.name} requires both project and doc`);
  }
  return buildCallArgs(action.name, input);
}

export function buildCallArgs(action, input = {}) {
  // Native identity is sufficient for drawing. Library replay and text metrology are opt-in.
  const payload = action === 'schematic.components.list' ? {
    includeDeviceIdentity: false,
    ...(input.payload?.includePagePrimitives === true ? { measureTextIds: [] } : {}),
    ...input.payload,
  } : input.payload;
  const args = [];
  if (input.project) args.push('--project', input.project);
  if (input.doc) args.push('--doc', input.doc);
  args.push('call', action);
  if (payload && Object.keys(payload).length > 0) {
    args.push('--payload', JSON.stringify(payload));
  }
  if (input.window) args.push('--window', input.window);
  return args;
}

// Reuse the installed CLI's action/API indexes; this is discovery, never a live capability claim.
export async function discoverActions(actions, input = {}, run = runEasyeda) {
  const started = performance.now();
  const { domain, mutates, search = '', limit = 10, offset = 0, includeApi = false } = input;
  if (Object.keys(input).some(k => !['domain','mutates','search','limit','offset','includeApi'].includes(k)) ||
      (domain !== undefined && !DOMAIN_NAMES.includes(domain)) ||
      (mutates !== undefined && typeof mutates !== 'boolean') ||
      typeof search !== 'string' || search.length > 200 ||
      typeof includeApi !== 'boolean' || !Number.isInteger(limit) || limit < 1 || limit > 50 ||
      !Number.isInteger(offset) || offset < 0) throw new Error('Invalid action discovery filters/pagination');
  const filtered = filterActions(actions, { domain, mutates, search });
  const exact = filtered.filter(a => a.name.toLowerCase() === search.trim().toLowerCase());
  const matches = exact.length ? exact : filtered;
  const selected = matches.slice(offset, offset + limit);
  const report = {
    status: matches.length ? 'typed_actions_found' : 'not_found_in_action_catalog',
    count: matches.length, returned: selected.length,
    actions: selected,
    nextOffset: offset + selected.length < matches.length ? offset + selected.length : null,
    evidence: 'Installed CLI action catalog, loaded by this MCP process; not a live editor test.',
    liveVerified: false, apiReferences: [], apiStatus: 'not_requested', designMutations: 0,
    nextAction: matches.length ? 'Reuse the matching typed action. Inspect its scope and read the target before writing.' :
      'No matching catalog entry is not proof of unsupported software capability. Narrow the query or inspect the native API.',
  };
  if (search.trim() && (!matches.length || includeApi)) {
    const result = await run(['api', 'search', search.trim(), '--json', '--limit', String(limit)], 15_000);
    if (result.ok && Array.isArray(result.result)) {
      report.apiReferences = result.result;
      report.apiStatus = 'reference_index_only';
      report.apiScope = 'Native API references do not establish a callable typed action, read-only status, cross-project support or runtime success. Domain/mutation filters apply only to actions.';
      if (!matches.length && result.result.length) {
        report.status = 'native_api_reference_found';
        report.nextAction = 'Native API reference found; verify scope and existing adapter before adding a minimal typed entry. Do not rebuild the design or execute arbitrary code from this result.';
      }
    } else {
      report.apiStatus = 'unavailable';
      report.apiError = 'Native API index lookup failed; no automatic retry and no unsupported-capability conclusion.';
    }
  }
  report.timing = { totalMs: performance.now() - started };
  return report;
}

export function buildRawArgs(input = {}) {
  if (input.allowArbitraryCode !== true) throw new Error('allowArbitraryCode must be true');
  if (typeof input.code !== 'string' || !input.code.trim() || input.code.length > 16_000) {
    throw new Error('code must contain 1 to 16000 characters');
  }
  for (const key of ['project', 'doc']) {
    if (typeof input[key] !== 'string' || !input[key].trim()) throw new Error(`${key} is required`);
  }
  return buildActionCallArgs({ name: 'debug.exec_js', mutates: true }, {
    project: input.project,
    doc: input.doc,
    window: input.window,
    payload: { code: input.code },
  });
}

export function buildWorkflowArgs(input) {
  if (!input.project) throw new Error('project is required for workflow operations');
  const args = ['--project', input.project];
  if (input.doc) args.push('--doc', input.doc);
  args.push('workflow', input.operation);
  switch (input.operation) {
    case 'init':
      break;
    case 'status':
      args.push('--json');
      if (input.reconcile) args.push('--reconcile');
      break;
    case 'advance':
      if (Number.isInteger(input.minScore)) args.push('--min-score', String(input.minScore));
      if (Number.isInteger(input.maxCrossings)) args.push('--max-crossings', String(input.maxCrossings));
      break;
    case 'confirm':
      if (!['layout', 'outline'].includes(input.confirmation)) {
        throw new Error('confirmation must be layout or outline');
      }
      args.push(input.confirmation);
      if (input.note) args.push('--note', input.note);
      break;
    case 'reset':
      if (input.resetAll === true) args.push('--all');
      else if (input.resetFrom) args.push('--from', input.resetFrom);
      else throw new Error('reset requires resetAll=true or resetFrom');
      break;
    default:
      throw new Error(`unsupported workflow operation: ${input.operation}`);
  }
  return args;
}

export function buildBlocksArgs(input) {
  switch (input.operation) {
    case 'list':
      return ['blocks', 'ls'];
    case 'search':
      if (!input.query) throw new Error('query is required for blocks search');
      return ['blocks', 'search', input.query];
    case 'show':
      if (!input.id) throw new Error('id is required for blocks show');
      return ['blocks', 'show', input.id];
    default:
      throw new Error(`unsupported blocks operation: ${input.operation}`);
  }
}

// Keep transport, application and verification independent. Absence of a negative
// flag is not readback proof (upstream writehealth also separates these concerns).
export function mutationOutcome(execution) {
  const envelope = execution.ok ? execution.result : execution.error?.stdout;
  const value = envelope?.result ?? envelope;
  const nonempty = x => Array.isArray(x) ? x.length > 0 : typeof x === 'number' ? x > 0 : x === true;
  const partial = value?.partial === true || nonempty(value?.notApplied) ||
    nonempty(value?.survived) || nonempty(value?.survivedIds) || nonempty(value?.survivedTotal) || value?.deleted === false || value?.disconnected === false;
  let apply_state = partial ? 'partial' : value?.writeState === 'not_applied' ? 'not_applied' : 'unknown';
  let verification_state = value?.verified === false ? 'failed' : 'unknown';
  if (!partial && value?.writeState !== 'unknown' && value?.verified === true && envelope?.ok !== false) {
    apply_state = 'applied'; verification_state = 'verified';
  }
  // Only the controlled path can attach this fresh, identity-bound reconciliation.
  const reconciliation = execution.reconciliation;
  if (reconciliation?.verified === true && reconciliation?.snapshot_id && reconciliation?.action_id) {
    apply_state = 'applied'; verification_state = 'verified';
  } else if (reconciliation?.effective_state === 'APPLIED_INTERMEDIATE' && reconciliation?.snapshot_id && reconciliation?.action_id) {
    apply_state = 'applied'; verification_state = 'intermediate';
  } else if (reconciliation?.snapshot_id && reconciliation?.action_id && ['NOT_APPLIED','PARTIAL'].includes(reconciliation.effective_state)) {
    apply_state = reconciliation.effective_state === 'NOT_APPLIED' ? 'not_applied' : 'partial';
    verification_state = reconciliation.verification_state;
  }
  if (execution.requiresReconciliation && !reconciliation?.snapshot_id) {
    apply_state = 'unknown'; verification_state = 'unknown';
  } else if (execution.requiresReconciliation && reconciliation?.effective_state === 'UNKNOWN') {
    apply_state = 'unknown'; verification_state = 'unknown';
  }
  if (execution.ok && envelope?.ok === true && value?.saved === true && value?.workflowSave?.passed === true) {
    apply_state = 'applied'; verification_state = 'pending_reopen';
  }
  return { transport_status: execution.ok ? 'ok' : 'error', apply_state, verification_state,
    effective_state: apply_state === 'applied' && verification_state === 'verified' ? 'APPLIED_VERIFIED' :
      verification_state === 'intermediate' ? 'APPLIED_INTERMEDIATE' :
      verification_state === 'pending_reopen' ? 'SAVE_ACCEPTED_PENDING_REOPEN' :
      apply_state === 'not_applied' ? 'NOT_APPLIED' : apply_state === 'partial' ? 'PARTIAL' : 'UNKNOWN' };
}

export function toMcpResult(execution, mutates = false) {
  const rawValue = execution.ok ? execution.result : execution.error;
  // Process exit, action outcome and actual editor state are distinct evidence.
  // Inspect only the protocol envelope, never arbitrary nested design/check data.
  const actionFailed = execution.ok && rawValue?.ok === false;
  const outcome = mutates ? mutationOutcome(execution) : null;
  const partial = outcome?.apply_state === 'partial';
  const baseValue = execution.ok && execution.stderr
    ? { result: rawValue, warnings: execution.stderr }
    : rawValue;
  const needsReadback = mutates && !['APPLIED_VERIFIED', 'APPLIED_INTERMEDIATE', 'NOT_APPLIED'].includes(outcome.effective_state);
  const value = mutates ? {
    ...(baseValue && typeof baseValue === 'object' && !Array.isArray(baseValue) ? baseValue : { result: baseValue }),
    outcome,
    ...(needsReadback ? { recovery: {
      outcome: partial ? 'partial' : 'unknown',
      readbackRequired: true,
      retryPolicy: 'readback_before_deciding',
      nextAction: 'Read the actual target/artifact and completed actions; resume only remaining actions. A failed or cancelled CLI does not prove the remote operation was rolled back.',
    } } : {}),
  } : baseValue;
  const result = {
    content: [{ type: 'text', text: JSON.stringify(value, null, 2) }],
    isError: mutates ? !['APPLIED_VERIFIED', 'SAVE_ACCEPTED_PENDING_REOPEN'].includes(outcome.effective_state) : !execution.ok || actionFailed,
  };
  if ((execution.ok || mutates) && value && typeof value === 'object' && !Array.isArray(value)) {
    result.structuredContent = value;
  }
  return result;
}

// Project-level transfer has no existing-document routing prerequisite.
export function buildProjectTransferArgs(input = {}) {
  for (const key of ['window', 'projectUuid']) {
    if (typeof input[key] !== 'string' || !input[key].trim()) throw new Error(`${key} is required`);
  }
  if (input.project || input.doc) throw new Error('Use projectUuid/window, not project/doc routing');
  const args = ['project', input.operation, '--window', input.window, '--project-uuid', input.projectUuid];
  if (input.operation === 'open') {
    if (input.allowDiscardUnsaved !== true) throw new Error('Save all documents first and explicitly acknowledge allowDiscardUnsaved');
    if (input.out) throw new Error('out applies only to export');
    args.push('--allow-discard-unsaved');
    if (input.pageUuid) args.push('--page-uuid', input.pageUuid);
  } else if (input.operation === 'export') {
    if (typeof input.out !== 'string' || !input.out.toLowerCase().endsWith('.epro2')) throw new Error('out must be a new .epro2 file');
    if (input.pageUuid) throw new Error('pageUuid applies only to open');
    if (input.allowDiscardUnsaved) throw new Error('allowDiscardUnsaved applies only to open');
    args.push('--out', input.out);
  } else throw new Error('operation must be open or export');
  return args;
}

// Keep full readback on disk; both MCP text and structuredContent contain only this summary.
export async function localSchematicSnapshot(execution, { outputFile, baselineFile }) {
  if (typeof outputFile !== 'string' || !isAbsolute(outputFile)) throw new Error('outputFile must be an absolute new local JSON file');
  if (baselineFile !== undefined && (typeof baselineFile !== 'string' || !isAbsolute(baselineFile))) throw new Error('baselineFile must be an absolute JSON path');
  const rawPacket = execution.ok ? execution.result : execution.error;
  const packet = execution.stderr && rawPacket && typeof rawPacket === 'object' ? { ...rawPacket, warnings: execution.stderr } : rawPacket;
  // Archive the actual read before optional comparison can fail; never refetch it merely to repair a baseline.
  const bytes = Buffer.from(JSON.stringify(packet));
  const file = await open(outputFile, 'wx'); // Never overwrite prior evidence, including a symlink.
  try { await file.writeFile(bytes); } finally { await file.close(); }
  const evidence = { path: outputFile, bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') };
  const value = execution.ok && packet?.ok === true ? packet.result : null;
  const summary = { status: value && value.partial !== true && packet.partial !== true ? 'snapshot_recorded' : 'read_failed', context: packet?.context,
    snapshot: snapshotStamp(value ? packet : packet?.stdout ?? packet),
    timing: value?.timing ?? null,
    evidence,
    saveGuardVersion: value?.saveGuardVersion ?? null, pageStateSha256: value?.pageStateSha256 ?? null,
    coverage: { pins: value?.pinNetsAvailable === true, wires: value?.wiresAvailable === true,
      primitives: !!value?.pagePrimitives, textMeasurements: !!value?.textMeasurements },
    counts: { components: value?.components?.length ?? null, wireSegments: value?.wires?.length ?? null,
      attributes: value?.pagePrimitives?.attributes?.length ?? null, texts: value?.textMeasurements?.texts?.length ?? null,
      textReadErrors: value?.textMeasurements?.errors?.length ?? null,
      unknownVisibility: value?.pagePrimitives?.attributes?.filter(t =>
        t.KeyVisible !== true && t.ValueVisible !== true && !(t.KeyVisible === false && t.ValueVisible === false)).length ?? null },
    limits: 'Local evidence only; identity, electrical, scope and layout verification use this file. Counts are not acceptance.' };
  if (summary.status === 'read_failed') {
    const error = packet?.stdout?.error ?? packet?.error;
    summary.error = { code: error?.code ?? packet?.code ?? null,
      message: String(error?.message ?? packet?.stderr ?? packet?.message ?? 'Read failed; inspect the retained evidence.').slice(0, 500) };
  }
  if (baselineFile !== undefined) {
    try {
    if (summary.status === 'read_failed') throw new Error('Current read incomplete; comparison unavailable');
    const bytes = await readFile(baselineFile);
    const before = JSON.parse(bytes.toString('utf8').replace(/^\uFEFF/, ''));
    if (!value || before.ok !== true || !before.result?.pagePrimitives || !value.pagePrimitives ||
      !before.context?.projectUuid || before.context.projectUuid !== packet.context?.projectUuid ||
      !before.context?.documentUuid || before.context.documentUuid !== packet.context?.documentUuid) {
      throw new Error('baseline/current snapshot missing full inventory or target mismatch');
    }
    const changes = [];
    const index = rows => {
      if (!Array.isArray(rows)) throw new Error('Incomplete primitive inventory');
      const ids = new Map(rows.map(row => [row.primitiveId, row]));
      if (ids.size !== rows.length || [...ids.keys()].some(id => typeof id !== 'string' || !id)) throw new Error('Ambiguous primitive inventory');
      return ids;
    };
    for (const kind of new Set([...Object.keys(before.result.pagePrimitives), ...Object.keys(value.pagePrimitives)])) {
      const old = index(before.result.pagePrimitives[kind]), current = index(value.pagePrimitives[kind]);
      for (const id of new Set([...old.keys(), ...current.keys()])) {
        const a = old.get(id), b = current.get(id);
        const fields = a && b ? [...new Set([...Object.keys(a), ...Object.keys(b)])].filter(k => JSON.stringify(a[k]) !== JSON.stringify(b[k])) : [];
        if (!a || !b || fields.length) changes.push({ kind, id, change: !a ? 'added' : !b ? 'removed' : 'modified', fields });
      }
    }
    summary.diff = { status: 'available', baselineFile, baselineSha256: createHash('sha256').update(bytes).digest('hex'),
      total: changes.length, items: changes.slice(0, 30), truncated: changes.length > 30,
      limits: 'Object/field differences; does not certify per-pin electrical equality.' };
    } catch (error) {
      summary.diff = { status: 'unavailable', baselineFile, message: String(error.message).slice(0, 500),
        retryPolicy: 'reuse_recorded_snapshot' };
    }
  }
  const result = toMcpResult({ ok: true, result: summary });
  result.content = [{ type: 'text', text: JSON.stringify(summary) }];
  result.isError = !execution.ok || packet?.ok !== true || summary.status === 'read_failed' || summary.diff?.status === 'unavailable';
  return result;
}

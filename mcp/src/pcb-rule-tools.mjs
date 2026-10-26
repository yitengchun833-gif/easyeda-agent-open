import path from 'node:path';
import { runEasyeda } from './core.mjs';
import { callAction } from './open.mjs';

const ruleAction = 'pcb.drc.rules.set';
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);

// Display JSON changes only; interpretation and rule verification stay in the native action.
function changes(before, after, at = '') {
  if (object(before) && object(after)) return [...new Set([...Object.keys(before), ...Object.keys(after)])].sort()
    .flatMap(key => changes(before[key], after[key], `${at}/${key.replaceAll('~', '~0').replaceAll('/', '~1')}`));
  if (JSON.stringify(before) === JSON.stringify(after)) return [];
  return [{ path: at, operation: before === undefined ? 'add' : after === undefined ? 'remove' : 'replace', before, after }];
}

export function hasRuleFile(action, payload) {
  return action === ruleAction && object(payload) && Object.hasOwn(payload, 'fromFile');
}

// Normalize all file inputs before the single existing native dispatch; never replay a write.
export async function callWithRuleFiles(action, input, { run = runEasyeda, call = callAction } = {}) {
  const batch = action === 'debug.batch';
  const steps = batch ? input.payload?.steps : [{ action, payload: input.payload }];
  const imports = (steps ?? []).flatMap((step, index) => hasRuleFile(step.action, step.payload) ? [{ index, payload: step.payload }] : []);
  if (!imports.length) return call(action, input);
  if (typeof input.window !== 'string' || !input.window.trim() || input.project || input.doc) {
    throw new Error('Rule file import requires window and optional target; omit project/doc navigation');
  }
  if (batch && input.payload.dryRun === true) throw new Error('Batch dryRun only lists actions; use each rule step payload.dryRun:true for actual rule differences');
  const prepared = steps.map(step => ({ ...step }));
  const reports = [];
  for (const { index, payload } of imports) {
    if (typeof payload.fromFile !== 'string' || !path.isAbsolute(payload.fromFile)) throw new Error('fromFile must be an absolute local rule JSON path');
    if (typeof payload.dryRun !== 'boolean') throw new Error('Rule file import requires explicit dryRun:true or dryRun:false');
    if (payload.module !== undefined && payload.module !== 'ruleConfig') throw new Error('Only module ruleConfig is supported');
    if (Object.keys(payload).some(key => !['fromFile', 'module', 'dryRun'].includes(key))) throw new Error('fromFile cannot be combined with inline rule fields or other payload options');
    const args = ['pcb', 'drc-rules-set', '--from', payload.fromFile, '--parse-only'];
    if (payload.module) args.push('--module', payload.module);
    const normalized = await run(args, 30_000);
    if (!normalized.ok) throw new Error(`Rule file parsing failed before native dispatch: ${normalized.error?.stderr ?? normalized.error?.message ?? 'unknown CLI error'}`);
    const value = normalized.result;
    if (!object(value?.payload) || !object(value.payload.ruleConfiguration)
      || value.unprocessedModules != null && (!Array.isArray(value.unprocessedModules) || value.unprocessedModules.some(name => typeof name !== 'string'))) {
      throw new Error('Companion CLI did not return normalized rule payload; update the matching CLI before importing');
    }
    prepared[index].payload = { ...value.payload, dryRun: payload.dryRun };
    reports.push({ index, fromFile: payload.fromFile, unprocessedModules: value.unprocessedModules ?? [], dryRun: payload.dryRun });
  }
  const response = await call(action, { ...input, payload: batch ? { ...input.payload, steps: prepared } : prepared[0].payload });
  const receipt = response.structuredContent;
  const result = receipt?.result;
  const ruleImports = reports.map(report => {
    const step = batch ? result?.results?.find(step => step.index === report.index && step.action === ruleAction) : receipt;
    const outcome = step?.result;
    const confirmed = step?.ok === true && outcome?.partial !== true && (report.dryRun
      ? outcome?.dryRun === true && object(outcome.before) && object(outcome.requested)
      : outcome?.verified === true);
    return { ...report, status: confirmed ? report.dryRun ? 'previewed' : 'verified' : 'unconfirmed',
      ...(confirmed && report.dryRun ? { changes: changes(outcome.before, outcome.requested) } : {}) };
  });
  return { ...response, isError: response.isError === true || ruleImports.some(report => report.status === 'unconfirmed'),
    content: [...response.content, { type: 'text', text: JSON.stringify({ ruleImports }) }],
    ...(receipt ? { structuredContent: { ...receipt, ruleImports } } : {}) };
}

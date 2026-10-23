import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { isAbsolute, dirname, resolve } from 'node:path';
import { readFile } from 'node:fs/promises';
import { runtimeGate } from './runtime.mjs';
import { buildCallArgs, runEasyeda } from './core.mjs';

const verifier = fileURLToPath(new URL('../../../skills/evidence-to-delivery/scripts/workflow.py', import.meta.url));

export function verifySave(report, snapshot, signal) {
  return new Promise((resolve, reject) => {
    const child = execFile(process.env.PYTHON_BIN || 'python',
      ['-X', 'utf8', '-B', verifier, '--root', dirname(report), 'verify-save', '--report', report],
      { windowsHide: true, encoding: 'utf8', timeout: 60_000, maxBuffer: 4 * 1024 * 1024, signal },
      (error, stdout, stderr) => {
        if (error) return reject(new Error(`Save verification refused: ${stderr.trim() || error.message}`));
        try { resolve(JSON.parse(stdout)); } catch { reject(new Error('Save verifier returned invalid JSON')); }
      });
    child.stdin.on('error', () => {}); // Early rejection can close stdin; execFile reports the cause.
    child.stdin.end(JSON.stringify(snapshot));
  });
}

export async function saveRuntime(input, mcp, run, signal) {
  const path = input.payload.verificationReport;
  const report = JSON.parse(await readFile(path, 'utf8'));
  const planPath = resolve(dirname(path), report.plan);
  const plan = JSON.parse(await readFile(planPath, 'utf8'));
  const health = await run(['daemon', 'health'], 30_000, signal);
  const checked = runtimeGate(health.result, plan.runtime, input, mcp);
  if (!health.ok || !checked.passed) throw new Error('Save runtime gate: ' + checked.issues.join(', '));
  const requirements = JSON.parse(await readFile(resolve(dirname(path), report.requirements), 'utf8'));
  const reviewed = JSON.parse(await readFile(resolve(dirname(path), report.snapshot), 'utf8'));
  return { mode: requirements.execution_rules?.edit_scope?.mode,
    measureTextIds: reviewed.result?.textMeasurements?.coverage?.measuredIds ?? [] };
}

export async function guardedSchematicSave(input, { mcp, signal, run = runEasyeda, verify = verifySave, runtimeCheck = saveRuntime } = {}) {
  const report = input.payload?.verificationReport;
  if (!input.project || !input.doc || typeof report !== 'string' || !isAbsolute(report)) {
    throw new Error('schematic.save requires project/doc and payload.verificationReport (absolute before_save report path).');
  }
  if (Object.keys(input.payload).some(key => key !== 'verificationReport')) throw new Error('Unexpected save payload; guard fields are generated from verified evidence.');
  const policy = await runtimeCheck(input, mcp, run, signal);
  const mode = typeof policy === 'string' ? policy : policy?.mode;
  const read = await run(buildCallArgs('schematic.components.list', { ...input, payload: {
    includePins: true, includePinNets: true, includeBBox: true, includeWires: true, includePagePrimitives: true, includeDeviceIdentity: mode !== 'layout',
    measureTextIds: policy?.measureTextIds ?? [],
  } }), undefined, signal);
  if (!read.ok || read.result?.ok !== true || read.result.result?.saveGuardVersion !== 1) {
    throw new Error('Fresh guarded snapshot unavailable; nothing saved. Update/reconnect Connector and read back.');
  }
  const result = await verify(report, read.result, signal);
  if (result.passed !== true || result.phase !== 'before_save' || result.project !== input.project || result.doc !== input.doc ||
      result.pageStateSha256 !== read.result.result.pageStateSha256 || !/^[0-9a-f]{64}$/.test(result.pageStateSha256)) {
    throw new Error('Save verifier identity/fingerprint mismatch; nothing saved.');
  }
  if (signal?.aborted) throw new Error('Save cancelled before dispatch.');
  const saved = await run(buildCallArgs('schematic.save', { ...input, payload: {
    expectedPageStateSha256: result.pageStateSha256, expectedProjectUuid: result.project, expectedDocumentUuid: result.doc,
    expectedRuntimeInstance: result.runtimeInstance, expectedMutationSeq: result.mutationSeq,
  } }), undefined, signal);
  if (saved.ok && saved.result?.ok === true && saved.result.result?.saved === true) {
    saved.result.result.workflowSave = result;
  }
  // A successful native save is not final workflow acceptance. Uncertain saves are never retried here.
  return saved;
}

import { exportSnapshot, preparePlan, loadPlan, snapshotData, renderPlan } from './project-files.mjs';
import { callAction, snapshotSteps } from './open.mjs';
import { toMcpResult } from './core.mjs';

// Files hold full observations; MCP returns only the paths/counts needed for the next step.
export async function captureSnapshot(input, invoke = callAction) {
  if (input.outputFile && (input.cacheOnly || input.domain !== 'schematic' || input.allPages)) {
    throw new Error('Snapshot export requires a fresh single-page schematic read');
  }
  const options = input.outputFile ? { ...input, detail: true, includeTexts: input.includeTexts ?? true } : input;
  const steps = snapshotSteps(options.domain, options.routing, options.detail, options.allPages, options)
    .map(step => input.target ? { ...step, payload: { ...step.payload, _target: input.target } } : step);
  const result = await invoke('debug.batch', { ...options, payload: { steps, stopOnError: true } });
  if (!input.outputFile || result.isError) return result;
  const { context } = snapshotData(result);
  for (const key of ['projectUuid', 'documentUuid']) if (input.target?.[key] && input.target[key] !== context[key]) {
    throw new Error(`Snapshot ${key} changed; no archive was written`);
  }
  const archived = await exportSnapshot(result, { outputFile: input.outputFile, overwrite: input.overwrite, baselineFile: input.baselineFile });
  return toMcpResult({ ok: true, result: archived });
}

export async function planTool(input, catalog, invoke = callAction) {
  if (input.stage !== undefined && input.operation !== 'apply') throw new Error('stage applies only to apply');
  if (input.operation === 'prepare') {
    return toMcpResult({ ok: true, result: await preparePlan(input.snapshotFile, input.outputFile, input) });
  }
  if (input.operation === 'render') return toMcpResult({ ok: true, result: await renderPlan(input.planFile, { expectedHash: input.expectedHash }) });
  if (!['inspect', 'check', 'apply'].includes(input.operation)) throw new Error('Unknown plan operation');
  // The exact reviewed bytes are required for writes; sources are rehashed before any EDA request.
  if (input.operation === 'apply' && !/^[a-f0-9]{64}$/.test(input.expectedHash ?? '')) throw new Error('apply requires expectedHash from the reviewed plan');
  const loaded = await loadPlan(input.planFile, { expectedHash: input.expectedHash, requireConfirmed: input.requireConfirmed });
  const { plan, ...inspection } = loaded;
  if (input.operation === 'inspect') return toMcpResult({ ok: true, result: inspection });
  if (!input.window) throw new Error('Plan check/apply requires an explicit window');
  if (input.target && ['projectUuid', 'documentUuid'].some(key => input.target[key] !== plan.target[key])) throw new Error('Requested target does not match the plan');
  const route = { window: input.window, target: plan.target, timeoutMs: input.timeoutMs, signal: input.signal };
  const check = { action: 'schematic.target.check', payload: { semanticTarget: plan.semanticTarget, _target: plan.target } };
  if (input.operation === 'check') {
    if (!catalog.has(check.action)) throw new Error('The installed Connector action catalog needs schematic.target.check');
    return invoke(check.action, { ...route, payload: check.payload });
  }
  const stage = plan.stages?.find(stage => stage.id === input.stage);
  if (plan.stages && !stage) throw new Error('Staged apply requires an existing explicit stage id');
  if (!plan.stages && input.stage !== undefined) throw new Error('This plan has no stages');
  const steps = stage?.steps ?? plan.steps;
  if ((!steps.length && stage?.check !== 'final') || steps.length >= 500) throw new Error('apply needs 1..499 explicit typed steps; a final stage may have zero steps');
  const checkAction = stage?.check === 'geometry' ? 'schematic.components.list' : check.action;
  if (!catalog.has(checkAction)) throw new Error(`The installed Connector action catalog needs ${checkAction}`);
  for (const step of steps) {
    if (!catalog.has(step.action)) throw new Error(`Unknown plan action: ${step.action}`);
    if (step.action === 'debug.batch') throw new Error('Plan steps already form one batch; flatten nested batches so every step has the same target');
    if (step.action.startsWith('debug.') || ['project.open', 'project.create', 'board.create', 'document.open', 'document.close', 'schematic.page.open', 'schematic.page.create', 'schematic.page.delete', 'system.page_reload'].includes(step.action)) {
      throw new Error('A drawing plan cannot navigate away from its bound page; select the target before applying');
    }
    if (step.payload?._target || step.payload?._execution || step.payload?._edit) throw new Error('Plan steps cannot override batch routing or execution context');
  }
  // Reuse the existing queue/partial receipts. No replay, save, new scheduler, or inferred layout.
  const boundSteps = steps.map(step => ({ action: step.action, payload: { ...step.payload, _target: plan.target } }));
  if (!stage) return invoke('debug.batch', { ...route, edit: input.edit,
    payload: { steps: [...boundSteps, check], stopOnError: true, dryRun: input.dryRun } });
  if (stage.check === 'geometry' && !stage.primitiveIds?.length && !steps.some(step => ['schematic.component.place', 'schematic.component.modify'].includes(step.action))) {
    throw new Error('Geometry stage needs explicit component primitiveIds or component place/modify steps');
  }
  const execution = steps.length ? await invoke('debug.batch', { ...route, edit: input.edit,
    payload: { steps: boundSteps, stopOnError: true, dryRun: input.dryRun } }) : null;
  const batch = receiptValue(execution);
  const executed = !steps.length || (!execution?.isError && batch?.ok === true && batch?.partial !== true
    && batch.completed === steps.length && batch.total === steps.length && batch.results?.length === steps.length && batch.results.every(result => result.ok === true));
  const report = { planFile: loaded.planFile, sha256: loaded.sha256, target: plan.target,
    stage: { id: stage.id, check: stage.check, completed: false }, wholePageComplete: false,
    execution, readback: null, targetCheck: null,
    limits: ['Stage completion is execution plus its declared readback, not whole-page delivery, visual acceptance or saved persistence', 'No automatic retry; compile subsequent steps from actual receipts'] };
  if (input.dryRun) return toMcpResult({ ok: true, result: { ...report, ok: !execution?.isError, dryRun: true } });
  if (stage.check === 'final') {
    if (executed) report.targetCheck = await invoke(check.action, { ...route, payload: check.payload });
    report.stage.completed = executed && !report.targetCheck?.isError && receiptValue(report.targetCheck)?.status === 'pass';
    return toMcpResult({ ok: true, result: { ...report, ok: report.stage.completed, partial: !report.stage.completed } });
  }
  // New IDs come only from actual component receipts. Submitted payloads are never observations.
  const ids = new Set(stage.primitiveIds ?? []);
  let missingCreatedId = false;
  for (const step of steps) if (step.action === 'schematic.component.modify' && typeof step.payload?.primitiveId === 'string') ids.add(step.payload.primitiveId);
  for (const result of batch?.results ?? []) if (['schematic.component.place', 'schematic.component.modify'].includes(result.action)) {
    const id = result.result?.component?.primitiveId ?? result.result?.primitiveId;
    if (typeof id === 'string' && id) ids.add(id);
    else if (result.action === 'schematic.component.place') missingCreatedId = true;
  }
  report.primitiveIds = [...ids];
  if (ids.size) {
    report.readback = await invoke('schematic.components.list', { ...route, payload: { _target: plan.target, primitiveIds: [...ids],
      includePins: true, includePinNets: false, includeDeviceIdentity: false, includeBBox: true, includeWires: false, includeNetIndex: false, includeTexts: false } });
    const observed = receiptValue(report.readback), scope = observed?.readScope;
    const unknownGeometryIds = [...ids].filter(id => !Array.isArray(observed?.components) || !observed.components.some(part => part?.primitiveId === id && measuredComponentGeometry(part)));
    if (unknownGeometryIds.length) report.readbackReason = `Measured pins or bounding box unavailable for components: ${unknownGeometryIds.join(', ')}`;
    report.stage.completed = executed && !missingCreatedId && !report.readback.isError && observed?.partial !== true && scope?.concurrentChange === false
      && Array.isArray(scope.missingIds) && !scope.missingIds.length && Array.isArray(observed.components)
      && !unknownGeometryIds.length;
  } else report.readbackReason = 'No observed component IDs; inspect the original execution receipt before deciding on a scoped recovery read';
  if (missingCreatedId) report.readbackReason = 'A component placement has no returned primitiveId; existing scoped IDs cannot prove that new component was read back';
  return toMcpResult({ ok: true, result: { ...report, ok: report.stage.completed, partial: !report.stage.completed } });
}

function receiptValue(receipt) {
  const value = receipt?.structuredContent;
  return value?.result ?? value;
}

function measuredComponentGeometry(part) {
  const box = part.bbox, pins = part.pins;
  return part.pinsAvailable === true && box && [box.minX, box.minY, box.maxX, box.maxY].every(Number.isFinite)
    && box.minX < box.maxX && box.minY < box.maxY && Array.isArray(pins) && pins.length > 0
    && pins.every(pin => pin && typeof pin.pinNumber === 'string' && pin.pinNumber.trim()
      && [pin.x, pin.y, pin.rotation].every(Number.isFinite));
}

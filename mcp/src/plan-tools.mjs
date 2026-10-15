import { createHash } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { mkdir, open, readFile } from 'node:fs/promises';
import path from 'node:path';
import nativeIdentity from './native-identity.cjs';
import { exportSnapshot, preparePlan, loadPlan, managedStageReceiptFile, snapshotData, renderPlan, stageReceiptBinding, verifyStagePrefix, executionSteps, bindPlacedComponents, finalRecheckBinding } from './project-files.mjs';
import { compileRoutes } from './route-tools.mjs';
import { compileLabels } from './label-tools.mjs';
import { summarizeDelivery } from './delivery-summary.mjs';
import { callAction, snapshotSteps, runtimeRequest } from './open.mjs';
import { toMcpResult } from './core.mjs';
import { needsDrc, receiptFacts, localRuleFacts, requiredEdit, requirementResults, displayOnlyStage, displayAttributesVerified, designPins, continuousInputs } from './plan-rules.mjs';

// Files hold full observations; MCP returns only the paths/counts needed for the next step.
export async function captureSnapshot(input, invoke = callAction) {
  if (input.outputFile && (input.cacheOnly || input.domain !== 'schematic' || input.allPages)) {
    throw new Error('Snapshot export requires a fresh single-page schematic read');
  }
  const options = input.outputFile ? { ...input, detail: true, includeTexts: input.includeTexts ?? (input.profile === undefined || input.profile === 'full') } : input;
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
  if (input.continuous !== undefined && (input.operation !== 'run' || typeof input.continuous !== 'boolean')) throw new Error('continuous is a boolean for run only');
  if (input.recheckFinal !== undefined && (!['run','apply'].includes(input.operation)||input.recheckFinal!==true||input.continuous)) throw new Error('recheckFinal:true is only for an explicit read-only final apply/run');
  if (input.operation === 'summary') return toMcpResult({ ok: true, result: await summarizeDelivery(input) });
  if (input.operation === 'finish') return finishPlanReview(input, catalog);
  if (input.operation === 'bind') return toMcpResult({ ok: true, result: await bindPlacedComponents(input) });
  if (input.operation === 'run') return runPlanStages(input, catalog, invoke);
  if (input.runStages !== undefined) throw new Error('runStages applies only to run');
  const started = performance.now(), calls = [];
  const call = invoke;
  invoke = async (action, args) => {
    const start = performance.now();
    try { return await call(action, args); }
    finally { calls.push({ action, durationMs: Math.round(performance.now() - start) }); }
  };
  if (input.stage !== undefined && input.operation !== 'apply') throw new Error('stage applies only to apply');
  if (input.receiptFile !== undefined) {
    if (input.operation !== 'apply') throw new Error('receiptFile applies only to apply');
    if (input.dryRun) throw new Error('receiptFile cannot be used with dryRun');
    if (typeof input.receiptFile !== 'string' || !path.isAbsolute(input.receiptFile) || !input.receiptFile.toLowerCase().endsWith('.json')) {
      throw new Error('receiptFile must be an absolute new JSON path');
    }
  }
  if (input.operation === 'prepare') {
    try { return toMcpResult({ ok: true, result: await preparePlan(input.snapshotFile, input.outputFile, input) }); }
    catch (error) {
      if (!error.missing) throw error;
      return toMcpResult({ ok: true, result: { ok: false, executionState: 'not_executed', missing: error.missing, error: { message: error.message } } });
    }
  }
  if (input.operation === 'render') return toMcpResult({ ok: true, result: await renderPlan(input.planFile, { expectedHash: input.expectedHash }) });
  if (!['inspect', 'check', 'apply'].includes(input.operation)) throw new Error('Unknown plan operation');
  // The exact reviewed bytes are required for writes; sources are rehashed before any EDA request.
  if (input.operation === 'apply' && !/^[a-f0-9]{64}$/.test(input.expectedHash ?? '')) throw new Error('apply requires expectedHash from the reviewed plan');
  const loaded = await loadPlan(input.planFile, { expectedHash: input.expectedHash, requireConfirmed: input.requireConfirmed, ...(input.operation==='apply'?{stage:input.stage}:{}) });
  const { plan, ...inspection } = loaded;
  if (input.operation === 'inspect') return toMcpResult({ ok: true, result: inspection });
  if (!input.window) throw new Error('Plan check/apply requires an explicit window');
  if (input.target && ['projectUuid', 'documentUuid'].some(key => input.target[key] !== plan.target[key])) throw new Error('Requested target does not match the plan');
  const route = { window: input.window, target: plan.target, timeoutMs: input.timeoutMs, signal: input.signal };
  const check = { action: 'schematic.target.check', payload: { semanticTarget: plan.semanticTarget, _target: plan.target } };
  if (input.operation === 'check') {
    if (!catalog.has(check.action)) throw new Error('The installed Connector action catalog needs schematic.target.check');
    return checkPlanTarget(plan,route,invoke);
  }
  const declaredStage = plan.stages?.find(stage => stage.id === input.stage);
  let stage = declaredStage;
  if (plan.stages && !stage) throw new Error('Staged apply requires an existing explicit stage id');
  if (!plan.stages && input.stage !== undefined) throw new Error('This plan has no stages');
  if (plan.requirements?.length && !stage) throw new Error('Requirement execution needs an explicit stage; unstaged legacy plans remain supported without requirements');
  if (input.receiptFile && !stage) throw new Error('receiptFile requires a staged apply');
  let steps = stage?.steps ?? plan.steps;
  const displayOnly = displayOnlyStage(stage);
  if ((!steps.length && stage?.check !== 'final' && !stage?.routeFrom && !stage?.labelsFrom) || steps.length >= 500) throw new Error('apply needs 1..499 typed steps; final/declared route/labels may have zero steps');
  if (stage?.labelsFrom && (input.dryRun !== undefined || input.edit?.mode !== 'layout' || input.edit.complete === false)) throw new Error('labelsFrom requires a complete scoped layout edit');
  if (stage?.routeFrom && (input.dryRun !== undefined || input.edit?.mode !== 'design' || input.edit.complete === false)) throw new Error('routeFrom requires a complete design edit; use compile_routes for a write-free preview');
  if (stage?.routeFrom && input.edit.primitiveIds && (input.edit.primitiveIds.length !== stage.primitiveIds.length || stage.primitiveIds.some(id => !input.edit.primitiveIds.includes(id)))) throw new Error('Route edit scope must match its declared component scope');
  const checkAction = stage?.check === 'geometry' ? 'schematic.components.list' : check.action;
  if (!catalog.has(checkAction)) throw new Error(`The installed Connector action catalog needs ${checkAction}`);
  validateStepActions(steps, catalog);
  if (stage?.check === 'geometry' && !stage.primitiveIds?.length && !steps.some(step => ['schematic.component.place', 'schematic.component.modify'].includes(step.action))) {
    throw new Error('Geometry stage needs explicit component primitiveIds or component place/modify steps');
  }
  const hasMemberNets = plan.semanticTarget.nets.some(net => net.match === 'members');
  if (hasMemberNets && !stage) throw new Error('Member nets require a staged plan');
  if (hasMemberNets && steps.some(step => step.action.startsWith('schematic.wire.') && catalog.get(step.action)?.mutates !== false)
    && (input.edit?.mode !== 'design' || input.edit.complete === false)) throw new Error('Member-net wiring needs one complete design edit with independent pin targets');
  let edit = plan.requirements?.length || hasMemberNets ? requiredEdit(plan, stage, input.edit) : input.edit;
  if (plan.requirements?.length) {
    const beforeChecks = requirementResults(plan, stage, {}, { before: true });
    if (beforeChecks.blocking) return toMcpResult({ ok: true, result: { ok: false, partial: false, execution: null,
      stage: { id: stage.id, check: stage.check, completed: false }, requirementResults: beforeChecks,
      reason: 'Required before-write evidence is unavailable; no EDA action was sent.' } });
  }
  const stageIndex = plan.stages?.indexOf(stage);
  const recheckOf=input.recheckFinal ? await finalRecheckBinding(loaded,stageIndex) : null;
  const originalReceiptFile=plan.executionReceipts ? managedStageReceiptFile(plan, stageIndex) : null;
  const managedReceiptFile=recheckOf?originalReceiptFile.replace(/\.json$/i,'.recheck.json'):originalReceiptFile;
  if (managedReceiptFile && input.receiptFile && path.resolve(input.receiptFile) !== managedReceiptFile) throw new Error('Managed stage receipt path is fixed by the plan');
  const prefix = managedReceiptFile ? await verifyStagePrefix(loaded, stageIndex) : { geometry: new Map() };
  let routeGeometry = prefix.geometry;
  let routePinNets = false;
  let compilation;
  if (stage?.routeFrom) {
    try {
    // Routes use their declared measurement, not a later unrelated stage's geometry.
    const measured = prefix.receipts.find(({ packet }) => packet.stage?.id === stage.routeFrom)?.packet.receipt?.readback;
    const components = receiptValue(measured)?.components ?? [];
    routeGeometry = new Map(components.map(part => [part.primitiveId, part]));
    if (!scopedReadComplete(measured, stage.primitiveIds, plan.target)
      || stage.primitiveIds.some(id => !measuredComponentGeometry(routeGeometry.get(id)))) throw new Error('Declared route measurement is incomplete');
    const keys = new Set(plan.semanticTarget.components.filter(part => stage.primitiveIds.includes(plan.semanticTarget.idMap[part.key])).map(part => part.key));
    routePinNets = plan.semanticTarget.nets.some(net => net.match !== 'members' && net.members.some(member => keys.has(member.componentKey)));
    if (routePinNets && components.some(part => part.netlistAvailable !== true)) throw new Error('Named route inheritance requires measured native pin nets');
    const file = managedReceiptFile.replace(/\.json$/i, '.routes.json');
    // Compile only after the predecessor measurement; reuse an unchanged artifact if no write was dispatched.
    try { await readFile(managedReceiptFile); throw new Error('Route stage already has an execution receipt; do not replay'); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    try { await readFile(file); }
    catch (error) {
      if (error.code !== 'ENOENT') throw error;
      const start = performance.now();
      try { await compileRoutes({ planFile: loaded.planFile, expectedHash: loaded.sha256, stage: stage.id,
        geometryReceiptFile: managedStageReceiptFile(plan, plan.stages.findIndex(previous => previous.id === stage.routeFrom)), outputFile: file }); }
      finally { calls.push({ action: 'compile_routes', durationMs: Math.round(performance.now() - start) }); }
    }
    compilation = { file, sha256: sha256(await readFile(file)) };
    steps = await executionSteps(plan, declaredStage, { compilation }, { planFile: loaded.planFile, planSha256: loaded.sha256 });
    validateStepActions(steps, catalog);
    stage = { ...stage, steps };
    edit = requiredEdit(plan, stage, { ...input.edit, primitiveIds: [...stage.primitiveIds] });
    } catch (error) {
      error.receipt = { error: { name: error.name, message: error.message }, executionState: 'not_executed',
        ...(compilation ? { compilation } : {}), timing: { elapsedMs: Math.round(performance.now() - started), calls } };
      throw error;
    }
  }
  if (stage?.labelsFrom) {
    const file = managedReceiptFile.replace(/\.json$/i, '.labels.json');
    try {
      try { await readFile(managedReceiptFile); throw new Error('Labels stage already has an execution receipt; do not replay'); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      try { await readFile(file); }
      catch (error) {
        if (error.code !== 'ENOENT') throw error;
        const start = performance.now();
        try { await compileLabels({planFile:loaded.planFile,expectedHash:loaded.sha256,stage:stage.id,
          wireReceiptFile:managedStageReceiptFile(plan,plan.stages.findIndex(previous=>previous.id===stage.labelsFrom)),outputFile:file}); }
        finally { calls.push({action:'compile_labels',durationMs:Math.round(performance.now()-start)}); }
      }
      compilation = {file,sha256:sha256(await readFile(file))};
      steps = await executionSteps(plan,declaredStage,{compilation},{planFile:loaded.planFile,planSha256:loaded.sha256});
      validateStepActions(steps,catalog);
      stage = {...stage,steps};
      edit = requiredEdit(plan,stage,{...input.edit,primitiveIds:[...stage.primitiveIds]});
    } catch (error) {
      error.receipt = {error:{name:error.name,message:error.message},executionState:'not_executed',
        ...(compilation?{compilation}:{}),timing:{elapsedMs:Math.round(performance.now()-started),calls}};
      throw error;
    }
  }
  const receipt = !input.dryRun && (managedReceiptFile || input.receiptFile) ? await beginReceipt(managedReceiptFile ?? input.receiptFile, loaded, declaredStage, managedReceiptFile ? stageReceiptBinding(plan, declaredStage) : null,recheckOf) : null;
  const finish = async result => {
    const timing = { elapsedMs: Math.round(performance.now() - started), calls,
      scope: 'planTool through checks; excludes receipt flush, caller planning and visual review' };
    if (result.structuredContent) {
      result.structuredContent = { ...result.structuredContent, timing };
      result.content[0].text = JSON.stringify(result.structuredContent);
    }
    return receipt ? settleReceipt(receipt, result) : result;
  };
  try {
  if (!input.dryRun && routeGeometry.size && steps.some(step => catalog.get(step.action)?.mutates !== false)) await verifyPredecessorGeometry(stage, routeGeometry, route, invoke, displayOnly, routePinNets);
  // Reuse the existing queue/partial receipts. No replay, save, new scheduler, or inferred layout.
  const boundSteps = steps.map(step => ({ action: step.action, payload: { ...step.payload, _target: plan.target } }));
  if (!stage) return finish(await invoke('debug.batch', { ...route, edit,
    payload: { steps: [...boundSteps, check], stopOnError: true, dryRun: input.dryRun } }));
  const execution = steps.length ? await invoke('debug.batch', { ...route, edit,
    payload: { steps: boundSteps, stopOnError: true, dryRun: input.dryRun } }) : null;
  const batch = receiptValue(execution);
  const executed = !steps.length || (receiptFacts(execution).ok && batch?.ok === true && batch?.partial !== true
    && batch.completed === steps.length && batch.total === steps.length && Array.isArray(batch.results) && batch.results.length === steps.length
    && batch.results.every((result, index) => result?.ok === true && result.action === steps[index].action));
  const report = { planFile: loaded.planFile, sha256: loaded.sha256, target: plan.target,
    ...(edit ? { edit } : {}),
    ...(compilation ? { compilation } : {}),
    knowledgeChecks:loaded.knowledgeChecks,deferredKnowledgeSources:loaded.deferredKnowledgeSources,knowledgeScope:loaded.knowledgeScope,
    stage: { id: stage.id, check: stage.check, completed: false }, wholePageComplete: false,
    ...(stage.check === 'geometry' ? { readbackProfile: displayOnly ? 'display' : 'geometry' } : {}),
    execution, readback: null, targetCheck: null,
    predecessors: (prefix.receipts ?? []).map(({ file, sha256 }) => ({ file, sha256 })),
    limits: ['Stage completion is execution plus its declared readback, not whole-page delivery, visual acceptance or saved persistence', 'No automatic retry; compile subsequent steps from actual receipts'] };
  const finishReport = async () => {
    if (plan.requirements !== undefined) {
      if (executed && report.stage.completed && needsDrc(plan, stage) && catalog.has('schematic.drc.check')) {
        report.drcCheck = await invoke('schematic.drc.check', { ...route, payload: { _target: plan.target } });
      }
      report.requirementResults = requirementResults(plan, stage, report, { complete: edit?.complete !== false, executed, previousReceipts: prefix.receipts ?? [] });
      // Actual writes stay recorded even when required evidence is unavailable. Never replay to obtain a check.
      const local = localRuleFacts(report), nativeRules = local.value.rules;
      if (report.designCheck && (!local.ok || !nativeRules)) {
        report.requirementResults.blocking = true;
        report.requirementResults.status = 'unknown';
      }
      if (edit && edit.complete !== false && nativeRules && nativeRules.status !== 'pass') {
        report.requirementResults.blocking = true;
        report.requirementResults.status = nativeRules.status === 'fail' || report.requirementResults.status === 'fail' ? 'fail' : 'unknown';
      }
    }
    const ok = report.stage.completed && !report.requirementResults?.blocking;
    return finish(toMcpResult({ ok: true, result: { ...report, ok, partial: !ok } }));
  };
  if (input.dryRun) return finish(toMcpResult({ ok: true, result: { ...report, ok: !execution?.isError, dryRun: true } }));
  if (stage.check === 'final') {
    if (executed) report.targetCheck = await checkPlanTarget(plan,route,invoke);
    const checked = receiptValue(report.targetCheck);
    report.stage.completed = executed && receiptFacts(report.targetCheck).ok && checked?.status === 'pass'
      && checked.verified === true && checked.partial !== true && matchesTarget(report.targetCheck, plan.target);
    if (recheckOf && report.stage.completed) {
      const primitiveIds=Object.values(plan.semanticTarget.idMap??{});
      const expectedIdentities=Object.fromEntries(plan.semanticTarget.components.map(part=>[plan.semanticTarget.idMap[part.key],
        {designator:part.ref,...(part.uniqueId?{uniqueId:part.uniqueId}:{})}]));
      const wireIds=[...new Set(prefix.receipts.flatMap(({packet})=>packet.receipt?.execution?.structuredContent?.result?.results??[])
        .filter(row=>row.ok===true&&row.action==='schematic.wire.create').map(row=>row.result?.primitiveId).filter(Boolean))];
      report.deliveryCheck=await invoke('schematic.components.list',{...route,payload:{_target:plan.target,primitiveIds,
        verifyDesign:{expectedPins:designPins(plan,primitiveIds),expectedIdentities,wireIds}}});
      report.stage.completed=receiptFacts(report.deliveryCheck).ok&&receiptValue(report.deliveryCheck)?.rules?.status==='pass'
        && matchesTarget(report.deliveryCheck,plan.target);
    }
    return await finishReport();
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
  const unresolved = batch?.rules?.items?.filter(item => item.severity === 'error' && item.coverage !== 'not_applicable' && item.status !== 'pass') ?? [];
  if (executed && edit?.mode === 'design' && edit.complete !== false && edit.expectedPins && edit.expectedIdentities
    && ids.size && unresolved.length && unresolved.every(item => item.id === 'pin_nets' && ['unknown', 'fail'].includes(item.status))) {
    const wireIds = batch.results.filter(result => result.action.startsWith('schematic.wire.'))
      .map(result => result.result?.primitiveId).filter(id => typeof id === 'string');
    report.designCheck = await invoke('schematic.components.list', { ...route, payload: { primitiveIds: edit.primitiveIds,
      verifyDesign: { expectedPins: edit.expectedPins, expectedIdentities: edit.expectedIdentities, wireIds }, _target: plan.target } });
    report.readback = report.designCheck;
    report.readbackSource = { kind: 'independent_design_check', requestId: report.designCheck.structuredContent?.requestId,
      originalPinCheckStatus: unresolved.map(item => item.status), originalExecutionPreserved: true };
  }
  if (ids.size) {
    const includePinNets = !displayOnly && steps.some(step => step.action === 'schematic.components.list' && step.payload?.includePinNets === true);
    // Only the final successful step can be reused: nothing was changed after this observation.
    const last = steps.at(-1), lastResult = batch?.results?.at(-1);
    if (!report.readback && executed && !displayOnly && last?.action === 'schematic.components.list'
      && last.payload?.includePins === true && last.payload?.includeBBox === true
      && (!includePinNets || last.payload?.includePinNets === true)) {
      const candidate = toMcpResult({ ok: true, result: { ...execution.structuredContent, result: lastResult.result } });
      if (scopedReadComplete(candidate, [...ids], plan.target)
        && receiptValue(candidate).components.every(part => measuredComponentGeometry(part)
          && (!includePinNets || part.netlistAvailable === true))) {
        report.readback = candidate;
        report.readbackSource = { kind: 'execution_step', index: steps.length - 1, requestId: execution.structuredContent.requestId };
      }
    }
    const wireDisplay = displayOnly && batch?.results?.some(result => !ids.has(result.result?.attribute?.ParentPrimitiveId));
    report.readback ??= await invoke('schematic.components.list', { ...route, payload: { _target: plan.target, primitiveIds: [...ids],
      includePins: !displayOnly || wireDisplay, includePinNets, includeDeviceIdentity: false, includeBBox: !displayOnly, includeWires: !!wireDisplay, includeNetIndex: false, includeTexts: plan.acceptanceScope === 'local_display' } });
    const observed = receiptValue(report.readback);
    const unknownGeometryIds = displayOnly ? [] : [...ids].filter(id => !Array.isArray(observed?.components) || !observed.components.some(part => part?.primitiveId === id && measuredComponentGeometry(part)));
    if (unknownGeometryIds.length) report.readbackReason = `Measured pins or bounding box unavailable for components: ${unknownGeometryIds.join(', ')}`;
    const displayVerified = !displayOnly || displayAttributesVerified(steps, execution, [...ids], report.readback);
    if (!displayVerified) report.readbackReason = 'Native display attribute identity, owner or requested fields are unavailable or differ from the plan';
    report.stage.completed = executed && !missingCreatedId && scopedReadComplete(report.readback, [...ids], plan.target) && Array.isArray(observed.components)
      && !unknownGeometryIds.length && displayVerified;
  } else report.readbackReason = 'No observed component IDs; inspect the original execution receipt before deciding on a scoped recovery read';
  if (missingCreatedId) report.readbackReason = 'A component placement has no returned primitiveId; existing scoped IDs cannot prove that new component was read back';
  return await finishReport();
  } catch (error) {
    error.receipt = { ...error.receipt, error: error.receipt?.error ?? { name: error.name, message: error.message },
      timing: { elapsedMs: Math.round(performance.now() - started), calls } };
    if (receipt && !receipt.finalizing) await settleReceiptError(receipt, error);
    throw error;
  }
}

const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');

async function checkPlanTarget(plan,route,invoke) {
  const original = await invoke('schematic.target.check',{...route,payload:{semanticTarget:plan.semanticTarget,_target:plan.target}});
  const packet=original.structuredContent, findings=packet?.result?.findings;
  if (packet?.ok!==true || packet.result?.status!=='unknown' || !Array.isArray(findings)||!findings.length
    || findings.some(row=>row.code!=='library_identity_domain_unknown'||row.status!=='unknown'||row.field!=='symbol')) return original;
  let current=null, exported=null;
  try {
    current=await invoke('schematic.components.list',{...route,payload:{includePins:true,includePinNets:true,
      includeDeviceIdentity:true,includeTexts:false,includeBBox:false,includeWires:false,_target:plan.target}});
    const observed=current.structuredContent;
    if (current.isError||observed?.ok!==true||observed.result?.readScope?.complete!==true
      || observed.result.readScope.concurrentChange!==false
      || ['projectUuid','documentUuid'].some(key=>observed.context?.[key]!==plan.target[key])) throw new Error('Fresh independent page identity unavailable');
    exported=await invoke('project.export_source',{...route,payload:{uuid:plan.target.projectUuid,_target:plan.target}});
    const native=exported.structuredContent, file=native?.result?.artifactPath;
    const artifact=native?.artifacts?.find(row=>row.path===file&&row.kind==='project_source');
    if (exported.isError||native?.ok!==true||!path.isAbsolute(file??'')||!/^[a-f0-9]{64}$/.test(artifact?.sha256??'')
      || !Number.isInteger(artifact.size)||artifact.size<=0||artifact.size>8*1024*1024
      || ['projectUuid','documentUuid'].some(key=>native.context?.[key]!==plan.target[key])) throw new Error('Bound native symbol source archive unavailable');
    const bytes=await readFile(file);
    if(bytes.length!==artifact.size||sha256(bytes)!==artifact.sha256)throw new Error('Native source archive bytes changed');
    const inventory=await nativeIdentity.readProjectFootprintSourceArchive(new Blob([bytes]),plan.target.documentUuid,'SYMBOL');
    const mappings=[], nativeTarget=structuredClone(plan.semanticTarget);
    for (const row of findings) {
      const wanted=plan.semanticTarget.components.find(part=>part.key===row.component);
      const id=plan.semanticTarget.idMap?.[wanted?.key];
      const matches=observed.result.components.filter(part=>part.primitiveId===id&&part.designator===wanted?.ref
        && (!wanted.uniqueId||part.uniqueId===wanted.uniqueId));
      if(matches.length!==1||matches[0].symbol?.uuid!==row.actual)throw new Error('Native symbol owner changed since the original check');
      const part=matches[0], source=nativeIdentity.readNativeFootprintSource(inventory,part.symbol.uuid,'SYMBOL');
      const expected=wanted.deviceResolution?.symbolSource??wanted.symbol;
      if(!source.source||source.source.libraryUuid!==part.symbol.libraryUuid||source.source.uuid!==expected.uuid
        ||source.source.libraryUuid!==expected.libraryUuid)throw new Error(source.error??'Native symbol source differs from the independent library target');
      const bound=nativeTarget.components.find(part=>part.key===wanted.key);
      bound.symbol={...bound.symbol,uuid:source.source.instanceUuid};
      if(bound.deviceResolution?.symbolSource)delete bound.deviceResolution.symbolSource;
      mappings.push({primitiveId:id,...source.source});
    }
    // Lower only proven symbol identities to the instance domain; keep the original plan and all native net predicates.
    const rebound=await invoke('schematic.target.check',{...route,payload:{semanticTarget:nativeTarget,_target:plan.target}});
    if(!matchesTarget(rebound,plan.target))throw new Error('Native target changed during the bound identity check');
    return toMcpResult({ok:true,result:{context:rebound.structuredContent.context,result:receiptValue(rebound),
      nativeSymbolEvidence:{kind:'official-project-symbol-source',originalTargetCheck:original,readback:current,
        exportReceipt:exported,archiveSha256:artifact.sha256,mappings,boundInstanceTargetCheck:rebound,
        nativeTarget,matcher:'existing native schematic.target.check; only source-proven symbol UUIDs rebound; original target/unknown preserved'}}});
  } catch(error) {
    return toMcpResult({ok:true,result:{...packet,symbolSourceRecovery:{ok:false,reason:error.message,current,exported},
      note:'Original symbol unknown retained; no inferred mapping or automatic retry.'}});
  }
}

function deliveryEvidence(plan,packets,catalog) {
  const last=packets.find(({packet})=>packet.receipt?.execution?.structuredContent?.result?.results
    ?.some(step=>catalog.has(step.action)&&catalog.get(step.action).mutates!==false));
  const original=last?.packet.receipt.execution.structuredContent;
  if(original?.result?.rules?.status!=='pass')return null;
  const final=packets.find(({packet})=>packet.recheckOf&&packet.stage?.check==='final'&&packet.stage.completed===true)?.packet;
  if(!final)return original;
  const check=final.receipt?.deliveryCheck, facts=receiptValue(check), ids=Object.values(plan.semanticTarget.idMap??{});
  const identities=Object.fromEntries(plan.semanticTarget.components.map(part=>[plan.semanticTarget.idMap[part.key],
    {designator:part.ref,...(part.uniqueId?{uniqueId:part.uniqueId}:{})}]));
  if(!receiptFacts(check).ok||!matchesTarget(check,plan.target)||facts?.rules?.status!=='pass'
    ||!isDeepStrictEqual(facts.designVerification?.primitiveIds,ids)
    ||!isDeepStrictEqual(facts.designVerification?.expectedPins,designPins(plan,ids))
    ||!isDeepStrictEqual(facts.designVerification?.expectedIdentities,identities))return null;
  return check.structuredContent;
}

export function planRunSummary(result, outputFile, elapsedMs) {
  const facts = result.structuredContent ?? result;
  const stopped = facts.stoppedResult?.structuredContent ?? facts;
  const execution = receiptFacts(stopped.execution).value;
  const failed = execution.results?.find(step => step.ok !== true);
  const ok = result.isError !== true && facts.ok !== false && facts.partial !== true;
  return { ok, outputFile, elapsedMs, stage: facts.stage, timing: facts.timing,
    ...(facts.jobFile ? { jobFile: facts.jobFile, job: facts.job, executionState: facts.executionState } : {}),
    ...(facts.summary ? { summary: facts.summary, markdownFile: facts.markdownFile } : {}),
    ...(facts.deliverySummary ? { deliverySummary: facts.deliverySummary } : {}),
    planFile: facts.planFile ?? (facts.jobFile ? facts.outputFile : undefined), sha256: facts.sha256, revision: facts.revision,
    ...(facts.componentKeys ? { componentKeys: facts.componentKeys, primitiveIds: facts.primitiveIds, evidence: facts.evidence } : {}),
    ...(facts.idMap ? {idMap:facts.idMap,primitiveIds:facts.primitiveIds,bindings:facts.bindings} : {}),
    missing:facts.missing,executionState:facts.executionState,
    error: facts.error?.message ? { message: facts.error.message } : facts.error,
    readbackSource: facts.readbackSource, completedStages: facts.completedStages,
    remainingStages: facts.remainingStages, review: facts.review, saved: facts.saved, receiptFile: facts.receiptFile,
    receiptSha256: facts.receiptSha256, stoppedStage: facts.stoppedStage, nextAction: facts.nextAction,
    ...(!ok ? { failure: { reason: stopped.readbackReason ?? stopped.reason ?? facts.reason,
      requestId: stopped.execution?.structuredContent?.requestId ?? stopped.execution?.structuredContent?.id,
      ...(failed ? { action: failed.action, index: failed.index, error: failed.error,
        reason: failed.result?.reason ?? stopped.readbackReason, mismatch: failed.result?.mismatch,
        unknown: failed.result?.unknown, observedAttribute: failed.result?.attribute } : {}),
      checks: stopped.requirementResults?.items?.filter(row => row.severity === 'error' && row.status !== 'pass')
        .map(row => ({ id: row.requirement.id, status: row.status, coverage: row.coverage })) } } : {}) };
}

function validateStepActions(steps, catalog) {
  for (const step of steps) {
    if (!catalog.has(step.action)) throw new Error(`Unknown plan action: ${step.action}`);
    if (step.action === 'debug.batch') throw new Error('Plan steps already form one batch; flatten nested batches so every step has the same target');
    if (step.action.startsWith('debug.') || ['project.open', 'project.create', 'board.create', 'document.open', 'document.close', 'schematic.page.open', 'schematic.page.create', 'schematic.page.delete', 'system.page_reload'].includes(step.action)) {
      throw new Error('A drawing plan cannot navigate away from its bound page; select the target before applying');
    }
    if (step.payload?._target || step.payload?._execution || step.payload?._edit) throw new Error('Plan steps cannot override batch routing or execution context');
  }
}

// Sequential calls share the existing managed receipts; this is not a second task state.
async function runPlanStages(input, catalog, invoke) {
  const started = performance.now();
  if (!input.window || !/^[a-f0-9]{64}$/.test(input.expectedHash ?? '')) throw new Error('run requires window and the reviewed plan expectedHash');
  if (['stage', 'edit', 'receiptFile', 'dryRun'].some(key => input[key] !== undefined)) throw new Error('run uses explicit runStages and managed receipts; stage/edit/receiptFile/dryRun are not accepted');
  let loaded = await loadPlan(input.planFile, { expectedHash: input.expectedHash, requireConfirmed: input.continuous === true ? true : input.requireConfirmed });
  let { plan } = loaded;
  const continuous = input.continuous === true;
  if (continuous && input.runStages !== undefined) throw new Error('continuous derives its fixed stages from the plan; do not also send runStages');
  const entries = continuous ? (plan.stages ?? []).map(stage => ({stage:stage.id})) : input.runStages;
  if (!Array.isArray(entries) || !entries.length || entries.length > 32
    || entries.some(entry => !entry || typeof entry !== 'object' || Array.isArray(entry) || typeof entry.stage !== 'string'
      || Object.keys(entry).some(key => !['stage', 'edit'].includes(key)))) throw new Error('runStages needs 1..32 explicit {stage, edit?} entries');
  if (!plan.stages || !plan.executionReceipts) throw new Error('run requires a staged plan with executionReceipts');
  if (input.target && ['projectUuid','documentUuid'].some(key => input.target[key] !== plan.target[key])) throw new Error('Requested target does not match the plan');
  if (continuous) {
    const missing = continuousInputs(plan,catalog);
    if (missing.length) return toMcpResult({ok:true,result:{ok:false,partial:false,executionState:'not_executed',missing,
      planFile:loaded.planFile,sha256:loaded.sha256,target:plan.target,completedStages:[],remainingStages:entries.map(entry=>entry.stage),
      nextAction:'Complete the listed confirmed inputs before any write; no EDA action sent.'}});
  }
  const indices = entries.map(entry => plan.stages.findIndex(stage => stage.id === entry.stage));
  if(input.recheckFinal && (entries.length!==1||indices[0]!==plan.stages.length-1))throw new Error('Final recheck cannot include completed mutation stages');
  if (indices.some((index, i) => index < 0 || (i > 0 && index !== indices[i - 1] + 1))) throw new Error('runStages must be a contiguous forward range of existing stages');
  for (let i = 0; i < indices.length; i++) {
    const stage = plan.stages[indices[i]];
    validateStepActions(stage.steps, catalog);
    if (!continuous) requiredEdit(plan, stage, entries[i].edit);
    if (stage.routeFrom && (!catalog.has('schematic.wire.create') || (!continuous && (entries[i].edit?.mode !== 'design' || entries[i].edit.complete === false)))) throw new Error('A declared route stage needs wire.create and its complete design edit before running any stage');
  }
  await verifyStagePrefix(loaded, indices[0]);
  // Refuse existing or unresolved receipts before sending any part of a new range.
  for (let i = 0; i < entries.length; i++) {
    const original=managedStageReceiptFile(plan,indices[i]);
    if(input.recheckFinal)await finalRecheckBinding(loaded,indices[i]);
    const file=input.recheckFinal?original.replace(/\.json$/i,'.recheck.json'):original;
    try { await readFile(file); }
    catch (error) { if (error.code === 'ENOENT') continue; throw error; }
    throw new Error(`Stage already has a receipt; inspect it before selecting remaining work: ${entries[i].stage}`);
  }
  const completed = [], receipts = [], calls = [], bindings = [];
  const report = (ok, extra = {}) => toMcpResult({ ok: true, result: { ok, partial: !ok,
    planFile: loaded.planFile, sha256: loaded.sha256, target: plan.target, completedStages: completed,
    remainingStages: entries.slice(completed.length).map(entry => entry.stage), receipts, bindings, revision:plan.revision,
    ...(continuous ? {primitiveIds:Object.values(plan.semanticTarget.idMap??{}),idMap:plan.semanticTarget.idMap} : {}),
    wholePageComplete: false, nextAction: ok ? 'Review the current image and use the existing delivery checks.' : 'Inspect the stage receipt and current state; do not replay the range.',
    timing: { elapsedMs: Math.round(performance.now() - started), calls, scope: 'run including stage receipt flush; excludes caller planning and visual review' }, ...extra } });
  for (let i = 0; i < entries.length; i++) {
    const entry = entries[i];
    if (input.signal?.aborted) return report(false, { stoppedStage: entry.stage, reason: 'Cancelled before dispatching this stage' });
    let result;
    try {
      const current = plan.stages[indices[i]];
      const edit = continuous && current.primitiveIds ? {mode:current.routeFrom?'design':'layout',primitiveIds:current.primitiveIds,
        expectedIdentities:Object.fromEntries(plan.semanticTarget.components.filter(part=>current.primitiveIds.includes(plan.semanticTarget.idMap?.[part.key]))
          .map(part=>[plan.semanticTarget.idMap[part.key],{designator:part.ref,...(part.uniqueId?{uniqueId:part.uniqueId}:{})}]))} : entry.edit;
      result = await planTool({ operation: 'apply', planFile: loaded.planFile, expectedHash: loaded.sha256,
        requireConfirmed: input.requireConfirmed, window: input.window, target: plan.target,
        stage: entry.stage, edit, ...(input.recheckFinal?{recheckFinal:true}:{}), timeoutMs: input.timeoutMs, signal: input.signal }, catalog, invoke);
    } catch (error) {
      calls.push(...(error.receipt?.timing?.calls ?? []));
      return report(false, { stoppedStage: entry.stage, error: { message: error.message, ...(error.receipt ? { receipt: error.receipt } : {}) } });
    }
    const facts = result.structuredContent;
    calls.push(...(facts?.timing?.calls ?? []));
    const originalReceipt=managedStageReceiptFile(plan,indices[i]);
    const receiptFile=input.recheckFinal?originalReceipt.replace(/\.json$/i,'.recheck.json'):originalReceipt;
    try {
      await readFile(receiptFile);
      receipts.push({ stage: entry.stage, file: receiptFile, completed: !result.isError && facts?.ok === true && facts.stage?.completed === true });
    } catch (error) {
      if (error.code !== 'ENOENT') return report(false, { stoppedStage: entry.stage, error: { message: error.message } });
    }
    if (result.isError || facts?.ok !== true || facts.partial === true || facts.stage?.completed !== true) {
      return report(false, { stoppedStage: entry.stage, stoppedResult: result });
    }
    completed.push(entry.stage);
    if (continuous && plan.stages[indices[i]].steps.every(step=>step.action==='schematic.component.place') && plan.stages[indices[i]].steps.length) {
      try {
        const start = performance.now();
        const bound = await bindPlacedComponents({planFile:loaded.planFile,expectedHash:loaded.sha256,stage:entry.stage,
          bindStages:plan.stages.slice(indices[i]+1).filter(stage=>stage.check==='geometry'&&stage.componentKeys&&!stage.primitiveIds).map(stage=>stage.id)});
        calls.push({action:'bind',durationMs:Math.round(performance.now()-start)});bindings.push(bound);
        loaded = await loadPlan(bound.planFile,{expectedHash:bound.sha256,requireConfirmed:true});plan = loaded.plan;
      } catch (error) {
        // Binding may already have committed a new revision before Markdown persistence failed.
        try { loaded=await loadPlan(input.planFile,{requireConfirmed:true});plan=loaded.plan; } catch { /* Keep the last verified plan, never replay placement. */ }
        return report(false,{stoppedStage:entry.stage,reason:'Placement completed; binding stopped. Read its saved receipt and current plan, never replay placement.',error:{message:error.message}});
      }
    }
  }
  const lastStage = plan.stages[indices.at(-1)];
  if (lastStage.check === 'final' || plan.acceptanceScope === 'local_display') {
    // Return the existing visual/save gate inputs; an AI review is still required.
    const packets = [...(await verifyStagePrefix(loaded, indices.at(-1) + 1)).receipts].reverse();
    const delivery=deliveryEvidence(plan,packets,catalog);
    if (!delivery)
      return report(false, { reason: 'Latest actual write needs passing native checks; never fall back to an earlier edit.' });
    if (delivery) {
      const requestId = delivery.requestId ?? delivery.id;
      const startedImage = performance.now();
      const image = await invoke('schematic.export.image', { window: input.window, target: plan.target, payload: {format:'png',scope:'page',_target:plan.target} });
      calls.push({action:'schematic.export.image',durationMs:Math.round(performance.now()-startedImage)});
      const capture = image.structuredContent;
      const state = await runtimeRequest({operation:'state',window:input.window});
      const observed = state.structuredContent;
      const doc = observed?.documents?.find(d => d.windowId === input.window);
      const editReceipt = observed?.receipts?.find(r => r.requestId === requestId);
      const imageReceipt = observed?.receipts?.find(r => r.requestId === (capture?.requestId ?? capture?.id));
      if (image.isError || capture?.ok !== true || !capture.result?.artifactPath || !doc
        || editReceipt?.generation !== doc.generation || imageReceipt?.generation !== doc.generation) {
        return report(false,{reason:'Stages completed; current review evidence is unavailable. Read current scope, never replay writes.'});
      }
      return report(true,{review:{imagePath:capture.result.artifactPath,imageSha256:sha256(await readFile(capture.result.artifactPath)),visualReview:{operation:'visual_review',window:input.window,requestId,
        generation:doc.generation,screenshotRequestId:capture.requestId ?? capture.id,source:'ai'},
        finish:{operation:'finish',window:input.window,requestId},saved:false},nextAction:'Inspect review.imagePath. Only if acceptable submit review.visualReview then review.finish; never replay completed stages.'});
    }
  }
  return report(true);
}

async function finishPlanReview(input, catalog) {
  if (input.visualAccepted !== true || !input.window || !path.isAbsolute(input.runResultFile ?? '')
    || !/^[a-f0-9]{64}$/.test(input.expectedRunResultHash ?? '')) throw new Error('finish requires an actual accepted image, window and hashed run result file');
  const bytes = await readFile(input.runResultFile);
  if (sha256(bytes) !== input.expectedRunResultHash) throw new Error('Reviewed run result changed; no control action sent');
  const result = JSON.parse(bytes.toString('utf8'));
  const run = result.result?.structuredContent ?? result.structuredContent;
  if (result.result?.isError || result.isError || run?.ok !== true || run.partial !== false
    || run.remainingStages?.length !== 0 || !run.completedStages?.length) throw new Error('finish requires a complete successful run result');
  if (!path.isAbsolute(run.planFile ?? '') || !/^[a-f0-9]{64}$/.test(run.sha256 ?? '')) throw new Error('Run lacks its bound plan path/hash; no control action sent');
  const loaded = await loadPlan(run.planFile, { expectedHash: run.sha256, requireConfirmed: true });
  const { plan } = loaded, last = plan.stages?.length - 1;
  if (!Array.isArray(plan.stages) || last < 0 || !plan.executionReceipts || run.completedStages.at(-1) !== plan.stages[last].id
    || (plan.stages[last].check !== 'final' && plan.acceptanceScope !== 'local_display')
    || ['projectUuid','documentUuid'].some(key => run.target?.[key] !== plan.target[key]
      || (input.target && input.target[key] !== plan.target[key]))) throw new Error('Run is not the bound plan delivery stage');
  const prefix = await verifyStagePrefix(loaded, last + 1);
  const execution=deliveryEvidence(plan,[...prefix.receipts].reverse(),catalog);
  if (execution?.result?.rules?.status !== 'pass') throw new Error('Latest actual write needs passing native checks; no control action sent');
  const requestId = execution?.requestId ?? execution?.id, review = run.review, visual = review?.visualReview;
  if (!requestId || visual?.requestId !== requestId || review.finish?.requestId !== requestId
    || visual.operation !== 'visual_review' || review.finish.operation !== 'finish' || visual.source !== 'ai'
    || visual.window !== input.window || review.finish.window !== input.window || review.saved !== false
    || !Number.isInteger(visual.generation) || visual.generation < 0 || !visual.screenshotRequestId
    || !path.isAbsolute(review.imagePath ?? '') || !/^[a-f0-9]{64}$/.test(review.imageSha256 ?? '')
    || sha256(await readFile(review.imagePath)) !== review.imageSha256) throw new Error('Review does not bind the verified edit and unchanged image');
  const file = path.join(plan.executionReceipts.directory, 'delivery.json');
  const handle = await open(file, 'wx');
  const record = { file, handle, envelope: { schemaVersion: 'easyeda.delivery/1', planFile: loaded.planFile,
    sha256: loaded.sha256, target: plan.target, runResultFile: path.resolve(input.runResultFile),
    runResultSha256: input.expectedRunResultHash, imagePath: review.imagePath, imageSha256: review.imageSha256,
    startedAt: new Date().toISOString(), completedAt: null, state: 'pending', receipt: null } };
  try { await writeReceipt(handle, record.envelope, file); }
  catch (error) { await handle.close(); throw error; }
  const started = performance.now(), calls = [];
  let visualReviewReceipt = null, finishReceipt = null, error = null;
  try {
    const route = { window: input.window, target: plan.target, timeoutMs: input.timeoutMs, signal: input.signal };
    let start = performance.now();
    visualReviewReceipt = await runtimeRequest({ ...route, operation: 'visual_review', requestId,
      generation: visual.generation, screenshotRequestId: visual.screenshotRequestId, source: 'ai' });
    calls.push({ operation: 'visual_review', durationMs: Math.round(performance.now() - start) });
    if (!visualReviewReceipt.isError && visualReviewReceipt.structuredContent?.ok === true) {
      start = performance.now();
      finishReceipt = await runtimeRequest({ ...route, operation: 'finish', requestId });
      calls.push({ operation: 'finish', durationMs: Math.round(performance.now() - start) });
    }
  } catch (failure) { error = { message: failure.message, executionState: 'unknown' }; }
  const saved = !finishReceipt?.isError && finishReceipt?.structuredContent?.ok === true
    && finishReceipt.structuredContent.result?.saved === true ? true : null;
  const completion = toMcpResult({ ok: true, result: { ok: saved === true, partial: saved !== true, saved,
    planFile: loaded.planFile, sha256: loaded.sha256, target: plan.target, visualReviewReceipt, finishReceipt,
    ...(error ? { error } : {}), timing: { elapsedMs: Math.round(performance.now() - started), calls,
      scope: 'review/save controls only; excludes drawing preparation, actual image inspection and receipt flush' },
    nextAction: saved === true ? 'Saved; reference this receipt for delivery and update task/PWF once.' : 'Inspect preserved receipts and current scope; do not replay run or assume the save failed.',
    limits: ['Caller must inspect the bound image before visualAccepted:true.', 'saved:true is not close/reopen persistence proof.', 'No automatic retry or task-intent overwrite.'] } });
  let settled;
  try { settled = await settleReceipt(record, completion); }
  catch (failure) { return toMcpResult({ ok: true, result: { ...completion.structuredContent, ok: false, partial: true,
    reason: `Delivery receipt persistence failed: ${failure.message}`,
    nextAction: 'Native save may have completed; preserve these returned receipts and inspect, never replay run or finish blindly.' } }); }
  // Derived bookkeeping cannot change the settled save receipt or turn a summary failure into a save retry.
  let deliverySummary;
  try {
    const derived = await summarizeDelivery({ deliveryFile: file, outputFile: path.join(plan.executionReceipts.directory, 'delivery.summary.json') });
    deliverySummary = { ok: true, outputFile: derived.outputFile, markdownFile: derived.markdownFile, sha256: derived.sha256,
      counts: derived.summary.counts, timing: derived.summary.timing };
  } catch (failure) { deliverySummary = { ok: false, error: failure.message, nextAction: 'Use summary on the preserved delivery.json; never repeat finish to recreate a derived view.' }; }
  const enhanced = { ...settled.structuredContent, deliverySummary };
  settled.structuredContent = enhanced;
  settled.content[0].text = JSON.stringify(enhanced);
  return settled;
}

async function beginReceipt(receiptFile, loaded, stage, binding, recheckOf) {
  const file = path.resolve(receiptFile);
  await mkdir(path.dirname(file), { recursive: true });
  const handle = await open(file, 'wx');
  const envelope = {
    schemaVersion: 'easyeda.execution/1', planFile: loaded.planFile, sha256: loaded.sha256,
    revision: loaded.plan.revision, target: loaded.plan.target, stage: { id: stage.id, check: stage.check, completed: false },
    sources: loaded.plan.sources.map(({ id, path, sha256 }) => ({ id, path, sha256 })),
    startedAt: new Date().toISOString(), completedAt: null, dryRun: false, state: 'pending', receipt: null,
    ...(binding ? { binding } : {}),
    ...(recheckOf ? {recheckOf} : {}),
    limits: ['Stage completion is execution plus its declared readback, not whole-page delivery, visual acceptance or saved persistence', 'No automatic retry; compile subsequent steps from actual receipts'],
  };
  try { await writeReceipt(handle, envelope, file); }
  catch (error) { await handle.close(); throw error; }
  return { file, handle, envelope };
}

async function settleReceipt(record, result) {
  record.finalizing = true;
  record.envelope.completedAt = new Date().toISOString();
  record.envelope.state = 'settled';
  record.envelope.receipt = result.structuredContent ?? null;
  record.envelope.stage = record.envelope.receipt?.stage ?? record.envelope.stage;
  record.envelope.limits = record.envelope.receipt?.limits ?? record.envelope.limits;
  let receiptSha256;
  try { receiptSha256 = await writeReceipt(record.handle, record.envelope, record.file); }
  finally { await record.handle.close(); }
  const report = result.structuredContent;
  if (report && typeof report === 'object' && !Array.isArray(report)) {
    const enhanced = { ...report, receiptFile: record.file, receiptSha256 };
    result.structuredContent = enhanced;
    result.content[0].text = JSON.stringify(enhanced);
  }
  return result;
}

async function settleReceiptError(record, error) {
  record.finalizing = true;
  record.envelope.completedAt = new Date().toISOString();
  record.envelope.state = 'unknown';
  record.envelope.receipt = error?.receipt ?? { error: { name: error?.name ?? 'Error', message: String(error?.message ?? error) } };
  try { await writeReceipt(record.handle, record.envelope, record.file); }
  finally { await record.handle.close(); }
}

async function verifyPredecessorGeometry(stage, previous, route, invoke, displayOnly = false, pinNets = false) {
  const ids = new Set([...(stage.primitiveIds ?? []), ...stage.steps.filter(step => step.action === 'schematic.component.modify').map(step => step.payload?.primitiveId)]);
  if (!ids.size && stage.steps.some(step => step.action.startsWith('schematic.wire.'))) throw new Error('Wire writes after geometry need explicit component primitiveIds');
  const scoped = [...ids].filter(id => previous.has(id));
  if (!scoped.length) return;
  const readback = await invoke('schematic.components.list', { ...route, payload: { _target: route.target, primitiveIds: scoped,
    includePins: !displayOnly, includePinNets: pinNets, includeDeviceIdentity: false, includeBBox: !displayOnly, includeWires: false, includeNetIndex: false, includeTexts: false } });
  const observed = receiptValue(readback), components = new Map((observed?.components ?? []).map(component => [component?.primitiveId, component]));
  const changed = scoped.filter(id => displayOnly ? !sameComponentPose(previous.get(id), components.get(id)) : !sameGeometry(previous.get(id), components.get(id), pinNets));
  if (!scopedReadComplete(readback, scoped, route.target) || changed.length) {
    const error = new Error(`Observed predecessor geometry changed or is unavailable: ${changed.join(', ') || scoped.join(', ')}`);
    error.receipt = { preflightReadback: readback, error: { name: error.name, message: error.message } };
    throw error;
  }
}

function sameGeometry(before, after, pinNets = false) {
  if (!before || !after || !measuredComponentGeometry(after) || pinNets && (before.netlistAvailable !== true || after.netlistAvailable !== true)) return false;
  const geometry = part => ({ bbox: part.bbox, pins: part.pins.map(pin => Object.fromEntries(['primitiveId', 'pinNumber', 'pinName', 'x', 'y', 'rotation', ...(pinNets ? ['net', 'noConnected'] : [])].map(key => [key, pin[key]]))), x: part.x, y: part.y, rotation: part.rotation, mirror: part.mirror });
  return JSON.stringify(geometry(before)) === JSON.stringify(geometry(after));
}

function sameComponentPose(before, after) {
  if (!before || !after || ![after.x, after.y, after.rotation].every(Number.isFinite)) return false;
  const keys = ['primitiveId', 'componentType', 'uniqueId', 'designator', 'component', 'footprint', 'supplierId', 'x', 'y', 'rotation', 'mirror'];
  return keys.every(key => JSON.stringify(before[key]) === JSON.stringify(after[key]));
}

async function writeReceipt(handle, envelope, file) {
  const bytes = Buffer.from(`${JSON.stringify(envelope, null, 2)}\n`);
  let offset = 0;
  while (offset < bytes.length) {
    const { bytesWritten } = await handle.write(bytes, offset, bytes.length - offset, offset);
    if (!Number.isInteger(bytesWritten) || bytesWritten <= 0) throw new Error('Receipt write made no progress');
    offset += bytesWritten;
  }
  await handle.truncate(bytes.length);
  await handle.sync();
  return sha256(await readFile(file ?? handle.path));
}

function receiptValue(receipt) {
  const value = receipt?.structuredContent;
  return value?.result ?? value;
}

function matchesTarget(receipt, target) {
  const context = receipt?.structuredContent?.context;
  return ['projectUuid', 'documentUuid'].every(key => context?.[key] === target[key]);
}

function scopedReadComplete(receipt, ids, target) {
  const value = receiptValue(receipt), scope = value?.readScope;
  return !receipt?.isError && value?.partial !== true && matchesTarget(receipt, target)
    && scope?.concurrentChange === false && Array.isArray(scope.missingIds) && !scope.missingIds.length
    && Array.isArray(scope.primitiveIds) && scope.primitiveIds.length === ids.length
    && new Set(scope.primitiveIds).size === ids.length && ids.every(id => scope.primitiveIds.includes(id))
    && Array.isArray(value.components) && value.components.length === ids.length
    && new Set(value.components.map(part => part?.primitiveId)).size === ids.length
    && value.components.every(part => ids.includes(part?.primitiveId));
}

function measuredComponentGeometry(part) {
  const box = part.bbox, pins = part.pins;
  return part.pinsAvailable === true && box && [box.minX, box.minY, box.maxX, box.maxY].every(Number.isFinite)
    && box.minX < box.maxX && box.minY < box.maxY && Array.isArray(pins) && pins.length > 0
    && pins.every(pin => pin && typeof pin.pinNumber === 'string' && pin.pinNumber.trim()
      && [pin.x, pin.y, pin.rotation].every(Number.isFinite));
}

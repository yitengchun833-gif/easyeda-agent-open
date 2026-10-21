import { createHash } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { isDeepStrictEqual } from 'node:util';
import { readJSON, validatePlan, verifyStagePrefix, protectFiles, writePair } from './project-files.mjs';
import { receiptFacts } from './plan-rules.mjs';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const reference = record => ({ file: record.file, sha256: record.sha256 });
const utc = value => typeof value === 'string' && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(value) && Number.isFinite(Date.parse(value));
function interval(start, end, boundary) {
  const ms = utc(start) && utc(end) ? Date.parse(end) - Date.parse(start) : null;
  return { ms: ms !== null && ms >= 0 ? ms : null, start: start ?? null, end: end ?? null,
    status: ms !== null && ms >= 0 ? 'recorded' : 'unmeasured', boundary };
}
function check(receipt) {
  if (!receipt) return null;
  const { value, ok } = receiptFacts(receipt), native = receipt.structuredContent ?? receipt;
  return { requestId: native.requestId ?? native.id ?? null, ok,
    actionOk: typeof value.ok === 'boolean' ? value.ok : null, completed: value.completed ?? null, total: value.total ?? null,
    status: value.status ?? value.rules?.status ?? null, counts: value.counts,
    findings: value.findings ?? value.rules?.items?.filter(row => ['fail','unknown'].includes(row.status)) ?? [],
    ...(value.error ? { error: value.error } : {}) };
}

// A local derived view: no live source/knowledge verification and no execution authority.
export async function summarizeDelivery(input) {
  const delivery = await readJSON(input.deliveryFile), d = delivery.value;
  if (d.schemaVersion !== 'easyeda.delivery/1' || d.state !== 'settled' || !d.receipt) throw new Error('A settled raw delivery receipt is required');
  const runFile = await readJSON(d.runResultFile), planFile = await readJSON(d.planFile);
  if (runFile.sha256 !== d.runResultSha256 || planFile.sha256 !== d.sha256) throw new Error('Delivery run/plan hash changed; original evidence must remain immutable');
  const run = runFile.value.result?.structuredContent ?? runFile.value.structuredContent;
  const plan = validatePlan(planFile.value), receipt = d.receipt;
  if (runFile.value.result?.isError || runFile.value.isError || run?.ok !== true || run.partial !== false
    || run.planFile !== planFile.file || run.sha256 !== planFile.sha256 || run.remainingStages?.length !== 0
    || !isDeepStrictEqual(run.target, d.target) || !isDeepStrictEqual(plan.target, d.target)
    || !isDeepStrictEqual(receipt.target, d.target) || receipt.planFile !== d.planFile || receipt.sha256 !== d.sha256
    || !plan.stages?.length || run.completedStages?.at(-1) !== plan.stages.at(-1).id)
    throw new Error('Delivery does not bind a complete run and its exact plan target');
  if (d.imagePath !== run.review?.imagePath || d.imageSha256 !== run.review?.imageSha256
    || hash(await readFile(d.imagePath)) !== d.imageSha256) throw new Error('Delivery image changed');
  const prefix = await verifyStagePrefix({ plan, planFile: planFile.file, sha256: planFile.sha256 }, plan.stages.length);
  const sources = [reference(delivery), reference(runFile), reference(planFile), { file: d.imagePath, sha256: d.imageSha256 }];
  const wires = new Set(), stages = [];
  for (const item of prefix.receipts) {
    sources.push({ file: item.file, sha256: item.sha256 });
    const packet = item.packet, r = packet.receipt, bridge = r.targetCheck?.structuredContent?.nativeSymbolEvidence;
    for (const step of receiptFacts(r.execution).value.results ?? []) if (step.ok === true && step.action === 'schematic.wire.create' && step.result?.primitiveId) wires.add(step.result.primitiveId);
    const stage = { id: packet.stage.id, completed: packet.stage.completed, evidence: { file: item.file, sha256: item.sha256 },
      timing: interval(packet.startedAt, packet.completedAt, 'managed stage including its receipt recording'),
      execution: check(r.execution), independentReadback: check(r.designCheck), target: check(r.targetCheck),
      drc: check(r.drcCheck), requirements: r.requirementResults?.items?.map(row => ({ id: row.requirement?.id, status: row.status, coverage: row.coverage })) ?? [],
      ...(bridge ? { sourceResolution: { kind: bridge.kind, original: check(bridge.originalTargetCheck), recheck: check(bridge.boundInstanceTargetCheck), archiveSha256: bridge.archiveSha256 } } : {}) };
    if (packet.recheckOf) {
      const prior = await readJSON(packet.recheckOf.file);
      if (prior.sha256 !== packet.recheckOf.sha256) throw new Error('Original failed final receipt changed');
      sources.push(reference(prior));
      stage.originalFinal = { evidence: reference(prior), target: check(prior.value.receipt?.targetCheck) };
    }
    stages.push(stage);
  }
  let anchor = null;
  if (input.timingFile !== undefined) {
    const timing = await readJSON(input.timingFile);
    if (!utc(timing.value.timing?.dailySampleStart) || timing.value.save?.receiptSha256 !== delivery.sha256
      || !isDeepStrictEqual(timing.value.target, d.target)) throw new Error('Historical timing anchor must identify this delivery and its UTC sample start');
    sources.push(reference(timing));
    anchor = { startedAt: timing.value.timing.dailySampleStart, boundary: timing.value.timing.startBoundary,
      evidence: reference(timing), origin: 'historical caller-recorded sample anchor, not native or user-message timing' };
  }
  const nativeSave = receipt.finishReceipt?.structuredContent;
  const saved = !receipt.finishReceipt?.isError && nativeSave?.ok === true && nativeSave.result?.saved === true;
  if (receipt.saved === true && (!saved || !isDeepStrictEqual(nativeSave.context && { projectUuid: nativeSave.context.projectUuid, documentUuid: nativeSave.context.documentUuid }, d.target)))
    throw new Error('Claimed save lacks matching native evidence');
  const savedAt = saved ? nativeSave.createdAt : null;
  const runReadyAt = (await stat(runFile.file)).mtime.toISOString();
  const calls = run.timing?.calls ?? [], controls = receipt.timing?.calls ?? [];
  const summary = { schemaVersion: 'easyeda.delivery-summary/1', generatedAt: new Date().toISOString(), derivedOnly: true,
    target: d.target, saved: saved && receipt.saved === true ? true : null, saveRequestId: nativeSave?.requestId ?? nativeSave?.id ?? null,
    visualAccepted: !receipt.visualReviewReceipt?.isError && receipt.visualReviewReceipt?.structuredContent?.ok === true,
    acceptanceScope:plan.acceptanceScope??(plan.taskKind==='local_edit'?'local':'page'),persistence:receipt.persistence??{status:'unverified'},engineeringAccepted:false,
    image: { path: d.imagePath, sha256: d.imageSha256 }, wholePageComplete: run.wholePageComplete === true,
    counts: { declaredComponents: plan.semanticTarget.components.length, declaredPins: plan.semanticTarget.components.reduce((n,c) => n+c.pins.length,0), createdWires: wires.size },
    completedStages: run.completedStages, stages,
    timing: { anchor,
      sampleStartToSaved: interval(anchor?.startedAt, savedAt, anchor?.boundary ?? 'Sample start was not measured'),
      preparationUntilFirstStage: interval(anchor?.startedAt, prefix.receipts[0]?.packet.startedAt, 'sample preparation, Agent/tool handoffs and preflight; model/host waiting not separated'),
      runElapsedMs: runFile.value.elapsedMs ?? null, runInnerElapsedMs: run.timing?.elapsedMs ?? null,
      runResultReadyToSaved: interval(runReadyAt, savedAt, 'local run-file mtime to native save, including actual review and Agent handoff; copied/touched files can invalidate this boundary'),
      actualImageInspectionMs: null, finishControlElapsedMs: receipt.timing?.elapsedMs ?? null,
      nativeSaveControlMs: controls.find(call => call.operation === 'finish')?.durationMs ?? null,
      deliveryRecording: interval(d.startedAt, d.completedAt, 'visual/save controls and delivery receipt recording; not whole task duration'),
      runTimingEntryCount: calls.length, runConnectorRequestCount: calls.filter(call => !['bind','compile_routes','compile_labels'].includes(call.action)).length,
      finishControlCount: controls.length, fullAgentToolCallCount: null },
    sources, limits: ['Derived from recorded receipts only; no new drawing, current-host verification, DRC or reopen proof.',
      'Original fail/unknown and independent readback/source resolution are retained; successful delivery does not imply zero anomalies.',
      'Missing timing is unmeasured. UTC parsed at millisecond precision; local file mtime is not immutable receipt time.',
      'No user-message end-to-end time, comparable speedup, production qualification or whole-page acceptance is inferred.'] };
  const lines = ['# 原理图交付摘要（派生视图）', '', `- 保存：${summary.saved === true ? '原生回执已保存' : '未知，先核对原回执'}`,
    `- 范围：${summary.counts.declaredComponents} 器件 / ${summary.counts.declaredPins} 引脚 / ${wires.size} 条新导线；wholePageComplete=${summary.wholePageComplete}`,
    `- 连续执行：${summary.timing.runElapsedMs ?? '未测量'} ms；保存控制：${summary.timing.nativeSaveControlMs ?? '未测量'} ms`,
    `- 样本准备至保存：${summary.timing.sampleStartToSaved.ms ?? '未测量'} ms；准备至首阶段：${summary.timing.preparationUntilFirstStage.ms ?? '未测量'} ms`,
    `- 运行结果就绪至保存：${summary.timing.runResultReadyToSaved.ms ?? '未测量'} ms（包含审图及 Agent 衔接，非纯审图时间）`,
    '', '## 原检查过程', '', ...stages.map(stage => `- ${stage.id}：原动作 ${stage.execution ? stage.execution.actionOk === true ? '成功' : stage.execution.actionOk === false ? '失败' : '结果未提供' : '无动作'}；原检查 ${stage.execution?.status ?? '状态未提供'}；额外独立复核 ${stage.independentReadback ? stage.independentReadback.status ?? '状态未提供' : '未额外执行'}；目标检查 ${stage.target?.status ?? '未提供'}${stage.sourceResolution ? `；来源 ${stage.sourceResolution.original?.status} → ${stage.sourceResolution.recheck?.status}（${stage.sourceResolution.kind}）` : ''}${stage.originalFinal ? `；原 final ${stage.originalFinal.target?.status} 保留` : ''}`),
    '', '## 原件', '', ...sources.map(source => `- ${source.file}；SHA256 ${source.sha256}`), '', ...summary.limits.map(limit => `- ${limit}`), ''];
  if (!path.isAbsolute(input.outputFile ?? '')) throw new Error('Summary outputFile must be an absolute new JSON path');
  await protectFiles([input.outputFile, input.outputFile.replace(/\.json$/i,'.md')], sources.map(source => source.file), 'Derived summary cannot replace original evidence');
  return { ok: true, ...(await writePair(input.outputFile, summary, lines.join('\n'))), summary };
}

import { createHash } from 'node:crypto';
import { access, mkdir, readFile, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { loadPlan, managedStageReceiptFile, stageReceiptBinding, verifyStagePrefix, executionSteps } from './project-files.mjs';

const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const isObject = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const text = value => typeof value === 'string' ? value.trim() : '';
const same = (left, right) => JSON.stringify(left) === JSON.stringify(right);
const sources = plan => plan.sources.map(({ id, path, sha256 }) => ({ id, path, sha256 }));

/** Compile confirmed Name visibility from actual created or freshly read wires; no EDA calls. */
export async function compileLabels(input) {
  if (!isObject(input) || !text(input.planFile) || !/^[a-f0-9]{64}$/.test(input.expectedHash ?? '')) throw new Error('planFile and expectedHash are required');
  if (!text(input.outputFile) || !text(input.stage)) throw new Error('outputFile and stage are required');
  const receiptMode = text(input.wireReceiptFile), readbackMode = text(input.wireReadbackFile);
  if (Boolean(receiptMode) === Boolean(readbackMode)) throw new Error('Exactly one of wireReceiptFile or wireReadbackFile is required');
  if (!path.isAbsolute(input.outputFile) || path.extname(input.outputFile).toLowerCase() !== '.json') throw new Error('outputFile must be an absolute JSON path');
  if ((receiptMode && !path.isAbsolute(input.wireReceiptFile)) || (readbackMode && !path.isAbsolute(input.wireReadbackFile))) throw new Error('Wire evidence path must be absolute');
  if (readbackMode && (!Array.isArray(input.wireIds) || !input.wireIds.length || input.wireIds.some(id => !text(id)) || new Set(input.wireIds).size !== input.wireIds.length)) throw new Error('wireReadbackFile requires nonempty unique wireIds');
  if (receiptMode && input.wireIds !== undefined) throw new Error('wireIds applies only to wireReadbackFile');
  if (input.labels !== undefined && !Array.isArray(input.labels)) throw new Error('labels must be an array');
  const loaded = await loadPlan(input.planFile, { expectedHash: input.expectedHash, requireConfirmed: true });
  const { plan } = loaded;
  const visible = plan.layoutHints?.visibleNetNames;
  if (visible === undefined && input.labels === undefined) throw new Error('Name visibility is undecided; declare visibleNetNames or supply explicit labels');
  if (!plan.executionReceipts) throw new Error('Label compilation requires managed staged receipts');
  const targetIndex = plan.stages?.findIndex(stage => stage.id === input.stage) ?? -1;
  if (targetIndex < 0) throw new Error('Target stage does not exist');
  const targetReceipt = managedStageReceiptFile(plan, targetIndex);
  try { await access(targetReceipt); throw new Error('Target stage already has a receipt; labels cannot overwrite or replay it'); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  await verifyStagePrefix(loaded, targetIndex);
  let wires, wireIds, evidence, binding;
  if (receiptMode) {
    const wireReceiptFile = path.resolve(input.wireReceiptFile);
    const wireReceiptBytes = await readFile(wireReceiptFile);
    const wireReceipt = JSON.parse(wireReceiptBytes.toString('utf8'));
    const wireStageIndex = plan.stages.findIndex(stage => stage.id === wireReceipt?.stage?.id);
    if (wireStageIndex < 0 || wireStageIndex >= targetIndex) throw new Error('Wire receipt must belong to an earlier plan stage');
    if (wireReceiptFile !== managedStageReceiptFile(plan, wireStageIndex)) throw new Error('wireReceiptFile is not the fixed receipt for its plan stage');
    verifyWireReceipt(wireReceipt, loaded, plan.stages[wireStageIndex]);
    const executedSteps = await executionSteps(plan,plan.stages[wireStageIndex],wireReceipt.receipt,{planFile:loaded.planFile,planSha256:wireReceipt.sha256});
    wires = actualWires(wireReceipt.receipt,executedSteps); wireIds = wires.map(wire => wire.primitiveId);
    if (!wireIds.length) throw new Error('Wire receipt has no successful observed wire primitiveIds');
    binding = stageReceiptBinding(plan, plan.stages[wireStageIndex]);
    evidence = { wireReceiptFile, wireReceiptSha256: sha256(wireReceiptBytes), wireStage: { id: plan.stages[wireStageIndex].id, check: plan.stages[wireStageIndex].check } };
  } else {
    const wireReadbackFile = path.resolve(input.wireReadbackFile);
    const wireReadbackBytes = await readFile(wireReadbackFile);
    const readback = JSON.parse(wireReadbackBytes.toString('utf8'));
    wireIds = [...input.wireIds];
    wires = readbackWires(readback, plan, plan.stages[targetIndex], wireIds);
    binding = stageReceiptBinding(plan, plan.stages[targetIndex]);
    evidence = { wireReadbackFile, wireReadbackSha256: sha256(wireReadbackBytes), wireIds };
  }
  const visibleGroups = visible === undefined ? null : visibleWireGroups(wires, new Set(visible), new Set(plan.semanticTarget.nets.map(net => net.name)));
  const labels = validateLabels(input.labels ?? visibleGroups.map(group => ({ wireId: group.wires[0].primitiveId })), new Set(wireIds));
  if (visibleGroups) {
    const byId = new Map(wires.map(wire => [wire.primitiveId, wire]));
    for (const label of labels) if (!visible.includes(byId.get(label.wireId).net)) throw new Error(`Label ${label.wireId} conflicts with confirmed visibleNetNames`);
    for (const group of visibleGroups) if (!labels.some(label => group.wires.some(wire => wire.primitiveId === label.wireId))) throw new Error(`Visible net ${group.net} lacks a Name on an observed wire island`);
  }
  const uncoveredVisibleNetNames = (visible ?? []).filter(net => !wires.some(wire => wire.net === net));
  const typedStep = { action: 'schematic.wire.labels.apply', payload: { wireIds, labels } };
  const artifact = {
    schemaVersion: 'easyeda.labels/1', planFile: loaded.planFile, planSha256: loaded.sha256, target: plan.target,
    stage: { id: plan.stages[targetIndex].id, check: plan.stages[targetIndex].check },
    binding: { semanticTargetSha256: binding.semanticTargetSha256, sourcesSha256: binding.sourcesSha256, wireStageSha256: binding.stageSha256,
      ...(binding.expressionSha256 ? { expressionSha256: binding.expressionSha256 } : {}) },
    ...evidence,
    ...(visible === undefined ? {} : { visibleNetNames: visible, uncoveredVisibleNetNames }),
    typedStep, typedSteps: [typedStep],
    limits: ['Labels control Name visibility only; they do not change nets or recreate wires', 'Automatic selection shows one existing Name per observed same-net wire island; placement and readability still need current visual review', 'Apply this typed step only in the declared unexecuted stage; visual acceptance and saved persistence remain separate evidence'],
  };
  const outputFile = path.resolve(input.outputFile);
  await mkdir(path.dirname(outputFile), { recursive: true });
  const bytes = Buffer.from(`${JSON.stringify(artifact, null, 2)}\n`);
  await writeFile(outputFile, bytes, { flag: 'wx' });
  return { outputFile, sha256: sha256(bytes), planFile: loaded.planFile, planSha256: loaded.sha256, target: plan.target,
    stage: artifact.stage, wireCount: wireIds.length, labelCount: labels.length, uncoveredVisibleNetNames };
}

function verifyWireReceipt(receipt, loaded, stage) {
  const binding = stageReceiptBinding(loaded.plan, stage);
  if (!isObject(receipt) || receipt.schemaVersion !== 'easyeda.execution/1' || receipt.state !== 'settled' || receipt.receipt?.stage?.completed !== true)
    throw new Error('Wire receipt must be a settled successful execution receipt');
  if (receipt.planFile !== loaded.planFile || !same(receipt.target, loaded.plan.target) || !same(receipt.sources, sources(loaded.plan))
    || receipt.stage?.id !== stage.id || receipt.stage?.check !== stage.check || receipt.binding?.receiptDirectory !== binding.receiptDirectory
    || receipt.binding?.semanticTargetSha256 !== binding.semanticTargetSha256 || receipt.binding?.sourcesSha256 !== binding.sourcesSha256
    || receipt.binding?.stageSha256 !== binding.stageSha256 || receipt.binding?.requirementsSha256 !== binding.requirementsSha256
    || receipt.binding?.expressionSha256 !== binding.expressionSha256) throw new Error('Wire receipt target, plan, sources, expression or stage binding changed');
  if (receipt.receipt.ok !== true || receipt.receipt.partial === true || receipt.receipt.execution?.isError === true) throw new Error('Wire receipt did not complete successfully');
}

function actualWires(report,steps) {
  const wires = new Map(), visit = value => {
    if (!isObject(value) || value.ok === false || value.isError === true) return;
    if (['schematic.wire.create', 'schematic.wire.from_pin'].includes(value.action) && value.ok === true && value.result?.dryRun !== true) {
      const wire = value.result;
      if (!text(wire?.primitiveId)) throw new Error('Successful wire creation lacks an observed primitiveId');
      const declared=steps?.[value.index];
      const internal=declared?.action==='schematic.wire.create' && !Object.hasOwn(declared.payload??{},'net');
      if (declared?.action==='schematic.wire.create' && !internal && wire.net!==declared.payload.net) throw new Error('Actual wire net differs from the verified executed route');
      const observed = { primitiveId: wire.primitiveId, net: wire.net, line: wire.line, ...(internal?{internal:true}:{}) };
      if (wires.has(wire.primitiveId) && !same(wires.get(wire.primitiveId), observed)) throw new Error(`Conflicting native wire observations: ${wire.primitiveId}`);
      wires.set(wire.primitiveId, observed);
    }
    for (const key of ['structuredContent', 'result', 'data', 'value']) visit(value[key]);
    if (Array.isArray(value.results)) for (const result of value.results) visit(result);
  };
  visit(report?.execution);
  return [...wires.values()];
}

function readbackWires(receipt, plan, stage, wireIds) {
  const packet = receipt?.structuredContent ?? receipt, observed = packet?.result, scope = observed?.readScope;
  const ids = stage.primitiveIds;
  const sameSet = (actual, expected) => Array.isArray(actual) && Array.isArray(expected) && actual.length === expected.length
    && new Set(actual).size === actual.length && actual.every(id => expected.includes(id));
  if (receipt?.isError === true || packet?.type !== 'response' || packet.ok !== true || !text(packet.id)
    || !Number.isFinite(Date.parse(packet.createdAt)) || !sameTarget(packet.context, plan.target)
    || observed?.partial === true || observed?.wiresAvailable !== true || !Array.isArray(observed.wires)
    || !ids?.length || scope?.componentRead !== 'native-get-by-id' || scope.concurrentChange !== false
    || !Array.isArray(scope.missingIds) || scope.missingIds.length || !sameSet(scope.primitiveIds, ids)
    || !Array.isArray(observed.components) || !sameSet(observed.components.map(part => part.primitiveId), ids)) {
    throw new Error('Wire readback lacks a successful fresh native scoped observation');
  }
  for (const part of observed.components) {
    const keys = Object.keys(plan.semanticTarget.idMap ?? {}).filter(key => plan.semanticTarget.idMap[key] === part.primitiveId);
    const target = plan.semanticTarget.components.find(target => target.key === keys[0]);
    if (keys.length !== 1 || !target || target.ref !== part.designator || target.uniqueId && target.uniqueId !== part.uniqueId) {
      throw new Error(`Wire readback component identity differs: ${part.primitiveId}`);
    }
  }
  const targetNets = new Set(plan.semanticTarget.nets.map(net => net.name));
  return wireIds.map(id => {
    const segments = observed.wires.filter(wire => wire.primitiveId === id);
    if (!segments.length || !text(segments[0].net) || !targetNets.has(segments[0].net)
      || segments.some(segment => segment.net !== segments[0].net || segment.rawEncoding !== segments[0].rawEncoding
        || !same(segment.rawLine, segments[0].rawLine))) throw new Error(`Missing or inconsistent observed wire: ${id}`);
    const wire = { primitiveId: id, net: segments[0].net, line: segments[0].rawLine };
    const actual = wireSegments(wire);
    if (segments.length !== actual.length || new Set(segments.map(segment => segment.segmentIndex)).size !== actual.length
      || segments.some(segment => !same(actual[segment.segmentIndex], [[segment.x0, segment.y0], [segment.x1, segment.y1]]))) {
      throw new Error(`Incomplete observed wire geometry: ${id}`);
    }
    return wire;
  });
}

function sameTarget(a, b) {
  return isObject(a) && a.projectUuid === b.projectUuid && a.documentUuid === b.documentUuid;
}

function wireSegments(wire) {
  if (!Array.isArray(wire.line)) throw new Error(`Wire ${wire.primitiveId} lacks actual line geometry for automatic Name selection`);
  const line = wire.line;
  let segments;
  if (line.length >= 4 && line.every(Number.isFinite) && line.length % 4 === 0) {
    segments = line.flatMap((_, index) => index % 4 ? [] : [[[line[index], line[index + 1]], [line[index + 2], line[index + 3]]]]);
  } else if (line.length && line.every(row => Array.isArray(row) && row.length === 4 && row.every(Number.isFinite))) {
    segments = line.map(row => [[row[0], row[1]], [row[2], row[3]]]);
  } else if (line.length >= 2 && line.every(row => Array.isArray(row) && row.length === 2 && row.every(Number.isFinite))) {
    segments = line.slice(1).map((point, index) => [line[index], point]);
  } else throw new Error(`Wire ${wire.primitiveId} has incomplete actual line geometry`);
  if (segments.some(([a, b]) => same(a, b))) throw new Error(`Wire ${wire.primitiveId} has a zero-length segment`);
  return segments;
}

function wiresTouch(left, right) {
  const cross = (a, b, c) => (b[0] - a[0]) * (c[1] - a[1]) - (b[1] - a[1]) * (c[0] - a[0]);
  const on = (a, b, p) => cross(a, b, p) === 0 && p.every((value, index) => value >= Math.min(a[index], b[index]) && value <= Math.max(a[index], b[index]));
  for (const [a, b] of left) for (const [c, d] of right) {
    if (on(a, b, c) || on(a, b, d) || on(c, d, a) || on(c, d, b)
      || cross(a, b, c) * cross(a, b, d) < 0 && cross(c, d, a) * cross(c, d, b) < 0) return true;
  }
  return false;
}

function visibleWireGroups(wires, visible, targets) {
  let groups = [];
  for (const wire of wires) {
    // Anonymous internal wires are proven by the bound execution steps, not guessed from $ names.
    if (wire.internal === true) {
      if (visible.has(wire.net)) throw new Error(`Internal wire ${wire.primitiveId} unexpectedly has a visible interface net`);
      continue;
    }
    if (!text(wire.net) || !targets.has(wire.net)) throw new Error(`Wire ${wire.primitiveId} lacks an observed target net; Name visibility cannot be inferred`);
    if (!visible.has(wire.net)) continue;
    const measured = { ...wire, points: wireSegments(wire) };
    const joined = groups.filter(group => group.net === wire.net && group.wires.some(member => wiresTouch(member.points, measured.points)));
    groups = groups.filter(group => !joined.includes(group));
    groups.push({ net: wire.net, wires: [...joined.flatMap(group => group.wires), measured] });
  }
  return groups;
}

function validateLabels(labels, wireIds) {
  const selected = new Set();
  return labels.map(raw => {
    if (!isObject(raw) || Object.keys(raw).some(key => !['wireId', 'attributeId', 'x', 'y', 'rotation'].includes(key))) throw new Error('Each label accepts only wireId, attributeId, x, y and rotation');
    const label = { ...raw };
    if (!text(label.wireId) || !wireIds.has(label.wireId)) throw new Error('Label wireId is outside the observed wire receipt');
    if (label.attributeId !== undefined && !text(label.attributeId)) throw new Error('attributeId must be nonempty text');
    for (const key of ['x', 'y', 'rotation']) if (label[key] !== undefined && !Number.isFinite(label[key])) throw new Error(`${key} must be finite`);
    const key = `${label.wireId}:${label.attributeId ?? '*'}`;
    if (selected.has(key)) throw new Error(`Duplicate label target ${key}`);
    selected.add(key);
    return label;
  });
}

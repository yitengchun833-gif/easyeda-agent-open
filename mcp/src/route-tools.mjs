import { createHash } from 'node:crypto';
import { access, mkdir, mkdtemp, open, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { loadPlan, managedStageReceiptFile, stageReceiptBinding, verifyStagePrefix } from './project-files.mjs';
import { runEasyeda } from './core.mjs';

const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const text = value => typeof value === 'string' ? value.trim() : '';
const isObject = value => value !== null && typeof value === 'object' && !Array.isArray(value);

function receiptResult(receipt) {
  const value = receipt?.receipt;
  return value?.result ?? value;
}

function routeResult(value) {
  if (!isObject(value)) throw new Error('route-only CLI returned no JSON result');
  if (!Array.isArray(value.wires)) throw new Error('route-only CLI result has no wires');
  return value;
}

function sameTarget(a, b) {
  return isObject(a) && isObject(b) && a.projectUuid === b.projectUuid && a.documentUuid === b.documentUuid;
}

function componentGeometry(part) {
  const box = part?.bbox, pins = part?.pins;
  return part?.pinsAvailable === true && text(part?.primitiveId) && text(part?.designator)
    && [part.x, part.y, part.rotation].every(Number.isFinite) && typeof part.mirror === 'boolean'
    && isObject(box) && [box.minX, box.minY, box.maxX, box.maxY].every(Number.isFinite) && box.minX < box.maxX && box.minY < box.maxY
    && Array.isArray(pins) && pins.length > 0 && pins.every(pin => text(pin?.pinNumber) && typeof pin?.pinName === 'string'
      && [pin.x, pin.y, pin.rotation].every(Number.isFinite));
}

function sameIDSet(actual, expected) {
  return actual.length === expected.length && new Set(actual).size === actual.length
    && new Set(expected).size === expected.length && actual.every(id => expected.includes(id));
}

function receiptGeometry(receipt, expectedIDs, target) {
  const result = receiptResult(receipt);
  const response = result?.readback;
  const readback = response?.structuredContent?.result;
  const context = response?.structuredContent?.context;
  if (response?.isError === true || !sameTarget(context, target)
    || !isObject(readback) || readback.partial === true || readback.readScope?.concurrentChange !== false
    || !Array.isArray(readback.readScope?.missingIds) || readback.readScope.missingIds.length
    || !Array.isArray(readback.components)) throw new Error('Geometry receipt lacks a complete successful scoped measurement');
  const ids = result?.primitiveIds;
  const requested = readback.readScope?.primitiveIds;
  if (!Array.isArray(ids) || !Array.isArray(requested) || !expectedIDs.length || ids.some(id => !text(id))
    || !sameIDSet(ids, expectedIDs) || !sameIDSet(requested, expectedIDs)) throw new Error('Geometry receipt scope does not exactly match the measured component IDs');
  const components = new Map();
  for (const part of readback.components) {
    if (!componentGeometry(part)) throw new Error(`Geometry receipt has incomplete component measurement: ${part?.primitiveId ?? '?'}`);
    if (components.has(part.primitiveId)) throw new Error(`Geometry receipt repeats primitive ID: ${part.primitiveId}`);
    components.set(part.primitiveId, part);
  }
  if (!sameIDSet([...components.keys()], expectedIDs)) throw new Error('Geometry receipt returned components outside its exact requested scope');
  return components;
}

function policiesFor(target, supplied) {
  if (!isObject(supplied)) throw new Error('netPolicies must be an object');
  const used = new Set();
  for (const part of target.components) for (const pin of part.pins) {
    if (pin.state === 'unknown') throw new Error(`Semantic target has unknown pin state: ${part.key}.${pin.number}`);
    if (pin.state === 'connected') used.add(pin.net);
  }
  const policies = {};
  for (const net of used) {
    const policy = supplied[net];
    if (!['direct', 'module_port', 'local_power', 'local_ground'].includes(policy)) throw new Error(`Net ${net} needs an explicit route policy`);
    policies[net] = policy;
  }
  for (const net of Object.keys(supplied)) if (!used.has(net)) throw new Error(`Unused net policy: ${net}`);
  return { policies, direct: [...used].filter(net => policies[net] === 'direct'), nonDirect: [...used].filter(net => policies[net] !== 'direct') };
}

function groupCoverage(plan) {
  if (plan.layoutHints?.groups === undefined) return;
  const groups = plan.layoutHints.groups.map(group => ({ ...group, primitiveIds: group.componentKeys.map(key => plan.semanticTarget.idMap[key]) }));
  const memberships = new Map(plan.semanticTarget.components.map(part => [part.key, groups.filter(group => group.componentKeys.includes(part.key)).map(group => group.id)]));
  return { groups, ungroupedComponentKeys: [...memberships].filter(([, ids]) => !ids.length).map(([key]) => key),
    multipleGroupComponentKeys: [...memberships].filter(([, ids]) => ids.length > 1).map(([key]) => key),
    crossGroupNets: plan.semanticTarget.nets.map(net => ({ net: net.name, groupIds: [...new Set(net.members.flatMap(member => memberships.get(member.componentKey) ?? []))] }))
      .filter(net => net.groupIds.length > 1),
    note: 'Declared functional groups only; no placement or connection strategy is inferred from membership' };
}

function measuredInput(plan, components, policies) {
  const mapped = plan.semanticTarget.idMap;
  if (!isObject(mapped)) throw new Error('Semantic target needs an explicit current idMap');
  const output = [];
  const usedIDs = new Set();
  for (const part of plan.semanticTarget.components) {
    const id = mapped[part.key];
    if (!text(id)) throw new Error(`No current primitive ID mapped for ${part.key}`);
    if (usedIDs.has(id)) throw new Error(`Repeated current primitive ID in idMap: ${id}`);
    usedIDs.add(id);
    const measured = components.get(id);
    if (!measured) throw new Error(`Mapped primitive ID is not measured by the geometry receipt: ${part.key}`);
    if (measured.designator !== part.ref) throw new Error(`Measured designator disagrees with semantic target: ${part.key}`);
    const targetPins = new Map(part.pins.map(pin => [pin.number, pin]));
    const pins = measured.pins.map(pin => {
      const target = targetPins.get(pin.pinNumber);
      if (!target) throw new Error(`Measured pin has no semantic target: ${part.key}.${pin.pinNumber}`);
      targetPins.delete(pin.pinNumber);
      if (target.state === 'unknown') throw new Error(`Semantic target has unknown pin state: ${part.key}.${target.number}`);
      return { number: pin.pinNumber, name: pin.pinName, net: target.state === 'connected' ? target.net : '', x: pin.x, y: pin.y, rotation: pin.rotation };
    });
    if (targetPins.size) throw new Error(`Semantic target pins are absent from geometry receipt: ${part.key}.${[...targetPins.keys()].join(',')}`);
    const pinStates = Object.fromEntries(part.pins.filter(pin => pin.state !== 'connected').map(pin => [pin.number, pin.state]));
    output.push({ id, measurement: { primitiveId: id, designator: measured.designator, x: measured.x, y: measured.y,
      rotation: measured.rotation, mirror: measured.mirror, bbox: measured.bbox, pins }, ...(Object.keys(pinStates).length ? { pinStates } : {}) });
  }
  if (components.size !== output.length) throw new Error('Geometry receipt includes components outside the explicit semantic idMap');
  return { schemaVersion: 1, components: output, netPolicies: policies };
}

function wiresPacket(wires) {
  return { routes: wires.map(wire => ({ net: wire.net, points: wire.points })) };
}

const samePoint = (a, b) => a[0] === b[0] && a[1] === b[1];
const touches = (points, anchor) => points.some((point, index) => {
    const next = points[index + 1];
    return next && (point[0] === next[0] && anchor[0] === point[0] && anchor[1] >= Math.min(point[1], next[1]) && anchor[1] <= Math.max(point[1], next[1])
      || point[1] === next[1] && anchor[1] === point[1] && anchor[0] >= Math.min(point[0], next[0]) && anchor[0] <= Math.max(point[0], next[0]));
});

function continuousRoutes(wires) {
  const pending = wires.map(wire => ({ ...wire, points: [...wire.points] })), result = [], degrees = new Map();
  const edges = wires.flatMap(wire => wire.points.slice(1).map((point, index) => ({ net: wire.net, points: [wire.points[index], point] })));
  const degree = (net, point) => {
    const key = JSON.stringify([net, ...point]);
    if (!degrees.has(key)) degrees.set(key, edges.reduce((count, edge) => count + (edge.net !== net ? 0
      : edge.points.some(end => samePoint(end, point)) ? 1 : touches(edge.points, point) ? 2 : 0), 0));
    return degrees.get(key);
  };
  while (pending.length) {
    const route = pending.shift();
    // Complete paths avoid fragile short native creates; keep every branch as a boundary.
    for (let joined = true; joined;) {
      joined = false;
      for (const head of [false, true]) {
        const joint = head ? route.points[0] : route.points.at(-1);
        if (degree(route.net, joint) !== 2) continue;
        const index = pending.findIndex(wire => wire.net === route.net && (samePoint(wire.points[0], joint) || samePoint(wire.points.at(-1), joint)));
        if (index < 0) continue;
        const next = pending.splice(index, 1)[0].points;
        const outward = samePoint(next[0], joint) ? next : next.toReversed();
        route.points = head ? [...outward.toReversed().slice(0, -1), ...route.points] : [...route.points, ...outward.slice(1)];
        joined = true;
        break;
      }
    }
    result.push(route);
  }
  return result;
}

function typedSteps(wires, plan, components) {
  // Rotated native pins carry floating-point tails; never emit a near-zero segment.
  wires = continuousRoutes(wires).map(wire => {
    const points = wire.points.map(point => point.map(value => Number(value.toFixed(6))))
      .filter((point, index, all) => index === 0 || !samePoint(point, all[index - 1]));
    if (points.length < 2) throw new Error(`Net ${wire.net} route collapses within native coordinate tolerance`);
    return { ...wire, points };
  });
  const visible = plan.layoutHints?.visibleNetNames;
  if (visible === undefined) return wires.map(wire => ({ action: 'schematic.wire.create', payload: { net: wire.net, points: wire.points } }));
  const steps = [];
  for (const net of new Set(wires.map(wire => wire.net))) {
    const pending = wires.filter(wire => wire.net === net);
    const anchors = [...components.values()].filter(part => part.netlistAvailable === true)
      .flatMap(part => part.pins.filter(pin => pin.net === net && pin.noConnected !== true).map(pin => [pin.x, pin.y]));
    const placed = [];
    const byMembers = plan.semanticTarget.nets.find(candidate => candidate.name === net)?.match === 'members';
    // Geometry-only receipts cannot prove the native network to inherit.
    if (!byMembers && !visible.includes(net) && !anchors.length) throw new Error(`Internal net ${net} needs a measured native pin-net anchor before unnamed creation; read scoped pin nets or resolve the naming capability, do not create then hide`);
    while (pending.length) {
      const named = !byMembers && visible.includes(net) && !anchors.length && placed.length === 0;
      const index = named || (byMembers && placed.length === 0) ? 0 : pending.findIndex(wire => (!byMembers && anchors.some(anchor => touches(wire.points, anchor)))
        || placed.some(previous => wire.points.some(point => touches(previous.points, point)) || previous.points.some(point => touches(wire.points, point))));
      if (index < 0) throw new Error(`Net ${net} has a route island without a proven native naming anchor; no executable route artifact emitted`);
      const [wire] = pending.splice(index, 1);
      steps.push({ action: 'schematic.wire.create', payload: { ...(named ? { net } : {}), points: wire.points } });
      placed.push(wire);
    }
  }
  return steps;
}

async function writeNewJSON(file, value) {
  if (typeof file !== 'string' || !path.isAbsolute(file) || path.extname(file).toLowerCase() !== '.json') throw new Error('outputFile must be an absolute .json path');
  const output = path.normalize(file);
  await mkdir(path.dirname(output), { recursive: true });
  const handle = await open(output, 'wx');
  try {
    const bytes = Buffer.from(JSON.stringify(value, null, 2) + '\n');
    await handle.writeFile(bytes);
    await handle.sync();
    return { output, sha256: sha256(bytes) };
  } finally { await handle.close(); }
}

/**
 * Compile existing route-only geometry into reviewed typed wire steps. This only
 * runs the offline CLI; it never calls the Connector or changes a plan file.
 */
export async function compileRoutes(input, run = runEasyeda) {
  if (!isObject(input)) throw new Error('compile_routes input must be an object');
  for (const name of ['planFile', 'expectedHash', 'geometryReceiptFile', 'stage', 'outputFile']) if (!text(input[name])) throw new Error(`${name} is required`);
  if (!/^[a-f0-9]{64}$/.test(input.expectedHash)) throw new Error('expectedHash must be a reviewed SHA-256');
  const loaded = await loadPlan(input.planFile, { expectedHash: input.expectedHash, requireConfirmed: true });
  const { plan, planFile, sha256: planSha256 } = loaded;
  const stageIndex = plan.stages?.findIndex(candidate => candidate.id === input.stage) ?? -1;
  if (stageIndex < 0) throw new Error('compile_routes requires an existing explicit stage');
  const targetStage = plan.stages[stageIndex];
  if (!plan.executionReceipts) throw new Error('compile_routes requires managed executionReceipts');
  const mappedIDs = targetStage.primitiveIds;
  const idMap = plan.semanticTarget.idMap ?? {};
  if (!Array.isArray(mappedIDs) || !mappedIDs.length || mappedIDs.some(id => !text(id)) || new Set(mappedIDs).size !== mappedIDs.length) {
    throw new Error('Route target stage needs nonempty unique measured component primitiveIds');
  }
  const scopedParts = plan.semanticTarget.components.filter(part => mappedIDs.includes(idMap[part.key]));
  if (!sameIDSet(scopedParts.map(part => idMap[part.key]), mappedIDs)) throw new Error('Route target scope is not uniquely mapped to semantic components');
  const scopedKeys = new Set(scopedParts.map(part => part.key));
  const scopedNets = plan.semanticTarget.nets.filter(net => net.members.some(member => scopedKeys.has(member.componentKey)));
  const scopedPlan = { ...plan, semanticTarget: { ...plan.semanticTarget, components: scopedParts, nets: scopedNets },
    ...(plan.layoutHints?.groups ? { layoutHints: { ...plan.layoutHints, groups: plan.layoutHints.groups
      .map(group => ({ ...group, componentKeys: group.componentKeys.filter(key => scopedKeys.has(key)) }))
      .filter(group => group.componentKeys.length) } } : {}) };
  try { await access(managedStageReceiptFile(plan, stageIndex)); throw new Error('Route target stage already has a receipt'); }
  catch (error) { if (error?.message === 'Route target stage already has a receipt') throw error; if (error?.code !== 'ENOENT') throw error; }

  const receiptFile = path.resolve(input.geometryReceiptFile);
  let receipt = JSON.parse((await readFile(receiptFile)).toString('utf8'));
  if (receipt.schemaVersion !== 'easyeda.execution/1' || receipt.state !== 'settled' || receipt.receipt?.stage?.completed !== true
    || receipt.stage?.check !== 'geometry' || !sameTarget(receipt.target, plan.target)) throw new Error('Geometry receipt is not a settled completed receipt for this target');
  const geometryStageIndex = plan.stages.findIndex(candidate => candidate.id === receipt.stage.id);
  if (geometryStageIndex < 0 || geometryStageIndex >= stageIndex) throw new Error('Geometry receipt must belong to an earlier plan stage');
  const geometryStage = plan.stages[geometryStageIndex];
  const binding = stageReceiptBinding(plan, geometryStage);
  if (receiptFile !== path.resolve(managedStageReceiptFile(plan, geometryStageIndex))) throw new Error('Geometry receipt must be the managed receipt for its plan stage');
  // This validates immutable semantic/sources/stage bindings and permits the
  // current plan to fill idMap from the receipt's observed geometry afterwards.
  await verifyStagePrefix(loaded, stageIndex);
  const settledBytes = await readFile(receiptFile);
  receipt = JSON.parse(settledBytes.toString('utf8'));
  const components = receiptGeometry(receipt, mappedIDs, plan.target);
  const declaredPolicies = plan.layoutHints?.netPolicies ?? {};
  if (input.netPolicies !== undefined && !isObject(input.netPolicies)) throw new Error('netPolicies must be an object');
  for (const [net, policy] of Object.entries(declaredPolicies)) {
    if (Object.hasOwn(input.netPolicies ?? {}, net) && input.netPolicies[net] !== policy) throw new Error(`Net ${net} route policy conflicts with confirmed plan intent`);
  }
  const localDeclaredPolicies = Object.fromEntries(Object.entries(declaredPolicies).filter(([net]) => scopedNets.some(candidate => candidate.name === net)));
  const { policies, direct, nonDirect } = policiesFor(scopedPlan.semanticTarget, { ...localDeclaredPolicies, ...input.netPolicies });
  for (const net of scopedNets) {
    const local = net.members.filter(member => scopedKeys.has(member.componentKey));
    const stubs = (plan.layoutHints?.interfaceStubs ?? []).filter(stub => local.some(member => member.componentKey === stub.componentKey && member.pinNumber === stub.pinNumber));
    const namedInterface = local.length === 1 && stubs.length === 1 && net.match !== 'members' && (plan.layoutHints?.visibleNetNames ?? []).includes(net.name);
    if (stubs.length && !namedInterface) throw new Error(`Interface stub ${net.name} requires one measured module endpoint on a visible named net`);
    // Only a declared named interface may connect to existing out-of-scope members.
    if (direct.includes(net.name) && net.members.some(member => !scopedKeys.has(member.componentKey)) && !namedInterface) {
      throw new Error(`Direct net ${net.name} has endpoints outside the measured route scope; include them or revise confirmed expression intent`);
    }
  }
  const measured = measuredInput(scopedPlan, components, policies);
  const groups = groupCoverage(scopedPlan);
  const uncoveredConnections = scopedNets.filter(net => nonDirect.includes(net.name)).map(net => ({ net: net.name, policy: policies[net.name],
    members: net.members.map(member => {
      if (!scopedKeys.has(member.componentKey)) return { ...member, measured: false };
      const primitiveId = plan.semanticTarget.idMap[member.componentKey];
      const pin = components.get(primitiveId).pins.find(pin => pin.pinNumber === member.pinNumber);
      if (!pin) throw new Error(`Unmeasured route endpoint: ${member.componentKey}.${member.pinNumber}`);
      return { ...member, primitiveId, x: pin.x, y: pin.y, rotation: pin.rotation };
    }) }));

  // Keep transient CLI inputs separate from durable plan and receipt artifacts.
  const temp = await mkdtemp(path.join(os.tmpdir(), 'easyeda-route-'));
  try {
    const measuredFile = path.join(temp, 'measured.json');
    const plannedFile = path.join(temp, 'planned-wires.json');
    await writeFile(measuredFile, JSON.stringify(measured));
    const routed = await run(['sch', 'layout-plan', '--route-only', '--from', measuredFile]);
    if (!routed?.ok) throw new Error(`route-only failed: ${routed?.error?.message ?? 'unknown error'}`);
    const generated = routeResult(routed.result);
    for (const stub of plan.layoutHints?.interfaceStubs ?? []) {
      const id = plan.semanticTarget.idMap[stub.componentKey], part = scopedPlan.semanticTarget.components.find(part => part.key === stub.componentKey);
      if (!part) continue;
      const pin = components.get(id)?.pins.find(pin => pin.pinNumber === stub.pinNumber);
      const net = part.pins.find(pin => pin.number === stub.pinNumber)?.net;
      if (!pin || !direct.includes(net)) throw new Error('Interface stub lacks its measured pin or direct net');
      const quarter = ((pin.rotation % 360) + 360) % 360 / 90;
      if (Math.abs(quarter - Math.round(quarter)) > 1e-6) throw new Error('Interface stub pin direction is not cardinal');
      const [dx, dy] = [[1, 0], [0, 1], [-1, 0], [0, -1]][Math.round(quarter) % 4];
      generated.wires.push({ net, points: [[pin.x, pin.y], [pin.x + dx * stub.length, pin.y + dy * stub.length]] });
    }
    const routes = wiresPacket(generated.wires);
    await writeFile(plannedFile, JSON.stringify(routes));
    const checked = await run(['sch', 'layout-plan', '--route-only', '--from', measuredFile, '--validate-wires', plannedFile]);
    if (!checked?.ok) throw new Error(`route validation failed: ${checked?.error?.message ?? 'unknown error'}`);
    const validated = routeResult(checked.result);
    const artifact = {
      schemaVersion: 'easyeda.route-artifact/1', createdAt: new Date().toISOString(),
      planFile, planSha256, target: plan.target, stage: { id: targetStage.id, check: targetStage.check },
      geometryReceiptFile: receiptFile, geometryReceiptSha256: sha256(settledBytes),
     binding: { semanticTargetSha256: binding.semanticTargetSha256, sourcesSha256: binding.sourcesSha256, geometryStageSha256: binding.stageSha256, idMap: binding.idMap,
       ...(binding.requirementsSha256 ? { requirementsSha256: binding.requirementsSha256 } : {}),
       ...(binding.expressionSha256 ? { expressionSha256: binding.expressionSha256 } : {}) },
      primitiveIds: [...components.keys()],
     netPolicies: policies, ...(groups ? { groupCoverage: groups } : {}), uncoveredConnections, typedSteps: typedSteps(validated.wires, scopedPlan, components),
      ...(plan.layoutHints?.visibleNetNames === undefined ? {} : { visibleNetNames: [...plan.layoutHints.visibleNetNames], nameCreation: 'reuse measured native anchors; one explicit seed only for visible direct nets without native anchors; other wires inherit connected anchors' }),
      limits: ['Offline route-only and validate-wires proof; no EDA write, save, visible labels, or plan mutation', 'Non-direct rails and ground remain for explicit AI planning', 'Native inheritance, visual acceptance and persistence require later checks; an unconnected internal named target cannot be silently replaced by an automatic net'],
    };
    const saved = await writeNewJSON(input.outputFile, artifact);
    return { ok: true, file: saved.output, sha256: saved.sha256, steps: artifact.typedSteps.length,
      directNets: direct, uncoveredNets: nonDirect, uncoveredConnections, ...(groups ? { groupCoverage: groups } : {}), limits: artifact.limits };
  } finally {
    if (temp) await rm(temp, { recursive: true, force: true });
  }
}

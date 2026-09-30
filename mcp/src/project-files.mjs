import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir, access, unlink, realpath } from 'node:fs/promises';
import path from 'node:path';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const copy = value => JSON.parse(JSON.stringify(value));
const isObject = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const text = value => typeof value === 'string' ? value : '';
const cell = value => String(value ?? '').replaceAll('|', '\\|').replace(/[\r\n]+/g, ' ');
const keyOf = part => part.uniqueId ? `unique:${part.uniqueId}` : `ref:${part.designator ?? part.ref ?? ''}`;

function absolute(file) {
  if (typeof file !== 'string' || !path.isAbsolute(file)) throw new Error('An absolute local file path is required');
  return path.normalize(file);
}

async function readJSON(file) {
  file = absolute(file);
  const bytes = await readFile(file);
  if (bytes.length > 64 * 1024 * 1024) throw new Error('JSON exceeds 64 MiB');
  return { file, bytes, sha256: hash(bytes), value: JSON.parse(bytes.toString('utf8').replace(/^\uFEFF/, '')) };
}

// Only response containers are traversed; submitted payload/steps are never observations.
export function snapshotData(input) {
  const candidates = [];
  function visit(value, context = {}, receipt = {}) {
    if (!isObject(value) || value.ok === false || value.isError === true) return;
    const ctx = isObject(value.context) ? value.context : context;
    const meta = value.requestId || value.id ? { requestId: value.requestId ?? value.id, action: value.action, observedAt: value.ts ?? value.createdAt } : receipt;
    if (Array.isArray(value.components)) candidates.push({ snapshot: value, context: ctx, receipt: meta });
    for (const name of ['structuredContent', 'result', 'data', 'value', 'snapshot']) visit(value[name], ctx, meta);
    if (Array.isArray(value.results)) for (const item of value.results) visit(item, ctx, meta);
  }
  visit(input);
  if (candidates.length !== 1) throw new Error(`Expected one schematic snapshot, found ${candidates.length}`);
  const result = candidates[0];
  if (result.context.documentType && result.context.documentType !== 'schematic') throw new Error('Snapshot is not a schematic');
  return result;
}

function scopeOf(snapshot, context) {
  const read = snapshot.readScope ?? {};
  const complete = read.complete === true && read.concurrentChange !== true && snapshot.partial !== true && !(read.primitiveIds?.length) && !(read.missingIds?.length) && read.allPages !== true;
  return { kind: complete ? 'page' : read.allPages ? 'multiple-pages' : 'partial-or-unknown', complete,
    projectUuid: context.projectUuid ?? null, documentUuid: context.documentUuid ?? null, readScope: copy(read) };
}

function pinState(pin) {
  if (pin.noConnected === true && typeof pin.net === 'string' && pin.net !== '') return 'unknown';
  if (pin.noConnected === true) return 'nc';
  if (typeof pin.net !== 'string') return 'unknown';
  if (pin.net !== '') return 'connected';
  return pin.noConnected === false ? 'unconnected' : 'unknown';
}

function missingData(snapshot) {
  const missing = [];
  for (const part of snapshot.components.filter(c => c.componentType === 'part')) {
    const ref = part.designator ?? part.primitiveId ?? '?';
    if (!part.uniqueId) missing.push({ ref, field: 'uniqueId', status: 'unknown', note: 'Reference fallback is valid only while the designator stays unchanged' });
    for (const field of ['device', 'symbol', 'footprint']) if (!isObject(part[field]) || !part[field].uuid) missing.push({ ref, field, status: 'unknown' });
    if (!Array.isArray(part.pins) || !part.pins.length) missing.push({ ref, field: 'pins', status: 'unknown' });
    for (const pin of part.pins ?? []) if (pinState(pin) === 'unknown') missing.push({ ref, pin: pin.pinNumber ?? pin.number, field: 'connection', status: 'unknown' });
  }
  return missing;
}

function partTable(components) {
  return ['| Key / ref | Value | Device | Footprint | Pins |', '|---|---|---|---|---|', ...components.map(c => `| ${cell(c.key ?? keyOf(c))} / ${cell(c.ref ?? c.designator)} | ${cell(c.properties?.name ?? c.name)} | ${cell(c.device?.name)} | ${cell(c.footprint?.name)} | ${c.pins?.length ?? 0} |`)].join('\n');
}

function pinTable(components) {
  return ['| Component | Pin | Name | State | Net |', '|---|---|---|---|---|', ...components.flatMap(c => (c.pins ?? []).map(p => `| ${cell(c.ref ?? c.designator)} | ${cell(p.number ?? p.pinNumber)} | ${cell(p.name ?? p.pinName)} | ${cell(p.state ?? pinState(p))} | ${cell(p.net)} |`))].join('\n');
}

function outputPaths(outputFile) {
  const file = absolute(outputFile);
  if (path.extname(file).toLowerCase() !== '.json') throw new Error('outputFile must end in .json');
  const markdownFile = file.slice(0, -5) + '.md';
  return { file, markdownFile };
}

async function canonicalFile(file) {
  file = absolute(file);
  try { file = await realpath(file); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  return process.platform === 'win32' ? file.toLowerCase() : file;
}

async function protectSources(outputFile, sources, message) {
  return protectFiles(Object.values(outputPaths(outputFile)), sources, message);
}

async function protectFiles(files, sources, message) {
  const outputs = await Promise.all(files.map(canonicalFile));
  for (const source of sources) if (outputs.includes(await canonicalFile(source))) throw new Error(message);
}

async function writePair(outputFile, value, markdown, overwrite = false) {
  const { file, markdownFile } = outputPaths(outputFile);
  if (!overwrite) for (const candidate of [file, markdownFile]) {
    try { await access(candidate); throw new Error(`Refusing to overwrite existing file: ${candidate}`); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  await mkdir(path.dirname(file), { recursive: true });
  const bytes = JSON.stringify(value, null, 2) + '\n';
  await writeFile(file, bytes, { flag: overwrite ? 'w' : 'wx' });
  try { await writeFile(markdownFile, markdown, { flag: overwrite ? 'w' : 'wx' }); }
  catch (error) { if (!overwrite) await unlink(file); throw error; }
  return { outputFile: file, markdownFile, sha256: hash(bytes), bytes: Buffer.byteLength(bytes) };
}

/** Archive native facts without coercing library objects into the older connectivity IR. */
export async function exportSnapshot(input, { outputFile, overwrite = false, source = null, baselineFile } = {}) {
  const { snapshot, context, receipt } = snapshotData(input);
  const scope = scopeOf(snapshot, context);
  const missing = missingData(snapshot);
  let baseline = null;
  if (baselineFile !== undefined) {
    const original = await readJSON(baselineFile);
    await protectSources(outputFile, [original.file], 'A patch cannot overwrite its baseline');
    const base = snapshotData(original.value);
    for (const field of ['projectUuid', 'documentUuid']) if (!context[field] || context[field] !== base.context[field]) throw new Error('Patch and baseline need matching document identities');
    baseline = { path: original.file, sha256: original.sha256, mode: 'patch-reference', note: 'Absent patch objects are not deletions. Base fields retain their original observation age; this patch does not refresh omitted electrical or library evidence.' };
  }
  const archive = { schemaVersion: 'easyeda.snapshot/1', exportedAt: new Date().toISOString(), context: copy(context), scope,
    source: source ? copy(source) : null, baseline, receipt, missing, snapshot: copy(snapshot),
    limits: ['Native observations, not material/footprint approval', 'Completeness applies only to the declared page and fields; rendering and saved persistence need separate evidence'] };
  const parts = snapshot.components.filter(c => c.componentType === 'part');
  const markdown = `# Schematic observations\n\n- Scope: ${scope.kind}; complete: ${scope.complete}\n- Project: ${cell(scope.projectUuid)}; document: ${cell(scope.documentUuid)}\n- Parts: ${parts.length}; unknown fields: ${missing.length}\n${baseline ? `- Base: ${cell(baseline.path)}; SHA256 ${baseline.sha256}. This file contains a scoped patch, not a fresh full-page merge.\n` : ''}- Native attributes, library references, page primitives and source metadata are retained in the companion JSON. This is an observation archive, not approval or an EDA save.\n\n${partTable(parts)}\n\n${pinTable(parts)}\n\n## Missing evidence\n\n\`\`\`json\n${JSON.stringify(missing, null, 2)}\n\`\`\`\n`;
  return { ok: true, ...(await writePair(outputFile, archive, markdown, overwrite)), scope, counts: { components: snapshot.components.length, parts: parts.length, pins: parts.reduce((n, c) => n + (c.pins?.length ?? 0), 0) }, missing };
}

async function sourceReference(source) {
  if (!isObject(source) || !text(source.id) || !text(source.path)) throw new Error('Each source needs id and an absolute path');
  const file = absolute(source.path);
  const sha256 = hash(await readFile(file));
  if (source.sha256 && source.sha256 !== sha256) throw new Error(`Source hash changed: ${source.id}`);
  if (source.anchor !== undefined && !text(source.anchor)) throw new Error('Source anchor must be nonempty text');
  return { ...copy(source), path: file, sha256 };
}

export async function preparePlan(snapshotFile, outputFile, { sources = [], goal = '', layoutMode = 'preserve', overwrite = false } = {}) {
  const original = await readJSON(snapshotFile);
  await protectSources(outputFile, [original.file], 'Plan cannot replace its source snapshot');
  const { snapshot, context } = snapshotData(original.value);
  const scope = scopeOf(snapshot, context);
  if (!scope.complete || !scope.projectUuid || !scope.documentUuid) throw new Error('A complete identified single-page snapshot is required to prepare a full target');
  const components = snapshot.components.filter(c => c.componentType === 'part').map(c => {
    const properties = {};
    for (const name of ['name', 'manufacturer', 'manufacturerId', 'supplier', 'supplierId', 'addIntoBom', 'addIntoPcb', 'otherProperty']) if (c[name] !== undefined) properties[name] = copy(c[name]);
    const part = { key: keyOf(c), uniqueId: c.uniqueId ?? null, ref: c.designator, sourcePrimitiveId: c.primitiveId ?? null, properties,
      pins: (c.pins ?? []).map(p => ({ number: text(p.pinNumber ?? p.number), name: text(p.pinName ?? p.name), net: typeof p.net === 'string' ? p.net : null, noConnected: typeof p.noConnected === 'boolean' ? p.noConnected : null, state: pinState(p) })) };
    for (const name of ['device', 'placedDevice', 'symbol', 'footprint', 'deviceResolution']) if (c[name] !== undefined) part[name] = copy(c[name]);
    return part;
  });
  const nets = new Map();
  for (const c of components) for (const p of c.pins) if (p.state === 'connected') {
    if (!nets.has(p.net)) nets.set(p.net, { name: p.net, scope: 'unknown', members: [] });
    nets.get(p.net).members.push({ componentKey: c.key, pinNumber: p.number });
  }
  const plan = { schemaVersion: 'easyeda.plan/1', revision: 1, goal: text(goal), target: { projectUuid: scope.projectUuid, documentUuid: scope.documentUuid },
    sources: [{ id: 'snapshot', path: original.file, sha256: original.sha256 }, ...await Promise.all(sources.map(sourceReference))],
    semanticTarget: { schemaVersion: 1, scope, components, nets: [...nets.values()], idMap: {} },
    layoutHints: { mode: layoutMode, parts: snapshot.components.filter(c => c.componentType === 'part').map(c => ({ key: keyOf(c), x: c.x, y: c.y, rotation: c.rotation, mirror: c.mirror })) },
    confirmation: { status: 'unconfirmed', authoredBy: 'caller', evidence: null }, steps: [], missing: missingData(snapshot),
    limits: ['Confirmation is a caller-authored record; this file does not prove human approval', 'Unknown network scope is not inferred from names', 'Source material/footprint qualification and provisional notes remain unchanged'] };
  await protectSources(outputFile, plan.sources.map(source => source.path), 'Plan output cannot overwrite a source reference');
  validatePlan(plan);
  return { ok: true, ...(await writePair(outputFile, plan, planMarkdown(plan), overwrite)), summary: planSummary(plan) };
}

function planMarkdown(plan) {
  const components = plan.semanticTarget.components;
  const stepTable = steps => steps.length ? ['| Step | Action | Payload summary |', '|---|---|---|',
    ...steps.map((step, index) => `| ${index + 1} | ${cell(step.action)} | ${cell(JSON.stringify(step.payload ?? {})).slice(0, 300)} |`)].join('\n') : 'No executable steps recorded.';
  const steps = plan.stages ? plan.stages.map(stage => `### ${cell(stage.id)}\n\n- Check: ${stage.check}; scoped components: ${cell((stage.primitiveIds ?? []).join(', ')) || 'from actual component receipts'}\n- Stage completion is not whole-page delivery.\n\n${stepTable(stage.steps)}`).join('\n\n') : stepTable(plan.steps);
  return `# Schematic plan\n\n- Goal: ${cell(plan.goal)}\n- Revision: ${plan.revision}; confirmation: ${cell(plan.confirmation.status)} (caller-authored; human approval not verified)\n- Confirmation reference: ${cell(plan.confirmation.evidence)}\n- Project: ${cell(plan.target.projectUuid)}; document: ${cell(plan.target.documentUuid)}\n- Snapshot SHA256: ${plan.sources.find(s => s.id === 'snapshot')?.sha256 ?? 'unknown'}\n- Layout: ${cell(plan.layoutHints?.mode)}; executable steps: ${plan.steps.length}\n- Network scope is unknown unless explicitly evidenced. Source qualification notes remain in JSON and must not be treated as approved parts.\n\n${partTable(components)}\n\n${pinTable(components)}\n\n## Layout hints\n\n\`\`\`json\n${JSON.stringify(plan.layoutHints ?? {}, null, 2)}\n\`\`\`\n\n## Steps\n\nPayload summaries are limited to 300 characters; exact executable parameters remain in the JSON.\n\n${steps}\n\n## Sources\n\n${plan.sources.map(s => `- ${cell(s.id)}: ${cell(s.path)}; SHA256 ${s.sha256}${s.anchor ? `; anchor ${cell(s.anchor)}` : ''}`).join('\n')}\n\n## Missing evidence\n\n\`\`\`json\n${JSON.stringify(plan.missing ?? [], null, 2)}\n\`\`\`\n`;
}

/** Rebuild the derived view from the exact current plan; never rewrite its JSON. */
export async function renderPlan(planFile, { expectedHash } = {}) {
  const loaded = await loadPlan(planFile, { expectedHash });
  const { markdownFile } = outputPaths(loaded.planFile);
  await protectFiles([markdownFile], [loaded.planFile, ...loaded.plan.sources.map(source => source.path)], 'Plan Markdown cannot overwrite the plan or a source reference');
  await writeFile(markdownFile, planMarkdown(loaded.plan));
  return { ok: true, planFile: loaded.planFile, markdownFile, sha256: loaded.sha256, hash: loaded.sha256, humanApprovalVerified: false };
}

export function validatePlan(plan) {
  if (!isObject(plan) || plan.schemaVersion !== 'easyeda.plan/1' || !Number.isInteger(plan.revision) || plan.revision < 1) throw new Error('Unsupported plan schema/revision');
  if (!text(plan.target?.projectUuid) || !text(plan.target?.documentUuid)) throw new Error('Plan needs exact project/document UUIDs');
  const target = plan.semanticTarget;
  if (!isObject(target) || target.schemaVersion !== 1 || !Array.isArray(target.components) || !Array.isArray(target.nets)) throw new Error('Plan needs a semantic target');
  if (target.scope?.complete !== true || target.scope?.kind !== 'page' || target.scope.projectUuid !== plan.target.projectUuid || target.scope.documentUuid !== plan.target.documentUuid) throw new Error('Semantic target scope must match the complete target page');
  const keys = new Set(), refs = new Set(), pins = new Map();
  for (const c of target.components) {
    if (!text(c.key) || !text(c.ref) || keys.has(c.key) || refs.has(c.ref) || !Array.isArray(c.pins)) throw new Error('Invalid or duplicate component identity');
    if (c.key !== (c.uniqueId ? `unique:${c.uniqueId}` : `ref:${c.ref}`)) throw new Error('Component key must match its stable identity');
    keys.add(c.key); refs.add(c.ref); const numbers = new Set(); pins.set(c.key, new Map());
    for (const p of c.pins) {
      if (!text(p.number) || numbers.has(p.number) || !['connected', 'nc', 'unconnected', 'unknown'].includes(p.state)) throw new Error('Invalid or duplicate pin');
      if (p.state === 'connected' && (!text(p.net) || p.noConnected === true)) throw new Error('Connected pin needs its exact net');
      if (p.state === 'nc' && (p.noConnected !== true || text(p.net))) throw new Error('NC pin cannot have a net');
      if (p.state === 'unconnected' && (p.net !== '' || p.noConnected !== false)) throw new Error('Unconnected pin needs explicit evidence');
      numbers.add(p.number); pins.get(c.key).set(p.number, p);
    }
  }
  const names = new Set(), members = new Set();
  for (const net of target.nets) {
    if (!text(net.name) || names.has(net.name) || !Array.isArray(net.members)) throw new Error('Invalid or duplicate net');
    names.add(net.name);
    for (const member of net.members) {
      const pin = pins.get(member.componentKey)?.get(member.pinNumber), key = JSON.stringify([member.componentKey, member.pinNumber]);
      if (!pin || pin.state !== 'connected' || pin.net !== net.name || members.has(key)) throw new Error('Net membership disagrees with pin target');
      members.add(key);
    }
  }
  for (const [key, ps] of pins) for (const pin of ps.values()) if (pin.state === 'connected' && !members.has(JSON.stringify([key, pin.number]))) throw new Error('Connected pin missing from net membership');
  if (!Array.isArray(plan.sources) || !plan.sources.length || new Set(plan.sources.map(s => s.id)).size !== plan.sources.length) throw new Error('Plan needs unique source references');
  for (const source of plan.sources) if (!text(source.id) || !/^[a-f0-9]{64}$/.test(source.sha256) || !path.isAbsolute(source.path ?? '')) throw new Error('Invalid source reference');
  const validSteps = steps => Array.isArray(steps) && steps.length <= 500 && !steps.some(s => !isObject(s) || !text(s.action) || Object.keys(s).some(k => !['action', 'payload'].includes(k)) || (s.payload !== undefined && !isObject(s.payload)));
  if (!validSteps(plan.steps)) throw new Error('Invalid typed steps');
  if (plan.stages !== undefined) {
    if (!Array.isArray(plan.stages) || !plan.stages.length || plan.stages.length > 32 || plan.steps.length) throw new Error('Staged plans need 1..32 stages and empty top-level steps');
    const stageIds = new Set();
    for (const [index, stage] of plan.stages.entries()) {
      if (!isObject(stage) || !text(stage.id).trim() || stageIds.has(stage.id) || !['geometry', 'final'].includes(stage.check) || !validSteps(stage.steps)
        || Object.keys(stage).some(key => !['id', 'steps', 'check', 'primitiveIds'].includes(key))) throw new Error('Invalid or duplicate stage');
      if (stage.check === 'final' && index !== plan.stages.length - 1) throw new Error('Only the last stage can use the final check');
      if (stage.primitiveIds !== undefined && (!Array.isArray(stage.primitiveIds) || !stage.primitiveIds.length || stage.primitiveIds.length > 500 || stage.primitiveIds.some(id => !text(id).trim()) || new Set(stage.primitiveIds).size !== stage.primitiveIds.length)) throw new Error('Invalid stage primitiveIds');
      stageIds.add(stage.id);
    }
  }
  if (!isObject(plan.confirmation) || !['unconfirmed', 'confirmed'].includes(plan.confirmation.status) || plan.confirmation.authoredBy !== 'caller') throw new Error('Invalid caller-authored confirmation');
  return plan;
}

function planSummary(plan) {
  return { revision: plan.revision, target: plan.target, components: plan.semanticTarget.components.length, pins: plan.semanticTarget.components.reduce((n, c) => n + c.pins.length, 0), nets: plan.semanticTarget.nets.length, steps: plan.steps.length,
    ...(plan.stages ? { stages: plan.stages.map(({ id, check, steps, primitiveIds }) => ({ id, check, steps: steps.length, primitiveIds: primitiveIds ?? [] })) } : {}),
    confirmation: plan.confirmation, humanApprovalVerified: false, missing: plan.missing ?? [] };
}

/** Hash exact bytes and verify source bindings before a caller prepares any EDA request. */
export async function loadPlan(planFile, { expectedHash, requireConfirmed = false, verifySources = true } = {}) {
  const { file, sha256, value: plan } = await readJSON(planFile);
  if (expectedHash !== undefined && expectedHash !== sha256) throw new Error('Plan hash changed');
  validatePlan(plan);
  if (requireConfirmed && (plan.confirmation.status !== 'confirmed' || !text(plan.confirmation.evidence))) throw new Error('A caller-authored confirmation reference is required; verify actual user authorization separately');
  if (verifySources) for (const source of plan.sources) await sourceReference(source);
  return { ok: true, planFile: file, sha256, hash: sha256, plan, summary: planSummary(plan) };
}

import { validateIncrementalRegion, fullInventory, measuredPlacementBounds } from './preservation.mjs';
import { createHash } from 'node:crypto';
import { readFile, writeFile, mkdir, access, unlink, realpath, open, rename } from 'node:fs/promises';
import path from 'node:path';
import { knowledgeRequest } from '../../knowledge/src/client.mjs';
import { canonical } from '../../knowledge/src/evidence.mjs';
import { continuousInputs, taskRequirements } from './plan-rules.mjs';

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

export async function readJSON(file) {
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

export async function protectFiles(files, sources, message) {
  const outputs = await Promise.all(files.map(canonicalFile));
  for (const source of sources) if (outputs.includes(await canonicalFile(source))) throw new Error(message);
}

export async function writePair(outputFile, value, markdown, overwrite = false) {
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

function snapshotSemantics(snapshot) {
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
  return { components, nets };
}

export async function preparePlan(snapshotFile, outputFile, options = {}) {
  if (options.planFile !== undefined) return prepareDeclaredModule(snapshotFile, outputFile, options);
  const { sources = [], goal = '', layoutMode = 'preserve', requirements, layoutHints = {}, overwrite = false, taskKind } = options;
  if (!isObject(layoutHints) || Object.keys(layoutHints).some(key => !['groups', 'netPolicies', 'visibleNetNames', 'interfaceStubs'].includes(key))) throw new Error('prepare layoutHints only accepts expression declarations');
  const original = await readJSON(snapshotFile);
  await protectSources(outputFile, [original.file], 'Plan cannot replace its source snapshot');
  const { snapshot, context } = snapshotData(original.value);
  const scope = scopeOf(snapshot, context);
  if (!scope.complete || !scope.projectUuid || !scope.documentUuid) throw new Error('A complete identified single-page snapshot is required to prepare a full target');
  const { components, nets } = snapshotSemantics(snapshot);
  if(taskKind!==undefined&&taskKind!=='local_edit')throw new Error('Snapshot prepare accepts taskKind local_edit; module declarations infer their own kind');
  const plan = { schemaVersion: 'easyeda.plan/1', ...(taskKind?{taskKind,acceptanceScope:'local'}:{}), revision: 1, goal: text(goal), target: { projectUuid: scope.projectUuid, documentUuid: scope.documentUuid },
    sources: [{ id: 'snapshot', path: original.file, sha256: original.sha256 }, ...await Promise.all(sources.map(sourceReference))],
    semanticTarget: { schemaVersion: 1, scope, components, nets: [...nets.values()], idMap: taskKind ? Object.fromEntries(components.map(part=>[part.key,part.sourcePrimitiveId])) : {} },
    layoutHints: { mode: layoutMode, parts: snapshot.components.filter(c => c.componentType === 'part').map(c => ({ key: keyOf(c), x: c.x, y: c.y, rotation: c.rotation, mirror: c.mirror })), ...copy(layoutHints) },
    ...(requirements === undefined ? {} : { requirements: copy(requirements) }),
    confirmation: { status: 'unconfirmed', authoredBy: 'caller', evidence: null }, steps: [], missing: missingData(snapshot),
    limits: ['Confirmation is a caller-authored record; this file does not prove human approval', 'Unknown network scope is not inferred from names', 'Source material/footprint qualification and provisional notes remain unchanged'] };
  await protectSources(outputFile, plan.sources.map(source => source.path), 'Plan output cannot overwrite a source reference');
  validatePlan(plan);
  return { ok: true, ...(await writePair(outputFile, plan, planMarkdown(plan), overwrite)), summary: planSummary(plan) };
}

// Compile existing plan declarations, not a second design IR or an inferred approval.
async function prepareDeclaredModule(snapshotFile, outputFile, input) {
  if (!text(input.window).trim() || input.overwrite) throw new Error('Module prepare requires an explicit window and new output files');
  const original = await readJSON(snapshotFile), draft = await readJSON(input.planFile), declared = draft.value;
  if (!isObject(declared) || declared.schemaVersion !== 'easyeda.plan/1'
    || Object.keys(declared).some(key => !['schemaVersion','goal','target','sources','semanticTarget','layoutHints','confirmation','requirements','missing','limits'].includes(key)))
    throw new Error('Module prepare accepts only existing plan design declarations; omit stages, revision and execution receipts');
  if (['sources','goal','layoutMode','requirements','layoutHints'].some(key => input[key] !== undefined))
    throw new Error('Keep module declarations in planFile; do not provide conflicting prepare overrides');
  const { snapshot, context } = snapshotData(original.value), scope = scopeOf(snapshot, context);
  if (!scope.complete || !scope.projectUuid || !scope.documentUuid || !Array.isArray(snapshot.wires))
    throw new Error('Module prepare requires a complete identified single-page snapshot with explicit wires');
  fullInventory(snapshot);
  const sheetIds=new Set(snapshot.components.filter(c=>c.componentType==='sheet').map(c=>c.primitiveId));
  const incremental = snapshot.components.some(c=>c.componentType!=='sheet') || snapshot.wires.length>0
    || Object.entries(snapshot.pagePrimitives).some(([kind,rows])=>kind!=='components'&&kind!=='wires'&&rows.some(record=>kind!=='attributes'||!sheetIds.has(record.ParentPrimitiveId)));
  if (incremental) validateIncrementalRegion(snapshot,declared.layoutHints?.region);
  const target = { projectUuid: scope.projectUuid, documentUuid: scope.documentUuid };
  for (const supplied of [declared.target, declared.semanticTarget?.scope, input.target]) if (supplied
    && ['projectUuid','documentUuid'].some(key => supplied[key] !== target[key])) throw new Error('Declared module target differs from the snapshot');
  if (declared.confirmation?.status !== 'confirmed' || declared.confirmation.authoredBy !== 'caller' || !text(declared.confirmation.evidence).trim())
    throw new Error('Module prepare requires existing caller-authored confirmation evidence; it cannot approve a design');
  const semantic = declared.semanticTarget;
  if (!isObject(semantic) || semantic.schemaVersion !== 1 || !Array.isArray(semantic.components) || !semantic.components.length
    || Object.keys(semantic.idMap ?? {}).length || semantic.components.some(c => c.sourcePrimitiveId))
    throw new Error('Declare new module components without observed/source primitive IDs');
  const hints = declared.layoutHints, layouts = hints?.parts;
  if (!isObject(hints) || !['preserve','relayout'].includes(hints.mode) || !Array.isArray(layouts)
    || layouts.length !== semantic.components.length || new Set(layouts.map(c => c.key)).size !== layouts.length)
    throw new Error('Declare one layoutHints.parts entry for each new component');
  const placementBounds={};
  if(incremental)for(const part of semantic.components){
    const layout=layouts.find(item=>item.key===part.key);
    let measured=snapshot;
    if(layout?.geometrySourceId && layout.geometrySourceId!=='snapshot'){
      const reference=(declared.sources??[]).find(source=>source.id===layout.geometrySourceId);
      if(!reference)throw new Error('Placement geometry source is not declared');
      await sourceReference(reference);
      measured=snapshotData((await readJSON(reference.path)).value).snapshot;
    }
    placementBounds[part.key]=measuredPlacementBounds(part,layout??{},measured,hints.region);
  }
  const steps = semantic.components.map(part => {
    const layout = layouts.find(item => item.key === part.key);
    if (!layout || Object.keys(layout).some(key => !['key','x','y','rotation','rotationMode','mirror','subPartName','geometrySourceId'].includes(key))
      || !['native','stored'].includes(layout.rotationMode) || !text(layout.subPartName).trim())
      throw new Error(`Explicit placement rotationMode/subPartName required: ${part.key}`);
    return { action: 'schematic.component.place', payload: { libraryUuid: part.device?.libraryUuid, uuid: part.device?.uuid,
      x: layout.x, y: layout.y, rotation: layout.rotation, rotationMode: layout.rotationMode, mirror: layout.mirror,
      subPartName: layout.subPartName, designator: part.ref, uniqueId: part.uniqueId,
      ...Object.fromEntries(['addIntoBom','addIntoPcb'].filter(key => part.properties?.[key] !== undefined).map(key => [key, part.properties[key]])) } };
  });
  const preserved = incremental ? snapshotSemantics(snapshot) : {components:[],nets:new Map()};
  if (preserved.components.some(old=>semantic.components.some(part=>part.key===old.key||part.ref===old.ref)))
    throw new Error('New module duplicates an existing stable identity or designator');
  for (const net of semantic.nets) {
    const old=preserved.nets.get(net.name);
    if (old && net.match==='members') throw new Error('New internal net cannot reuse an existing net name');
    preserved.nets.set(net.name,{...copy(net),members:[...(old?.members??[]),...copy(net.members)]});
  }
  const { file, markdownFile } = outputPaths(outputFile), stem = file.slice(0, -5), jobFile = stem + '.job.json';
  const keys = semantic.components.map(c => c.key);
  const plan = { ...copy(declared), taskKind:incremental?'incremental_module':'new_module', revision: 1, target,
    sources: [await sourceReference({ id: 'module-declaration', path: draft.file, sha256: draft.sha256 }),
      await sourceReference({ id: 'snapshot', path: original.file, sha256: original.sha256 }),
      ...await Promise.all((declared.sources ?? []).map(sourceReference))],
    semanticTarget: { ...copy(semantic), scope, components:[...preserved.components,...copy(semantic.components)], nets:[...preserved.nets.values()],
      idMap:Object.fromEntries(preserved.components.map(part=>[part.key,part.sourcePrimitiveId])) },
    ...(incremental ? { acceptanceScope:'module',preservation:{sourceId:'snapshot',region:copy(hints.region),placementBounds} } : {}), steps: [], missing: declared.missing ?? [],
    executionReceipts: { directory: stem + '.execution' }, stages: [
      { id: 'place', check: 'geometry', steps },
      { id: 'measure', check: 'geometry', componentKeys: keys, steps: [] },
      { id: 'routes', check: 'geometry', componentKeys: keys, routeFrom: 'measure', steps: [] },
      { id: 'labels', check: 'geometry', componentKeys: keys, labelsFrom: 'routes', steps: [] },
      { id: 'final', check: 'final', steps: [] },
    ], limits: [...(declared.limits ?? []), 'Prepared input only, not execution or human approval proof; observed IDs are bound by run.'] };
  validatePlan(plan);
  const missing = continuousInputs(plan);
  if (missing.length) { const error = new Error('Module design inputs are incomplete; no plan/job written'); error.missing = missing; throw error; }
  await protectFiles([file, markdownFile, jobFile], plan.sources.map(source => source.path), 'Prepared files cannot replace their sources');
  for (const candidate of [file, markdownFile, jobFile, plan.executionReceipts.directory]) {
    try { await access(candidate); throw new Error(`Refusing to reuse prepared output: ${candidate}`); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
  }
  const written = await writePair(file, plan, planMarkdown(plan));
  const job = { operation: 'run', continuous: true, planFile: file, expectedHash: written.sha256, window: input.window, target };
  try { await writeFile(jobFile, JSON.stringify(job, null, 2) + '\n', { flag: 'wx' }); }
  catch (error) { error.message += `; plan remains at ${file}, no EDA action was sent`; throw error; }
  return { ok: true, ...written, jobFile, job, summary: planSummary(plan), executionState: 'not_executed' };
}

function requirementsMarkdown(plan) {
  if (plan.requirements === undefined) return '';
  return '\n\n## Requirements (declared_only)\n\nPlanned checks, not results or proof of coverage. Execution receipts and current visual evidence are still required.\n\n'
    + ['| ID / source | When / stage | Targets | Planned method / check | Severity |', '|---|---|---|---|---|',
      ...plan.requirements.map(rule => `| ${cell(rule.id)} / ${cell(rule.sourceId)} | ${rule.when} / ${cell(rule.stageId ?? 'page')} | ${cell([...rule.componentKeys, ...rule.netNames].join(', ')) || 'whole declared stage/page'} | ${rule.method} / ${cell(rule.check)} | ${rule.severity} |`)].join('\n');
}

function planMarkdown(plan) {
  const components = plan.semanticTarget.components;
  const stepTable = steps => steps.length ? ['| Step | Action | Payload summary |', '|---|---|---|',
    ...steps.map((step, index) => `| ${index + 1} | ${cell(step.action)} | ${cell(JSON.stringify(step.payload ?? {})).slice(0, 300)} |`)].join('\n') : 'No executable steps recorded.';
  const steps = plan.stages ? plan.stages.map(stage => `### ${cell(stage.id)}\n\n- Check: ${stage.check}; scoped components: ${cell((stage.primitiveIds ?? []).join(', ')) || 'from actual component receipts'}${stage.componentKeys ? `\n- Logical scope: ${cell(stage.componentKeys.join(', '))}` : ''}${stage.routeFrom ? `\n- Compile direct routes from managed geometry stage: ${cell(stage.routeFrom)}; actual steps stay in the compiler artifact.` : ''}${stage.labelsFrom ? `\n- Compile real Name visibility from wire stage: ${cell(stage.labelsFrom)}.` : ''}\n- Stage completion is not whole-page delivery.\n\n${stepTable(stage.steps)}`).join('\n\n') : stepTable(plan.steps);
  return `# Schematic plan\n\n- Goal: ${cell(plan.goal)}\n- Revision: ${plan.revision}; confirmation: ${cell(plan.confirmation.status)} (caller-authored; human approval not verified)\n- Acceptance scope: ${plan.acceptanceScope ?? 'page'}\n- Confirmation reference: ${cell(plan.confirmation.evidence)}\n- Project: ${cell(plan.target.projectUuid)}; document: ${cell(plan.target.documentUuid)}\n- Snapshot SHA256: ${plan.sources.find(s => s.id === 'snapshot')?.sha256 ?? 'unknown'}\n- Layout: ${cell(plan.layoutHints?.mode)}; executable steps: ${plan.steps.length}\n- Network scope is unknown unless explicitly evidenced. Source qualification notes remain in JSON and must not be treated as approved parts.\n\n${partTable(components)}\n\n${pinTable(components)}\n\n## Network comparison\n\n${plan.semanticTarget.nets.map(net => `- ${cell(net.name)}: ${net.match === "members" ? "exact pin members; native name may differ" : "exact name and pin connections"}`).join("\n")}${requirementsMarkdown(plan)}\n\n## Layout hints\n\n\`\`\`json\n${JSON.stringify(plan.layoutHints ?? {}, null, 2)}\n\`\`\`\n\n## Steps\n\nPayload summaries are limited to 300 characters; exact executable parameters remain in the JSON.\n\n${steps}\n\n## Sources\n\n${plan.sources.map(s => `- ${cell(s.id)}: ${cell(s.path)}; SHA256 ${s.sha256}${s.anchor ? `; anchor ${cell(s.anchor)}` : ''}${s.knowledge ? `; adoption ${cell(s.knowledge.adoptedFor)}; conditions ${cell(s.knowledge.conditions)}; components ${cell(s.knowledge.componentKeys.join(', '))}; nets ${cell(s.knowledge.netNames.join(', '))}; check method ${cell(s.knowledge.checkMethod)} (not a check result)` : ''}`).join('\n')}\n\n## Missing evidence\n\n\`\`\`json\n${JSON.stringify(plan.missing ?? [], null, 2)}\n\`\`\`\n`;
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
  if (plan.acceptanceScope !== undefined && !['page', 'module', 'local', 'local_display'].includes(plan.acceptanceScope)) throw new Error('Unsupported plan acceptanceScope');
  if (!text(plan.target?.projectUuid) || !text(plan.target?.documentUuid)) throw new Error('Plan needs exact project/document UUIDs');
  if (plan.preservation && (plan.acceptanceScope!=='module' || plan.preservation.sourceId!=='snapshot' || !plan.layoutHints?.region)) throw new Error('Preservation requires the bound incremental module baseline');
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
    if (net.match !== undefined && !['name', 'members'].includes(net.match)) throw new Error('Invalid net match mode');
    if (net.match === 'members' && (net.scope !== 'local' || net.members.length < 2
      || plan.layoutHints?.netPolicies?.[net.name] !== 'direct'
      || !Array.isArray(plan.layoutHints?.visibleNetNames) || plan.layoutHints.visibleNetNames.includes(net.name))) {
      throw new Error('Member matching requires a local direct net with at least two pins and explicitly no visible name');
    }
    for (const member of net.members) {
      const pin = pins.get(member.componentKey)?.get(member.pinNumber), key = JSON.stringify([member.componentKey, member.pinNumber]);
      if (!pin || pin.state !== 'connected' || pin.net !== net.name || members.has(key)) throw new Error('Net membership disagrees with pin target');
      members.add(key);
    }
  }
  for (const [key, ps] of pins) for (const pin of ps.values()) if (pin.state === 'connected' && !members.has(JSON.stringify([key, pin.number]))) throw new Error('Connected pin missing from net membership');
  if (!Array.isArray(plan.sources) || !plan.sources.length || new Set(plan.sources.map(s => s.id)).size !== plan.sources.length) throw new Error('Plan needs unique source references');
  for (const source of plan.sources) if (!text(source.id) || !/^[a-f0-9]{64}$/.test(source.sha256) || !path.isAbsolute(source.path ?? '')) throw new Error('Invalid source reference');
  for (const source of plan.sources) if (source.knowledge !== undefined) {
    const k=source.knowledge;
    if (!isObject(k) || !text(k.adoptedFor).trim() || !text(k.conditions).trim()
      || !['native','checker','ai_review','manual_unknown'].includes(k.checkMethod)
      || !Array.isArray(k.componentKeys) || k.componentKeys.some(key=>!keys.has(key))
      || !Array.isArray(k.netNames) || k.netNames.some(name=>!names.has(name))
      || (!k.componentKeys.length && !k.netNames.length)) throw new Error('Knowledge source needs adoption conditions, valid targets and check method');
  }
  const validSteps = steps => Array.isArray(steps) && steps.length <= 500 && !steps.some(s => !isObject(s) || !text(s.action) || Object.keys(s).some(k => !['action', 'payload'].includes(k)) || (s.payload !== undefined && !isObject(s.payload)));
  if (!validSteps(plan.steps)) throw new Error('Invalid typed steps');
  if (plan.stages !== undefined) {
    if (!Array.isArray(plan.stages) || !plan.stages.length || plan.stages.length > 32 || plan.steps.length) throw new Error('Staged plans need 1..32 stages and empty top-level steps');
    const stageIds = new Set();
    for (const [index, stage] of plan.stages.entries()) {
      if (!isObject(stage) || !text(stage.id).trim() || stageIds.has(stage.id) || !['geometry', 'final'].includes(stage.check) || !validSteps(stage.steps)
        || Object.keys(stage).some(key => !['id', 'steps', 'check', 'primitiveIds', 'componentKeys', 'routeFrom', 'labelsFrom'].includes(key))) throw new Error('Invalid or duplicate stage');
      if (stage.componentKeys !== undefined && (!Array.isArray(stage.componentKeys) || !stage.componentKeys.length
        || new Set(stage.componentKeys).size !== stage.componentKeys.length || stage.componentKeys.some(key => !keys.has(key)))) throw new Error('Stage componentKeys needs unique declared component identities');
      const logicalIds = stage.componentKeys?.map(key => target.idMap?.[key]);
      if (stage.componentKeys && stage.primitiveIds && (logicalIds.some(id => !id)
        || canonical([...logicalIds].sort()) !== canonical([...stage.primitiveIds].sort()))) throw new Error('Stage logical scope differs from its observed IDs');
      if (stage.check === 'final' && index !== plan.stages.length - 1) throw new Error('Only the last stage can use the final check');
      if (stage.primitiveIds !== undefined && (!Array.isArray(stage.primitiveIds) || !stage.primitiveIds.length || stage.primitiveIds.length > 500 || stage.primitiveIds.some(id => !text(id).trim()) || new Set(stage.primitiveIds).size !== stage.primitiveIds.length)) throw new Error('Invalid stage primitiveIds');
      if (stage.routeFrom !== undefined) {
        const geometry = plan.stages.slice(0, index).find(previous => previous.id === stage.routeFrom);
        const pending = !stage.primitiveIds && stage.componentKeys?.length && !logicalIds.some(Boolean);
        const parts = plan.semanticTarget.components.filter(part => pending ? stage.componentKeys.includes(part.key) : stage.primitiveIds?.includes(plan.semanticTarget.idMap?.[part.key]));
        if (stage.labelsFrom || stage.check !== 'geometry' || stage.steps.length || (!stage.primitiveIds?.length && !pending) || geometry?.check !== 'geometry'
          || (pending ? geometry.primitiveIds || canonical(geometry.componentKeys) !== canonical(stage.componentKeys)
            : !geometry.primitiveIds || canonical([...geometry.primitiveIds].sort()) !== canonical([...stage.primitiveIds].sort()))
          || !path.isAbsolute(plan.executionReceipts?.directory ?? '')
          || parts.length !== (stage.primitiveIds?.length ?? stage.componentKeys?.length) || !Array.isArray(plan.layoutHints?.visibleNetNames)
          || plan.confirmation.status !== 'confirmed' || !text(plan.confirmation.evidence)
          || parts.some(part => part.pins.some(pin => pin.state === 'unknown' || pin.state === 'connected' && plan.layoutHints?.netPolicies?.[pin.net] !== 'direct'))) {
          throw new Error('routeFrom needs a confirmed scoped direct-route stage, empty steps, mapped IDs and an earlier geometry stage');
        }
      }
      if (stage.labelsFrom !== undefined) {
        const wired = plan.stages.slice(0, index).find(previous => previous.id === stage.labelsFrom);
        const sameScope = stage.primitiveIds && wired?.primitiveIds
          ? canonical([...stage.primitiveIds].sort()) === canonical([...wired.primitiveIds].sort())
          : stage.componentKeys && canonical(stage.componentKeys) === canonical(wired?.componentKeys ?? null);
        if (stage.routeFrom || stage.check !== 'geometry' || stage.steps.length || wired?.check !== 'geometry'
          || !sameScope || !path.isAbsolute(plan.executionReceipts?.directory ?? '') || !Array.isArray(plan.layoutHints?.visibleNetNames)
          || (!wired.routeFrom && !wired.steps.some(step => step.action === 'schematic.wire.create'))) throw new Error('labelsFrom needs an earlier wired stage, the same scope and confirmed visible names');
      }
      stageIds.add(stage.id);
    }
  }
  const automatic=taskRequirements(plan);
  if(automatic!==undefined)plan.requirements=automatic;
  validateRuleDeclarations(plan, keys, names);
  if (plan.acceptanceScope === 'local_display') {
    const fields = ['x', 'y', 'rotation', 'color', 'fontName', 'fontSize', 'bold', 'italic', 'underLine', 'alignMode', 'fillColor', 'keyVisible', 'valueVisible'];
    if (!plan.stages?.length || plan.stages.some(stage => stage.check !== 'geometry' || !stage.primitiveIds?.length || !stage.steps.length || stage.steps.some(step => {
      const payload = step.payload;
      return step.action !== 'schematic.attribute.modify' || !isObject(payload) || !text(payload.primitiveId).trim()
        || Object.keys(payload).some(key => !['primitiveId', 'props', 'fontSizeUnit'].includes(key))
        || (payload.fontSizeUnit !== undefined && !['native', 'display'].includes(payload.fontSizeUnit))
        || !isObject(payload.props) || !Object.keys(payload.props).length || Object.keys(payload.props).some(key => !fields.includes(key));
    }))) throw new Error('local_display needs only scoped nonempty geometry stages of official display attribute changes');
    if ((plan.requirements ?? []).some(rule => rule.when === 'delivery')) throw new Error('local_display cannot declare delivery requirements; visual and persistence evidence remain separate');
  }
  if (plan.executionReceipts !== undefined && (!plan.stages || !isObject(plan.executionReceipts) || !path.isAbsolute(plan.executionReceipts.directory ?? '')
    || Object.keys(plan.executionReceipts).some(key => key !== 'directory'))) throw new Error('executionReceipts needs an absolute directory');
  if (target.idMap !== undefined && (!isObject(target.idMap) || Object.entries(target.idMap).some(([key, id]) => !keys.has(key) || !text(id).trim())
    || new Set(Object.values(target.idMap)).size !== Object.keys(target.idMap).length)) throw new Error('Semantic target idMap needs unique observed IDs for existing component keys');
  if (!isObject(plan.confirmation) || !['unconfirmed', 'confirmed'].includes(plan.confirmation.status) || plan.confirmation.authoredBy !== 'caller') throw new Error('Invalid caller-authored confirmation');
  return plan;
}

const ruleWhen = ['before_write', 'after_edit', 'delivery'];
const ruleMethods = ['native', 'checker', 'ai_review', 'manual_unknown'];
const netPolicies = ['direct', 'module_port', 'local_power', 'local_ground'];
const stringArray = { type: 'array', items: { type: 'string', minLength: 1 }, uniqueItems: true };
export const requirementsSchema = { type: 'array', items: { type: 'object', properties: {
  id: { type: 'string', minLength: 1 }, sourceId: { type: 'string', minLength: 1 },
  when: { type: 'string', enum: ruleWhen }, method: { type: 'string', enum: ruleMethods },
  check: { type: 'string', minLength: 1 }, severity: { type: 'string', enum: ['error', 'warn'] },
  componentKeys: stringArray, netNames: stringArray, stageId: { type: 'string', minLength: 1 },
}, required: ['id', 'sourceId', 'when', 'method', 'check', 'severity', 'componentKeys', 'netNames'], additionalProperties: false }, description: 'prepare/inspect only declare requirements; staged apply maps supported predicates to actual receipts. Targets and sources must exist in the same plan.' };
export const expressionSchema = { type: 'object', properties: {
  region: {type:'object',properties:Object.fromEntries(['minX','minY','maxX','maxY'].map(key=>[key,{type:'number'}])),required:['minX','minY','maxX','maxY'],additionalProperties:false},
  groups: { type: 'array', items: { type: 'object', properties: { id: { type: 'string', minLength: 1 }, purpose: { type: 'string', minLength: 1 }, componentKeys: { ...stringArray, minItems: 1 } }, required: ['id', 'purpose', 'componentKeys'], additionalProperties: false } },
  netPolicies: { type: 'object', additionalProperties: { type: 'string', enum: netPolicies } },
  visibleNetNames: stringArray,
  interfaceStubs: { type: 'array', items: { type: 'object', properties: { componentKey: { type: 'string' }, pinNumber: { type: 'string' }, length: { type: 'number', exclusiveMinimum: 0 } }, required: ['componentKey', 'pinNumber', 'length'], additionalProperties: false } },
}, additionalProperties: false, description: 'prepare: optional expression intent, not executed layout. Omitted visibility means undecided; [] explicitly declares no visible wire names, subject to actual connection review.' };

function validateRuleDeclarations(plan, keys, names) {
  const targets = (values, allowed) => Array.isArray(values) && new Set(values).size === values.length && values.every(value => allowed.has(value));
  if (plan.requirements !== undefined) {
    if (!Array.isArray(plan.requirements)) throw new Error('requirements must be an array');
    const bindings = new Set(), sources = new Set(plan.sources.map(source => source.id)), stages = new Set((plan.stages ?? []).map(stage => stage.id));
    for (const rule of plan.requirements) {
      if (!isObject(rule) || Object.keys(rule).some(key => !Object.hasOwn(requirementsSchema.items.properties, key))
        || !text(rule.id).trim() || !sources.has(rule.sourceId) || !ruleWhen.includes(rule.when) || !ruleMethods.includes(rule.method)
        || !text(rule.check).trim() || !['error', 'warn'].includes(rule.severity)
        || !targets(rule.componentKeys, keys) || !targets(rule.netNames, names)
        || (rule.stageId !== undefined && !stages.has(rule.stageId))) throw new Error('Invalid requirement declaration, source, stage or targets');
      const binding = JSON.stringify([rule.id, rule.when, rule.stageId ?? null]);
      if (bindings.has(binding)) throw new Error('Duplicate requirement at the same stage and trigger');
      bindings.add(binding);
    }
  }
  const hints = plan.layoutHints;
  if (hints === undefined) return;
  if (!isObject(hints)) throw new Error('layoutHints must be an object');
  if (hints.groups !== undefined) {
    if (!Array.isArray(hints.groups)) throw new Error('layoutHints.groups must be an array');
    const ids = new Set();
    for (const group of hints.groups) {
      if (!isObject(group) || Object.keys(group).some(key => !['id', 'purpose', 'componentKeys'].includes(key))
        || !text(group.id).trim() || ids.has(group.id) || !text(group.purpose).trim()
        || !targets(group.componentKeys, keys) || !group.componentKeys.length) throw new Error('Invalid expression group or component targets');
      ids.add(group.id);
    }
  }
  if (hints.netPolicies !== undefined && (!isObject(hints.netPolicies) || Object.entries(hints.netPolicies).some(([name, policy]) => !names.has(name) || !netPolicies.includes(policy)))) throw new Error('Invalid expression net policy or target');
  if (hints.visibleNetNames !== undefined && !targets(hints.visibleNetNames, names)) throw new Error('Invalid visible net targets');
  if (hints.interfaceStubs !== undefined) {
    if (!Array.isArray(hints.interfaceStubs)) throw new Error('interfaceStubs must be an array');
    const used = new Set();
    for (const stub of hints.interfaceStubs) {
      const part = plan.semanticTarget.components.find(part => part.key === stub?.componentKey);
      const pin = part?.pins.find(pin => pin.number === stub?.pinNumber);
      const key = JSON.stringify([stub?.componentKey, stub?.pinNumber]);
      if (!isObject(stub) || Object.keys(stub).some(k => !['componentKey', 'pinNumber', 'length'].includes(k))
        || !pin || pin.state !== 'connected' || !Number.isFinite(stub.length) || stub.length <= 0 || used.has(key)
        || hints.netPolicies?.[pin.net] !== 'direct' || !hints.visibleNetNames?.includes(pin.net)
        || plan.semanticTarget.nets.find(net => net.name === pin.net)?.match === 'members') throw new Error('Interface stub requires an explicit visible named direct net and positive length');
      used.add(key);
    }
  }
}

const receiptSources = plan => plan.sources.map(({ id, path, sha256 }) => ({ id, path, sha256 }));
const fingerprint = value => hash(Buffer.from(canonical(value)));
export const managedStageReceiptFile = (plan, index) => path.join(plan.executionReceipts.directory, `stage-${String(index + 1).padStart(2, '0')}.json`);
export function stageReceiptBinding(plan, stage) {
  const { idMap = {}, ...semanticTarget } = plan.semanticTarget;
  const requirements = (plan.requirements ?? []).filter(rule => (!rule.stageId || rule.stageId === stage.id) && (rule.when !== 'delivery' || stage.check === 'final'));
  return { receiptDirectory: path.normalize(plan.executionReceipts.directory), semanticTargetSha256: fingerprint(semanticTarget), sourcesSha256: fingerprint(receiptSources(plan)), stageSha256: fingerprint(stage), idMap: copy(idMap),
    ...(requirements.length ? { requirementsSha256: fingerprint(requirements) } : {}),
    ...(plan.taskKind?{acceptanceSha256:fingerprint({acceptanceScope:plan.acceptanceScope??'page',taskKind:plan.taskKind})}:{}),
    ...(plan.acceptanceScope === 'local_display' ? { acceptanceSha256: fingerprint({ acceptanceScope: plan.acceptanceScope, stages: plan.stages.map(({ id, primitiveIds }) => ({ id, primitiveIds })) }) } : {}),
    ...(plan.preservation ? {preservationSha256:fingerprint(plan.preservation)} : {}),
    ...(plan.layoutHints ? { expressionSha256: fingerprint(plan.layoutHints) } : {}) };
}

// A route declaration stays immutable; only its bound compiler artifact supplies executable steps.
export async function executionSteps(plan, stage, report, { planFile, planSha256 }) {
  if (stage.labelsFrom) {
    const index = plan.stages.indexOf(stage), wireIndex = plan.stages.findIndex(previous => previous.id === stage.labelsFrom);
    const file = managedStageReceiptFile(plan, index).replace(/\.json$/i, '.labels.json');
    if (report.compilation?.file !== file) throw new Error('Labels lack their managed compiler artifact');
    const bytes = await readFile(file), artifact = JSON.parse(bytes.toString('utf8'));
    const wireFile = managedStageReceiptFile(plan, wireIndex), binding = stageReceiptBinding(plan, plan.stages[wireIndex]);
    const wireBytes = await readFile(wireFile), wirePacket = JSON.parse(wireBytes.toString('utf8'));
    const wireSteps = await executionSteps(plan,plan.stages[wireIndex],wirePacket.receipt,{planFile,planSha256:wirePacket.sha256});
    const wired = wirePacket.receipt?.execution?.structuredContent?.result?.results ?? [];
    const created = wired.flatMap((row,index) => row.ok===true && wireSteps[index]?.action==='schematic.wire.create'
      ? [{id:row.result?.primitiveId,net:wireSteps[index].payload.net??null}] : []);
    const step = artifact.typedStep;
    if (hash(bytes) !== report.compilation.sha256 || artifact.schemaVersion !== 'easyeda.labels/1'
      || artifact.planFile !== planFile || artifact.planSha256 !== planSha256 || canonical(artifact.target) !== canonical(plan.target)
      || canonical(artifact.stage) !== canonical({ id: stage.id, check: stage.check })
      || artifact.wireReceiptFile !== wireFile || artifact.wireReceiptSha256 !== hash(wireBytes)
      || ['semanticTargetSha256','sourcesSha256','expressionSha256'].some(key => artifact.binding?.[key] !== binding[key])
      || artifact.binding?.wireStageSha256 !== binding.stageSha256
      || canonical(artifact.visibleNetNames) !== canonical(plan.layoutHints.visibleNetNames)
      || step?.action !== 'schematic.wire.labels.apply' || canonical(artifact.typedSteps) !== canonical([step])
      || !Array.isArray(step.payload?.wireIds) || !step.payload.wireIds.length || !Array.isArray(step.payload.labels)
      || Object.keys(step.payload).some(key=>!['wireIds','labels'].includes(key)) || created.some(wire=>!text(wire.id))
      || canonical([...step.payload.wireIds].sort()) !== canonical(created.map(wire=>wire.id).sort())
      || step.payload.labels.some(label => !isObject(label)||Object.keys(label).some(key=>key!=='wireId')
        || !created.some(wire=>wire.id===label.wireId&&plan.layoutHints.visibleNetNames.includes(wire.net)))
      || created.some(wire=>plan.layoutHints.visibleNetNames.includes(wire.net)&&!step.payload.labels.some(label=>created.some(member=>member.id===label.wireId&&member.net===wire.net)))) throw new Error('Labels artifact differs from its declared wire evidence');
    return artifact.typedSteps;
  }
  if (!stage.routeFrom) return stage.steps;
  const index = plan.stages.indexOf(stage), geometryIndex = plan.stages.findIndex(previous => previous.id === stage.routeFrom);
  const file = managedStageReceiptFile(plan, index).replace(/\.json$/i, '.routes.json');
  if (report.compilation?.file !== file || !/^[a-f0-9]{64}$/.test(report.compilation?.sha256 ?? '')) throw new Error('Route stage lacks its managed compiler artifact');
  const bytes = await readFile(file), artifact = JSON.parse(bytes.toString('utf8'));
  const geometryFile = managedStageReceiptFile(plan, geometryIndex), geometryBytes = await readFile(geometryFile);
  const binding = stageReceiptBinding(plan, plan.stages[geometryIndex]);
  const scopedParts = plan.semanticTarget.components.filter(part => stage.primitiveIds.includes(plan.semanticTarget.idMap[part.key]));
  const nets = new Set(scopedParts.flatMap(part => part.pins.filter(pin => pin.state === 'connected').map(pin => pin.net)));
  if (hash(bytes) !== report.compilation.sha256 || artifact.schemaVersion !== 'easyeda.route-artifact/1'
    || artifact.planFile !== planFile || artifact.planSha256 !== planSha256 || canonical(artifact.target) !== canonical(plan.target)
    || artifact.stage?.id !== stage.id || artifact.stage?.check !== stage.check
    || canonical([...artifact.primitiveIds ?? []].sort()) !== canonical([...stage.primitiveIds].sort())
    || artifact.geometryReceiptFile !== geometryFile || artifact.geometryReceiptSha256 !== hash(geometryBytes)
    || ['semanticTargetSha256', 'sourcesSha256', 'requirementsSha256', 'expressionSha256'].some(key => artifact.binding?.[key] !== binding[key])
    || artifact.binding?.geometryStageSha256 !== binding.stageSha256 || canonical(artifact.binding?.idMap) !== canonical(binding.idMap)
    || canonical(artifact.visibleNetNames) !== canonical(plan.layoutHints.visibleNetNames)
    || canonical(artifact.netPolicies) !== canonical(Object.fromEntries([...nets].map(net => [net, plan.layoutHints.netPolicies[net]])))
    || !Array.isArray(artifact.uncoveredConnections) || artifact.uncoveredConnections.length
    || !Array.isArray(artifact.typedSteps) || !artifact.typedSteps.length || artifact.typedSteps.length >= 500
    || artifact.typedSteps.some(step => step?.action !== 'schematic.wire.create' || !isObject(step.payload)
      || Object.keys(step.payload).some(key => !['net', 'points'].includes(key)) || !Array.isArray(step.payload.points)
      || step.payload.points.length < 2 || step.payload.points.some(point => !Array.isArray(point) || point.length !== 2 || !point.every(Number.isFinite))
      || step.payload.net !== undefined && (!nets.has(step.payload.net) || !plan.layoutHints.visibleNetNames.includes(step.payload.net)))) {
    throw new Error('Compiler artifact differs from its reviewed route declaration or geometry evidence');
  }
  // Apply declared instance values in the complete wiring unit, after real IDs exist.
  const measured = JSON.parse(geometryBytes.toString('utf8')).receipt?.readback?.structuredContent?.result?.components ?? [];
  const properties = stage.componentKeys ? scopedParts.flatMap(part => {
    const id = plan.semanticTarget.idMap[part.key], actual = measured.find(part => part.primitiveId === id);
    if (!actual) throw new Error('Declared instance properties lack their measured native owner');
    if (Object.keys(part.properties??{}).some(key=>!['name','manufacturer','manufacturerId','supplier','supplierId','addIntoBom','addIntoPcb','otherProperty'].includes(key))) throw new Error('Instance values cannot replace placement geometry or identities');
    const patch = Object.fromEntries(Object.entries(part.properties ?? {}).filter(([key,value]) => !Object.hasOwn(actual,key) || canonical(actual[key]) !== canonical(value)));
    return Object.keys(patch).length ? [{action:'schematic.component.modify',payload:{primitiveId:id,patch}}] : [];
  }) : [];
  return [...properties, ...artifact.typedSteps];
}

function receiptResult(receipt) {
  const value = receipt?.receipt;
  return value?.result ?? value;
}

function observedIds(receipt) {
  const result = receiptResult(receipt), ids = new Set(result?.primitiveIds ?? []);
  const components = result?.readback?.structuredContent?.result?.components;
  for (const component of components ?? []) if (text(component?.primitiveId)) ids.add(component.primitiveId);
  return ids;
}

function successfulBatch(execution, steps) {
  const find = value => {
    if (!isObject(value)) return null;
    if (Array.isArray(value.results) && ['ok', 'partial', 'completed', 'total'].every(key => key in value)) return value;
    return find(value.result) ?? find(value.structuredContent);
  };
  const batch = find(execution);
  return !execution?.isError && batch?.ok === true && batch.partial !== true && batch.completed === steps.length && batch.total === steps.length
    && Array.isArray(batch.results) && batch.results.length === steps.length && batch.results.every((result, index) => result?.ok === true && result.action === steps[index].action);
}

function completedReceipt(receipt, stage, steps = stage.steps) {
  const started = Date.parse(receipt.startedAt), completed = Date.parse(receipt.completedAt), report = receipt.receipt;
  return receipt.dryRun === false && Number.isFinite(started) && Number.isFinite(completed) && started <= completed
    && receipt.stage?.id === stage.id && receipt.stage?.check === stage.check && receipt.stage?.completed === true
    && report?.ok === true && report.partial !== true && report.stage?.id === stage.id && report.stage?.check === stage.check
    && report.stage?.completed === true && (!steps.length || successfulBatch(report.execution, steps));
}

export async function bindPlacedComponents(input) {
  if (!/^[a-f0-9]{64}$/.test(input.expectedHash ?? '')) throw new Error('bind requires the reviewed plan hash');
  const loaded = await loadPlan(input.planFile, { expectedHash: input.expectedHash });
  const { plan, planFile } = loaded;
  const index = plan.stages?.findIndex(stage => stage.id === input.stage) ?? -1;
  const stage = plan.stages?.[index];
  if (index < 0 || !stage.steps.length || stage.steps.some(step => step.action !== 'schematic.component.place')) throw new Error('bind requires a managed component-placement stage');
  const prefix = await verifyStagePrefix(loaded, index + 1);
  const prior = prefix.receipts.at(-1), report = receiptResult(prior.packet);
  const actual = report?.readback?.structuredContent;
  if (!actual?.ok || actual.context?.projectUuid !== plan.target.projectUuid || actual.context?.documentUuid !== plan.target.documentUuid
    || actual.result?.partial === true || actual.result?.readScope?.concurrentChange !== false
    || !Array.isArray(actual.result.readScope.missingIds) || actual.result.readScope.missingIds.length) throw new Error('bind needs current scoped native placement readback');
  const execution = report.execution?.structuredContent?.result;
  const mapping = { ...plan.semanticTarget.idMap };
  const componentKeys = [];
  for (const [i, step] of stage.steps.entries()) {
    const part = plan.semanticTarget.components.find(part => part.ref === step.payload?.designator);
    const created = execution?.results?.[i]?.result;
    const id = created?.component?.primitiveId ?? created?.primitiveId;
    const matches = actual.result.components?.filter(record => record.primitiveId === id && record.designator === part?.ref);
    if (!part || !id || matches?.length !== 1 || !matches[0].pinsAvailable || !matches[0].pins?.length
      || part.uniqueId && matches[0].uniqueId !== part.uniqueId
      || step.payload.uuid !== part.device?.uuid || step.payload.libraryUuid !== part.device?.libraryUuid
      || mapping[part.key] && mapping[part.key] !== id
      || Object.entries(mapping).some(([key, value]) => key !== part.key && value === id)) throw new Error('Placement ID, source device or native part identity is ambiguous; no plan updated');
    const numbers = matches[0].pins.map(pin => pin.pinNumber).sort();
    if (canonical(numbers) !== canonical(part.pins.map(pin => pin.number).sort())) throw new Error('Placed native pins differ from the independent target');
    mapping[part.key] = id;
    componentKeys.push(part.key);
  }
  const updated = copy(plan), ids = componentKeys.map(key => mapping[key]);
  updated.semanticTarget.idMap = mapping;
  const nextStages = input.bindStages;
  if (!Array.isArray(nextStages) || !nextStages.length || nextStages.some(name => !text(name).trim()) || new Set(nextStages).size !== nextStages.length
    || input.routeStages !== undefined && !isObject(input.routeStages)) throw new Error('bindStages must explicitly name unexecuted geometry stages and routeStages must be a map');
  for (const name of nextStages) {
    const j = plan.stages.findIndex(stage => stage.id === name), future = updated.stages[j];
    if (j <= index || future?.check !== 'geometry') throw new Error('Cannot bind completed or final stages');
    try { await access(managedStageReceiptFile(plan, j)); throw new Error('A future stage already has a receipt; do not rewrite its scope'); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (future.primitiveIds?.length || future.steps.length) throw new Error('bind fills only empty stage scopes and steps');
    const futureIds = future.componentKeys ? future.componentKeys.map(key => mapping[key]) : ids;
    if (futureIds.some(id => !id)) throw new Error('Future stage has an unobserved logical component identity');
    future.primitiveIds = futureIds;
    if (input.routeStages?.[name]) future.routeFrom = input.routeStages[name];
    if (future.routeFrom || future.labelsFrom) continue;
    future.steps = [{ action: 'schematic.components.list', payload: { primitiveIds: futureIds, includePins: true,
      includePinNets: true, includeBBox: true, includeDeviceIdentity: false, includeTexts: false, includeWires: false } }];
  }
  if (Object.keys(input.routeStages ?? {}).some(name => !nextStages.includes(name))) throw new Error('Route stage is outside bindStages');
  updated.revision++;
  validatePlan(updated);
  const lock = planFile + '.bind.lock', handle = await open(lock, 'wx');
  const temp = planFile + '.bind.tmp';
  try {
    if (hash(await readFile(planFile)) !== loaded.sha256) throw new Error('Plan changed before binding; reload it');
    await writeFile(temp, JSON.stringify(updated, null, 2) + '\n', { flag: 'wx' });
    await rename(temp, planFile);
  } finally {
    await handle.close();
    await unlink(lock);
    await unlink(temp).catch(error => { if (error.code !== 'ENOENT') throw error; });
  }
  const sha256 = hash(await readFile(planFile));
  await renderPlan(planFile, { expectedHash: sha256 });
  return { ok: true, planFile, sha256, revision: updated.revision, primitiveIds: ids, componentKeys,
    evidence: { receiptFile: prior.file, receiptSha256: prior.sha256 },
    nextAction: 'Run the bound measurement and route stages with this hash; no EDA write or saved claim was made.' };
}

export async function finalRecheckBinding(loaded, index) {
  const {plan,planFile,sha256}=loaded, stage=plan.stages?.[index];
  if(index!==plan.stages?.length-1||stage?.check!=='final'||stage.steps.length||stage.routeFrom||stage.labelsFrom)
    throw new Error('Only the last empty read-only final stage can be rechecked');
  const file=managedStageReceiptFile(plan,index), bytes=await readFile(file), packet=JSON.parse(bytes.toString('utf8'));
  const expected=stageReceiptBinding(plan,stage), report=packet.receipt;
  if(packet.schemaVersion!=='easyeda.execution/1'||packet.state!=='settled'||packet.dryRun!==false
    ||packet.planFile!==planFile||packet.sha256!==sha256||packet.revision!==plan.revision
    ||canonical(packet.target)!==canonical(plan.target)||canonical(packet.sources)!==canonical(receiptSources(plan))
    ||canonical(packet.binding)!==canonical(expected)||packet.stage?.id!==stage.id||packet.stage.check!=='final'
    ||packet.stage.completed!==false||report?.stage?.completed!==false||report.ok!==false||report.partial!==true
    ||report.execution!==null)
    throw new Error('Final recheck requires the unchanged settled failed read-only final receipt; mutations are never replayed');
  return {file,sha256:hash(bytes)};
}

export async function verifyStagePrefix(loaded, stageIndex) {
  const { plan, planFile } = loaded;
  if (!plan.executionReceipts || stageIndex === 0) return { geometry: new Map(), receipts: [] };
  const current = stageReceiptBinding(plan, plan.stages[stageIndex] ?? plan.stages.at(-1)), currentSources = receiptSources(plan);
  const observed = new Set(), geometry = new Map(), receipts = [];
  let expectedIdMap = {};
  for (let index = 0; index < stageIndex; index++) {
    let file = managedStageReceiptFile(plan, index);
    let receipt;
    let bytes;
    try { bytes = await readFile(file); receipt = JSON.parse(bytes.toString('utf8')); }
    catch (error) { if (error.code === 'ENOENT') throw new Error(`Previous stage receipt is missing: ${plan.stages[index].id}`); throw error; }
    if (plan.stages[index].check==='final') {
      const rechecked=file.replace(/\.json$/i,'.recheck.json');
      let replacement;
      try { replacement=await readFile(rechecked); } catch(error) { if(error.code!=='ENOENT')throw error; }
      if(replacement) {
        const prior=await finalRecheckBinding(loaded,index), next=JSON.parse(replacement.toString('utf8'));
        if(canonical(next.recheckOf??null)!==canonical(prior))throw new Error('Final recheck lost its original failure hash');
        file=rechecked;bytes=replacement;receipt=next;
      }
    }
    const binding = receipt.binding, expected = stageReceiptBinding(plan, plan.stages[index]);
    const steps = await executionSteps(plan, plan.stages[index], receipt.receipt ?? {}, { planFile, planSha256: receipt.sha256 });
    if (receipt.schemaVersion !== 'easyeda.execution/1' || receipt.state !== 'settled' || !completedReceipt(receipt, plan.stages[index], steps))
      throw new Error(`Previous stage needs a complete settled execution receipt: ${plan.stages[index].id}`);
    if (receipt.planFile !== planFile || canonical(receipt.target) !== canonical(plan.target) || canonical(receipt.sources) !== canonical(currentSources)
      || binding?.receiptDirectory !== current.receiptDirectory || binding?.semanticTargetSha256 !== current.semanticTargetSha256 || binding?.sourcesSha256 !== current.sourcesSha256
      || binding?.stageSha256 !== expected.stageSha256 || binding?.requirementsSha256 !== expected.requirementsSha256
      || binding?.acceptanceSha256 !== expected.acceptanceSha256
      || binding?.expressionSha256 !== expected.expressionSha256 || binding?.preservationSha256 !== expected.preservationSha256)
      throw new Error(`Previous stage receipt binding changed: ${plan.stages[index].id}`);
    for (const [key, id] of Object.entries(binding.idMap ?? {})) {
      if (expectedIdMap[key] !== undefined && expectedIdMap[key] !== id) throw new Error(`Previous stage changed an observed ID mapping: ${key}`);
      expectedIdMap[key] = id;
    }
    receipts.push({ file, sha256: hash(bytes), packet: receipt });
    for (const id of observedIds(receipt)) observed.add(id);
    const components = receiptResult(receipt)?.readback?.structuredContent?.result?.components;
    for (const component of components ?? []) if (text(component?.primitiveId) && component.pinsAvailable === true && component.bbox && Array.isArray(component.pins) && component.pins.length) geometry.set(component.primitiveId, component);
  }
  for (const [key, id] of Object.entries(expectedIdMap)) if (plan.semanticTarget.idMap?.[key] !== id) throw new Error(`Observed ID mapping changed: ${key}`);
  for (const [key, id] of Object.entries(plan.semanticTarget.idMap ?? {})) if (expectedIdMap[key] === undefined && !observed.has(id))
    throw new Error(`New ID mapping was not observed in a completed predecessor: ${key}`);
  return { geometry, receipts };
}

function planSummary(plan) {
  return { revision: plan.revision, target: plan.target, acceptanceScope: plan.acceptanceScope ?? 'page', components: plan.semanticTarget.components.length, pins: plan.semanticTarget.components.reduce((n, c) => n + c.pins.length, 0), nets: plan.semanticTarget.nets.length, steps: plan.steps.length,
    ...(plan.stages ? { stages: plan.stages.map(({ id, check, steps, primitiveIds, componentKeys, routeFrom, labelsFrom }) => ({ id, check, steps: steps.length, primitiveIds: primitiveIds ?? [], ...(componentKeys?{componentKeys}:{}), ...(routeFrom?{routeFrom}:{}), ...(labelsFrom?{labelsFrom}:{}) })) } : {}),
    knowledgeBindings: plan.sources.filter(source=>source.knowledge).map(source=>({sourceId:source.id,...source.knowledge})),
    ...(plan.requirements === undefined ? {} : { requirements: { status: 'declared_only', items: copy(plan.requirements) } }),
    ...(Object.keys(expressionSchema.properties).some(key => plan.layoutHints?.[key] !== undefined)
      ? { expression: { status: 'declared_only', ...Object.fromEntries(Object.keys(expressionSchema.properties).filter(key => plan.layoutHints[key] !== undefined).map(key => [key, copy(plan.layoutHints[key])])) } } : {}),
    confirmation: plan.confirmation, humanApprovalVerified: false, missing: plan.missing ?? [] };
}

async function verifyKnowledgeSource(source, request) {
  const {value}=await readJSON(source.path),context=value.context,ref=context?.evidenceRef;
  if(value.readMode!=='pinned' || !ref?.sourceId || !ref.sourceRevision || !/^[a-f0-9]{64}$/.test(ref.sourceHash??'')
    || context.content?.contextCompleteness!=='complete' || context.content?.truncated!==false || context.content?.nextCursor)
    throw new Error(`Knowledge source needs a complete pinned knowledge_get receipt: ${source.id}`);
  const pinned=await request('/api/get?'+new URLSearchParams({evidenceRef:JSON.stringify(ref),maxChars:'200'}));
  if(canonical(pinned.context?.evidenceRef)!==canonical(ref))throw new Error(`Knowledge evidence reference mismatch: ${source.id}`);
  for(const field of ['identity','scope','applicability','verification'])
    if(canonical(pinned.context?.[field])!==canonical(context[field]))throw new Error(`Knowledge receipt ${field} mismatch: ${source.id}`);
  const result=await request('/api/get?'+new URLSearchParams({id:ref.sourceId,maxChars:'200'}));
  const current=result.context,actual=current?.evidenceRef;
  if(!actual || actual.sourceRevision!==ref.sourceRevision || actual.sourceHash!==ref.sourceHash
    || current.states?.review!=='approved' || current.states?.parse!=='success')
    throw new Error(`Knowledge source changed or unavailable: ${source.id}`);
  for(const field of ['identity','scope','applicability','verification'])
    if(canonical(current[field])!==canonical(context[field]))throw new Error(`Knowledge source ${field} changed: ${source.id}`);
  return {sourceId:source.id,evidenceRef:ref,...source.knowledge,status:'current',checkResult:'unknown'};
}

function stageKnowledgeScope(plan, stageId) {
  const stage=plan.stages?.find(stage=>stage.id===stageId);
  if(!stage || stage.check!=='geometry' || !stage.steps.length
    || stage.steps.some(step=>step.action!=='schematic.component.modify' || step.payload?.preserveInstance!==true
      || !isObject(step.payload.patch) || Object.keys(step.payload.patch).some(key=>!['x','y','rotation','mirror'].includes(key))))return null;
  const ids=[...(stage.primitiveIds??[]),...stage.steps.map(step=>step.payload?.primitiveId)];
  const parts=ids.map(id=>plan.semanticTarget.components.find(part=>id && (part.sourcePrimitiveId===id || plan.semanticTarget.idMap?.[part.key]===id)));
  if(parts.some(part=>!part))return null;
  const componentKeys=new Set(parts.map(part=>part.key));
  const netNames=new Set(plan.semanticTarget.nets.filter(net=>net.members.some(member=>componentKeys.has(member.componentKey))).map(net=>net.name));
  return {componentKeys,netNames};
}

/** Hash exact bytes and verify source bindings before a caller prepares any EDA request. */
export async function loadPlan(planFile, { expectedHash, requireConfirmed = false, verifySources = true, knowledgeRead = knowledgeRequest, stage } = {}) {
  const { file, sha256, value: plan } = await readJSON(planFile);
  if (expectedHash !== undefined && expectedHash !== sha256) throw new Error('Plan hash changed');
  validatePlan(plan);
  if (requireConfirmed && (plan.confirmation.status !== 'confirmed' || !text(plan.confirmation.evidence))) throw new Error('A caller-authored confirmation reference is required; verify actual user authorization separately');
  const scope=stageKnowledgeScope(plan,stage),knowledgeChecks=[],deferredKnowledgeSources=[];
  const selectedStage = plan.stages?.find(item => item.id === stage);
  const requiredSources = new Set((plan.requirements ?? []).filter(rule => (!rule.stageId || rule.stageId === stage)
    && (rule.when !== 'delivery' || selectedStage?.check === 'final')).map(rule => rule.sourceId));
  for(const source of plan.sources){
    const k=source.knowledge;
    if(k && scope && !requiredSources.has(source.id) && !k.componentKeys.some(key=>scope.componentKeys.has(key)) && !k.netNames.some(net=>scope.netNames.has(net))){
      deferredKnowledgeSources.push({sourceId:source.id,status:'not_checked_outside_stage',...k});continue;
    }
    if(verifySources)await sourceReference(source);
    if(k)knowledgeChecks.push(await verifyKnowledgeSource(source,knowledgeRead));
  }
  return { ok: true, planFile: file, sha256, hash: sha256, plan, summary: planSummary(plan),knowledgeChecks,deferredKnowledgeSources,
    knowledgeScope:scope?{kind:'stage',componentKeys:[...scope.componentKeys],netNames:[...scope.netNames]}:{kind:'all_bound_sources'} };
}

import { createHash } from 'node:crypto';
import { userTypesafeKey, validateDecision, requestChoice } from './jev.mjs';
export { validateDecision } from './jev.mjs';
import { buildCallArgs, runEasyeda } from './core.mjs';


const routeProperties = {
  project: { type: 'string', minLength: 1, description: 'Active project name or UUID; never opens another project.' },
  doc: { type: 'string', minLength: 1, description: 'Active schematic page UUID; never switches pages.' },
  window: { type: 'string', minLength: 1 },
  timeoutMs: { type: 'integer', minimum: 1000, maximum: 120000, default: 60000 },
};
const filterProperties = Object.fromEntries(
  ['designator', 'primitiveId', 'net', 'role', 'value', 'designatorPrefix'].map(k => [k, { type: 'string', minLength: 1, maxLength: 200 }]),
);
filterProperties.pinCount = { type: 'integer', minimum: 0, maximum: 1024 };

export const taskTools = [
  {
    name: 'easyeda_inspect', title: 'Inspect the active schematic in one read-only task',
    description: 'Read parts/pins, run the installed five-stage sch gate, verify readback and return a report. gate verdict combines native DRC and additional local checks; read nativeDrc separately before judging the schematic. No design writes or page switching. Client timeout must exceed timeoutMs.',
    inputSchema: { type: 'object', properties: { ...routeProperties, strict: { type: 'boolean', default: true }, includeDetails: { type: 'boolean', default: false } }, additionalProperties: false },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'easyeda_resolve', title: 'Resolve an existing schematic component',
    description: 'Generate candidates from live parts/pins. Apply exact filters first; unique rule matches use no Jev. 2–6 candidates may use Jev only with allowJev=true (sends goal, minimal candidate attributes and adjacent pin/net data to TypeSafe). Never edits. Low confidence, no match or unavailable Jev returns needs_input. Readback verifies identity/connectivity, not semantic correctness.',
    inputSchema: { type: 'object', required: ['goal'], properties: {
      ...routeProperties, goal: { type: 'string', minLength: 1, maxLength: 2000 },
      filters: { type: 'object', properties: filterProperties, additionalProperties: false },
      allowJev: { type: 'boolean', default: false },
      threshold: { type: 'number', minimum: 0, maximum: 1, default: 0.75 },
    }, additionalProperties: false },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: false, openWorldHint: true },
  },
];

function fail(code, message, status = 'blocked') {
  throw Object.assign(new Error(message), { code, status });
}
function object(v) { return v !== null && typeof v === 'object' && !Array.isArray(v); }
function text(v) { return typeof v === 'string' ? v : ''; }
function canonical(v) {
  if (Array.isArray(v)) return v.map(canonical);
  if (!object(v)) return v;
  return Object.fromEntries(Object.keys(v).sort().map(k => [k, canonical(v[k])]));
}
function digest(v) { return createHash('sha256').update(JSON.stringify(canonical(v))).digest('hex'); }
function abort(signal) { if (signal?.aborted) throw signal.reason ?? new Error('Aborted'); }

// ponytail: one process-wide queue; per-window queues only if multi-window throughput is needed.
export function createSerialQueue() {
  let tail = Promise.resolve();
  let pending = 0;
  return async (fn, signal) => {
    if (pending >= 16) fail('BUSY', 'MCP control queue is full');
    abort(signal);
    pending++;
    const run = tail.then(() => { abort(signal); return fn(); });
    tail = run.catch(() => {}).finally(() => { pending--; });
    if (!signal) return run;
    let listener;
    try {
      return await Promise.race([run, new Promise((_, reject) => {
        listener = () => reject(signal.reason ?? new Error('Aborted'));
        signal.addEventListener('abort', listener, { once: true });
        if (signal.aborted) listener();
      })]);
    } finally { signal.removeEventListener('abort', listener); }
  };
}

export function validateTaskInput(name, input) {
  const spec = taskTools.find(t => t.name === name)?.inputSchema;
  if (!spec || !object(input)) fail('INVALID_INPUT', 'Invalid task or input');
  for (const k of Object.keys(input)) if (!Object.hasOwn(spec.properties, k)) fail('INVALID_INPUT', `Unknown task field: ${k}`);
  for (const k of ['project', 'doc', 'window']) {
    if (input[k] !== undefined && (typeof input[k] !== 'string' || !input[k].trim() || input[k].length > 256)) fail('INVALID_INPUT', `Invalid ${k}`);
  }
  if (input.timeoutMs !== undefined && (!Number.isInteger(input.timeoutMs) || input.timeoutMs < 1000 || input.timeoutMs > 120000)) fail('INVALID_INPUT', 'timeoutMs must be 1000–120000');
  for (const k of ['strict', 'includeDetails', 'allowJev']) if (input[k] !== undefined && typeof input[k] !== 'boolean') fail('INVALID_INPUT', `${k} must be boolean`);
  if (name === 'easyeda_resolve') {
    if (typeof input.goal !== 'string' || !input.goal.trim() || input.goal.length > 2000) fail('INVALID_INPUT', 'goal must contain 1–2000 characters');
    if (input.threshold !== undefined && (!Number.isFinite(input.threshold) || input.threshold < 0 || input.threshold > 1)) fail('INVALID_INPUT', 'threshold must be 0–1');
    if (input.filters !== undefined && !object(input.filters)) fail('INVALID_INPUT', 'filters must be an object');
    for (const [k, v] of Object.entries(input.filters ?? {})) {
      if (!Object.hasOwn(filterProperties, k)) fail('INVALID_INPUT', `Unknown filter: ${k}`);
      if (k === 'pinCount' ? (!Number.isInteger(v) || v < 0 || v > 1024) : (typeof v !== 'string' || !v.trim() || v.length > 200)) fail('INVALID_INPUT', `Invalid filter: ${k}`);
    }
  }
}

export function selectWindow(health, input) {
  const windows = health?.found?.raw?.windows;
  if (health?.status !== 'found' || !Array.isArray(windows) || !windows.length) fail('NO_CONNECTOR', 'No connected EasyEDA window');
  const matches = windows.filter(w => (!input.window || w.windowId === input.window)
    && (!input.project || [w.context?.projectUuid, w.context?.projectName].includes(input.project))
    && (!input.doc || w.context?.documentUuid === input.doc));
  if (matches.length !== 1) fail('AMBIGUOUS_TARGET', 'Specify an already-active project, page UUID and window; no automatic switching', 'needs_input');
  const w = matches[0];
  if (w.context?.documentType !== 'schematic' || !w.context.projectUuid || !w.context.documentUuid || !w.windowId) fail('NOT_SCHEMATIC', 'Target must be an active schematic page', 'needs_input');
  return { project: w.context.projectUuid, projectName: w.context.projectName, doc: w.context.documentUuid, window: w.windowId, connectorVersion: w.connectorVersion, easyedaVersion: w.easyedaVersion };
}

export function normalizeParts(data) {
  if (!object(data) || !Array.isArray(data.components) || data.pinNetsAvailable !== true) fail('MISSING_PIN_NETS', 'Authoritative component/pin/net data is unavailable', 'incomplete');
  if (!Number.isInteger(data.count) || data.count !== data.components.length) fail('INCOMPLETE_INVENTORY', 'Component inventory count is missing or inconsistent', 'incomplete');
  const ids = new Set();
  return data.components.filter(c => c.componentType === 'part').map(c => {
    if (!text(c.primitiveId) || ids.has(c.primitiveId) || c.pinsAvailable !== true || c.netlistAvailable !== true || !Array.isArray(c.pins)) fail('MISSING_PART_DATA', 'Duplicate ID or incomplete physical pin data', 'incomplete');
    ids.add(c.primitiveId);
    const pinIds = new Set();
    const pins = c.pins.map(p => {
      const number = String(p.pinNumber ?? '');
      if (!number || pinIds.has(number) || (p.net !== null && typeof p.net !== 'string') || typeof p.noConnected !== 'boolean') fail('INVALID_PIN', `Incomplete or duplicate physical pin on ${c.designator}`, 'incomplete');
      pinIds.add(number);
      return { number, name: text(p.pinName), net: p.net, nc: p.noConnected, x: p.x ?? null, y: p.y ?? null, rotation: p.rotation ?? null };
    }).sort((a, b) => a.number.localeCompare(b.number, 'en', { numeric: true }));
    const attrs = object(c.otherProperty) ? c.otherProperty : {};
    return {
      primitiveId: c.primitiveId, designator: text(c.designator), uniqueId: text(c.uniqueId),
      part: text(c.manufacturerId) || text(c.component?.name), manufacturer: text(c.manufacturer),
      value: text(attrs.Value), role: text(attrs.Role ?? attrs.role), description: text(attrs.Description),
      supplierId: text(c.supplierId), footprint: c.footprint ?? null, device: c.device ?? null, symbol: c.symbol ?? null,
      x: c.x ?? null, y: c.y ?? null, rotation: c.rotation ?? null, mirror: c.mirror ?? null,
      attributes: attrs, pins,
    };
  }).sort((a, b) => a.primitiveId.localeCompare(b.primitiveId));
}

export function ruleCandidates(parts, goal, filters = {}) {
  let candidates = parts.filter(c => Object.entries(filters).every(([k, v]) => {
    if (k === 'net') return c.pins.some(p => p.net === v);
    if (k === 'pinCount') return c.pins.length === v;
    if (k === 'designatorPrefix') return c.designator.startsWith(v) && /^\d+$/.test(c.designator.slice(v.length));
    return c[k] === v;
  }));
  const exact = parts.filter(c => [c.designator, c.primitiveId, c.part].includes(goal.trim()));
  let basis = Object.keys(filters).length ? 'explicit_filters' : null;
  if (exact.length) {
    candidates = candidates.filter(c => exact.includes(c));
    basis = 'exact_match_and_filters';
  }
  return { candidates, deterministic: candidates.length === 1 && basis !== null, basis };
}

function publicPart(c) {
  const { attributes, device, symbol, mirror, ...rest } = c;
  return rest;
}
function candidateState(candidates, parts) {
  const byNet = new Map();
  for (const c of parts) for (const p of c.pins) if (p.net) {
    if (!byNet.has(p.net)) byNet.set(p.net, []);
    byNet.get(p.net).push({ designator: c.designator, part: c.part, pin: p.number, function: p.name });
  }
  return candidates.map((c, i) => ({
    id: `c${i}`, designator: c.designator, part: c.part, value: c.value, role: c.role,
    description: c.description.slice(0, 600), footprint: c.footprint?.name ?? '',
    descriptionTruncated: c.description.length > 600,
    pins: c.pins.map(({ number, name, net, nc }) => ({ number, name, net, nc })),
    connectedNets: [...new Set(c.pins.map(p => p.net).filter(Boolean))].map(net => ({ net, members: byNet.get(net) })),
  }));
}


export function normalizeGate(raw, includeDetails) {
  const expected = ['layout-lint', 'clusters', 'check', 'bridge-check', 'drc'];
  if (!object(raw) || !['pass', 'fail', 'blocked'].includes(raw.verdict) || !Array.isArray(raw.stages)
      || expected.some(n => raw.stages.filter(s => s.name === n).length !== 1)
      || raw.stages.some(s => !['pass', 'fail', 'error', 'skipped'].includes(s.status))) fail('INVALID_GATE', 'Installed gate did not return every expected checker', 'incomplete');
  const computed = raw.stages.some(s => ['error', 'skipped'].includes(s.status)) ? 'blocked' : raw.stages.some(s => s.status === 'fail') ? 'fail' : 'pass';
  if (raw.verdict !== computed) fail('INCONSISTENT_GATE', 'Gate verdict contradicts its stages', 'incomplete');
  const stages = raw.stages.map(s => {
    let errors = typeof s.errors === 'number' ? s.errors : null;
    let warnings = typeof s.warnings === 'number' ? s.warnings : null;
    if (['error', 'skipped'].includes(s.status)) { errors = null; warnings = null; }
    let coverage;
    if (s.name === 'drc') {
      coverage = { countsAvailable: s.detail?.countsAvailable === true, detailsAvailable: s.detail?.detailsAvailable === true, nativePassed: typeof s.detail?.nativePassed === 'boolean' ? s.detail.nativePassed : null };
      if (!coverage.countsAvailable) { errors = null; warnings = null; }
      if (s.status === 'pass' && coverage.nativePassed !== true) fail('UNPROVEN_DRC', 'Passing DRC lacks a true native verdict', 'incomplete');
    }
    return { name: s.name, status: s.status, errors, warnings, summary: s.summary ?? null, blockingReasons: s.blockingReasons ?? [], error: s.error ?? null, ...(coverage ? { coverage } : {}), ...(includeDetails ? { detail: s.detail ?? null } : {}) };
  });
  return { verdict: computed, stages, blockers: raw.blockers ?? [], warnings: raw.warnings ?? [] };
}

export async function runTask(name, input, { signal: parentSignal, run = runEasyeda, fetchImpl = fetch, env = process.env, lookupKey = userTypesafeKey } = {}) {
  const started = performance.now();
  const startedAt = new Date().toISOString();
  const steps = [];
  const warnings = [];
  let target, gate, result, pinNetCoverage, decision = { attempted: false, fallback: null, ms: 0 };
  let timerSignal, signal;
  try {
    validateTaskInput(name, input);
    timerSignal = AbortSignal.timeout(input.timeoutMs ?? 60000);
    signal = parentSignal ? AbortSignal.any([parentSignal, timerSignal]) : timerSignal;
    abort(signal);
    const step = async (label, args, gateOutput = false) => {
      abort(signal);
      const before = performance.now();
      const startedAt = new Date().toISOString();
      let outcome;
      try {
        outcome = await run(args, Math.max(1, Math.ceil((input.timeoutMs ?? 60000) - (performance.now() - started))), signal);
        abort(signal);
        if (outcome.stderr) warnings.push({ step: label, message: outcome.stderr });
        let data = outcome.ok ? outcome.result : outcome.error?.stdout;
        if (gateOutput && object(data) && typeof data.verdict === 'string'
            && (outcome.ok || (outcome.error?.code === 1 && ['fail', 'blocked'].includes(data.verdict)))) return data;
        if (!outcome.ok) fail('CLI_ERROR', `Step ${label} failed (${outcome.error?.code ?? 'no code'}); no automatic retry`);
        if (object(data) && data.ok === false) fail('ACTION_ERROR', `Step ${label} returned an unsuccessful action`);
        if (target && args.includes('call')) {
          const ctx = data?.context;
          if (ctx?.projectUuid !== target.project || ctx?.documentUuid !== target.doc || ctx?.documentType !== 'schematic') fail('STATE_CHANGED', 'Live response context does not match the requested schematic', 'incomplete');
        }
        if (object(data) && Object.hasOwn(data, 'result')) data = data.result;
        return data;
      } finally {
        steps.push({ name: label, startedAt, endedAt: new Date().toISOString(), ms: performance.now() - before, processOk: outcome?.ok === true });
      }
    };
    const health = await step('health', ['daemon', 'health']);
    target = selectWindow(health, input);
    // --doc invokes a navigation guard in the CLI, including for reads. Never pass it here.
    const route = { project: target.project, window: target.window };
    const verifyTarget = async label => {
      const current = await step(label, buildCallArgs('document.current', route));
      if (current?.uuid !== target.doc || current?.parentProjectUuid !== target.project || current?.documentType !== 'schematic') fail('STATE_CHANGED', 'Active document changed; no automatic page switching', 'incomplete');
    };
    await verifyTarget('verify_initial_target');
    const read = async label => normalizeParts(await step(label, buildCallArgs('schematic.components.list', { ...route, payload: { includePins: true } })));
    const initial = await read('read_parts');
    // Connector null means unknown (including ambiguous/missing netlist membership), never NC or floating.
    const unknownPins = initial.flatMap(c => c.pins.filter(p => p.net === null).map(p => ({ designator: c.designator, primitiveId: c.primitiveId, pin: p.number })));
    pinNetCoverage = { complete: unknownPins.length === 0, unknownPins };
    const beforeHash = digest(initial);
    const verify = async () => {
      const after = await read('readback_parts');
      await verifyTarget('verify_target');
      if (digest(after) !== beforeHash) fail('STATE_CHANGED', 'Observed parts, attributes, positions or pin/net state changed during the task', 'incomplete');
      return { unchanged: true, sha256: beforeHash, scope: 'component identity, attributes, positions and physical pin/net data; excludes complete wire/graphic geometry' };
    };
    if (name === 'easyeda_inspect') {
      const args = ['--project', target.project, 'sch', 'gate', '--window', target.window, '--json'];
      if (input.strict !== false) args.push('--strict');
      gate = normalizeGate(await step('sch_gate', args, true), input.includeDetails === true);
      const readback = await verify();
      const drc = gate.stages.find(s => s.name === 'drc');
      result = { status: gate.verdict === 'blocked' ? 'blocked' : 'completed', verdict: gate.verdict,
        nativeDrc: { status: drc.status, nativePassed: drc.coverage.nativePassed, errors: drc.errors, warnings: drc.warnings },
        additionalChecks: gate.stages.filter(s => s.name !== 'drc').map(s => ({ name: s.name, status: s.status, errors: s.errors, warnings: s.warnings })),
        verdictScope: 'Combined gate verdict includes local checks beyond EasyEDA native DRC; their findings do not establish an electrical fault.',
        strict: input.strict !== false, componentCount: initial.length, gate, readback };
      if (!pinNetCoverage.complete) Object.assign(result, { status: 'incomplete', verdict: 'incomplete',
        error: { code: 'UNKNOWN_PIN_NETS', message: 'Some physical pins have unknown net membership; gate diagnostics do not establish complete connectivity.' } });
    } else {
      const { candidates, deterministic, basis } = ruleCandidates(initial, input.goal, input.filters);
      const candidateSummary = candidates.map(c => ({ primitiveId: c.primitiveId, designator: c.designator, part: c.part, value: c.value, pinCount: c.pins.length }));
      let chosen;
      // Identity-only lookup is usable with partial nets. Electrical/semantic selection needs full context.
      if (!pinNetCoverage.complete && (!deterministic || input.filters?.net)) decision.fallback = 'unknown_pin_nets';
      else if (deterministic) chosen = candidates[0];
      else if (candidates.length < 2 || candidates.length > 6) decision.fallback = candidates.length === 0 ? 'no_candidate' : candidates.length === 1 ? 'unproven_goal_match' : 'narrow_candidates';
      else if (input.allowJev !== true) decision.fallback = 'jev_not_enabled';
      else {
        const apiKey = env.TYPESAFE_API_KEY || lookupKey();
        if (!apiKey) decision.fallback = 'missing_api_key';
        else {
          const state = { goal: input.goal, candidates: candidateState(candidates, initial) };
          if (JSON.stringify(state).length > 24000) decision.fallback = 'state_too_large';
          else {
            decision.attempted = true;
            const began = performance.now();
            decision.startedAt = new Date().toISOString();
            try {
              const criteria = Object.fromEntries(state.candidates.map(c => [c.id, `Existing component ${c.designator}`]));
              criteria.none = 'No listed component satisfies the goal, or required evidence is missing.';
              const response = await requestChoice({ state, criteria, apiKey, model: env.TYPESAFE_MODEL || 'jev-latest', signal: AbortSignal.any([signal, AbortSignal.timeout(10000)]), fetchImpl });
              if (!response.ok) decision.fallback = `http_${response.status}`;
              else {
                const body = await response.json();
                Object.assign(decision, validateDecision(body.answers?.target, Object.keys(criteria), input.threshold ?? 0.75));
                decision.model = body.model ?? env.TYPESAFE_MODEL ?? 'jev-latest';
                if (!decision.fallback) chosen = candidates[Number(decision.selected.slice(1))];
              }
            } catch (error) {
              abort(signal);
              decision.fallback = error?.name === 'TimeoutError' || error?.name === 'AbortError' ? 'jev_timeout' : 'jev_error';
            } finally { decision.ms = performance.now() - began; decision.endedAt = new Date().toISOString(); }
          }
        }
      }
      const readback = await verify();
      result = { status: chosen ? 'resolved' : 'needs_input', deterministic, ruleBasis: basis, candidateCount: candidates.length,
        candidates: candidateSummary.slice(0, 100), candidatesTruncated: candidates.length > 100,
        selected: chosen ? publicPart(chosen) : null, readback,
        semanticCorrectness: deterministic ? 'matched_explicit_constraints_only' : 'not_independently_proven',
        nextAction: chosen ? 'Use returned evidence; validate engineering interpretation independently.' : 'Codex should inspect candidates, narrow exact filters or ask for missing requirements; do not execute a low-confidence choice.' };
    }
  } catch (error) {
    const cancelled = parentSignal?.aborted;
    const timedOut = timerSignal?.aborted;
    result = { status: cancelled ? 'cancelled' : timedOut ? 'timeout' : error.status ?? 'blocked', verdict: 'incomplete',
      error: { code: cancelled ? 'CANCELLED' : timedOut ? 'TIMEOUT' : error.code ?? 'TASK_ERROR', message: cancelled || timedOut ? 'Task stopped; already-dispatched editor reads may finish remotely.' : error.message },
      ...(gate ? { gate } : {}) };
  }
  const totalMs = performance.now() - started;
  const cliMs = steps.reduce((s, x) => s + x.ms, 0);
  return { taskApiVersion: 1, task: name, ...result, target: target ?? null, pinNetCoverage: pinNetCoverage ?? null, decision, warnings, steps,
    timing: { startedAt, endedAt: new Date().toISOString(), totalMs, cliMs, jevMs: decision.ms, localMs: Math.max(0, totalMs - cliMs - decision.ms) },
    designMutations: 0, taskRetries: 0, cliInternalRetries: null };
}

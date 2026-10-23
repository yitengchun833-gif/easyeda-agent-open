import test from 'node:test';
import assert from 'node:assert/strict';
import { createSerialQueue, normalizeGate, normalizeParts, ruleCandidates, runTask, selectWindow, validateDecision } from '../src/tasks.mjs';

const health = { status: 'found', found: { raw: { windows: [{ windowId: 'w', context: { projectUuid: 'p', projectName: 'Project', documentUuid: 'd', documentType: 'schematic' } }] } } };
const part = (ref, value = '10nF', net = 'VCC') => ({
  componentType: 'part', primitiveId: `id-${ref}`, designator: ref, manufacturerId: `MPN-${ref}`,
  pinsAvailable: true, netlistAvailable: true, otherProperty: { Value: value, Role: 'bypass' },
  pins: [{ pinNumber: '1', pinName: '1', net, noConnected: false }, { pinNumber: '2', pinName: '2', net: 'GND', noConnected: false }],
});
const parts = { count: 3, pinNetsAvailable: true, components: [part('C1'), part('C2', '100nF'), part('C3', '1uF')] };
const gate = () => ({ verdict: 'pass', stages: ['layout-lint', 'clusters', 'check', 'bridge-check', 'drc'].map(name => ({ name, status: 'pass', errors: 0, warnings: 0, detail: name === 'drc' ? { nativePassed: true, countsAvailable: false, detailsAvailable: false, summary: null } : {} })) });
function fixture({ gateValue = gate(), failExit = false, change, inventory = parts, missing = false, wrongContext = false } = {}) {
  const calls = [];
  let reads = 0;
  return { calls, run: async (args, timeout, signal) => {
    calls.push(args);
    assert.ok(timeout > 0);
    assert.ok(Number.isInteger(timeout));
    assert.ok(signal instanceof AbortSignal);
    assert.equal(args.includes('--doc'), false, 'read-only tasks must never activate the CLI page-switch guard');
    if (args[0] === 'daemon') return { ok: true, result: health };
    const context = { ...health.found.raw.windows[0].context, ...(wrongContext ? { documentUuid: 'other-page' } : {}) };
    if (args.includes('document.current')) return { ok: true, result: { ok: true, context, result: { uuid: 'd', parentProjectUuid: 'p', documentType: 'schematic' } } };
    if (args.includes('schematic.components.list')) {
      reads++;
      const data = structuredClone(inventory);
      if (missing) data.pinNetsAvailable = false;
      if (reads > 1 && change) change(data);
      return { ok: true, result: { ok: true, type: 'response', context, result: data } };
    }
    assert.ok(args.includes('gate'), 'unexpected command (possibly a write)');
    return failExit ? { ok: false, error: { code: 1, stdout: gateValue } } : { ok: true, result: gateValue };
  } };
}
const jevReply = (choice = 'c1', confidence = 0.9) => ({ ok: true, json: async () => ({ model: 'test-jev', answers: { target: { type: 'choice', choice, confidence, probabilities: Object.fromEntries(['c0', 'c1', 'c2', 'none'].map(k => [k, k === choice ? 0.85 : 0.05])) } } }) });
const neverFetch = async () => { assert.fail('deterministic/blocked paths must not call Jev'); };

test('unknown pin nets remain unknown: identity lookup works, electrical and semantic conclusions stay incomplete', async () => {
  const inventory = structuredClone(parts);
  inventory.components[0].pins[0].net = null;
  const normalized = normalizeParts(inventory);
  assert.equal(normalized[0].pins[0].net, null);
  assert.equal(normalized[0].pins[0].nc, false);
  for (const net of [undefined, 42, {}]) {
    const invalid = structuredClone(inventory);
    invalid.components[0].pins[0].net = net;
    assert.throws(() => normalizeParts(invalid), /physical pin/);
  }
  const lookup = await runTask('easyeda_resolve', { goal: 'C1' }, { run: fixture({ inventory }).run, fetchImpl: neverFetch });
  assert.equal(lookup.status, 'resolved');
  assert.equal(lookup.selected.pins[0].net, null);
  assert.equal(lookup.readback.unchanged, true);
  assert.deepEqual(lookup.pinNetCoverage.unknownPins, [{ designator: 'C1', primitiveId: 'id-C1', pin: '1' }]);
  for (const input of [{ goal: 'Find a bypass', allowJev: true }, { goal: 'C2', filters: { net: 'VCC' } }]) {
    const r = await runTask('easyeda_resolve', input, { run: fixture({ inventory }).run, fetchImpl: neverFetch });
    assert.equal(r.status, 'needs_input');
    assert.equal(r.selected, null);
    assert.equal(r.decision.fallback, 'unknown_pin_nets');
  }
  const inspection = await runTask('easyeda_inspect', {}, { run: fixture({ inventory }).run });
  assert.equal(inspection.status, 'incomplete');
  assert.equal(inspection.verdict, 'incomplete');
  assert.equal(inspection.gate.verdict, 'pass', 'diagnostic gate evidence remains available but does not certify unknown nets');
  assert.equal(inspection.readback.unchanged, true);
  const changed = await runTask('easyeda_resolve', { goal: 'C2' }, { run: fixture({ inventory, change: d => d.components[0].pins[0].net = '' }).run });
  assert.equal(changed.error.code, 'STATE_CHANGED', 'null and known-floating must not hash identically');
});

test('unique rules, conflicting filters and whole-goal exact matches do not guess a referenced IC', () => {
  const normalized = normalizeParts(parts);
  assert.equal(ruleCandidates(normalized, 'C2').candidates[0].designator, 'C2');
  assert.equal(ruleCandidates(normalized, 'C2', { value: '1uF' }).candidates.length, 0);
  assert.equal(ruleCandidates(normalized, 'C2 的旁路电容').deterministic, false);
  const rules = ruleCandidates(normalized, 'Find the one matching all constraints', { designatorPrefix: 'C', net: 'VCC', role: 'bypass', pinCount: 2, value: '100nF' });
  assert.equal(rules.deterministic, true);
  assert.equal(rules.candidates[0].designator, 'C2');
});

test('duplicate physical pins, absent netlist and ambiguous windows fail closed', () => {
  const data = structuredClone(parts);
  data.components[0].pins[1].pinNumber = '1';
  assert.throws(() => normalizeParts(data), /duplicate physical pin/);
  assert.throws(() => normalizeParts({ ...parts, pinNetsAvailable: false }), /unavailable/);
  const multi = structuredClone(health);
  multi.found.raw.windows.push({ ...multi.found.raw.windows[0], windowId: 'w2' });
  assert.throws(() => selectWindow(multi, {}), /Specify/);
  assert.equal(selectWindow(multi, { window: 'w2' }).window, 'w2');
});

test('gate retains native pass with unknown counts; missing or contradictory evidence cannot pass', () => {
  assert.equal(normalizeGate(gate(), false).stages.at(-1).errors, null);
  assert.equal(normalizeGate(gate(), false).stages.at(-1).coverage.countsAvailable, false);
  const missing = gate(); missing.stages.splice(1, 1);
  assert.throws(() => normalizeGate(missing), /every expected checker/);
  const falsePass = gate(); falsePass.stages.at(-1).detail.nativePassed = false;
  assert.throws(() => normalizeGate(falsePass), /native verdict/);
  const contradiction = gate(); contradiction.verdict = 'fail';
  assert.throws(() => normalizeGate(contradiction), /contradicts/);
});

test('deterministic task reads back and routes by actual UUIDs; no Jev', async () => {
  const f = fixture();
  const r = await runTask('easyeda_resolve', { goal: 'C2', allowJev: true }, { run: f.run, fetchImpl: neverFetch });
  assert.equal(r.status, 'resolved');
  assert.equal(r.selected.designator, 'C2');
  assert.equal(r.decision.attempted, false);
  assert.equal(r.readback.unchanged, true);
  assert.equal(r.steps.length, 5);
  for (const call of f.calls.filter(a => a.includes('call'))) {
    assert.equal(call[call.indexOf('--project') + 1], 'p');
    assert.equal(call.includes('--doc'), false);
    assert.equal(call[call.indexOf('--window') + 1], 'w');
  }
});

test('a design finding on nonzero CLI exit stays a finding, not a transport failure', async () => {
  const g = gate(); g.verdict = 'fail'; g.stages[0].status = 'fail'; g.stages[0].blockingReasons = ['overlap'];
  const f = fixture({ gateValue: g, failExit: true });
  const r = await runTask('easyeda_inspect', { includeDetails: true }, { run: f.run });
  assert.equal(r.status, 'completed');
  assert.equal(r.verdict, 'fail');
  assert.deepEqual(r.nativeDrc, { status: 'pass', nativePassed: true, errors: null, warnings: null });
  assert.deepEqual(r.additionalChecks.filter(s => s.status === 'fail').map(s => s.name), ['layout-lint']);
  assert.deepEqual(r.gate.stages[0].blockingReasons, ['overlap']);
  assert.ok(f.calls.find(a => a.includes('gate')).includes('--strict'));
});

test('infrastructure failure, missing coverage and changed state never become success', async () => {
  const switched = await runTask('easyeda_inspect', {}, { run: fixture({ wrongContext: true }).run });
  assert.equal(switched.status, 'incomplete');
  assert.equal(switched.error.code, 'STATE_CHANGED');
  const crashed = await runTask('easyeda_inspect', {}, { run: fixture({ failExit: true }).run });
  assert.equal(crashed.status, 'blocked', 'nonzero exit cannot be accepted as a successful gate');
  const missing = await runTask('easyeda_inspect', {}, { run: fixture({ missing: true }).run });
  assert.equal(missing.status, 'incomplete');
  for (const change of [d => d.components[0].pins[0].net = 'OTHER', d => d.components[0].otherProperty.Value = 'changed']) {
    const r = await runTask('easyeda_resolve', { goal: 'C2' }, { run: fixture({ change }).run });
    assert.equal(r.status, 'incomplete');
    assert.equal(r.error.code, 'STATE_CHANGED');
    assert.equal(r.selected, undefined);
  }
  const r = await runTask('easyeda_inspect', {}, { run: async () => ({ ok: false, error: { code: 2 } }) });
  assert.equal(r.status, 'blocked');
  assert.equal(r.steps.length, 1);
});

test('ambiguous live candidates only send bounded state, and Jev selection is re-read', async () => {
  let sent;
  const f = fixture();
  const r = await runTask('easyeda_resolve', { goal: 'Find the capacitor best fitting the described function', allowJev: true }, {
    run: f.run, env: { TYPESAFE_API_KEY: 'test-only' },
    fetchImpl: async (_, options) => { sent = JSON.parse(options.body); return jevReply(); },
  });
  assert.equal(r.selected.designator, 'C2');
  assert.equal(r.decision.model, 'test-jev');
  assert.equal(sent.state.candidates.length, 3);
  assert.equal(sent.state.project, undefined);
  assert.equal(sent.state.candidates[0].primitiveId, undefined);
  assert.equal(r.semanticCorrectness, 'not_independently_proven');
  assert.equal(r.readback.unchanged, true);
});

test('low confidence, none, out-of-list, malformed distributions and HTTP errors abstain', async () => {
  const invalid = jevReply();
  const invalidBody = await invalid.json(); invalidBody.answers.target.probabilities.c0 = 1.2;
  for (const response of [jevReply('c1', 0.2), jevReply('none'), jevReply('bad'), { ok: true, json: async () => invalidBody }, { ok: false, status: 503 }]) {
    const r = await runTask('easyeda_resolve', { goal: 'Which capacitor fits?', allowJev: true }, { run: fixture().run, env: { TYPESAFE_API_KEY: 'test-only' }, fetchImpl: async () => response });
    assert.equal(r.status, 'needs_input');
    assert.equal(r.selected, null);
    assert.ok(r.decision.fallback);
    assert.equal(r.decision.attempted, true);
  }
  assert.equal(validateDecision({ type: 'choice', choice: 'x', confidence: NaN }, ['x'], 0.75).fallback, 'invalid_response');
});

test('Jev is opt-in; missing key and a lone unproven candidate do not call a model', async () => {
  const r = await runTask('easyeda_resolve', { goal: 'Pick a suitable capacitor' }, { run: fixture().run, fetchImpl: neverFetch });
  assert.equal(r.decision.fallback, 'jev_not_enabled');
  const noKey = await runTask('easyeda_resolve', { goal: 'Pick a suitable capacitor', allowJev: true }, { run: fixture().run, env: {}, lookupKey: () => '', fetchImpl: neverFetch });
  assert.equal(noKey.decision.fallback, 'missing_api_key');
  let authorization;
  const userKey = await runTask('easyeda_resolve', { goal: 'Pick a suitable capacitor', allowJev: true }, {
    run: fixture().run, env: {}, lookupKey: () => 'user-scope-key',
    fetchImpl: async (_, options) => { authorization = options.headers.Authorization; return jevReply(); },
  });
  assert.equal(userKey.status, 'resolved');
  assert.equal(authorization, 'Bearer user-scope-key');
  assert.equal(JSON.stringify(userKey).includes('user-scope-key'), false);
  assert.equal(ruleCandidates(normalizeParts({ ...parts, count: 1, components: [parts.components[0]] }), 'Find a voltage regulator').deterministic, false);
});

test('unknown input is rejected before any process; cancellation stops dispatch', async () => {
  const f = fixture();
  const bad = await runTask('easyeda_inspect', { arbitraryCommand: 'save' }, { run: f.run });
  assert.equal(bad.error.code, 'INVALID_INPUT');
  assert.equal(f.calls.length, 0);
  const controller = new AbortController();
  const pending = runTask('easyeda_inspect', {}, { signal: controller.signal, run: async (_, __, signal) => new Promise((resolve, reject) => signal.addEventListener('abort', () => reject(signal.reason), { once: true })) });
  controller.abort();
  assert.equal((await pending).status, 'cancelled');
});

test('queue serializes requests, skips cancelled work and survives rejection', async () => {
  const queue = createSerialQueue();
  let release;
  const order = [];
  const first = queue(async () => { order.push(1); await new Promise(r => release = r); order.push(2); });
  await new Promise(r => setImmediate(r));
  const controller = new AbortController();
  const cancelled = queue(() => assert.fail('cancelled work must not run'), controller.signal);
  controller.abort();
  await assert.rejects(cancelled);
  const last = queue(() => order.push(3));
  release();
  await Promise.all([first, last]);
  assert.deepEqual(order, [1, 2, 3]);
  await assert.rejects(queue(() => { throw new Error('expected'); }));
  assert.equal(await queue(() => 4), 4);
});

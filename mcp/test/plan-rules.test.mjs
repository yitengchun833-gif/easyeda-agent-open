import test from 'node:test';
import assert from 'node:assert/strict';
import { needsDrc, requiredEdit, requirementResults, localRuleFacts, stageInvalidatesRequirement } from '../src/plan-rules.mjs';

const target = { projectUuid: 'p', documentUuid: 'd' };
const stage = { id: 'local', check: 'geometry', primitiveIds: ['a'], steps: [{ action: 'schematic.component.modify', payload: { primitiveId: 'a', preserveInstance: true, patch: { x: 1 } } }] };
const rule = (patch = {}) => ({ id: 'pin-check', sourceId: 'rules', when: 'after_edit', method: 'checker', check: 'pin_nets', severity: 'error', componentKeys: ['unique:a'], netNames: [], stageId: 'local', ...patch });
const plan = requirements => ({ target, requirements, sources: [{ id: 'rules', sha256: 'a'.repeat(64) }], semanticTarget: { idMap: { 'unique:a': 'a', 'unique:b': 'b' }, components: [
  { key: 'unique:a', uniqueId: 'a', ref: 'R1', pins: [{ number: '1', state: 'connected', net: 'VIN' }] },
  { key: 'unique:b', uniqueId: 'b', ref: 'R2', pins: [{ number: '1', state: 'connected', net: 'VIN' }] },
], nets: [{ name: 'VIN', members: [{ componentKey: 'unique:a' }, { componentKey: 'unique:b' }] }] } });
const envelope = (result, context = target) => ({ structuredContent: { context, result } });
const report = (patch = {}) => ({ primitiveIds: ['a'], execution: envelope({ ok: true, rules: { scope: ['a'], items: [{ id: 'pin_nets', status: 'pass', coverage: 'checked', severity: 'error' }] } }),
  readback: envelope({ components: [{ primitiveId: 'a', uniqueId: 'a', designator: 'R1' }], readScope: { primitiveIds: ['a'], missingIds: [], concurrentChange: false } }), ...patch });
const evaluate = (requirements, evidence = report(), options = {}) => requirementResults(plan(requirements), stage, evidence, { executed: true, ...options });

test('only verified display writes preserve electrical evidence; unknown remote writes invalidate it', () => {
  const display = { id: 'text', check: 'geometry', primitiveIds: ['a'], steps: [{ action: 'schematic.attribute.modify', payload: { primitiveId: 'label', props: { x: 5 } } }] };
  const evidence = { stage: { completed: true }, execution: envelope({ rules: { status: 'pass', items: [{ id: 'identity', status: 'pass', coverage: 'checked' }] },
    results: [{ action: 'schematic.attribute.modify', ok: true, result: { primitiveId: 'label', verified: true,
      attribute: { primitiveId: 'label', ParentPrimitiveId: 'a', Key: 'Designator', Value: 'R1', X: 5 } } }] }) };
  assert.equal(stageInvalidatesRequirement(display, 'pin_nets', evidence, target), false);
  assert.equal(stageInvalidatesRequirement(display, 'text_overlap', evidence, target), true);
  evidence.execution.structuredContent.result.results[0].result.verified = false;
  assert.equal(stageInvalidatesRequirement(display, 'pin_nets', evidence, target), true);
  assert.equal(stageInvalidatesRequirement({ ...stage, primitiveIds: ['remote'] }, 'pin_nets', {}, target), true);
});

test('independent design read cannot replace a failed write or a differently bound target', () => {
  const edit = { mode: 'design', complete: true, primitiveIds: ['a'], expectedPins: { 'a.1': 'VIN' }, expectedIdentities: { a: { designator: 'R1' } } };
  const evidence = { target, edit, execution: envelope({ ok: true, partial: false, rules: { items: [{ id: 'pin_nets', status: 'unknown', coverage: 'unavailable', severity: 'error' }] } }),
    designCheck: envelope({ designVerification: { ...edit }, rules: { status: 'pass' } }) };
  assert.equal(localRuleFacts(evidence).ok, true);
  const delayed = structuredClone(evidence); delayed.execution.structuredContent.result.rules.items[0].status = 'fail';
  assert.equal(localRuleFacts(delayed).ok, true, 'Only the successful write pin-net verdict may be replaced by its bound independent read');
  delayed.designCheck.structuredContent.result.rules.status = 'fail';
  assert.equal(localRuleFacts(delayed).value.rules.status, 'fail', 'A failing independent read is retained, not promoted');
  for (const mutate of [
    value => { value.execution.structuredContent.result.partial = true; },
    value => { value.execution.structuredContent.result.ok = false; },
    value => { value.designCheck.structuredContent.context = { ...target, documentUuid: 'other' }; },
    value => { value.designCheck.structuredContent.result.designVerification.expectedPins = { 'a.1': 'GND' }; },
  ]) { const changed = structuredClone(evidence); mutate(changed); assert.equal(localRuleFacts(changed).ok, false); }
});

test('local predicate needs exact mapped scope, native target and actual checked evidence', () => {
  assert.equal(evaluate([rule()]).items[0].status, 'pass');
  for (const change of [
    value => { value.execution.structuredContent.context = { ...target, documentUuid: 'other' }; },
    value => { value.execution.structuredContent.result.rules.scope = ['b']; },
    value => { value.execution.structuredContent.result.rules.items[0].coverage = 'unavailable'; },
    value => { value.execution.structuredContent.result.rules.items.push({ ...value.execution.structuredContent.result.rules.items[0] }); },
    value => { value.readback.structuredContent.result.components[0].uniqueId = 'another-part'; },
    value => { value.execution.structuredContent.result.rules.items = {}; },
    value => { value.execution.structuredContent.result.rules.items = [null]; },
    value => { value.readback.structuredContent.result.components = {}; },
    value => { value.readback.structuredContent.result.components = []; },
    value => { value.readback.structuredContent.result.components.push(null); },
    value => { value.readback.structuredContent.result.components.push({ ...value.readback.structuredContent.result.components[0] }); },
  ]) { const evidence = report(); change(evidence); assert.equal(evaluate([rule()], evidence).blocking, true); }
  assert.equal(evaluate([rule({ componentKeys: [], netNames: ['VIN'] })]).items[0].status, 'unknown', 'all net members need coverage');
  assert.equal(evaluate([rule({ componentKeys: [], stageId: undefined })]).items[0].status, 'unknown', 'local is not page coverage');
  const missing = report(); missing.readback.structuredContent.result.components = [];
  assert.equal(evaluate([rule({ componentKeys: [] })], missing).blocking, true, 'stage scope still needs actual component records');
  const input = plan([rule()]); delete input.semanticTarget.idMap['unique:a'];
  assert.equal(requirementResults(input, stage, report(), { executed: true }).blocking, true);
});

test('WARN stays visible without blocking and cannot downgrade a native error', () => {
  const evidence = report(); evidence.execution.structuredContent.result.rules.items = [{ id: 'text_overlap', status: 'fail', coverage: 'checked', severity: 'warn' }];
  const warnings = evaluate([rule({ check: 'text_overlap', severity: 'warn' })], evidence);
  assert.equal(warnings.items[0].status, 'fail'); assert.equal(warnings.blocking, false);
  evidence.execution.structuredContent.result.rules.items = [{ id: 'pin_nets', status: 'fail', coverage: 'checked', severity: 'error' }];
  assert.equal(evaluate([rule({ severity: 'warn' })], evidence).blocking, true);
});

test('partial, unknown, manual and deferred checks do not become passes', () => {
  assert.equal(evaluate([rule()], report(), { executed: false }).blocking, true);
  assert.equal(evaluate([rule({ check: 'unregistered-checker' })]).blocking, true);
  assert.equal(evaluate([rule({ method: 'ai_review', check: 'visual_review', severity: 'warn' })]).items[0].status, 'unknown');
  const deferred = evaluate([rule()], report(), { complete: false });
  assert.equal(deferred.items[0].coverage, 'deferred'); assert.equal(deferred.blocking, false);
  const future = evaluate([rule({ when: 'delivery', stageId: 'final' })]);
  assert.equal(future.items[0].coverage, 'deferred'); assert.equal(future.blocking, false);
  assert.equal(requirementResults(plan([rule()]), { ...stage, id: 'final', check: 'final' }, report(), { executed: true }).blocking, true);
});

test('source binding is a fixed before-write predicate, not an arbitrary check execution', () => {
  const required = rule({ when: 'before_write', check: 'source_binding' });
  const results = requirementResults(plan([required]), stage, {}, { before: true });
  assert.equal(results.items[0].evidence.sourceSha256, 'a'.repeat(64)); assert.equal(results.status, 'pass');
  assert.equal(requirementResults(plan([rule({ when: 'before_write', check: 'pin_nets' })]), stage, {}, { before: true }).blocking, true);
});

test('delivery checks run at final only and keep native DRC unknown separate from warnings', () => {
  const required = rule({ when: 'delivery', stageId: 'final', method: 'native', check: 'drc' }), final = { ...stage, id: 'final', check: 'final' }, input = plan([required]);
  assert.equal(needsDrc(input, stage), false); assert.equal(needsDrc(input, final), true);
  const evidence = report({ drcCheck: envelope({ countsAvailable: true, summary: { error: 0, fatal: 0, unknown: 0, warn: 500 } }) });
  assert.equal(requirementResults(input, final, evidence, { executed: true }).status, 'pass');
  evidence.drcCheck.structuredContent.result.summary.error = 1;
  assert.equal(requirementResults(input, final, evidence, { executed: true }).status, 'fail');
  evidence.drcCheck.structuredContent.result.summary.error = 0; delete evidence.drcCheck.structuredContent.result.summary.unknown;
  assert.equal(requirementResults(input, final, evidence, { executed: true }).status, 'unknown');
});

test('final target can prove known identity/pin predicates but never an arbitrary named requirement', () => {
  const final = { ...stage, id: 'final', check: 'final' }, evidence = report({ targetCheck: envelope({ status: 'pass', verified: true, partial: false }) });
  for (const check of ['identity', 'pin_nets', 'schematic.target.check']) assert.equal(requirementResults(plan([rule({ check, when: 'delivery', stageId: 'final' })]), final, evidence, { executed: true }).status, 'pass');
  assert.equal(requirementResults(plan([rule({ check: 'circuit-is-safe', when: 'delivery', stageId: 'final' })]), final, evidence, { executed: true }).status, 'unknown');
});

test('complete local units derive edit scope once and reject unbound hard scopes before writing', () => {
  const expectedIdentities = { a: { designator: 'R1', uniqueId: 'a' } };
  assert.deepEqual(requiredEdit(plan([rule()]), stage), { mode: 'layout', primitiveIds: ['a'], expectedIdentities });
  assert.throws(() => requiredEdit(plan([rule()]), stage, { expectedIdentities: { a: { designator: 'R2' } } }), /conflicts/);
  assert.throws(() => requiredEdit(plan([rule({ componentKeys: ['unique:b'] })]), stage), /scope is not bound/);
  const explicit = { mode: 'design', primitiveIds: ['a'], expectedPins: { 'a.1': 'VIN' } };
  assert.throws(() => requiredEdit(plan([rule({ check: 'identity' })]), stage, explicit), /independent delivery target/);
  assert.deepEqual(requiredEdit(plan([rule()]), stage, explicit), { ...explicit, expectedIdentities });
  assert.throws(() => requiredEdit(plan([rule()]), stage, { ...explicit, expectedPins: { 'a.1': 'GND' } }), /conflicts/);
  const evidence = report(); evidence.execution.structuredContent.result.rules.mode = 'design'; evidence.edit = explicit;
  assert.equal(evaluate([rule()], evidence).items[0].status, 'pass');
  evidence.edit = { ...explicit, expectedPins: { 'a.1': 'GND' } };
  assert.equal(evaluate([rule()], evidence).blocking, true);
  evidence.edit = { mode: 'design', primitiveIds: ['a', 'b'], expectedPins: { 'a.1': 'VIN', 'b.1': 'VIN' } };
  assert.equal(evaluate([rule()], evidence).blocking, false, 'a requirement may cover a subset of the complete edit');
  evidence.edit.expectedPins['extra.1'] = 'VIN';
  assert.equal(evaluate([rule()], evidence).blocking, true);
  assert.throws(() => requiredEdit(plan([rule()]), stage, { complete: false }), /complete modification stage/);
  assert.deepEqual(requiredEdit(plan([rule({ severity: 'warn' })]), stage, { complete: false }), { complete: false });
});

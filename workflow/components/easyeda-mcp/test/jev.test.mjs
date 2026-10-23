import test from 'node:test';
import assert from 'node:assert/strict';
import { chooseWorkflow } from '../src/jev.mjs';
const input = { goal: 'Choose next evidence to inspect', stage: 'research', candidates: [
  { id: 'a', label: 'Official datasheet', evidence: 'Exact model and manufacturer' },
  { id: 'b', label: 'Catalog description', evidence: 'Family only' },
] };
const reply = choice => ({ ok: true, json: async () => ({ model: 'mock', answers: { target: {
  type: 'choice', choice, confidence: .9, probabilities: { a: choice === 'a' ? .9 : .05, b: .05, none: choice === 'none' ? .9 : .05 },
} } }) });
test('generic choice uses supplied evidence without EDA and preserves failure boundaries', async () => {
  let calls = 0;
  const options = { env: { TYPESAFE_API_KEY: 'test' }, fetchImpl: async (_, req) => {
    calls++; assert.deepEqual(JSON.parse(req.body).state, input); return reply('a');
  } };
  assert.equal((await chooseWorkflow(input, options)).selected, 'a');
  assert.equal((await chooseWorkflow({ ...input, candidates: [input.candidates[0], input.candidates[0]] }, options)).fallback, 'invalid_input');
  assert.equal(calls, 1);
  assert.equal((await chooseWorkflow(input, { env: {}, lookupKey: () => '' })).fallback, 'missing_api_key');
  for (const [response, fallback] of [[reply('none'), 'no_match'], [reply('outside'), 'invalid_response'], [{ ok: false, status: 503 }, 'http_503']]) {
    const r = await chooseWorkflow(input, { ...options, fetchImpl: async () => response });
    assert.equal(r.fallback, fallback); assert.equal(r.selected, null);
  }
  assert.equal((await chooseWorkflow({ ...input, threshold: .95 }, options)).fallback, 'low_confidence');
  assert.equal((await chooseWorkflow(input, { ...options, fetchImpl: async () => { throw new DOMException('', 'TimeoutError'); } })).fallback, 'jev_timeout');
  assert.equal((await chooseWorkflow(input, { ...options, signal: AbortSignal.abort() })).fallback, 'cancelled');
});

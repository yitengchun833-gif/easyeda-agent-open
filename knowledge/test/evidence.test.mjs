import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { captureEvidence, EvidenceError, loadEvidence, readEvidence, referenceFor } from '../src/evidence.mjs';
import { AjvJsonSchemaValidator } from '../../mcp/node_modules/@modelcontextprotocol/sdk/dist/esm/validation/ajv-provider.js';
import { knowledgeContextSchema } from '../src/context-contract.mjs';

const root = mkdtempSync(path.join(os.tmpdir(), 'easyeda-evidence-'));
const hash = letter => letter.repeat(64);
const source = (patch = {}) => ({ sourceId: 'kb-test', revision: 'rev-one', sourceHash: hash('a'), name: 'test.md', title: 'Test evidence', type: 'reference', domain: 'both', status: 'approved', parseStatus: 'success', indexStatus: 'ready', metadata: { models: ['TEST-1'], project: 'demo', validation: { connectivity: 'unknown', layout: 'unknown', measurement: 'unknown', engineerReview: 'unknown' }, ...patch } });
const chunks = [{ chunkId: 'chunk-one', anchor: 'section/one', text: 'frozen text one', contentHash: hash('b'), metadata: { locations: [{ anchor: 'section/one', pages: [1] }] } }];
const validator = new AjvJsonSchemaValidator().getValidator(knowledgeContextSchema);
const throws = (fn, code) => assert.throws(fn, error => error instanceof EvidenceError && error.code === code);

test('frozen evidence survives current metadata changes; pure index/model changes do not change identity', () => {
  const first = captureEvidence(root, source(), chunks);
  const metadataOnly = captureEvidence(root, source({ project: 'changed' }), chunks);
  assert.notEqual(first.evidenceRef.evidenceId, metadataOnly.evidenceRef.evidenceId);
  const modelOnly = captureEvidence(root, { ...source(), indexStatus: 'failed', updatedAt: 'tomorrow', enabled: false, metadata: { ...source().metadata, vectorSignature: 'other-model' } }, chunks);
  assert.equal(first.evidenceRef.evidenceId, modelOnly.evidenceRef.evidenceId);
  assert.equal(loadEvidence(root, first.evidenceRef).source.metadata.project, 'demo');
});

test('strict reference, corruption and missing evidence never fall back to current content', () => {
  const corruptedSource = { ...source({ tags: ['project_result'] }), sourceHash: hash('c') };
  const { snapshot, evidenceRef } = captureEvidence(root, corruptedSource, chunks);
  const wrong = { ...evidenceRef, selector: { chunkId: 'missing', anchor: null } };
  throws(() => loadEvidence(root, wrong), 'EVIDENCE_SELECTOR_INVALID');
  throws(() => loadEvidence(root, { ...evidenceRef, sourceHash: hash('d') }), 'EVIDENCE_INTEGRITY');
  const file = path.join(root, 'evidence', `${evidenceRef.evidenceId}.json`);
  const validSnapshot = JSON.parse(readFileSync(file, 'utf8'));
  validSnapshot.records[0].text = 'altered body';
  writeFileSync(file, JSON.stringify(validSnapshot));
  throws(() => loadEvidence(root, evidenceRef), 'EVIDENCE_INTEGRITY');
  throws(() => loadEvidence(root, { ...evidenceRef, evidenceId: 'ev-' + hash('c'), snapshotHash: hash('c') }), 'EVIDENCE_UNAVAILABLE');
  assert.equal(snapshot.source.kind, 'artifact');
});

test('legacy pre-trim record hashes are retained as provenance while frozen text gets its own hash', () => {
  const { snapshot } = captureEvidence(root, source(), [{ ...chunks[0], text: 'trimmed body', contentHash: hash('f') }]);
  assert.notEqual(snapshot.records[0].contentHash, hash('f'));
  assert.equal(snapshot.records[0].legacyContentHash, hash('f'));
});

test('selector and checksummed cursor are bound to snapshot, scope and budget', () => {
  const { snapshot, evidenceRef } = captureEvidence(root, source({ validationKind: 'unknown' }), [{ ...chunks[0], text: 'x'.repeat(450) }]);
  const ref = referenceFor(snapshot, { chunkId: 'chunk-one', anchor: 'section/one' });
  const first = readEvidence(snapshot, ref, { scope: 'chunk', maxChars: 200 }, source());
  assert.equal(first.context.content.truncated, true);
  assert.equal(validator(first.context).valid, true);
  const second = readEvidence(snapshot, ref, { scope: 'chunk', maxChars: 200, cursor: first.context.content.nextCursor }, source());
  assert.equal(second.context.content.text.length, 200);
  throws(() => readEvidence(snapshot, ref, { scope: 'source', maxChars: 200, cursor: first.context.content.nextCursor }), 'CURSOR_MISMATCH');
  assert.equal(readEvidence(snapshot, ref, { scope: 'section', maxChars: 200 }).context.content.contextExpansion.relationStatus, 'unconfirmed');
});

test('source reads use every frozen record while retaining the locating selector', () => {
  const allChunks = [
    { ...chunks[0], text: 'first frozen record', metadata: { locations: [{ page_no: 2 }] } },
    { chunkId: 'chunk-two', anchor: 'section/two', text: 'second frozen record', contentHash: hash('d'), metadata: { locations: [{ page_no: 3 }] } }
  ];
  const { snapshot } = captureEvidence(root, source(), allChunks);
  const ref = referenceFor(snapshot, { chunkId: 'chunk-one', anchor: 'section/one' });
  const { context, records } = readEvidence(snapshot, ref, { scope: 'source', maxChars: 200 }, { indexStatus: 'indexing', parseStatus: 'failed' });
  assert.equal(records.length, 2);
  assert.match(context.content.text, /second frozen record/);
  assert.deepEqual(context.evidenceRef.selector, ref.selector);
  assert.deepEqual(context.provenance.locations.map(item => item.pages), [[2], [3]]);
  assert.equal(context.states.parse, 'success');
  assert.equal(context.states.index, 'unknown');
  assert.equal(context.content.contextCompleteness, 'complete');
});

test('legacy anchor prefixes and numeric offsets become a frozen reference and a new cursor', () => {
  const prefixChunks = [
    { ...chunks[0], text: 'a'.repeat(240), anchor: 'section/one' },
    { chunkId: 'chunk-child', anchor: 'section/one/child', text: 'b'.repeat(240), contentHash: hash('e'), metadata: {} }
  ];
  const { snapshot } = captureEvidence(root, source(), prefixChunks);
  const ref = referenceFor(snapshot, { chunkId: null, anchor: 'section/one' });
  const result = readEvidence(snapshot, ref, { scope: 'chunk', maxChars: 200, legacyOffset: 20 }, source());
  assert.equal(result.records.length, 1, 'locations describe only the returned page');
  assert.equal(result.context.evidenceRef.selector.anchor, 'section/one');
  assert.equal(result.context.content.text.length, 200);
  assert.equal(typeof result.context.content.nextCursor, 'string');
  throws(() => readEvidence(snapshot, ref, { scope: 'chunk', maxChars: 200, legacyOffset: -1 }), 'CURSOR_MISMATCH');
  throws(() => readEvidence(snapshot, ref, { scope: 'chunk', maxChars: 200, legacyOffset: 0, cursor: result.context.content.nextCursor }), 'CURSOR_MISMATCH');
});

test('all three kinds produce a shared schema-compatible packet', () => {
  for (const [type, metadata, kind] of [['reference', {}, 'source'], ['lesson', {}, 'experience'], ['reference', { tags: ['project_result'] }, 'artifact']]) {
    const { snapshot } = captureEvidence(root, { ...source(metadata), type }, chunks);
    const ref = referenceFor(snapshot, { chunkId: 'chunk-one', anchor: 'section/one' });
    const { context } = readEvidence(snapshot, ref, { scope: 'chunk', maxChars: 200 }, source());
    assert.equal(validator(context).valid, true);
    assert.equal(context.kind, kind);
  }
});

test('empty optional UI fields remain unknown instead of breaking the machine contract', () => {
  const { snapshot, evidenceRef } = captureEvidence(root, source({
    version: '', project: '', module: '', manufacturer: '', package: '',
    models: ['TEST-1', ''], artifacts: [], validation: { scope: '', note: '' }
  }), chunks);
  const { context } = readEvidence(snapshot, evidenceRef, { scope: 'source', maxChars: 200 });
  const result = validator(context);assert.equal(result.valid, true, result.errorMessage);
  assert.equal(context.scope.documentVersion, null);
  assert.equal(context.verification.scope, null);
});

test.after(() => rmSync(root, { recursive: true, force: true }));

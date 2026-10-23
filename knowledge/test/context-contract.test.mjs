import test from 'node:test';
import assert from 'node:assert/strict';
import { AjvJsonSchemaValidator } from '../../mcp/node_modules/@modelcontextprotocol/sdk/dist/esm/validation/ajv-provider.js';
import {
  knowledgeContextSchema, knowledgeSearchOutputSchema,
  knowledgeGetOutputSchema, knowledgeGetInputSchema
} from '../src/context-contract.mjs';

const validator = new AjvJsonSchemaValidator();
const validateContext = validator.getValidator(knowledgeContextSchema);
const validateSearch = validator.getValidator(knowledgeSearchOutputSchema);
const validateGet = validator.getValidator(knowledgeGetOutputSchema);
const validateInput = validator.getValidator(knowledgeGetInputSchema);

// Synthetic wire samples only: no hardware facts and no writes to the real corpus.
const sample = {
  schemaVersion: 1, kind: 'source', title: '协议测试样例', sourceType: 'reference', domain: null,
  evidenceRef: {
    schemaVersion: 1, evidenceId: 'ev-' + 'a'.repeat(64), sourceId: 'kb-sample',
    sourceRevision: 'sample-revision', sourceHash: 'b'.repeat(64), snapshotHash: 'a'.repeat(64),
    selector: { chunkId: 'chunk-sample', anchor: 'sample/0' }
  },
  identity: { models: [], manufacturer: null, package: null },
  scope: { project: null, module: null, documentVersion: null },
  applicability: { conditions: null, exclusions: null },
  verification: { claimOrigin: 'unknown', connectivity: 'unknown', layout: 'unknown',
    measurement: 'unknown', engineerReview: 'unknown', scope: null, note: null },
  states: { review: 'approved', parse: 'success', index: 'keyword_ready' },
  provenance: { file: 'sample.md', parser: 'utf8-text', locations: [{ anchor: 'sample/0', pages: [] }] },
  artifacts: [], engineeringRefs: [],
  content: { text: '纯协议样例，不作为硬件证据。', readScope: 'source', truncated: false,
    nextCursor: null, contextCompleteness: 'unknown' }
};
const valid = (check, value) => { const result = check(value); assert.equal(result.valid, true, result.errorMessage); };
const invalid = (check, value) => assert.equal(check(value).valid, false);

test('three knowledge kinds share the same context, with explicit unknown validation', () => {
  for (const [kind, sourceType] of [['source', 'reference'], ['experience', 'lesson'], ['artifact', 'reference']]) {
    const context = { ...sample, kind, sourceType };
    valid(validateContext, context);
    valid(validateGet, { schemaVersion: 1, context, readMode: 'pinned', text: context.content.text });
  }
  const excerpt = structuredClone(sample);
  excerpt.content.readScope = 'excerpt'; excerpt.content.contextCompleteness = 'partial';
  excerpt.content.truncated = true;
  valid(validateSearch, { schemaVersion: 1, items: [{ context: excerpt, score: 0.1, matchReasons: [] }],
    retrieval: { mode: 'keyword', degradedReason: '测试用向量未就绪' } });
  valid(validateSearch, { schemaVersion: 1, items: [{ context: excerpt, score: 0.1, matchReasons: [],
    sourceId: excerpt.evidenceRef.sourceId, revision: excerpt.evidenceRef.sourceRevision,
    chunkId: excerpt.evidenceRef.selector.chunkId, anchor: excerpt.evidenceRef.selector.anchor,
    text: excerpt.content.text, citation: { file: excerpt.provenance.file } }],
    retrieval: { mode: 'hybrid', degradedReason: null } });
  valid(validateSearch, { schemaVersion: 1, items: [], retrieval: { mode: 'hybrid', degradedReason: null } });
});

test('version identity and validation cannot be silently omitted or renamed', () => {
  for (const key of ['sourceId', 'sourceRevision', 'sourceHash', 'snapshotHash', 'selector']) {
    const context = structuredClone(sample); delete context.evidenceRef[key]; invalid(validateContext, context);
  }
  for (const key of ['sourceHash', 'snapshotHash']) {
    const context = structuredClone(sample); context.evidenceRef[key] = 'not-a-hash'; invalid(validateContext, context);
  }
  const context = structuredClone(sample);
  context.verification.schematic = 'pass'; invalid(validateContext, context);
  delete context.verification.schematic;
  delete context.verification.measurement; invalid(validateContext, context);
  invalid(validateContext, { ...sample, kind: 'experience', sourceType: 'datasheet' });
  invalid(validateContext, { ...sample, kind: 'source', sourceType: 'lesson' });
  invalid(validateContext, { ...sample, kind: 'artifact', sourceType: 'company_rule' });
});

test('excerpt, bounded reads and continuation state are distinct', () => {
  const context = structuredClone(sample);
  context.content.truncated = true; invalid(validateContext, context);
  context.content.nextCursor = 'opaque-version-bound-cursor'; valid(validateContext, context);
  context.content.contextCompleteness = 'complete'; invalid(validateContext, context);
  context.content.contextCompleteness = 'partial'; context.content.truncated = false; invalid(validateContext, context);
  context.content.nextCursor = null; context.content.readScope = 'excerpt';
  valid(validateContext, context);
  invalid(validateGet, { schemaVersion: 1, context, readMode: 'pinned' });
  context.content.contextCompleteness = 'complete'; invalid(validateContext, context);
});

test('search excludes candidates, and exact-reference input cannot mix current IDs or anchors', () => {
  const context = structuredClone(sample);
  context.content.readScope = 'excerpt'; context.content.contextCompleteness = 'partial';
  context.states.review = 'pending';
  invalid(validateSearch, { schemaVersion: 1, items: [{ context, score: 1, matchReasons: [] }],
    retrieval: { mode: 'hybrid', degradedReason: null } });
  valid(validateInput, { evidenceRef: sample.evidenceRef, scope: 'source', maxChars: 2000 });
  valid(validateInput, { evidenceRef: sample.evidenceRef, scope: 'chunk' });
  const wholeSourceRef = { ...sample.evidenceRef, selector: { chunkId: null, anchor: null } };
  valid(validateInput, { evidenceRef: wholeSourceRef, scope: 'source' });
  invalid(validateInput, { evidenceRef: wholeSourceRef, scope: 'chunk' });
  invalid(validateInput, { evidenceRef: wholeSourceRef, scope: 'section' });
  valid(validateInput, { id: sample.evidenceRef.sourceId, anchor: 'sample/0', limit: 10 });
  invalid(validateInput, {});
  invalid(validateInput, { evidenceRef: sample.evidenceRef, id: 'kb-current' });
  invalid(validateInput, { evidenceRef: sample.evidenceRef, anchor: 'different-anchor' });
  invalid(validateInput, { evidenceRef: sample.evidenceRef, maxChars: 30001 });
});

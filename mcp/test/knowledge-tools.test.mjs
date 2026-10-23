import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, open, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { AjvJsonSchemaValidator } from '@modelcontextprotocol/sdk/validation/ajv';
import { knowledgeTools, readKnowledgeImportFile, validateKnowledgeImportStatus } from '../src/knowledge-tools.mjs';

const hash = 'a'.repeat(64);
const evidenceRef = {
  schemaVersion: 1, evidenceId: `ev-${hash}`, sourceId: 'source-1', sourceRevision: 'revision-1',
  sourceHash: hash, snapshotHash: hash, selector: { chunkId: 'chunk-1', anchor: 'Power input' },
};

function context({ readScope = 'excerpt', truncated = false, nextCursor = null, completeness = 'partial' } = {}) {
  return {
    schemaVersion: 1, kind: 'source', title: 'Input rule', sourceType: 'company_rule', domain: 'schematic', evidenceRef,
    identity: { models: ['RS2251'], manufacturer: null, package: null },
    scope: { project: null, module: null, documentVersion: 'Rev C.1' },
    applicability: { conditions: '5 V input', exclusions: null },
    verification: { claimOrigin: 'source-record', connectivity: 'unknown', layout: 'unknown', measurement: 'unknown', engineerReview: 'unknown', scope: null, note: null },
    states: { review: 'approved', parse: 'success', index: 'ready' },
    provenance: { file: 'rules/input.md', parser: null, locations: [{ anchor: 'Power input', pages: [1] }] },
    artifacts: [], engineeringRefs: [],
    content: { text: 'Keep the input path short.', readScope, truncated, nextCursor, contextCompleteness: completeness },
  };
}

function tool(name) {
  const value = knowledgeTools.find(item => item.name === name);
  assert.ok(value, `${name} should be registered`);
  return value;
}

test('knowledge tools expose the shared get and output contracts', () => {
  const search = tool('knowledge_search');
  const get = tool('knowledge_get');
  assert.ok(search.outputSchema);
  assert.ok(get.outputSchema);

  const validator = new AjvJsonSchemaValidator();
  const validGetInput = validator.getValidator(get.inputSchema);
  assert.equal(validGetInput({ evidenceRef, scope: 'chunk', maxChars: 400 }).valid, true);
  assert.equal(validGetInput({ id: 'source-1', scope: 'source' }).valid, true, 'legacy id access remains available');
  assert.equal(validGetInput({ evidenceRef, scope: 'section' }).valid, true, 'section reads use frozen context');
  assert.equal(validGetInput({ id: 'source-1', evidenceRef }).valid, false, 'a pinned read must not silently fall back to current access');

  const validSearchOutput = validator.getValidator(search.outputSchema);
  assert.equal(validSearchOutput({ schemaVersion: 1, items: [{ context: context(), score: 0.1, matchReasons: ['keyword'] }], retrieval: { mode: 'hybrid', degradedReason: null } }).valid, true);

  const validGetOutput = validator.getValidator(get.outputSchema);
  assert.equal(validGetOutput({ schemaVersion: 1, context: context({ readScope: 'source', completeness: 'complete' }), readMode: 'pinned' }).valid, true);
});

test('knowledge import validates the MCP schema and one bounded local attachment', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'easyeda-knowledge-import-'));
  try {
    const allowed = path.join(root, 'rule.md');
    const unsupported = path.join(root, 'rule.exe');
    await writeFile(allowed, '# Input rule');
    await writeFile(unsupported, 'x');
    const imported = await readKnowledgeImportFile({ filePath: allowed, metadata: { type: 'company_rule', domain: 'schematic', tags: ['power'], partNumbers: ['RS2251'] }, signal: AbortSignal.abort() });
    assert.equal(imported.name, 'rule.md');
    const image=path.join(root,'original.png');await writeFile(image,Buffer.from('fixture transport; parser validates bytes'));
    assert.equal((await readKnowledgeImportFile({filePath:image})).name,'original.png');
    assert.equal(Buffer.from(imported.contentBase64, 'base64').toString(), '# Input rule');
    assert.equal((await readKnowledgeImportFile({ filePath: allowed, name: 'input-rule.markdown' })).name, 'input-rule.markdown');
    await assert.rejects(readKnowledgeImportFile({ filePath: 'rule.md' }), /absolute path/);
    await assert.rejects(readKnowledgeImportFile({ filePath: root }), /regular file/);
    await assert.rejects(readKnowledgeImportFile({ filePath: unsupported }), /supports only/);
    await assert.rejects(readKnowledgeImportFile({ filePath: allowed, name: 'folder/rule.md' }), /leaf file name/);
    await assert.rejects(readKnowledgeImportFile({ filePath: allowed, name: 'rule?.md' }), /leaf file name/);
    await assert.rejects(readKnowledgeImportFile({ filePath: allowed, name: 'rule.pdf' }), /extension must match/);
    await assert.rejects(readKnowledgeImportFile({ filePath: allowed, metadata: { status: 'approved' } }), /Invalid knowledge_import input/);
    const huge = path.join(root, 'huge.pdf');
    const handle = await open(huge, 'w');
    await handle.truncate(100 * 1024 * 1024 + 1);
    await handle.close();
    await assert.rejects(readKnowledgeImportFile({ filePath: huge }), /100 MiB/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('knowledge import tools expose explicit submission and status contracts', () => {
  const imported = tool('knowledge_import');
  const status = tool('knowledge_import_status');
  const validator = new AjvJsonSchemaValidator();
  const importInput = validator.getValidator(imported.inputSchema);
  assert.equal(importInput({ filePath: 'C:\\attachments\\usb-c.pdf', metadata: { type: 'datasheet', models: ['TPS25750'] } }).valid, true);
  assert.equal(importInput({ filePath: 'C:\\attachments\\usb-c.pdf', metadata: { approved: true } }).valid, false);
  assert.equal(imported.outputSchema.required.includes('jobId'), true);
  assert.deepEqual(validateKnowledgeImportStatus({ jobId: 'job-1', signal: AbortSignal.abort() }), { jobId: 'job-1' });
  assert.throws(() => validateKnowledgeImportStatus({}), /Invalid knowledge_import_status input/);
  assert.throws(() => validateKnowledgeImportStatus({ jobId: 'job-1', approved: true }), /Invalid knowledge_import_status input/);
  const importStatus = validator.getValidator(status.outputSchema);
  assert.equal(importStatus({ job: { jobId: 'job-1', kind: 'import', status: 'completed', detail: {}, createdAt: '2026-10-02T00:00:00Z', updatedAt: '2026-10-02T00:00:01Z' } }).valid, true);
});

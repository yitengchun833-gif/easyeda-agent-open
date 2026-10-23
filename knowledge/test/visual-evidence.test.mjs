import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, copyFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
import os from 'node:os';
import { createServer } from 'node:net';
import { captureEvidence, referenceFor, readEvidence } from '../src/evidence.mjs';
import { readVisuals } from '../src/visual-evidence.mjs';
import { chunkText } from '../src/ingest.mjs';
import { knowledgeGetResult } from '../../mcp/src/knowledge-tools.mjs';
import { knowledgeGetInputSchema, knowledgeGetOutputSchema } from '../src/context-contract.mjs';
import { AjvJsonSchemaValidator } from '../../mcp/node_modules/@modelcontextprotocol/sdk/dist/esm/validation/ajv-provider.js';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64');
function fixture(root) {
  const parsedPath = path.join(root, 'parsed', 'fixture.md'), assets = parsedPath + '.assets';
  mkdirSync(assets, { recursive: true });
  const items = {
    '#/texts/0': { text: 'LIMITS', label: 'section_header', locations: [{ page_no: 1 }] },
    '#/tables/0': { text: 'Parameter | Limit\nInput | 5 V (1)', label: 'table', asset: 'table.png', locations: [{ page_no: 1 }] },
    '#/texts/1': { text: '(1) Example only, not a hardware specification.', label: 'footnote', locations: [{ page_no: 1 }] },
    '#/pictures/0': { text: 'Pin diagram', label: 'picture', asset: 'pins.png', locations: [{ page_no: 1 }] }
  };
  writeFileSync(path.join(assets, 'manifest.json'), JSON.stringify({ source_hash: hash('fixture'), parser: 'docling', parser_version: 'test', document_version: 'v1' }));
  writeFileSync(path.join(assets, 'index.json'), JSON.stringify({ document_version: 'v1', items }));
  writeFileSync(path.join(assets, 'document.json'), JSON.stringify({ texts: [{ self_ref: '#/texts/0', level: 1 }, { self_ref: '#/texts/1' }], tables: [{ self_ref: '#/tables/0', footnotes: [{ $ref: '#/texts/1' }] }], pictures: [{ self_ref: '#/pictures/0' }] }));
  writeFileSync(path.join(assets, 'table.png'), pixel); writeFileSync(path.join(assets, 'pins.png'), pixel);
  const source = { sourceId: 'fixture', revision: 'r1', sourceHash: hash('fixture'), name: 'fixture.pdf', type: 'datasheet', parseStatus: 'success', status: 'approved', metadata: { parser: 'docling@test', parsedPath } };
  const chunks = Object.entries(items).map(([anchor, item], i) => ({ chunkId: 'c' + i, anchor, text: item.text, contentHash: hash(item.text), metadata: { locations: item.locations, ...(item.asset ? { assetPath: path.join(assets, item.asset) } : {}) } }));
  return { source, chunks, assets };
}

test('frozen images preserve versions, scope and explicit unavailable states', () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'kb-visual-'));
  try {
    const { source, chunks, assets } = fixture(root);
    const { snapshot } = captureEvidence(root, source, chunks);
    const ref = referenceFor(snapshot, { chunkId: 'c1', anchor: '#/tables/0' });
    const reading = readEvidence(snapshot, ref, { scope: 'section' });
    assert.match(reading.context.content.text, /Example only/);
    const visuals = readVisuals(root, snapshot, reading.records, { pinned: true, assetIds: ['c1'] });
    assert.equal(visuals.items[0].data, pixel.toString('base64'));
    assert.equal(readVisuals(root, snapshot, [chunks[0]], { pinned: true, assetIds: ['c1'] }).items[0].status, 'not_in_read_scope');
    assert.equal(readVisuals(root, snapshot, reading.records, { pinned: false }).items[0].status, 'unpinned');
    const { visuals: _old, ...legacy } = snapshot;
    assert.equal(readVisuals(root, legacy, reading.records, { pinned: true }).items[0].status, 'unavailable_legacy');
    writeFileSync(path.join(assets, 'table.png'), 'not PNG');
    assert.equal(readVisuals(root, snapshot, reading.records, { pinned: true, assetIds: ['c1'] }).items[0].data, pixel.toString('base64'));
    assert.equal(captureEvidence(root, source, chunks).snapshot.visuals.find(item => item.assetId === 'c1').status, 'invalid_asset');
    writeFileSync(path.join(root, 'evidence', 'assets', hash(pixel) + '.png'), 'changed');
    assert.notEqual(readVisuals(root, snapshot, reading.records, { pinned: true, assetIds: ['c1'] }).items[0].status, 'ready');
    writeFileSync(path.join(assets, 'pins.png'), Buffer.alloc(2 * 1024 * 1024 + 1));
    assert.equal(captureEvidence(root, source, chunks).snapshot.visuals.find(item => item.assetId === 'c3').status, 'too_large');
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test('isolated HTTP and Agent knowledge_get returns text plus actual MCP image blocks', async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'kb-visual-api-'));
  const probe = createServer(); await new Promise(resolve => probe.listen(0, '127.0.0.1', resolve));
  const port = probe.address().port; await new Promise(resolve => probe.close(resolve));
  process.env.EASYEDA_KNOWLEDGE_ROOT = root; process.env.EASYEDA_KNOWLEDGE_PORT = String(port); process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR = '0';
  const { startKnowledgeService } = await import('../src/service.mjs');
  const service = await startKnowledgeService();
  try {
    let f;
    if (process.env.KB_VISUAL_SAMPLE_PARSED) {
      const originalAssets = process.env.KB_VISUAL_SAMPLE_PARSED + '.assets';
      const parsedPath = path.join(root, 'parsed', 'sample.md'), assets = parsedPath + '.assets';
      mkdirSync(assets, { recursive: true });
      for (const name of ['manifest.json', 'index.json', 'document.json']) copyFileSync(path.join(originalAssets, name), path.join(assets, name));
      const manifest = JSON.parse(readFileSync(path.join(assets, 'manifest.json')));
      const index = JSON.parse(readFileSync(path.join(assets, 'index.json')));
      const chunks = [];
      for (const [anchor, item] of Object.entries(index.items)) {
        const text = item.text || (item.asset ? '图表 ' + anchor : ''); if (!text) continue;
        if (item.asset) { mkdirSync(path.dirname(path.join(assets, item.asset)), { recursive: true }); copyFileSync(path.join(originalAssets, item.asset), path.join(assets, item.asset)); }
        const parts = item.label === 'table' ? [{ text }] : chunkText(text);
        parts.forEach((part, i) => chunks.push({ anchor: parts.length === 1 ? anchor : anchor + '/' + i, text: part.text, contentHash: hash(part.text), metadata: { locations: item.locations || [], ...(item.asset ? { assetPath: path.join(assets, item.asset) } : {}) } }));
      }
      f = { source: { name: 'existing-docling-sample.pdf', type: 'datasheet', sourceHash: manifest.source_hash, parseStatus: manifest.parse_status, metadata: { parser: manifest.parser + '@' + manifest.parser_version, parsedPath } }, chunks, assets };
    } else f = fixture(root);
    const published = service.store.publish({ ...f.source, contentPath: path.join(root, 'fixture.pdf') }, f.chunks);
    const records = service.store.chunks(published.sourceId);
    const { snapshot } = captureEvidence(root, published, records);
    const table = snapshot.records.find(item => item.anchor === '#/tables/0' && item.metadata.assetPath) || snapshot.records.find(item => item.metadata.assetPath);
    const evidenceRef = referenceFor(snapshot, { chunkId: table.chunkId, anchor: table.anchor });
    const call = async options => {
      const query = new URLSearchParams(Object.entries({ evidenceRef: JSON.stringify(evidenceRef), scope: 'section', ...options }));
      const response = await fetch(service.baseUrl + '/api/get?' + query, { headers: { 'X-Knowledge-Token': service.token } });
      assert.equal(response.status, 200); return response.json();
    };
    const plain = await call({}); assert.equal(plain.visuals, undefined);
    const visual = await call({ includeVisuals: 'true', assetIds: JSON.stringify([table.chunkId]) });
    assert.equal(visual.text, plain.text); if (!process.env.KB_VISUAL_SAMPLE_PARSED) assert.match(visual.text, /Example only/);
    const validator = new AjvJsonSchemaValidator();
    assert(validator.getValidator(knowledgeGetInputSchema)({ evidenceRef, scope: 'section', includeVisuals: true }).valid);
    assert(validator.getValidator(knowledgeGetOutputSchema)(visual).valid);
    const { knowledgeTool } = await import('../../mcp/src/knowledge-tools.mjs');
    const result = await knowledgeTool('knowledge_get', { evidenceRef, scope: 'section', assetIds: [table.chunkId] });
    assert.equal(result.isError, false);
    assert.equal(result.content.filter(item => item.type === 'image').length, 1);
    const expectedImage = readFileSync(table.metadata.assetPath).toString('base64');
    assert.equal(result.content.find(item => item.type === 'image').data, expectedImage);
    assert.equal(result.structuredContent.visuals.items[0].data, undefined);
    assert(!result.content[0].text.includes(expectedImage));
    assert.equal(knowledgeGetResult(plain).content.length, 1);
    const imported=await fetch(service.baseUrl+'/api/sources',{method:'POST',headers:{'X-Knowledge-Token':service.token,'content-type':'application/json'},body:JSON.stringify({name:'KBPNG.png',contentBase64:pixel.toString('base64'),metadata:{models:['KBPNG']}})});
    assert.equal(imported.status,202);const {jobId}=await imported.json();let job;
    for(let i=0;i<100;i++){job=service.store.getJob(jobId);if(['completed','failed'].includes(job.status))break;await new Promise(resolve=>setTimeout(resolve,10));}
    assert.equal(job.status,'completed');const imageSource=service.store.get(job.detail.sourceId);
    assert.equal(imageSource.parseStatus,'partial');assert.equal(imageSource.metadata.qualityStatus,'unreviewed');
    const current=await (await fetch(service.baseUrl+'/api/get?id='+imageSource.sourceId,{headers:{'X-Knowledge-Token':service.token}})).json();
    const imageRead=await (await fetch(service.baseUrl+'/api/get?'+new URLSearchParams({evidenceRef:JSON.stringify(current.evidenceRef),includeVisuals:'true',scope:'source'}),{headers:{'X-Knowledge-Token':service.token}})).json();
    assert.equal(imageRead.visuals.items[0].data,pixel.toString('base64'));assert.equal(imageRead.context.content.contextCompleteness,'unknown');
    assert.equal(imageRead.context.verification.measurement,'unknown');assert.match(imageRead.text,/尚未解析/);
    const pngChunk=service.store.chunks(imageSource.sourceId)[0];
    assert.equal((await fetch(service.baseUrl+'/api/asset?chunkId='+pngChunk.chunk_id,{headers:{'X-Knowledge-Token':service.token}})).status,200);
    writeFileSync(imageSource.contentPath,'changed');
    const frozen=await (await fetch(service.baseUrl+'/api/get?'+new URLSearchParams({evidenceRef:JSON.stringify(current.evidenceRef),includeVisuals:'true',scope:'source'}),{headers:{'X-Knowledge-Token':service.token}})).json();
    assert.equal(frozen.visuals.items[0].data,pixel.toString('base64'),'old evidence must preserve the exact original image');
    const receipt=await call({scope:'source',maxChars:'30000'}),receiptFile=path.join(root,'read-receipt.json'),planFile=path.join(root,'plan.json');
    assert.equal(receipt.context.content.contextCompleteness,'complete');writeFileSync(receiptFile,JSON.stringify(receipt));
    const plan={schemaVersion:'easyeda.plan/1',revision:1,target:{projectUuid:'fixture-project',documentUuid:'fixture-doc'},semanticTarget:{schemaVersion:1,scope:{complete:true,kind:'page',projectUuid:'fixture-project',documentUuid:'fixture-doc'},components:[{key:'ref:R1',ref:'R1',pins:[]}],nets:[]},sources:[{id:'datasheet',path:receiptFile,sha256:hash(readFileSync(receiptFile)),knowledge:{adoptedFor:'fixture evidence binding',conditions:'not hardware evidence',componentKeys:['ref:R1'],netNames:[],checkMethod:'ai_review'}}],confirmation:{status:'unconfirmed',authoredBy:'caller'},steps:[{action:'schematic.component.modify',payload:{primitiveId:'fixture-id',patch:{x:20}}}]};
    writeFileSync(planFile,JSON.stringify(plan));
    const {planTool}=await import('../../mcp/src/plan-tools.mjs');let edaCalls=0;
    const invoke=async()=>{edaCalls++;throw Error('EDA must not be called');};
    const inspection=await planTool({operation:'inspect',planFile},new Map(),invoke);
    assert.equal(inspection.isError,false);assert.equal(inspection.structuredContent.knowledgeChecks[0].status,'current');
    service.store.patchSource(f.source.sourceId,{module:'changed-fixture-module'});
    await assert.rejects(planTool({operation:'apply',planFile,window:'fixture',expectedHash:hash(readFileSync(planFile))},new Map(),invoke),/scope changed/);
    assert.equal(edaCalls,0,'changed evidence must block before any EDA request');
    console.log(JSON.stringify({ validation: process.env.KB_VISUAL_SAMPLE_PARSED ? 'existing-docling-isolated-get' : 'isolated-live-get', textPreserved: true, imageBlocks: 1, imageBytes: Buffer.from(expectedImage, 'base64').length, imageAnchor: table.anchor, sourceRevision: result.structuredContent.visuals.sourceRevision }));
  } finally { await new Promise(resolve => service.server.close(resolve)); service.store.close(); rmSync(root, { recursive: true, force: true }); }
});

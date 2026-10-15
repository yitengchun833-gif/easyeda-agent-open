import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, renameSync, unlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { freezeContext, sectionContext } from './context-map.mjs';
import { freezeVisuals } from './visual-evidence.mjs';

export class EvidenceError extends Error {
  constructor(code, message, status = 400) { super(message); this.name = 'EvidenceError'; this.code = code; this.status = status; }
}

const fail = (code, message, status) => { throw new EvidenceError(code, message, status); };
const sha256 = value => createHash('sha256').update(value).digest('hex');
const isHash = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
const list = value => value == null ? [] : Array.isArray(value) ? value : [value];
const jsonValue = value => {
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value;
  if (typeof value === 'number') { if (!Number.isFinite(value)) fail('EVIDENCE_INVALID', 'Evidence contains a non-finite number'); return value; }
  if (Array.isArray(value)) return value.map(jsonValue);
  if (!value || typeof value !== 'object' || (Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null)) fail('EVIDENCE_INVALID', 'Evidence contains a non-plain JSON object');
  const result = Object.create(null);
  for (const key of Object.keys(value).sort()) {
    if (value[key] === undefined) fail('EVIDENCE_INVALID', `Evidence contains undefined at ${key}`);
    result[key] = jsonValue(value[key]);
  }
  return result;
};
export const canonical = value => JSON.stringify(jsonValue(value));
const parseMetadata = value => {
  if (value == null) return {};
  if (typeof value === 'string') { try { return JSON.parse(value); } catch { fail('EVIDENCE_INVALID', 'Invalid stored metadata'); } }
  return value;
};
const stableMetadata = value => {
  const metadata = { ...parseMetadata(value) };
  delete metadata.vectorSignature; delete metadata.embeddingModel; delete metadata.embeddingSignature;
  delete metadata.vectorModel; delete metadata.indexStatus; delete metadata.enabled; delete metadata.updatedAt;
  return metadata;
};
const sourceIdOf = source => source?.sourceId ?? source?.source_id;
const revisionOf = source => source?.revision ?? source?.sourceRevision;
const sourceHashOf = source => source?.sourceHash ?? source?.source_hash;
const recordIdOf = record => record?.chunkId ?? record?.chunk_id;
const recordHashOf = record => record?.contentHash ?? record?.content_hash;
const recordMetadata = record => stableMetadata(record?.metadata ?? record?.metadata_json);
const locationsOf = record => recordMetadata(record).locations ?? [];
const optionalText = value => typeof value === 'string' && value.trim() ? value : null;
const snapshotPayload = snapshot => {
  const { evidenceId, snapshotHash, ...payload } = snapshot || {};
  return payload;
};
const snapshotHashOf = snapshot => sha256(canonical(snapshotPayload(snapshot)));
const evidenceDirectory = root => path.join(path.resolve(root), 'evidence');
const snapshotFile = (root, evidenceId) => path.join(evidenceDirectory(root), `${evidenceId}.json`);

function normaliseSource(source) {
  const sourceId = sourceIdOf(source), sourceRevision = revisionOf(source), sourceHash = sourceHashOf(source);
  if (typeof sourceId !== 'string' || !sourceId || typeof sourceRevision !== 'string' || !sourceRevision || !isHash(sourceHash)) {
    fail('EVIDENCE_INVALID', 'Source requires sourceId, revision and SHA-256 sourceHash');
  }
  const metadata = stableMetadata(source.metadata ?? source.metadata_json);
  const type = source.type || 'reference';
  const tags = list(metadata.tags);
  const kind = type === 'lesson' ? 'experience' : (type === 'reference' && tags.includes('project_result') ? 'artifact' : 'source');
  return {
    sourceId, sourceRevision, sourceHash, kind,
    title: source.title || source.name || sourceId, sourceType: type, domain: source.domain ?? null,
    name: source.name || source.contentPath || source.content_path || sourceId,
    contentPath: source.contentPath ?? source.content_path ?? null,
    metadata, status: source.status ?? 'unknown', parseStatus: source.parseStatus ?? source.parse_status ?? 'unknown'
  };
}
function normaliseRecords(chunks) {
  if (!Array.isArray(chunks)) fail('EVIDENCE_INVALID', 'Evidence chunks must be an array');
  return chunks.map((record, index) => {
    const chunkId = recordIdOf(record), anchor = record?.anchor;
    if (typeof chunkId !== 'string' || !chunkId || typeof anchor !== 'string' || !anchor || typeof record?.text !== 'string') {
      fail('EVIDENCE_INVALID', `Invalid evidence chunk at index ${index}`);
    }
    const metadata = recordMetadata(record), suppliedHash = recordHashOf(record);
    if (suppliedHash !== undefined && !isHash(suppliedHash)) fail('EVIDENCE_INVALID', `Invalid content hash at index ${index}`);
    // Older ingestion hashed before trimming. Freeze the bytes we actually return;
    // retain a differing legacy digest only as provenance, never as a body check.
    const contentHash = sha256(record.text);
    return { chunkId, anchor, text: record.text, contentHash, ...(suppliedHash && suppliedHash !== contentHash ? { legacyContentHash: suppliedHash } : {}), metadata, locations: locationsOf(record) };
  });
}
function select(snapshot, selector = { chunkId: null, anchor: null }) {
  const normalized = { chunkId: selector?.chunkId ?? null, anchor: selector?.anchor ?? null };
  if (normalized.chunkId !== null && typeof normalized.chunkId !== 'string') fail('EVIDENCE_SELECTOR_INVALID', 'Invalid chunk selector');
  if (normalized.anchor !== null && typeof normalized.anchor !== 'string') fail('EVIDENCE_SELECTOR_INVALID', 'Invalid anchor selector');
  const matched = snapshot.records.filter(record =>
    (normalized.chunkId === null || record.chunkId === normalized.chunkId) &&
    (normalized.anchor === null || record.anchor === normalized.anchor || (normalized.chunkId === null && record.anchor.startsWith(`${normalized.anchor}/`))));
  if ((normalized.chunkId !== null && matched.length !== 1) || (normalized.chunkId === null && normalized.anchor !== null && matched.length === 0)) {
    fail('EVIDENCE_SELECTOR_INVALID', 'Evidence selector does not identify frozen evidence');
  }
  return { selector: normalized, records: matched };
}

export function captureEvidence(root, source, chunks) {
  const frozenSource = normaliseSource(source);
  const records = normaliseRecords(chunks);
  const structure = freezeContext(root, frozenSource, records);
  const visuals = freezeVisuals(root, frozenSource, records, structure);
  const payload = { snapshotVersion: 1, source: frozenSource, records, structure, ...(visuals ? { visuals } : {}) };
  const snapshotHash = sha256(canonical(payload));
  const evidenceId = `ev-${snapshotHash}`;
  const serialized = canonical(payload);
  const directory = evidenceDirectory(root), file = snapshotFile(root, evidenceId);
  mkdirSync(directory, { recursive: true });
  if (existsSync(file)) {
    const prior = loadEvidence(root, { schemaVersion: 1, evidenceId, sourceId: frozenSource.sourceId, sourceRevision: frozenSource.sourceRevision, sourceHash: frozenSource.sourceHash, snapshotHash, selector: { chunkId: null, anchor: null } });
    if (canonical(snapshotPayload(prior)) !== serialized) fail('EVIDENCE_INTEGRITY', 'Existing evidence file is not the requested snapshot', 409);
  } else {
    const temporary = `${file}.${process.pid}.${Date.now()}.tmp`;
    try { writeFileSync(temporary, serialized, { encoding: 'utf8', flag: 'wx' }); renameSync(temporary, file); }
    finally { if (existsSync(temporary)) unlinkSync(temporary); }
  }
  const snapshot = loadEvidence(root, { schemaVersion: 1, evidenceId, sourceId: frozenSource.sourceId, sourceRevision: frozenSource.sourceRevision, sourceHash: frozenSource.sourceHash, snapshotHash, selector: { chunkId: null, anchor: null } });
  return { snapshot, evidenceRef: referenceFor(snapshot, { chunkId: null, anchor: null }) };
}

export function loadEvidence(root, ref) {
  if (!ref || !isHash(ref.snapshotHash) || ref.evidenceId !== `ev-${ref.snapshotHash}`) fail('EVIDENCE_INTEGRITY', 'Invalid evidence reference');
  const file = snapshotFile(root, ref.evidenceId);
  if (!existsSync(file)) fail('EVIDENCE_UNAVAILABLE', 'Frozen evidence is unavailable', 404);
  let payload; try { payload = JSON.parse(readFileSync(file, 'utf8')); } catch { fail('EVIDENCE_INTEGRITY', 'Frozen evidence cannot be decoded', 409); }
  const snapshotHash = snapshotHashOf(payload);
  if (snapshotHash !== ref.snapshotHash || payload.snapshotVersion !== 1 || !payload.source || !Array.isArray(payload.records)) fail('EVIDENCE_INTEGRITY', 'Frozen evidence hash does not match its reference', 409);
  const snapshot = { ...payload, snapshotHash, evidenceId: `ev-${snapshotHash}` };
  const source = snapshot.source;
  if (source.sourceId !== ref.sourceId || source.sourceRevision !== ref.sourceRevision || source.sourceHash !== ref.sourceHash) fail('EVIDENCE_INTEGRITY', 'Evidence reference belongs to another source version', 409);
  select(snapshot, ref.selector);
  return snapshot;
}

export function referenceFor(snapshot, selector = { chunkId: null, anchor: null }) {
  const snapshotHash = snapshot?.snapshotHash || snapshotHashOf(snapshot);
  if (!isHash(snapshotHash) || !snapshot?.source) fail('EVIDENCE_INTEGRITY', 'Invalid frozen evidence');
  const found = select(snapshot, selector).selector;
  return { schemaVersion: 1, evidenceId: `ev-${snapshotHash}`, sourceId: snapshot.source.sourceId,
    sourceRevision: snapshot.source.sourceRevision, sourceHash: snapshot.source.sourceHash, snapshotHash, selector: found };
}

const state = (value, allowed) => allowed.includes(value) ? value : 'unknown';
const sourceFields = snapshot => {
  const m = snapshot.source.metadata || {}, validation = m.validation || {};
  const claimOrigin = ['proposer-claim', 'source-record', 'unknown'].includes(m.validationKind) ? m.validationKind : (snapshot.source.kind === 'experience' ? 'proposer-claim' : 'source-record');
  return {
    identity: { models: list(m.models ?? m.partNumbers ?? m.partNumber ?? m.aliases).filter(value => optionalText(value) !== null), manufacturer: optionalText(m.manufacturer), package: optionalText(m.package) },
    scope: { project: optionalText(m.project), module: optionalText(m.module), documentVersion: optionalText(m.version) },
    applicability: { conditions: optionalText(m.conditions ?? m.applicability), exclusions: optionalText(m.notApplicable ?? m.exclusions) },
    verification: { claimOrigin,
      connectivity: state(validation.connectivity, ['pass', 'fail', 'unknown']), layout: state(validation.layout, ['pass', 'fail', 'unknown']), measurement: state(validation.measurement, ['pass', 'fail', 'unknown']),
      engineerReview: state(validation.engineerReview, ['accepted', 'rejected', 'unknown']), scope: optionalText(validation.scope), note: optionalText(validation.note) },
    artifacts: list(m.artifacts).filter(value => optionalText(value) !== null),
    engineeringRefs: list(m.engineeringRefs).filter(ref => ref && ['block', 'part', 'rule'].includes(ref.kind) && typeof ref.id === 'string' && ref.id.trim()).map(ref => ({ kind: ref.kind, id: ref.id, version: optionalText(ref.version) }))
  };
};

export function contextFor(snapshot, sourceRuntime = {}, selector = { chunkId: null, anchor: null }, content, readRecords = null) {
  const selected = select(snapshot, selector), fields = sourceFields(snapshot), runtime = sourceRuntime || {};
  const records = readRecords || selected.records;
  const locations = records.flatMap(record => {
    const values = record.locations?.length ? record.locations : [{}];
    return values.map(location => ({ anchor: location?.anchor ?? record.anchor,
      pages: [...new Set(list(location?.pages).filter(page => Number.isInteger(page) && page > 0).concat(Number.isInteger(location?.page_no) && location.page_no > 0 ? [location.page_no] : []))] }));
  });
  const evidenceRef = referenceFor(snapshot, selected.selector);
  return {
    schemaVersion: 1, kind: snapshot.source.kind, title: snapshot.source.title, sourceType: snapshot.source.sourceType, domain: snapshot.source.domain,
    evidenceRef, ...fields,
    states: { review: state(snapshot.source.status, ['approved', 'pending', 'rejected', 'unknown']), parse: state(snapshot.source.parseStatus, ['success', 'partial', 'failed', 'unknown']), index: state(runtime.indexStatus ?? runtime.index_status, ['keyword_ready', 'ready', 'failed', 'unknown']) },
    provenance: { file: snapshot.source.name, parser: optionalText(snapshot.source.metadata?.parser), locations },
    content: content || { text: '', readScope: selected.selector.chunkId || selected.selector.anchor ? 'chunk' : 'source', truncated: false, nextCursor: null, contextCompleteness: 'unknown' }
  };
}

const cursorFor = value => {
  const payload = canonical(value), checksum = sha256(payload).slice(0, 24);
  const cursor = Buffer.from(JSON.stringify({ ...value, checksum })).toString('base64url');
  if (cursor.length > 500) fail('EVIDENCE_INVALID', 'Evidence cursor exceeds the protocol limit');
  return cursor;
};
const parseCursor = cursor => {
  try { const value = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8')); const { checksum, ...payload } = value; if (checksum !== sha256(canonical(payload)).slice(0, 24)) throw Error(); return payload; }
  catch { fail('CURSOR_MISMATCH', 'Cursor is invalid or has been changed'); }
};

export function readEvidence(snapshot, ref, { scope, cursor = null, legacyOffset, maxChars = 30000, limit } = {}, sourceRuntime = {}) {
  const pinned = referenceFor(snapshot, ref?.selector);
  if (!ref || canonical(pinned) !== canonical(ref)) fail('EVIDENCE_INTEGRITY', 'Read reference does not match frozen evidence', 409);
  const selected = select(snapshot, ref.selector);
  const priorCursor=cursor?parseCursor(cursor):null;
  const readScope = scope || priorCursor?.scope || (snapshot.source.kind==='experience'?'source':ref.selector.chunkId || ref.selector.anchor ? 'chunk' : 'source');
  if (!['chunk', 'section', 'source'].includes(readScope)) fail('EVIDENCE_INVALID', 'Invalid evidence read scope');
  if (!Number.isInteger(maxChars) || maxChars < 200 || maxChars > 30000) fail('EVIDENCE_INVALID', 'maxChars must be between 200 and 30000');
  if(limit!==undefined&&(!Number.isInteger(limit)||limit<1||limit>100))fail('EVIDENCE_INVALID','limit must be between 1 and 100');
  if (readScope !== 'source' && ref.selector.chunkId === null && ref.selector.anchor === null) fail('EVIDENCE_SELECTOR_INVALID', 'Local reads require a frozen chunk or anchor selector');
  let records = readScope === 'source' ? snapshot.records : selected.records;
  let segments=records.map(record=>({text:record.text,chunkId:record.chunkId})),separator='\n\n';
  let expansion={basis:readScope==='source'?'source':'exact',relationStatus:'not_applicable',warnings:[]};
  let complete=readScope==='source';
  if(readScope==='section') {
    const section=sectionContext(snapshot,selected.records);
    ({records,segments,separator,expansion,complete}=section);
  }else if(readScope==='source'&&snapshot.structure?.units?.length) {
    segments=snapshot.structure.units.map((unit,i)=>({text:unit.text,unit:i}));
    separator=snapshot.structure.basis==='markdown_headings'?'':'\n\n';
  }
  const text=segments.map(segment=>segment.text).join(separator);
  let position=0;
  const spans=segments.map((segment,i)=>{const start=position;position+=segment.text.length+(i+1<segments.length?separator.length:0);return {...segment,start,end:position};});
  const bindingOptions={selector:ref.selector,scope:readScope,maxChars,...(limit===undefined?{}:{limit}),...(readScope==='section'?{selectionHash:sha256(text)}:{})};
  const binding = { evidenceId: ref.evidenceId, bindingHash: sha256(canonical(bindingOptions)), scope: readScope, maxChars };
  let offset = 0;
  if (cursor && legacyOffset !== undefined) fail('CURSOR_MISMATCH', 'A pinned cursor cannot be combined with a legacy offset');
  if (cursor) { const payload = priorCursor; if (canonical({ evidenceId: payload.evidenceId, bindingHash: payload.bindingHash, scope: payload.scope, maxChars: payload.maxChars }) !== canonical(binding)) fail('CURSOR_MISMATCH', 'Cursor belongs to another evidence read'); offset = payload.offset; }
  else if (legacyOffset !== undefined) offset = legacyOffset;
  if (!Number.isInteger(offset) || offset < 0 || offset > text.length) fail('CURSOR_MISMATCH', 'Cursor offset is invalid');
  const allowed=spans.filter(span=>span.end>offset).slice(0,limit??100);
  let end=Math.min(offset+maxChars,allowed.at(-1)?.end??text.length,text.length);
  if(end<text.length&&/[\uD800-\uDBFF]/.test(text[end-1])&&/[\uDC00-\uDFFF]/.test(text[end]))end--;
  const truncated=end<text.length;
  const nextCursor = truncated ? cursorFor({ ...binding, offset: end }) : null;
  const contextCompleteness = truncated||offset>0 ? 'partial' : (complete&&snapshot.source.parseStatus==='success'?'complete':'unknown');
  const visible=spans.filter(span=>span.end>offset&&span.start<end);
  const visibleIds=new Set(visible.map(span=>span.chunkId)),visibleUnits=new Set(visible.map(span=>span.unit).filter(i=>i!==undefined));
  const pageRecords=records.filter(record=>visibleIds.has(record.chunkId)||(snapshot.structure?.recordUnits?.[record.chunkId]||[]).some(i=>visibleUnits.has(i)));
  const content = { text: text.slice(offset, end), readScope, truncated, nextCursor, contextCompleteness, contextExpansion:expansion };
  return { context: contextFor(snapshot, sourceRuntime, ref.selector, content, pageRecords), records:pageRecords };
}

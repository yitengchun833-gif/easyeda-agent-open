import { DatabaseSync } from 'node:sqlite';
import { mkdirSync } from 'node:fs';
import path from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import { canonical, EvidenceError } from './evidence.mjs';
import { keywordScore, strictIdentityMatches, terms, isEvidence } from './search-index.mjs';

const now = () => new Date().toISOString();
const json = value => JSON.stringify(value ?? {});
const parse = value => { try { return JSON.parse(value || '{}'); } catch { return {}; } };
const sourceMetadata = value => {
  try {const metadata=JSON.parse(value);if(!metadata||typeof metadata!=='object'||Array.isArray(metadata))throw Error();return metadata;}
  catch {throw Object.assign(new Error('Stored source metadata is corrupt'),{code:'EVIDENCE_INTEGRITY',status:409});}
};
const array = value => value == null ? [] : Array.isArray(value) ? value : [value];
const same = (a,b) => String(a).toLowerCase() === String(b).toLowerCase();
const editableMetadata = ['tags','version','source','models','pins','projectScope','manufacturer','partNumbers','aliases','project','module','confidentiality','engineeringRefs'];
export function checkMetadata(metadata = {}) {
  if(!metadata || typeof metadata!=='object' || Array.isArray(metadata))throw Error('Metadata must be an object');
  for(const key of ['project','module'])if(metadata[key]!==undefined && (typeof metadata[key]!=='string'||metadata[key].length>200))throw Error('Invalid metadata '+key);
  // Classification is descriptive; this single-user service does not implement group ACLs.
  if(metadata.confidentiality!==undefined && !['public','internal','restricted'].includes(metadata.confidentiality))throw Error('Invalid confidentiality classification');
  if(metadata.engineeringRefs!==undefined){
    if(!Array.isArray(metadata.engineeringRefs)||metadata.engineeringRefs.length>32)throw Error('Invalid engineering references');
    for(const ref of metadata.engineeringRefs)if(!ref || !['block','part','rule'].includes(ref.kind)||typeof ref.id!=='string'||!ref.id.trim()||ref.id.length>200||(ref.version!==undefined&&(typeof ref.version!=='string'||ref.version.length>200)))throw Error('Invalid engineering reference');
  }
  return metadata;
}
export function matchesFilters(source, filters = {}, query = '') {
  const m = source.metadata || {};
  for (const [field, requested] of [['type',filters.type ?? filters.types],['domain',filters.domain ?? filters.domains],['status',filters.status]]) {
    if (requested && !(field === 'domain' && source.domain === 'both') && !array(requested).includes(source[field])) return false;
  }
  if (filters.sourceIds && !array(filters.sourceIds).includes(source.sourceId)) return false;
  if (filters.projectScope && m.projectScope && m.projectScope !== filters.projectScope) return false;
  if (filters.manufacturer && !same(m.manufacturer,filters.manufacturer)) return false;
  for(const field of ['project','module','version','confidentiality'])if(filters[field] && !array(filters[field]).some(value=>same(m[field],value)))return false;
  if (array(filters.tags).some(tag => !array(m.tags).some(value => same(value,tag)))) return false;
  for (const [wanted, available] of [[filters.models ?? filters.model ?? filters.partNumbers,array(m.models).concat(array(m.partNumbers),array(m.partNumber),array(m.aliases))],[filters.pins,m.pins]]) {
    if (wanted && !array(wanted).every(value => array(available).some(item => same(item,value)))) return false;
  }
  // Explicit part-like tokens must not silently match longer/shorter model IDs.
  const partTokens = (query.match(/[A-Za-z][A-Za-z0-9._+\-/]{4,}/g) || []).filter(token => /\d/.test(token));
  const models = array(m.models).concat(array(m.partNumbers),array(m.partNumber),array(m.aliases));
  if (partTokens.length && models.length && !partTokens.some(token => models.some(model => same(token,model)))) return false;
  return true;
}

export class KnowledgeStore {
  constructor(root) {
    mkdirSync(root, { recursive: true });
    this.db = new DatabaseSync(path.join(root, 'catalog.sqlite'));
    this.db.exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
      CREATE TABLE IF NOT EXISTS sources (
        source_id TEXT PRIMARY KEY, revision TEXT NOT NULL, name TEXT NOT NULL, title TEXT NOT NULL,
        type TEXT NOT NULL, domain TEXT, status TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
        source_hash TEXT NOT NULL, content_path TEXT NOT NULL, metadata_json TEXT NOT NULL,
        parse_status TEXT NOT NULL, index_status TEXT NOT NULL, generation INTEGER NOT NULL DEFAULT 0,
        created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT);
      CREATE TABLE IF NOT EXISTS chunks (
        chunk_id TEXT PRIMARY KEY, source_id TEXT NOT NULL REFERENCES sources(source_id), revision TEXT NOT NULL,
        anchor TEXT NOT NULL, text TEXT NOT NULL, content_hash TEXT NOT NULL, metadata_json TEXT NOT NULL,
        generation INTEGER NOT NULL, created_at TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS candidates (
        candidate_id TEXT PRIMARY KEY, payload_json TEXT NOT NULL, status TEXT NOT NULL,
        review_json TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
      CREATE TABLE IF NOT EXISTS jobs (
        job_id TEXT PRIMARY KEY, kind TEXT NOT NULL, status TEXT NOT NULL, detail_json TEXT NOT NULL,
        created_at TEXT NOT NULL, updated_at TEXT NOT NULL);`);
    this.db.exec('BEGIN IMMEDIATE');
    try {
      if(!this.db.prepare('PRAGMA table_info(candidates)').all().some(column=>column.name==='revision'))
        this.db.exec('ALTER TABLE candidates ADD COLUMN revision INTEGER NOT NULL DEFAULT 1');
      this.db.exec('CREATE TABLE IF NOT EXISTS candidate_submissions (submission_id TEXT PRIMARY KEY, request_hash TEXT NOT NULL, receipt_json TEXT NOT NULL)');
      if(!this.db.prepare('PRAGMA table_info(candidates)').all().some(column=>column.name==='source_versions_json'))
        this.db.exec('ALTER TABLE candidates ADD COLUMN source_versions_json TEXT');
      this.db.exec('CREATE TABLE IF NOT EXISTS candidate_reviews (candidate_id TEXT NOT NULL, candidate_revision INTEGER NOT NULL, decision TEXT NOT NULL, receipt_json TEXT NOT NULL, PRIMARY KEY(candidate_id,candidate_revision))');
      this.db.exec('COMMIT');
    }catch(error){this.db.exec('ROLLBACK');this.db.close();throw error;}
  }
  source(row) { return { sourceId: row.source_id, revision: row.revision, name: row.name, title: row.title, type: row.type, domain: row.domain, status: row.status, enabled: !!row.enabled, sourceHash: row.source_hash, contentPath: row.content_path, metadata: sourceMetadata(row.metadata_json), parseStatus: row.parse_status, indexStatus: row.index_status, generation: row.generation, chunkCount: this.db.prepare('SELECT count(*) AS n FROM chunks WHERE source_id=?').get(row.source_id).n, createdAt: row.created_at, updatedAt: row.updated_at }; }
  list(filters = {}) {
    let sql = 'SELECT * FROM sources WHERE deleted_at IS NULL'; const args = [];
    if (filters.enabled !== undefined) { sql += ' AND enabled=?'; args.push(filters.enabled === true || filters.enabled === 'true' ? 1 : 0); }
    if (filters.q) { sql += ' AND (title LIKE ? OR name LIKE ?)'; args.push(`%${filters.q}%`, `%${filters.q}%`); }
    sql += ' ORDER BY updated_at DESC';
    const offset=Math.max(+filters.offset || +filters.cursor || 0,0);
    return this.db.prepare(sql).all(...args).map(row => this.source(row)).filter(source => matchesFilters(source,filters)).slice(offset,offset+Math.min(+filters.limit || 100,10000));
  }
  get(sourceId) { const row = this.db.prepare('SELECT * FROM sources WHERE source_id=? AND deleted_at IS NULL').get(sourceId); return row && this.source(row); }
  findImport(name,sourceHash) { const row=this.db.prepare('SELECT * FROM sources WHERE name=? AND source_hash=? AND deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1').get(name,sourceHash);return row&&this.source(row); }
  saveSource(input) {
    checkMetadata(input.metadata);
    const sourceId = input.sourceId || `kb-${randomUUID()}`; const revision = randomUUID(); const time = now();
    const old = this.get(sourceId); const generation = (old?.generation ?? 0) + 1;
    this.db.prepare(`INSERT INTO sources(source_id,revision,name,title,type,domain,status,enabled,source_hash,content_path,metadata_json,parse_status,index_status,generation,created_at,updated_at)
      VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(source_id) DO UPDATE SET revision=excluded.revision,name=excluded.name,title=excluded.title,type=excluded.type,domain=excluded.domain,status=excluded.status,enabled=excluded.enabled,source_hash=excluded.source_hash,content_path=excluded.content_path,metadata_json=excluded.metadata_json,parse_status=excluded.parse_status,index_status=excluded.index_status,generation=excluded.generation,updated_at=excluded.updated_at,deleted_at=NULL`)
      .run(sourceId, revision, input.name, input.title || input.name, input.type || 'reference', input.domain || null, input.status || 'approved', input.enabled === false ? 0 : 1, input.sourceHash, input.contentPath, json(input.metadata), input.parseStatus || 'success', input.indexStatus || 'keyword_ready', generation, old?.createdAt || time, time);
    return this.get(sourceId);
  }
  replaceChunks(source, chunks) {
    this.db.prepare('DELETE FROM chunks WHERE source_id=?').run(source.sourceId);
    const put = this.db.prepare('INSERT INTO chunks(chunk_id,source_id,revision,anchor,text,content_hash,metadata_json,generation,created_at) VALUES(?,?,?,?,?,?,?,?,?)');
    for (const item of chunks) put.run(`chunk-${randomUUID()}`, source.sourceId, source.revision, item.anchor, item.text, item.contentHash, json(item.metadata), source.generation, now());
  }
  patchSource(id, patch) {
    const item=this.get(id); if(!item)return null;
    const metadata={...item.metadata};
    for(const key of editableMetadata) if(patch[key]!==undefined)metadata[key]=patch[key];
    for(const key of editableMetadata) if(patch.metadata?.[key]!==undefined)metadata[key]=patch.metadata[key];
    checkMetadata(metadata);
    this.db.prepare('UPDATE sources SET title=?,type=?,domain=?,enabled=?,metadata_json=?,updated_at=? WHERE source_id=?').run(patch.title??item.title,patch.type??item.type,patch.domain??item.domain,patch.enabled===undefined?Number(item.enabled):Number(!!patch.enabled),json(metadata),now(),id);
    return this.get(id);
  }
  publish(input,chunks,verifyEvidence) {
    this.db.exec('BEGIN IMMEDIATE');
    try {const source=this.saveSource(input);this.replaceChunks(source,chunks);verifyEvidence?.(source);this.db.exec('COMMIT');return this.get(source.sourceId);}catch(error){this.db.exec('ROLLBACK');throw error;}
  }
  chunks(id) {return this.db.prepare('SELECT * FROM chunks WHERE source_id=? ORDER BY rowid').all(id);}
  evidenceState(id) {
    this.db.exec('SAVEPOINT evidence_read');
    try {
      const source=this.get(id),chunks=source?this.chunks(id):[];
      if(source&&chunks.some(chunk=>chunk.revision!==source.revision))throw new Error('Source and chunks have inconsistent revisions');
      this.db.exec('RELEASE evidence_read');return {source,chunks};
    }catch(error){this.db.exec('ROLLBACK TO evidence_read; RELEASE evidence_read');throw error;}
  }
  indexed(id,status,signature,revision) {
    const source=this.get(id); if(!source)return;
    if(revision&&source.revision!==revision)return false;
    const metadata={...source.metadata}; if(signature)metadata.vectorSignature=signature;
    return !!this.db.prepare('UPDATE sources SET index_status=?,metadata_json=?,updated_at=? WHERE source_id=? AND revision=?').run(status,json(metadata),now(),id,source.revision).changes;
  }
  deleteSource(id) { const result = this.db.prepare('UPDATE sources SET enabled=0, deleted_at=?, updated_at=? WHERE source_id=? AND deleted_at IS NULL').run(now(), now(), id); return result.changes > 0; }
  search(query, filters = {}, topK = 8, identityQuery = query) {
    const params = [];
    let sql = `SELECT c.*,s.title,s.type,s.domain,s.status,s.enabled,s.source_hash,s.metadata_json AS source_metadata FROM chunks c JOIN sources s ON s.source_id=c.source_id WHERE s.deleted_at IS NULL AND s.enabled=1 AND s.status='approved' AND c.revision=s.revision`;
    const type = filters.type ?? filters.types; const domain = filters.domain ?? filters.domains;
    for (const [field, value] of [['type', type], ['domain', domain]]) if (value) { const values=[...(Array.isArray(value)?value:[value]),...(field==='domain'?['both']:[])]; sql += ` AND s.${field} IN (${values.map(()=>'?').join(',')})`; params.push(...values); }
    const rows = this.db.prepare(sql).all(...params).filter(row=>matchesFilters({sourceId:row.source_id,type:row.type,domain:row.domain,status:row.status,metadata:sourceMetadata(row.source_metadata)},filters,identityQuery));
    const scored = rows.map(row => {
      const metadata = {...sourceMetadata(row.source_metadata),...parse(row.metadata_json)},exact = strictIdentityMatches(identityQuery,metadata);
      const topical=terms(query).filter(term=>!exact.includes(term));
      const coverage=topical.length?keywordScore(topical.join(' '),row.text):keywordScore(query,`${row.title}\n${row.text}`);
      const score=isEvidence(row.text,metadata,topical.join(' '))&&coverage>0?coverage+exact.length*2:0;
      return {chunkId:row.chunk_id,sourceId:row.source_id,title:row.title,type:row.type,domain:row.domain,text:row.text,anchor:row.anchor,metadata,revision:row.revision,sourceHash:row.source_hash,score,exactMatches:exact,match:exact.length?'strict_identity':'keyword',generation:row.generation};
    }).filter(item=>item.score>0);
    // Once a query names an indexed hardware identity, partial/prefix text hits
    // are unsafe. Generic material stays available for queries with no identity.
    const exact = scored.filter(item => item.match === 'strict_identity');
    return (exact.length ? exact : scored).sort((a,b) => b.score - a.score).slice(0, Math.min(Math.max(+topK || 8, 1), 50));
  }
  getChunk(id) { return this.db.prepare('SELECT c.* FROM chunks c JOIN sources s ON s.source_id=c.source_id WHERE c.chunk_id=? AND c.revision=s.revision AND s.deleted_at IS NULL AND s.status=\'approved\'').get(id); }
  sourceVersions(payload) {
    const ids=payload.sourceIds||[], expected=payload.sourceVersionSet;
    if(new Set(ids).size!==ids.length)throw new EvidenceError('SOURCE_VERSION_INVALID','Duplicate sourceIds');
    if(expected && (expected.length!==ids.length||new Set(expected.map(item=>item.sourceId)).size!==ids.length||expected.some(item=>!ids.includes(item.sourceId))))
      throw new EvidenceError('SOURCE_VERSION_INVALID','sourceVersionSet must cover exactly sourceIds');
    return ids.map(sourceId=>{
      const source=this.get(sourceId);
      if(!source||!source.enabled||source.status!=='approved'||!['success','partial'].includes(source.parseStatus))throw new EvidenceError('SOURCE_UNAVAILABLE','Referenced source is unavailable: '+sourceId,409);
      const pinned=expected?.find(item=>item.sourceId===sourceId);
      if(pinned&&(pinned.sourceRevision!==source.revision||pinned.sourceHash!==source.sourceHash))throw new EvidenceError('SOURCE_VERSION_STALE','Referenced source changed since reading: '+sourceId,409);
      const {vectorSignature,...metadata}=source.metadata;
      return {sourceId,sourceRevision:source.revision,sourceHash:source.sourceHash,contextHash:createHash('sha256').update(canonical({title:source.title,type:source.type,domain:source.domain,metadata})).digest('hex'),basis:pinned?'read-version':'submission-version'};
    });
  }
  checkCandidateSources(candidate) {
    if(!candidate.payload.sourceIds?.length)return;
    if(!candidate.sourceVersions)throw new EvidenceError('REVIEW_SOURCES_UNPINNED','Legacy candidate has no source baseline; reread sources and revise the pending candidate',409);
    const current=this.sourceVersions(candidate.payload);
    if(canonical(current.map(({basis,...item})=>item))!==canonical(candidate.sourceVersions.map(({basis,...item})=>item)))
      throw new EvidenceError('REVIEW_SOURCES_STALE','Referenced sources or their conditions changed; reread and revise the pending candidate',409);
  }
  propose(payload) { const versions=this.sourceVersions(payload);const id = `candidate-${randomUUID()}`; const time = now(); this.db.prepare('INSERT INTO candidates(candidate_id,payload_json,status,review_json,created_at,updated_at,revision,source_versions_json) VALUES(?,?,?,?,?,?,1,?)').run(id, json(payload), 'pending', null, time, time,json(versions)); return this.candidate(id); }
  candidate(id) { const row=this.db.prepare('SELECT * FROM candidates WHERE candidate_id=?').get(id); return row && { candidateId:row.candidate_id,revision:row.revision,payload:parse(row.payload_json),sourceVersions:row.source_versions_json===null?null:parse(row.source_versions_json),status:row.status,review:parse(row.review_json),createdAt:row.created_at,updatedAt:row.updated_at }; }
  submitCandidate(payload, {submissionId,candidateId,expectedCandidateRevision} = {}) {
    if(submissionId===undefined&&candidateId===undefined&&expectedCandidateRevision===undefined)return {candidate:this.propose(payload)};
    if(typeof submissionId!=='string'||!submissionId.trim()||submissionId.length>200||
       (candidateId!==undefined&&(typeof candidateId!=='string'||!candidateId.trim()||candidateId.length>300))||
       !Number.isSafeInteger(expectedCandidateRevision)||expectedCandidateRevision<0||(candidateId&&expectedCandidateRevision<1)||(!candidateId&&expectedCandidateRevision!==0))
      throw new EvidenceError('CANDIDATE_REQUEST_INVALID','Versioned submission requires submissionId and expectedCandidateRevision (0 for creation)');
    const requestHash=createHash('sha256').update(canonical({operation:candidateId?'update':'create',candidateId:candidateId??null,expectedCandidateRevision,payload})).digest('hex');
    this.db.exec('BEGIN IMMEDIATE');
    try {
      const prior=this.db.prepare('SELECT * FROM candidate_submissions WHERE submission_id=?').get(submissionId);
      let result;
      if(prior){
        if(prior.request_hash!==requestHash)throw new EvidenceError('IDEMPOTENCY_CONFLICT','submissionId was already used for different content',409);
        result={...JSON.parse(prior.receipt_json),replayed:true};
        result.currentRevision=this.candidate(result.candidate.candidateId)?.revision??null;
      }else{
        let candidate;
        if(candidateId){
          const current=this.candidate(candidateId);
          if(!current)throw new EvidenceError('CANDIDATE_NOT_FOUND','Candidate not found',404);
          if(current.revision!==expectedCandidateRevision)throw Object.assign(new EvidenceError('CANDIDATE_STALE','Candidate changed; read the returned current candidate and merge before submitting with a new submissionId',409),{currentCandidate:current});
          if(current.status!=='pending')throw new EvidenceError('CANDIDATE_NOT_PENDING','Reviewed knowledge cannot be overwritten by proposal',409);
          this.db.prepare('UPDATE candidates SET payload_json=?,source_versions_json=?,revision=revision+1,updated_at=? WHERE candidate_id=?').run(json(payload),json(this.sourceVersions(payload)),now(),candidateId);
          candidate=this.candidate(candidateId);
        }else candidate=this.propose(payload);
        result={candidate,submissionId,replayed:false};
        this.db.prepare('INSERT INTO candidate_submissions VALUES(?,?,?)').run(submissionId,requestHash,json(result));
      }
      this.db.exec('COMMIT');return result;
    }catch(error){this.db.exec('ROLLBACK');throw error;}
  }
  candidates(status) { return this.db.prepare(`SELECT * FROM candidates ${status ? 'WHERE status=?' : ''} ORDER BY created_at DESC`).all(...(status?[status]:[])).map(r=>this.candidate(r.candidate_id)); }
  reviewReceipt(id,revision,decision) {
    const row=this.db.prepare('SELECT * FROM candidate_reviews WHERE candidate_id=? AND candidate_revision=?').get(id,revision);
    if(!row)return null;
    if(row.decision!==decision)throw new EvidenceError('REVIEW_CONFLICT','This candidate version was already reviewed with another decision',409);
    return {...JSON.parse(row.receipt_json),replayed:true};
  }
  saveReviewReceipt(id,revision,decision,result) {
    this.db.prepare('INSERT INTO candidate_reviews VALUES(?,?,?,?)').run(id,revision,decision,json(result));
  }
  reviewCandidate(id, decision, reviewer, expectedCandidateRevision) {
    if(!['approved','rejected'].includes(decision))throw new Error('decision must be approved or rejected');
    if(!Number.isSafeInteger(expectedCandidateRevision)||expectedCandidateRevision<1)throw new EvidenceError('REVIEW_VERSION_REQUIRED','Refresh the management UI before reviewing');
    const review={reviewer,decision,at:now(),candidateRevision:expectedCandidateRevision};
    const changed=this.db.prepare("UPDATE candidates SET status=?,review_json=?,updated_at=?,revision=revision+1 WHERE candidate_id=? AND revision=? AND status='pending'").run(decision,json(review),review.at,id,expectedCandidateRevision);
    if(!changed.changes)throw Object.assign(new EvidenceError('REVIEW_STALE','Candidate changed or was already reviewed; refresh and review again',409),{currentCandidate:this.candidate(id)??null});
    return this.candidate(id);
  }
  job(kind, status='queued', detail={}) { const id=`job-${randomUUID()}`; const time=now(); this.db.prepare('INSERT INTO jobs VALUES(?,?,?,?,?,?)').run(id,kind,status,json(detail),time,time); return id; }
  updateJob(id,status,detail={}) {this.db.prepare('UPDATE jobs SET status=?,detail_json=?,updated_at=? WHERE job_id=?').run(status,json(detail),now(),id);}
  getJob(id) { const r=this.db.prepare('SELECT * FROM jobs WHERE job_id=?').get(id);return r&&{jobId:r.job_id,kind:r.kind,status:r.status,detail:parse(r.detail_json),createdAt:r.created_at,updatedAt:r.updated_at}; }
  jobs() { return this.db.prepare('SELECT * FROM jobs ORDER BY created_at DESC LIMIT 100').all().map(r=>({jobId:r.job_id,kind:r.kind,status:r.status,detail:parse(r.detail_json),createdAt:r.created_at,updatedAt:r.updated_at})); }
  close() { this.db.close(); }
}

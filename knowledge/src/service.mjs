import { createServer } from 'node:http';
import { mkdir, readFile, writeFile, rename, unlink } from 'node:fs/promises';
import { randomBytes, createHash, timingSafeEqual } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { KnowledgeStore, matchesFilters, checkMetadata } from './store.mjs';
import { ingest, chunkText } from './ingest.mjs';
import { loadVectorRuntime } from './vector.mjs';
import { isEvidence } from './search-index.mjs';
import { captureEvidence, loadEvidence, referenceFor, contextFor, readEvidence, EvidenceError } from './evidence.mjs';
import { resolveKnowledgeFile } from './relocation.mjs';
import { readVisuals } from './visual-evidence.mjs';

const packageRoot = fileURLToPath(new URL('../', import.meta.url));
const projectRoot = path.resolve(packageRoot, path.basename(path.dirname(packageRoot)) === 'source' ? '../../..' : '../..');
export const knowledgeRoot = path.resolve(process.env.EASYEDA_KNOWLEDGE_ROOT || path.join(projectRoot, 'knowledge'));
const uiRoot = path.join(packageRoot, 'ui');
const runtimeDir = path.join(knowledgeRoot, '.runtime');
const runtimeFile = path.join(runtimeDir, 'service.json');
const version = '1.8.1-open.9.6+knowledge.11';
const citationFor = (source, chunk = {}) => ({ sourceId:source.sourceId,file:source.name,documentVersion:source.metadata.version||null,revision:source.revision,sourceHash:source.sourceHash,project:source.metadata.project||null,module:source.metadata.module||null,anchor:chunk.anchor||null,pages:[...new Set((chunk.metadata?.locations||[]).map(loc=>loc.page_no).filter(page=>page!=null))] });
const reply = (res, status, value) => { res.writeHead(status, { 'content-type':'application/json; charset=utf-8', 'cache-control':'no-store' }); res.end(JSON.stringify(value)); };
const parseJson = text => { const value=JSON.parse(text); if(!value || Array.isArray(value) || typeof value!=='object')throw new Error('Expected JSON object');return value; };
function checkCandidate(input) {
  const allowed=['problem','cause','solution','evidence','conditions','validation','title','project','module','version','domain','models','engineeringRefs','notApplicable','artifacts','taskId','sourceIds','sourceVersionSet','tags','validationKind'];
  if(Object.keys(input).some(key=>!allowed.includes(key)))throw new Error('Unknown candidate field');
  if(input.validationKind!==undefined&&input.validationKind!=='proposer-claim')throw new Error('Invalid candidate validation kind');
  for(const [key,max] of [['problem',4000],['cause',4000],['solution',6000],['conditions',4000]])
    if(typeof input[key]!=='string'||!input[key].trim()||input[key].length>max)throw new Error('Invalid candidate field '+key);
  for(const [key,max] of [['title',400],['notApplicable',4000],['taskId',300],['version',200]])
    if(input[key]!==undefined&&(typeof input[key]!=='string'||input[key].length>max))throw new Error('Invalid candidate field '+key);
  const stringList=(value,maxLength)=>Array.isArray(value)&&value.length<=32&&value.every(item=>typeof item==='string'&&!!item.trim()&&item.length<=maxLength);
  if(!(typeof input.evidence==='string'&&input.evidence.trim()&&input.evidence.length<=6000)&&!(stringList(input.evidence,600)&&input.evidence.length))throw new Error('Invalid candidate evidence');
  for(const [key,max] of [['artifacts',600],['sourceIds',300],['tags',120],['models',200]])if(input[key]!==undefined&&!stringList(input[key],max))throw new Error('Invalid candidate '+key);
  if(input.artifacts!==undefined&&!input.artifacts.length)throw new Error('Invalid candidate artifacts');
  if(input.sourceVersionSet!==undefined&&(!Array.isArray(input.sourceVersionSet)||input.sourceVersionSet.length>32||input.sourceVersionSet.some(item=>!item||Object.keys(item).some(key=>!['sourceId','sourceRevision','sourceHash'].includes(key))||typeof item.sourceId!=='string'||!item.sourceId||item.sourceId.length>300||typeof item.sourceRevision!=='string'||!item.sourceRevision||item.sourceRevision.length>200||!/^([a-f0-9]{64})$/.test(item.sourceHash))))throw new Error('Invalid sourceVersionSet');
  if(Array.isArray(input.engineeringRefs)&&input.engineeringRefs.some(ref=>!ref||Object.keys(ref).some(key=>!['kind','id','version'].includes(key))))throw new Error('Invalid candidate engineering reference');
  const v=input.validation;
  if(typeof v==='string'){if(!v.trim()||v.length>4000)throw new Error('Invalid candidate validation');}
  else {
    const keys=['connectivity','layout','measurement','engineerReview','scope','note'];
    if(!v||typeof v!=='object'||Array.isArray(v)||!Object.keys(v).length||Object.keys(v).some(key=>!keys.includes(key)))throw new Error('Invalid candidate validation');
    for(const key of ['connectivity','layout','measurement'])if(v[key]!==undefined&&!['pass','fail','unknown'].includes(v[key]))throw new Error('Invalid candidate validation '+key);
    if(v.engineerReview!==undefined&&!['accepted','rejected','unknown'].includes(v.engineerReview))throw new Error('Invalid candidate engineer review');
    for(const key of ['scope','note'])if(v[key]!==undefined&&(typeof v[key]!=='string'||v[key].length>2000))throw new Error('Invalid candidate validation '+key);
  }
  if(input.domain!==undefined&&!['schematic','pcb','both'].includes(input.domain))throw new Error('Invalid candidate domain');
  checkMetadata(input);
}
const displayValue=value=>Array.isArray(value)?value.map(item=>'- '+(typeof item==='string'?item:JSON.stringify(item))).join('\n'):typeof value==='object'?JSON.stringify(value,null,2):String(value??'未记录');
function knowledgeCard(payload) {
  const fields=[['可复用结论',payload.solution],['适用条件',payload.conditions],['不适用范围',payload.notApplicable||'未声明；使用前核对当前条件'],['问题与原因',payload.problem+'\n\n'+payload.cause],['已验证范围（提交者声明）',payload.validation],['原始证据',payload.evidence],['历史成果入口',payload.artifacts?.length?payload.artifacts:'未记录']];
  const context=Object.fromEntries(['project','module','domain','models','version','taskId','sourceIds','sourceVersionSet','engineeringRefs','tags'].filter(key=>payload[key]!==undefined).map(key=>[key,payload[key]]));
  return '# '+(payload.title||payload.problem)+'\n\n'+fields.map(([title,value])=>'## '+title+'\n\n'+displayValue(value)).join('\n\n')+'\n\n## 关联信息\n\n'+displayValue(context)+'\n';
}
async function body(req) {
  const parts=[]; let size=0;
  for await(const chunk of req){size+=chunk.length;if(size>140*1024*1024)throw new Error('File exceeds upload limit');parts.push(chunk);}
  return parts.length ? parseJson(Buffer.concat(parts).toString('utf8')) : {};
}
export async function startKnowledgeService() {
  await mkdir(runtimeDir,{recursive:true});
  let config={};try{config=parseJson(await readFile(path.join(knowledgeRoot,'config.json'),'utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}
  config={...config,root:knowledgeRoot,enableVector:process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR ? process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR==='1' : !!config.enableVector,model:process.env.EASYEDA_KNOWLEDGE_MODEL||config.model||'Xenova/multilingual-e5-small',localModelPath:process.env.EASYEDA_KNOWLEDGE_LOCAL_MODEL_PATH||config.localModelPath||path.join(knowledgeRoot,'models')};
  const port=Number(process.env.EASYEDA_KNOWLEDGE_PORT||60933);
  const baseUrl='http://127.0.0.1:'+port;
  const allowedOrigins=new Set([baseUrl,...(config.allowedOrigins||['https://pro.lceda.cn'])]);
  let previous;try{previous=JSON.parse(await readFile(runtimeFile,'utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}
  const token=process.env.EASYEDA_KNOWLEDGE_TOKEN||previous?.token||randomBytes(32).toString('hex');
  const instance=randomBytes(16).toString('hex');
  const store=new KnowledgeStore(knowledgeRoot);
  const captureCurrent = id => {
    const state=store.evidenceState(id);
    if(!state.source)throw new EvidenceError('EVIDENCE_UNAVAILABLE','Source is unavailable',404);
    return {...captureEvidence(knowledgeRoot,state.source,state.chunks),source:state.source};
  };
  const readableSource = id => {
    const source=store.get(id);
    if(!source||source.status!=='approved')throw new EvidenceError('EVIDENCE_UNAVAILABLE','Source is unavailable or not approved',404);
    if(!source.enabled)throw new EvidenceError('SOURCE_DISABLED','Source is disabled',403);
    return source;
  };
  const patchWithEvidence = (id,patch) => {
    store.db.exec('SAVEPOINT evidence_patch');
    try {
      captureCurrent(id);const source=store.patchSource(id,patch);captureCurrent(id);
      store.db.exec('RELEASE evidence_patch');return source;
    }catch(error){store.db.exec('ROLLBACK TO evidence_patch; RELEASE evidence_patch');throw error;}
  };
  store.db.prepare("UPDATE jobs SET status='interrupted' WHERE status IN ('queued','running')").run();
  let vector={ready:false,reason:'disabled'};
  try {vector=await loadVectorRuntime(config);}catch(error){vector={ready:false,reason:error.message};}
  let queue=Promise.resolve();
  const enqueue=(kind,work,persistedJobId)=>{
    const jobId=persistedJobId||store.job(kind);
      queue=queue.then(async()=>{store.updateJob(jobId,'running',store.getJob(jobId).detail);try{const detail=await work();store.updateJob(jobId,detail?.vector==='unavailable'&&kind==='index-lesson'?'deferred':'completed',detail||{});}catch(error){store.updateJob(jobId,'failed',{...store.getJob(jobId).detail,error:error.message,...(error.sourceId?{sourceId:error.sourceId}:{})});}});
    return jobId;
  };
  async function indexSource(source) {
    if(!vector.ready)return {sourceId:source.sourceId,vector:'unavailable',reason:vector.reason};
    if(source.metadata.vectorSignature===vector.signature && source.indexStatus==='ready')return {sourceId:source.sourceId,unchanged:true};
    if(!store.indexed(source.sourceId,'indexing',undefined,source.revision))throw new Error('Source revision changed before indexing');
    try {await vector.publish(source,store.chunks(source.sourceId));if(!store.indexed(source.sourceId,'ready',vector.signature,source.revision))throw new Error('Source revision changed while indexing');return {sourceId:source.sourceId};}
    catch(error){store.indexed(source.sourceId,'failed',undefined,source.revision);error.sourceId=source.sourceId;throw error;}
  }
  const resumeLessonIndex=job=>enqueue('index-lesson',async()=>{
    const source=store.get(job.detail.sourceId);
    if(!source||source.revision!==job.detail.sourceRevision)throw new Error('Published source is missing or has another revision');
    return {...await indexSource(source),sourceRevision:source.revision};
  },job.jobId);
  // Only committed lesson jobs are restartable; import/reindex jobs keep their existing recovery policy.
  for(const row of store.db.prepare("SELECT job_id FROM jobs WHERE kind='index-lesson' AND status IN ('queued','running','interrupted','deferred','failed')").all()){
    const job=store.getJob(row.job_id);
    if(job.detail.sourceId&&job.detail.sourceRevision)resumeLessonIndex(job);
  }
  const validToken = value => typeof value==='string' && Buffer.byteLength(value)===Buffer.byteLength(token) && timingSafeEqual(Buffer.from(value),Buffer.from(token));
  const server=createServer(async(req,res)=>{
    try {
      if(!['127.0.0.1:'+port,'localhost:'+port].includes(req.headers.host))return reply(res,403,{error:'Invalid local host'});
      const origin=req.headers.origin;
      if(origin&&!allowedOrigins.has(origin))return reply(res,403,{error:'Origin denied'});
      if(origin){res.setHeader('access-control-allow-origin',origin);res.setHeader('vary','Origin');}
      if(req.method==='OPTIONS'){res.writeHead(204,{'access-control-allow-methods':'GET,POST,PATCH,DELETE,OPTIONS','access-control-allow-headers':'content-type,X-Knowledge-Token','access-control-allow-private-network':'true'});return res.end();}
      const url=new URL(req.url,baseUrl), route=decodeURIComponent(url.pathname), method=req.method;
      if(route==='/health')return reply(res,200,{ok:true,service:'easyeda-knowledge',version,instance,root:knowledgeRoot,pid:process.pid});
      if(method==='GET' && ['/', '/index.html','/app.js','/style.css','/UPSTREAM-NOTICE'].includes(route)){
        const file=path.join(uiRoot,route==='/'?'index.html':route.slice(1));
        const content=await readFile(file);
        const mime=file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html';
        res.writeHead(200,{'content-type':mime+'; charset=utf-8','cache-control':'no-store','x-content-type-options':'nosniff','content-security-policy':"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data: blob:; object-src 'none'; base-uri 'none'"});return res.end(content);
      }
      if(!validToken(req.headers['x-knowledge-token']))return reply(res,403,{error:'Local knowledge token required'});
      if(route==='/api/asset' && method==='GET'){
        const chunk=store.getChunk(url.searchParams.get('chunkId'));
        const asset=chunk&&resolveKnowledgeFile(knowledgeRoot,JSON.parse(chunk.metadata_json).assetPath);
        if(!asset)return reply(res,404,{error:'Registered image not found'});
        const relative=path.relative(path.join(knowledgeRoot,'parsed'),asset);
        const source=store.get(chunk.source_id),original=source?.metadata.parser==='image-original/v1' && resolveKnowledgeFile(knowledgeRoot,source.contentPath)===asset;
        if((!original&&(relative.startsWith('..')||path.isAbsolute(relative)))||!asset.endsWith('.png'))throw new Error('Invalid registered image');
        if(original && createHash('sha256').update(await readFile(asset)).digest('hex')!==source.sourceHash)throw new Error('Original image hash changed');
        res.writeHead(200,{'content-type':'image/png','cache-control':'no-store','x-content-type-options':'nosniff'});return res.end(await readFile(asset));
      }
      if(route==='/api/status' && method==='GET'){
        const rows=store.list({limit:10000});
        return reply(res,200,{service:'easyeda-knowledge',version,sourceCount:rows.length,chunkCount:rows.reduce((sum,row)=>sum+row.chunkCount,0),indexStatus:vector.ready?'本地向量与精确检索':'精确检索（向量未就绪）',vector:{ready:vector.ready,model:vector.model,revision:vector.revision,signature:vector.signature,reason:vector.reason},jobs:store.jobs().slice(0,5)});
      }
      if(route==='/api/sources' && method==='GET'){
        const filters={...(url.searchParams.has('filters')?parseJson(url.searchParams.get('filters')):{}),...Object.fromEntries([...url.searchParams].filter(([key])=>key!=='filters'))};
        const all=store.list({...filters,limit:10000,offset:0,cursor:0}),offset=Math.max(0,Number(filters.cursor||filters.offset)||0),limit=Math.min(200,Math.max(1,Number(filters.limit)||100));
        return reply(res,200,{items:all.slice(offset,offset+limit),total:all.length,nextCursor:offset+limit<all.length?String(offset+limit):null});
      }
      if(route==='/api/sources' && method==='POST'){
        const input=await body(req), metadata=input.metadata||{};
        checkMetadata(metadata);
        if(metadata.type==='lesson')throw new Error('Lessons enter through candidate review');
        const jobId=enqueue('import',async()=>{
          const parsed=await ingest({...input,root:knowledgeRoot});
          if(parsed.parseStatus==='failed')throw new Error(parsed.issue||'Parsing failed');
          const duplicate=store.findImport(parsed.name,parsed.sourceHash);
          const prior=input.sourceId?store.get(input.sourceId):duplicate;
          if(input.sourceId&&!prior)throw new Error('Unknown sourceId');
          if(input.sourceId&&duplicate&&duplicate.sourceId!==input.sourceId)throw new Error('Import conflicts with existing source '+duplicate.sourceId+'; existing sources were not merged or changed');
          if(prior?.type==='lesson')throw new Error('Lessons enter through candidate review');
          if(prior?.sourceHash===parsed.sourceHash){const source=patchWithEvidence(prior.sourceId,metadata);return {...await indexSource(source),sourceId:source.sourceId,unchanged:true,parseStatus:source.parseStatus,issues:source.metadata.parseIssues||[]};}
          if(prior)captureCurrent(prior.sourceId);
          const source=store.publish({sourceId:prior?.sourceId,name:parsed.name,title:metadata.title??prior?.title??parsed.name,type:metadata.type??prior?.type??'reference',domain:metadata.domain??prior?.domain,status:'approved',enabled:metadata.enabled??prior?.enabled??true,metadata:{...prior?.metadata,...metadata,parser:parsed.parser,qualityStatus:'unreviewed',parseIssues:parsed.issues||[],parsedPath:parsed.parsedPath},sourceHash:parsed.sourceHash,contentPath:parsed.contentPath,parseStatus:parsed.parseStatus,indexStatus:'keyword_ready'},parsed.chunks,published=>captureCurrent(published.sourceId));
          return {...await indexSource(source),parseStatus:parsed.parseStatus,issues:parsed.issues||[]};
        });
        return reply(res,202,{jobId});
      }
      const sourceMatch=route.match(/^\/api\/sources\/([^/]+)$/);
      if(sourceMatch){
        const source=store.get(sourceMatch[1]);if(!source)return reply(res,404,{error:'Source not found'});
        if(method==='GET')return reply(res,200,{source});
        if(method==='PATCH')return reply(res,200,{source:patchWithEvidence(source.sourceId,await body(req))});
        if(method==='DELETE'){store.deleteSource(source.sourceId);if(vector.ready)enqueue('delete-index',()=>vector.remove(source.sourceId));return reply(res,200,{ok:true});}
      }
      if(route==='/api/search' && method==='POST'){
        const input=await body(req), query=String(input.query||'').trim(),filters=input.filters||{}, topK=Math.min(20,Math.max(1,Number(input.topK)||8));
        if(!query||query.length>2000)throw new Error('Query must contain 1–2000 characters');
        const expansion=Object.entries(config.queryAliases||{}).filter(([term])=>query.toLowerCase().includes(term.toLowerCase())).flatMap(([,aliases])=>Array.isArray(aliases)?aliases.filter(value=>typeof value==='string'):[]).slice(0,32);
        const searchQuery=[query,...expansion].join(' ');
        const lexical=store.search(searchQuery,filters,50,query), fused=new Map();
        // Exact lexical evidence gets more weight than approximate semantic recall.
        lexical.forEach((item,i)=>fused.set(item.chunkId,{...item,score:2/(60+i+1),retrieval:{keywordRank:i+1}}));
        const eligible=store.list({enabled:true,status:'approved',limit:10000}).filter(source=>matchesFilters(source,filters,query)&&source.metadata.vectorSignature===vector.signature&&source.indexStatus==='ready');
        let vectorError;
        if(vector.ready&&eligible.length)try{
          const candidates=await vector.search(searchQuery,eligible.map(source=>source.sourceId),Math.max(20,topK*3));
          candidates.forEach((hit,i)=>{
            const source=store.get(hit.sourceId),chunk=store.getChunk(hit.chunkId);
            if(!source||!chunk||!source.enabled||source.revision!==hit.revision||!isEvidence(chunk.text,JSON.parse(chunk.metadata_json),searchQuery))return;
            const old=fused.get(hit.chunkId),row={chunkId:hit.chunkId,sourceId:hit.sourceId,title:source.title,type:source.type,domain:source.domain,text:chunk.text,anchor:chunk.anchor,metadata:{...source.metadata,...JSON.parse(chunk.metadata_json)},revision:source.revision,sourceHash:source.sourceHash};
            fused.set(hit.chunkId,{...row,score:(old?.score||0)+1/(60+i+1),match:old?.match==='strict_identity'?'strict_identity':old?'hybrid':'semantic',exactMatches:old?.exactMatches||[],retrieval:{...old?.retrieval,vectorRank:i+1}});
          });
        }catch(error){vectorError=error.message;}
        const captured=new Map();
        const items=[];
        for(const item of [...fused.values()].sort((a,b)=>b.score-a.score)){
          const current=store.get(item.sourceId);
          if(!current||!current.enabled||current.status!=='approved'||!matchesFilters(current,filters,query)||current.revision!==item.revision)continue;
          let state=captured.get(item.sourceId);
          if(!state){state=captureCurrent(item.sourceId);captured.set(item.sourceId,state);}
          const {source,snapshot}=state,record=snapshot.records.find(row=>row.chunkId===item.chunkId);
          if(!record)continue;
          const selector={chunkId:record.chunkId,anchor:record.anchor};
          const content={text:record.text.slice(0,1800),readScope:'excerpt',truncated:record.text.length>1800,nextCursor:null,contextCompleteness:'partial'};
          const context=contextFor(snapshot,source,selector,content);
          const matchReasons=[...(item.exactMatches?.length?['来源精确字段：'+item.exactMatches.join(', ')]:[]),...(item.retrieval.keywordRank?['关键词召回第 '+item.retrieval.keywordRank+' 位']:[]),...(item.retrieval.vectorRank?['语义召回第 '+item.retrieval.vectorRank+' 位']:[])];
          const metadata={...snapshot.source.metadata,...record.metadata},metadataOmitted=[];
          // Full card bodies remain in the pinned snapshot; search carries applicability and verification.
          for(const key of ['problem','cause','solution','evidence','sourceVersionSet','review','artifacts'])if(Object.hasOwn(metadata,key)){delete metadata[key];metadataOmitted.push(key);}
          items.push({...item,title:source.title,type:source.type,domain:source.domain,metadata,metadataOmitted,text:content.text,truncated:content.truncated,context,evidenceRef:context.evidenceRef,matchReasons,citation:citationFor(source,{anchor:record.anchor,metadata:record.metadata}),engineeringRefs:context.engineeringRefs});
          if(items.length===topK)break;
        }
        return reply(res,200,{schemaVersion:1,items,retrieval:{mode:vector.ready&&!vectorError?'hybrid':'keyword',degradedReason:vectorError||(!vector.ready?vector.reason||'Vector unavailable':null)},scoreKind:'rrf_rank_score_not_confidence',fusion:{method:'weighted_rrf',keywordWeight:2,vectorWeight:1,k:60},...(expansion.length?{queryExpansion:expansion}:{}),vector:vector.ready?'ready':'unavailable',...(vectorError?{vectorError}:{})});
      }
      if(route==='/api/get' && method==='GET'){
        const visualFlag=url.searchParams.get('includeVisuals');
        if(visualFlag!==null&&!['true','false'].includes(visualFlag))throw new EvidenceError('EVIDENCE_INVALID','includeVisuals must be boolean');
        const assetIds=url.searchParams.has('assetIds')?JSON.parse(url.searchParams.get('assetIds')):undefined;
        if(assetIds!==undefined&&(!Array.isArray(assetIds)||assetIds.length<1||assetIds.length>3||assetIds.some(id=>typeof id!=='string'||!id||id.length>300)||new Set(assetIds).size!==assetIds.length))throw new EvidenceError('EVIDENCE_INVALID','assetIds must contain 1–3 distinct registered IDs');
        if(assetIds&&visualFlag==='false')throw new EvidenceError('EVIDENCE_INVALID','assetIds cannot be combined with includeVisuals:false');
        const hasRef=url.searchParams.has('evidenceRef'),id=url.searchParams.get('id'),anchor=url.searchParams.get('anchor');
        if(hasRef&&(id!==null||anchor!==null))throw new EvidenceError('EVIDENCE_INVALID','Do not mix an evidence reference with a current ID or anchor');
        const max=url.searchParams.has('maxChars')?Number(url.searchParams.get('maxChars')):12000;
        if(!Number.isInteger(max)||max<200||max>30000)throw new EvidenceError('EVIDENCE_INVALID','maxChars must be between 200 and 30000');
        if(url.searchParams.has('limit')){const limit=Number(url.searchParams.get('limit'));if(!Number.isInteger(limit)||limit<1||limit>100)throw new EvidenceError('EVIDENCE_INVALID','Invalid legacy limit');}
        const cursor=url.searchParams.get('cursor'),scope=url.searchParams.get('scope')||undefined;
        if(cursor!==null&&(!cursor.length||cursor.length>500))throw new EvidenceError('CURSOR_MISMATCH','Invalid cursor length');
        let snapshot,ref,source;
        if(hasRef){
          ref=parseJson(url.searchParams.get('evidenceRef'));snapshot=loadEvidence(knowledgeRoot,ref);source=readableSource(ref.sourceId);
        }else{
          if(!id?.trim())throw new EvidenceError('EVIDENCE_INVALID','An evidence reference or current ID is required');
          const chunk=store.getChunk(id);source=readableSource(chunk?.source_id||id);
          snapshot=captureCurrent(source.sourceId).snapshot;
          ref=referenceFor(snapshot,{chunkId:chunk?.chunk_id||null,anchor:chunk?.anchor||anchor||null});
        }
        const legacyOffset=!hasRef&&cursor!==null&&/^\d+$/.test(cursor)?Number(cursor):undefined;
        const {context,records}=readEvidence(snapshot,ref,{scope,cursor:legacyOffset===undefined?cursor:null,legacyOffset,maxChars:max,limit:url.searchParams.has('limit')?Number(url.searchParams.get('limit')):undefined},source);
        const frozen=snapshot.source;
        const alias={...frozen,revision:frozen.sourceRevision,type:frozen.sourceType,metadata:frozen.metadata};
        const visuals=visualFlag==='true'||assetIds?readVisuals(knowledgeRoot,snapshot,records,{pinned:hasRef,assetIds}):undefined;
        return reply(res,200,{schemaVersion:1,context,readMode:hasRef?'pinned':'current_unpinned',source:alias,runtime:{indexStatus:context.states.index,enabled:source.enabled},evidenceRef:context.evidenceRef,citation:citationFor(alias,{anchor:ref.selector.anchor,metadata:{locations:records.flatMap(item=>item.locations||[])}}),engineeringRefs:context.engineeringRefs,text:context.content.text,truncated:context.content.truncated,nextCursor:context.content.nextCursor,locations:records.slice(0,100).map(item=>({chunkId:item.chunkId,anchor:item.anchor,...item.metadata})),...(visuals?{visuals}:{})});
      }
      if(route==='/api/reindex' && method==='POST'){
        const input=await body(req);
        const jobId=enqueue('reindex',async()=>{const sources=store.list({enabled:true,status:'approved',limit:10000}).filter(source=>!input.sourceId||source.sourceId===input.sourceId);const items=[];for(const source of sources)items.push(await indexSource(source));return {items};});
        return reply(res,202,{jobId});
      }
      if(route==='/api/jobs' && method==='GET')return reply(res,200,{items:store.jobs()});
      const jobMatch=route.match(/^\/api\/jobs\/([^/]+)$/);
      if(jobMatch && method==='GET'){
        const job=store.getJob(jobMatch[1]);
        if(!job)return reply(res,404,{error:'Import job not found'});
        const source=job.detail.sourceId?store.get(job.detail.sourceId):undefined;
        return reply(res,200,{job,...(source?{source}:{})});
      }
      if(route==='/api/candidates' && method==='GET')return reply(res,200,{items:store.candidates(url.searchParams.get('status')||undefined)});
      if(route==='/api/candidates' && method==='POST'){
        const input=await body(req);
        if(JSON.stringify(input).length>30000)throw new Error('Candidate too large');
        const {status,review,reviewer,submissionId,candidateId,expectedCandidateRevision,...payload}=input;
        checkCandidate(payload);
        return reply(res,candidateId?200:201,store.submitCandidate({...payload,validationKind:'proposer-claim'},{submissionId,candidateId,expectedCandidateRevision}));
      }
      const reviewMatch=route.match(/^\/api\/candidates\/([^/]+)\/review$/);
      if(reviewMatch && method==='POST'){
        // Review is a browser action; MCP exposes only candidate submission.
        if(!origin)throw new Error('Review requires the local management UI');
        const input=await body(req);
        if(Object.keys(input).some(key=>!['decision','expectedCandidateRevision'].includes(key)))throw new Error('Unknown review field');
        if(!Number.isSafeInteger(input.expectedCandidateRevision)||input.expectedCandidateRevision<1)throw new EvidenceError('REVIEW_VERSION_REQUIRED','Refresh the management UI before reviewing');
        if(!['approved','rejected'].includes(input.decision))throw new Error('decision must be approved or rejected');
        const receipt=store.reviewReceipt(reviewMatch[1],input.expectedCandidateRevision,input.decision);
        if(receipt)return reply(res,200,receipt);
        if(input.decision==='rejected'){
          store.db.exec('BEGIN IMMEDIATE');
          try{
            const result={candidate:store.reviewCandidate(reviewMatch[1],'rejected','local-user',input.expectedCandidateRevision),replayed:false};
            store.saveReviewReceipt(reviewMatch[1],input.expectedCandidateRevision,'rejected',result);
            store.db.exec('COMMIT');return reply(res,200,result);
          }catch(error){store.db.exec('ROLLBACK');throw error;}
        }
        const pending=store.candidate(reviewMatch[1]);
        if(!pending||pending.status!=='pending'||pending.revision!==input.expectedCandidateRevision)throw Object.assign(new EvidenceError('REVIEW_STALE','Candidate changed or was already reviewed; refresh and review again',409),{currentCandidate:pending??null});
        const p=pending.payload;checkCandidate(p);store.checkCandidateSources(pending);
        const text=knowledgeCard({...p,sourceVersionSet:pending.sourceVersions}),lessonPath=path.join(knowledgeRoot,'lessons',pending.candidateId+'-'+randomBytes(8).toString('hex')+'.md');
        await mkdir(path.dirname(lessonPath),{recursive:true});await writeFile(lessonPath,text);
        const hash=createHash('sha256').update(text).digest('hex');let candidate,source,result;
        // Publish and approve together: a failed write must leave the candidate retryable.
        store.db.exec('BEGIN IMMEDIATE');
        try {
          const concurrentReceipt=store.reviewReceipt(pending.candidateId,input.expectedCandidateRevision,'approved');
          if(concurrentReceipt)result=concurrentReceipt;
          else {
            const latest=store.candidate(pending.candidateId);
            if(latest?.revision!==input.expectedCandidateRevision||latest.status!=='pending')throw new EvidenceError('REVIEW_STALE','Candidate changed; refresh and review again',409);
            store.checkCandidateSources(latest);
            candidate=store.reviewCandidate(pending.candidateId,'approved','local-user',input.expectedCandidateRevision);
            source=store.saveSource({name:path.basename(lessonPath),title:p.title||p.problem,type:'lesson',domain:p.domain||'both',status:'approved',metadata:{...p,sourceVersionSet:pending.sourceVersions,schemaVersion:1,parser:'knowledge-card/v1',candidateId:candidate.candidateId,review:candidate.review},sourceHash:hash,contentPath:lessonPath});
            store.replaceChunks(source,chunkText(text).map(item=>({...item,anchor:'#/lesson'+item.anchor.slice(1)})));
            captureCurrent(source.sourceId);
            const jobId=store.job('index-lesson','queued',{sourceId:source.sourceId,sourceRevision:source.revision});
            result={candidate,sourceId:source.sourceId,jobId,replayed:false};
            store.saveReviewReceipt(pending.candidateId,input.expectedCandidateRevision,'approved',result);
          }
          store.db.exec('COMMIT');
        }catch(error){store.db.exec('ROLLBACK');try{await unlink(lessonPath);}catch(cleanupError){if(cleanupError.code!=='ENOENT')error.message+='; failed to remove unpublished card: '+cleanupError.message;}throw error;}
        if(result.replayed)await unlink(lessonPath);else resumeLessonIndex(store.getJob(result.jobId));
        return reply(res,200,result);
      }
      return reply(res,404,{error:'Not found'});
    }catch(error){reply(res,error.status||400,{error:error.message,...(error.code?{code:error.code}:{}),...(error.currentCandidate!==undefined?{currentCandidate:error.currentCandidate}:{})});}
  });
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,'127.0.0.1',resolve);});
  const descriptor={service:'easyeda-knowledge',version,instance,baseUrl,token,pid:process.pid,root:knowledgeRoot,startedAt:new Date().toISOString()};
  await writeFile(runtimeFile+'.tmp',JSON.stringify(descriptor,null,2),{mode:0o600});await rename(runtimeFile+'.tmp',runtimeFile);
  return {server,store,token,baseUrl,vector,drain:()=>queue};
}
if(process.argv[1]===fileURLToPath(import.meta.url))startKnowledgeService().then(({baseUrl})=>console.log('Knowledge service '+baseUrl)).catch(error=>{console.error(error.stack||error);process.exitCode=1;});

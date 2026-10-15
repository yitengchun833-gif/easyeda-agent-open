/*
 * Adapted from eext-knowledge-base iframe/src/main.ts document-tree and
 * preview patterns, commit 39acb6a4dc9e04d9216a1bcb0d2218ff60cb7a11
 * (Apache-2.0). It intentionally exposes management/retrieval only.
 */
(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const state = { config: null, items: [], selected: null, pending: [], preview: 0 };
  const list = (value) => String(value || '').split(',').map(x => x.trim()).filter(Boolean);
  const safeText = (value) => String(value ?? '');

  function toast(message) {
    const el = $('toast'); el.textContent = message; el.classList.add('show');
    clearTimeout(toast.timer); toast.timer = setTimeout(() => el.classList.remove('show'), 2600);
  }
  function errorMessage(error) { return error instanceof Error ? error.message : String(error); }

  async function getConfig() {
    const fragment = new URLSearchParams(location.hash.slice(1));
    const urlBase = fragment.get('baseUrl');
    const urlToken = fragment.get('token') || sessionStorage.getItem('knowledgeToken');
    if (fragment.has('token')) { sessionStorage.setItem('knowledgeToken',urlToken); history.replaceState(null,'',location.pathname); }
    let stored = {};
    try {
      if (typeof eda !== 'undefined' && eda.sys_Storage) {
        stored = await Promise.resolve(eda.sys_Storage.getExtensionUserConfig('knowledgeService')) || {};
      }
    } catch (_) { /* browser entry keeps URL configuration */ }
    const baseUrl = String(urlBase || stored.baseUrl || (location.protocol.startsWith('http') ? location.origin : 'http://127.0.0.1:60933')).replace(/\/$/, '');
    return { baseUrl, token: String(urlToken || stored.token || '') };
  }
  async function api(path, init = {}) {
    const headers = new Headers(init.headers || {});
    if (state.config.token) headers.set('X-Knowledge-Token', state.config.token);
    if (init.body) headers.set('Content-Type', 'application/json');
    const response = await fetch(`${state.config.baseUrl}${path}`, { ...init, headers });
    const contentType = response.headers.get('content-type') || '';
    const data = contentType.includes('application/json') ? await response.json() : await response.text();
    if (!response.ok) throw Object.assign(new Error(typeof data === 'object' ? data.error || data.message || `请求失败 (${response.status})` : data || `请求失败 (${response.status})`),{code:data?.code});
    return data;
  }
  function statusClass(source) {
    const text = source.indexStatus === 'ready' ? 'ready' : source.indexStatus === 'failed' || source.parseStatus === 'failed' ? 'failed' : '';
    const label = source.indexStatus === 'ready' ? '可检索' : source.indexStatus === 'failed' ? '索引失败' : source.parseStatus === 'partial' ? '解析部分完成' : source.parseStatus === 'failed' ? '解析失败' : source.indexStatus === 'keyword_ready' ? '关键词可检索' : '正在索引';
    return { text, label };
  }
  function normalizeSource(source) {
    const metadata = source.metadata || {};
    return { ...metadata, ...source, metadata, models: source.models || metadata.models || [], pins: source.pins || metadata.pins || [], tags: source.tags || metadata.tags || [] };
  }
  function normalizeCandidate(candidate) { return { ...(candidate.payload || {}), ...candidate }; }
  function make(tag, className, text) { const el = document.createElement(tag); if (className) el.className = className; if (text !== undefined) el.textContent = text; return el; }
  const labels = { source:'资料', experience:'经验', artifact:'历史成果', datasheet:'数据手册', reference:'参考设计', company_rule:'规则', lesson:'经验', schematic:'原理图', pcb:'PCB', both:'原理图与PCB', approved:'允许检索', pending:'待审核', rejected:'已拒绝', success:'解析成功', partial:'部分', failed:'失败', ready:'向量就绪', keyword_ready:'关键词就绪', unknown:'未知/未验证', pass:'通过（限声明范围）', fail:'未通过', accepted:'已接受', 'proposer-claim':'提交者声明', 'source-record':'来源记录', chunk:'命中片段', section:'相关章节', excerpt:'检索节选', complete:'所选范围完整', confirmed:'关联已确认', unconfirmed:'关联未确认', not_applicable:'未扩展关联', docling_structure:'已冻结解析结构', markdown_headings:'已冻结标题范围', same_page:'有限同页内容', neighbors:'有限邻近内容', exact:'精确片段' };
  const label = value => labels[value] || value || '未记录';
  const display = value => Array.isArray(value) ? value.map(item => typeof item==='object' ? `${label(item.kind)}：${item.id}${item.version?' · '+item.version:''}` : String(item)).join('\n') || '未记录' : typeof value==='object'&&value ? JSON.stringify(value,null,2) : value || '未记录';
  function fields(root, entries) {
    const dl=make('dl','evidence-fields');
    for(const [name,value] of entries)dl.append(make('dt','',name),make('dd','',display(value)));
    root.append(dl);
  }
  function validationFields(value, origin) {
    const v=typeof value==='object'&&value ? value : {};
    return [['验证来源',label(origin||'proposer-claim')],['连通性',label(v.connectivity||'unknown')],['布局',label(v.layout||'unknown')],['实测',label(v.measurement||'unknown')],['工程师评审',label(v.engineerReview||'unknown')],['实际验证范围',v.scope],['验证说明',typeof value==='string'?value:v.note]];
  }
  function renderContext(root, context, readMode) {
    if(context?.schemaVersion!==1||!context.evidenceRef||!context.content)throw Error('服务未返回完整证据，请刷新后重试');
    root.replaceChildren();root.dataset.evidenceId=context.evidenceRef.evidenceId;
    const {identity,scope,applicability,verification,states,provenance,content,evidenceRef}=context;
    fields(root,[['知识类别',label(context.kind)],['类型 / 领域',`${label(context.sourceType)} / ${label(context.domain)}`],['型号 / 厂商 / 封装',[...identity.models,identity.manufacturer,identity.package].filter(Boolean)],['项目 / 模块',[scope.project,scope.module].filter(Boolean)],['资料版本',scope.documentVersion],['适用条件',applicability.conditions],['不适用条件',applicability.exclusions],['审核 / 解析 / 索引',[label(states.review),label(states.parse),label(states.index)]],['读取范围',content.readScope==='source'?'整份来源 / 知识卡':label(content.readScope)],['上下文完整度',label(content.contextCompleteness)],['关联依据 / 状态',[label(content.contextExpansion?.basis),label(content.contextExpansion?.relationStatus)]]]);
    const verified=make('details');verified.append(make('summary','','验证范围与成果入口'));
    fields(verified,[...validationFields(verification,verification.claimOrigin),['成果入口',context.artifacts],['工程关联',context.engineeringRefs]]);root.append(verified);
    const provenanceDetails=make('details');provenanceDetails.append(make('summary','','本次读取的原文位置'));
    fields(provenanceDetails,[['原文文件 / 解析器',[provenance.file,provenance.parser].filter(Boolean)],['读取方式',readMode==='current_unpinned'?'当前版定位，后续按此证据固定续读':'固定证据'],['原文位置',provenance.locations.map(loc=>`${loc.anchor||'未记录位置'}${loc.pages.length?' · 第 '+loc.pages.join(', ')+' 页':''}`)]]);root.append(provenanceDetails);
    for(const warning of content.contextExpansion?.warnings||[])root.append(make('p','context-warning',warning));
  }

  // Source tree is intentionally based on the upstream renderNode/renderDocList
  // pattern, with service-backed document records rather than browser state.
  function renderSourceTree() {
    const tree = $('source-tree'); tree.replaceChildren();
    const byType = new Map();
    for (const source of state.items) { const type = source.type || 'reference'; if (!byType.has(type)) byType.set(type, []); byType.get(type).push(source); }
    const names = { datasheet: '数据手册', reference: '参考设计', company_rule: '原理图/PCB规则', lesson: '已审核经验' };
    for (const [type, sources] of byType) {
      const group = make('section', 'source-group'); const header = make('div', 'group-header');
      header.append(make('span', '', '▾'), make('strong', '', names[type] || type), make('span', 'group-count', String(sources.length)));
      const children = make('div'); header.addEventListener('click', () => { children.hidden = !children.hidden; header.firstChild.textContent = children.hidden ? '▸' : '▾'; });
      for (const source of sources) {
        const node = make('button', `source-node${state.selected?.sourceId === source.sourceId ? ' selected' : ''}`); node.type = 'button';
        node.append(make('span', '', '📄'));
        const copy = make('span'); copy.append(make('span', '', source.title || source.name || source.sourceId), make('small', '', `${(source.models || []).join(', ') || source.version || '未标记型号'} · ${statusClass(source).label}`)); node.append(copy);
        node.addEventListener('click', () => previewSource(source)); children.append(node);
      }
      group.append(header, children); tree.append(group);
    }
    if (!state.items.length) tree.append(make('p', 'empty', '没有匹配的资料'));
    $('source-count').textContent = String(state.items.length);
  }
  function renderSourcesTable() {
    const body = $('sources-body'); body.replaceChildren();
    for (const source of state.items) {
      const row = document.createElement('tr'); const st = statusClass(source);
      const title = document.createElement('td'); title.append(make('strong', '', source.title || source.name || source.sourceId), make('div', 'sub', source.source || source.sourceId));
      const type = document.createElement('td'); type.append(make('div', '', source.type || 'reference'), make('div', 'sub', source.domain || '未设置范围'));
      const model = document.createElement('td'); model.append(make('div', '', (source.models || []).join(', ') || '—'), make('div', 'sub', source.version || '未标记版本'));
      const stat = document.createElement('td'); stat.append(make('span', `state ${st.text}`, st.label), make('div', 'sub', `${source.chunkCount ?? 0} chunks`));
      const actions = document.createElement('td'); const group = make('div', 'actions');
      const preview = make('button', 'secondary', '预览'); preview.type = 'button'; preview.addEventListener('click', () => previewSource(source));
      const edit = make('button', 'secondary', '编辑'); edit.type = 'button'; edit.addEventListener('click', () => openMetadata(source));
      const toggle = make('button', 'secondary', source.enabled === false ? '启用' : '停用'); toggle.type = 'button'; toggle.addEventListener('click', () => patchSource(source.sourceId, { enabled: source.enabled === false }));
      const remove = make('button', 'secondary danger', '删除'); remove.type = 'button'; remove.addEventListener('click', () => deleteSource(source));
      group.append(preview, edit, toggle, remove); actions.append(group); row.append(title, type, model, stat, actions); body.append(row);
    }
    if (!state.items.length) { const row = document.createElement('tr'); const cell = make('td', 'empty', '尚未导入资料'); cell.colSpan = 5; row.append(cell); body.append(row); }
  }
  function renderCandidates() {
    const root = $('candidate-list'); root.replaceChildren();
    for (const candidate of state.pending) {
      const card = make('article', 'candidate'); const header = document.createElement('header'); header.append(make('h3', '', candidate.title || candidate.problem || '未命名经验候选'), make('span', 'state', candidate.status || 'pending')); card.append(header);
      fields(card,[['问题',candidate.problem],['原因（含提交者推测）',candidate.cause],['处理方式',candidate.solution],['原始证据',candidate.evidence],['适用条件',candidate.conditions],['不适用条件',candidate.notApplicable],['项目 / 模块',[candidate.project,candidate.module].filter(Boolean)],['领域 / 型号',[label(candidate.domain),...(candidate.models||[])]],['版本 / 任务',[candidate.version,candidate.taskId].filter(Boolean)],...validationFields(candidate.validation,candidate.validationKind),['成果入口',candidate.artifacts],['工程关联',candidate.engineeringRefs],['来源ID',candidate.sourceIds],['标签',candidate.tags]]);
      card.append(make('p','hint','批准表示允许检索；上述验证为提交者声明，未验证项保持未知。'));
      fields(card,[['引用资料版本',candidate.sourceVersions?.map(item=>`${item.sourceId} · ${item.sourceRevision} · ${item.sourceHash}`)|| (candidate.sourceIds?.length?'旧候选未绑定版本，需要重新读取资料并修订':'未引用知识库资料')]]);
      const actions = make('div', 'actions'); const approve = make('button', '', '批准加入知识库'); const reject = make('button', 'secondary danger', '拒绝');
      approve.addEventListener('click', () => review(candidate, 'approved',actions)); reject.addEventListener('click', () => review(candidate, 'rejected',actions)); actions.append(approve, reject); card.append(actions); root.append(card);
    }
    if (!state.pending.length) root.append(make('p', 'empty', '没有待审核经验'));
    $('pending-count').hidden = !state.pending.length; $('pending-count').textContent = String(state.pending.length);
  }
  function metadataFromForm() { return { title: $('meta-title').value.trim(), type: $('meta-type').value, domain: $('meta-domain').value, tags: list($('meta-tags').value), version: $('meta-version').value.trim(), source: $('meta-source').value.trim(), models: list($('meta-models').value), pins: list($('meta-pins').value), projectScope: $('meta-scope').value.trim(), project: $('meta-project').value.trim(), module: $('meta-module').value.trim() }; }
  function openMetadata(source, file) {
    const form = $('metadata-form'); form.reset(); $('editing-id').value = source?.sourceId || ''; $('metadata-title').textContent = source ? '编辑资料元数据' : `导入资料：${file?.name || ''}`;
    const data = source || { title: file?.name.replace(/\.[^.]+$/, '') || '' };
    $('meta-title').value = data.title || data.name || ''; $('meta-type').value = data.type || 'datasheet'; $('meta-domain').value = data.domain || 'both'; $('meta-tags').value = (data.tags || []).join(', '); $('meta-version').value = data.version || ''; $('meta-source').value = data.source || ''; $('meta-models').value = (data.models || []).join(', '); $('meta-pins').value = (data.pins || []).join(', '); $('meta-scope').value = data.projectScope || '';
    $('meta-project').value=data.project||'';$('meta-module').value=data.module||'';
    $('meta-type').querySelector('option[value="lesson"]').disabled=data.type!=='lesson';
    form.dataset.fileName = file?.name || ''; form.dataset.fileBase64 = file?.base64 || ''; $('metadata-dialog').showModal();
  }
  async function previewSource(source, evidenceRef) {
    const session=++state.preview;
    state.selected=source;renderSourceTree();$('preview-title').textContent='读取证据…';$('preview-meta').replaceChildren();$('preview-context').replaceChildren();delete $('preview-context').dataset.evidenceId;$('preview-assets').replaceChildren();$('preview-more').hidden=true;$('preview-reference').textContent='';$('preview-progress').textContent='';
    $('preview-body').textContent='读取原文…';if(!$('preview-modal').open)$('preview-modal').showModal();
    try {
      const options={maxChars:12000,limit:100,...(evidenceRef?{evidenceRef:JSON.stringify(evidenceRef),scope:source.kind==='experience'?'source':'section'}:{id:source.sourceId||source.id,scope:'source'})};
      const data=await api('/api/get?'+new URLSearchParams(options));if(session!==state.preview)return;
      const context=data.context;renderContext($('preview-context'),context,data.readMode);$('preview-title').textContent=context.title;
      $('preview-meta').append(make('span','',label(context.kind)),make('span','',context.scope.documentVersion||'未记录版本'));
      $('preview-body').textContent=context.content.text||'暂无文本';
      $('preview-reference').textContent=JSON.stringify(context.evidenceRef,null,2);
      const pinned={...options,evidenceRef:JSON.stringify(context.evidenceRef)};delete pinned.id;
      let cursor=context.content.nextCursor;const more=$('preview-more');more.hidden=!cursor;more.disabled=false;
      const assets=new Set();
      function addAssets(packet) {for(const loc of (packet.locations || []).filter(x=>x.assetPath&&!assets.has(x.chunkId))){
        assets.add(loc.chunkId);
        const details=make('details','preview-extra');details.append(make('summary','',`第 ${(loc.locations||[]).map(x=>x.page_no).join(',')} 页 · ${loc.label} · ${loc.anchor}`));
        details.append(make('p','hint','原图按当前登记位置读取，图像字节未冻结；历史版本须另核对原件。'));
        details.addEventListener('toggle',async()=>{if(!details.open || details.dataset.loaded)return;
          try{const response=await fetch(state.config.baseUrl+'/api/asset?chunkId='+encodeURIComponent(loc.chunkId),{headers:{'X-Knowledge-Token':state.config.token}});if(!response.ok)throw Error('图像读取失败');
            const image=make('img','source-image');image.alt=loc.anchor;const objectUrl=URL.createObjectURL(await response.blob());image.src=objectUrl;image.onload=()=>URL.revokeObjectURL(objectUrl);details.append(image);details.dataset.loaded='1';
          }catch(err){toast(errorMessage(err));}});$('preview-assets').append(details);
      }}
      addAssets(data);
      more.onclick=async()=>{if(more.disabled||!cursor)return;more.disabled=true;
        try{const next=await api('/api/get?'+new URLSearchParams({...pinned,cursor}));if(session!==state.preview)return;
          const a=next.context.evidenceRef,b=context.evidenceRef;
          if(['schemaVersion','evidenceId','sourceId','sourceRevision','sourceHash','snapshotHash'].some(key=>a?.[key]!==b[key])||a?.selector?.chunkId!==b.selector.chunkId||a?.selector?.anchor!==b.selector.anchor)throw Error('证据版本发生变化，停止续读');
          renderContext($('preview-context'),next.context,next.readMode);
          $('preview-body').textContent+=next.context.content.text;cursor=next.context.content.nextCursor;more.hidden=!cursor;addAssets(next);
          $('preview-progress').textContent=cursor?'已继续读取，仍有后文':'所选范围已读尽；条件与验证状态仍需核对';
        }catch(err){toast(errorMessage(err));}finally{if(session===state.preview)more.disabled=false;}
      };
      $('preview-progress').textContent=cursor?'正文较长，可继续读取同一份证据':'所选范围已读尽';
    }
    catch (err) { if(session===state.preview)$('preview-body').textContent = `读取失败：${errorMessage(err)}`; }
  }
  async function patchSource(id, patch) { try { await api(`/api/sources/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(patch) }); toast('资料已更新'); await refresh(); } catch (err) { toast(`更新失败：${errorMessage(err)}`); } }
  async function deleteSource(source) { if (!confirm(`删除“${source.title || source.sourceId}”吗？此操作会从知识库移除该资料。`)) return; try { await api(`/api/sources/${encodeURIComponent(source.sourceId)}`, { method: 'DELETE' }); toast('资料已删除'); await refresh(); } catch (err) { toast(`删除失败：${errorMessage(err)}`); } }
  async function review(candidate, decision, actions) {
    const id=candidate.id||candidate.candidateId;
    if(!id)return toast('候选记录缺少 ID');const buttons=[...actions.querySelectorAll('button')];if(buttons.some(button=>button.disabled))return;
    buttons.forEach(button=>button.disabled=true);
    try{await api(`/api/candidates/${encodeURIComponent(id)}/review`,{method:'POST',body:JSON.stringify({decision,expectedCandidateRevision:candidate.revision})});toast(decision==='approved'?'已批准并加入知识库':'已拒绝候选');await refresh();}
    catch(err){const sourceError=['REVIEW_SOURCES_STALE','REVIEW_SOURCES_UNPINNED','SOURCE_VERSION_STALE','SOURCE_UNAVAILABLE'].includes(err.code);toast(sourceError?'引用资料已变化或缺少版本，请让 Agent 重新读取并修订候选后再审核':err.code==='REVIEW_STALE'?'候选已经变化，已刷新，请重新审阅后再决定':`审核未确认：${errorMessage(err)}；刷新查看状态，仍待审核时可重试`);await refresh();}finally{buttons.forEach(button=>button.disabled=false);}
  }
  async function toBase64(file) { const bytes = new Uint8Array(await file.arrayBuffer()); let binary = ''; for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000)); return btoa(binary); }
  async function importFiles(files) { for (const file of files) { if (!/\.(md|markdown|txt|pdf|png)$/i.test(file.name)) { toast(`${file.name} 不是支持的资料类型`); continue; } openMetadata(undefined, { name: file.name, base64: await toBase64(file) }); await new Promise(resolve => $('metadata-dialog').addEventListener('close', resolve, { once: true })); } }
  async function refresh() {
    try {
      const q = $('source-filter').value.trim(); const params = new URLSearchParams(); for (const [key, value] of [['type', $('type-filter').value], ['domain', $('domain-filter').value], ['q', q]]) if (value) params.set(key, value);
      const [sources, status, candidates] = await Promise.all([api(`/api/sources?${params}`), api('/api/status'), api('/api/candidates')]);
      state.items = (sources.items || []).map(normalizeSource); state.pending = (candidates.items || candidates || []).map(normalizeCandidate).filter(x => (x.status || 'pending') === 'pending');
      $('summary').textContent = `${status.sourceCount ?? state.items.length} 份资料 · ${status.chunkCount ?? 0} 个 chunk · ${status.indexStatus || '索引状态未知'}`; $('health').textContent = '服务已连接';
      const active=(status.jobs || []).filter(job=>['queued','running'].includes(job.status));
      const failed=(status.jobs || []).find(job=>job.status==='failed');
      if(active.length) $('summary').textContent += ' · 处理中 '+active.length;
      const deferred=(status.jobs||[]).filter(job=>job.status==='deferred'&&!status.vector?.ready);if(deferred.length) $('summary').textContent += ' · '+deferred.length+'项索引待恢复（当前可用关键词检索）';
      if(failed) $('summary').textContent += ' · 最近失败：'+failed.detail.error;
      clearTimeout(refresh.timer); if(active.length)refresh.timer=setTimeout(refresh,1500);
      renderSourceTree(); renderSourcesTable(); renderCandidates();
    } catch (err) { $('health').textContent = '服务未连接'; $('summary').textContent = `无法读取知识库：${errorMessage(err)}`; }
  }
  async function submitSearch(event) { event.preventDefault(); const root = $('search-results'); root.replaceChildren(); const query = $('search-query').value.trim(); try { const data = await api('/api/search', { method: 'POST', body: JSON.stringify({ query, filters: { type: $('type-filter').value || undefined, domain: $('domain-filter').value || undefined }, topK: 8 }) }); const items = data.items || [];
      for (const item of items) { const context=item.context;if(!context?.evidenceRef)throw Error('检索结果缺少固定证据引用');
        const card=make('article','result'),head=make('header');head.append(make('h3','',context.title),make('span','state',label(context.kind)));card.append(head);
        fields(card,[['型号 / 版本',[...context.identity.models,context.scope.documentVersion].filter(Boolean)],['项目 / 模块',[context.scope.project,context.scope.module].filter(Boolean)],['文件 / 位置',[context.provenance.file,...context.provenance.locations.map(loc=>`${loc.anchor||''} · 页 ${loc.pages.join(',')||'未记录'}`)]],['检索依据',item.matchReasons],['读取范围','检索节选，点击核对完整相关证据']]);
        card.append(make('p','',context.content.text||'没有片段'));const open=make('button','secondary','读取证据');open.type='button';open.addEventListener('click',()=>previewSource({sourceId:context.evidenceRef.sourceId,kind:context.kind},context.evidenceRef));card.append(open);root.append(card);
      }
      if (!items.length) root.append(make('p', 'empty', '没有找到符合条件的资料'));
    } catch (err) { root.append(make('p', 'empty', `检索失败：${errorMessage(err)}`)); }
  }
  async function initialize() {
    state.config = await getConfig();
    document.querySelectorAll('.tab').forEach(button => button.addEventListener('click', () => { document.querySelectorAll('.tab,.view').forEach(x => x.classList.remove('active')); button.classList.add('active'); $(`${button.dataset.view}-view`).classList.add('active'); }));
    $('file-input').addEventListener('change', event => { const files = [...event.target.files]; event.target.value = ''; void importFiles(files); });
    $('source-filter').addEventListener('input', () => { clearTimeout(initialize.filterTimer); initialize.filterTimer = setTimeout(refresh, 250); }); $('type-filter').addEventListener('change', refresh); $('domain-filter').addEventListener('change', refresh);
    $('search-form').addEventListener('submit', submitSearch); $('reindex-btn').addEventListener('click', async () => { try { await api('/api/reindex', { method: 'POST', body: '{}' }); toast('索引更新任务已提交'); await refresh(); } catch (err) { toast(`提交失败：${errorMessage(err)}`); } });
    $('metadata-form').addEventListener('submit', async event => { event.preventDefault(); const form = $('metadata-form'); const id = $('editing-id').value; try { if (id) await api(`/api/sources/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(metadataFromForm()) }); else await api('/api/sources', { method: 'POST', body: JSON.stringify({ name: form.dataset.fileName, contentBase64: form.dataset.fileBase64, metadata: metadataFromForm() }) }); $('metadata-dialog').close(); toast(id ? '资料元数据已保存' : '已提交导入，请等待解析和索引完成'); await refresh(); } catch (err) { toast(`保存失败：${errorMessage(err)}`); } });
    document.querySelectorAll('[data-close-dialog], dialog .icon').forEach(button => button.addEventListener('click', () => button.closest('dialog').close()));
    $('preview-modal').addEventListener('close',()=>{state.preview++;$('preview-assets').replaceChildren();});
    await refresh();
  }
  void initialize();
})();

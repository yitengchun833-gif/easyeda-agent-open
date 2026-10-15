import assert from 'node:assert/strict';
import {readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';
import {knowledgeRequest as api,ensureKnowledgeService} from './src/client.mjs';
const files=[
 ['D:/Projects/Active/codex-workbench/项目/芯片测试3/解析/RS2057.md','RS2057','datasheet'],
 ['D:/Projects/Active/codex-workbench/项目/芯片测试3/解析/RS2058.md','RS2058','datasheet'],
 ['D:/Projects/Active/codex-workbench/项目/芯片测试3/解析/RS2299.md','RS2299','datasheet'],
 ['D:/Projects/Active/codex-workbench/项目/芯片测试3/工作-RS2251-PCB布局/RS2251-PCB布线规则.md','RS2251','company_rule'],
 ['D:/EasyEDA_AI/datasheet-to-easyeda/input/RS2251_RevC1.pdf','RS2251','datasheet']
];
const submitted=[];
for(const [file,model,type] of files){const result=await api('/api/sources',{method:'POST',body:{name:path.basename(file),contentBase64:(await readFile(file)).toString('base64'),metadata:{title:path.basename(file),type,domain:'both',models:[model],source:'本机已授权资料',version:type==='datasheet'&&model==='RS2251'?'Rev C.1':''}}});submitted.push({...result,file:path.basename(file)});}
console.log(JSON.stringify({submitted}));
const deadline=Date.now()+600000;
for(;;){const jobs=(await api('/api/jobs')).items;const current=submitted.map(x=>jobs.find(j=>j.jobId===x.jobId));if(current.some(j=>j.status==='failed'))throw Error(JSON.stringify(current));if(current.every(j=>j.status==='completed')){console.log(JSON.stringify({jobs:current}));break;}if(Date.now()>deadline)throw Error('Import timeout');await new Promise(r=>setTimeout(r,2500));}
const search=await api('/api/search',{method:'POST',body:{query:'RS2251 导通电阻 RON',filters:{model:'RS2251'},topK:5}});assert(search.items.length);assert(search.items.every(x=>x.metadata.models.includes('RS2251')));assert(search.items.some(x=>x.match==='hybrid'||x.match==='semantic'));
const pdf=(await api('/api/sources')).items.find(x=>x.name.endsWith('.pdf'));assert(pdf);const original=await api('/api/get?id='+pdf.sourceId);assert(original.locations.some(x=>x.locations?.length));
const before=pdf.revision;const patched=await api('/api/sources/'+pdf.sourceId,{method:'PATCH',body:{tags:['模拟开关','RS2251']}});assert.equal(patched.source.revision,before);
const summary={at:new Date().toISOString(),status:await api('/api/status'),query:'RS2251 导通电阻 RON',hits:search.items.map(x=>({title:x.title,match:x.match,anchor:x.anchor,locations:x.metadata.locations})),pdf:{parseStatus:pdf.parseStatus,chunkCount:pdf.chunkCount,locationCount:original.locations.length},metadataEditPreservesRevision:true};
await writeFile(new URL('../../../工作-外部硬件知识库调研/真实资料验收.json',import.meta.url),JSON.stringify(summary,null,2));
console.log(JSON.stringify(summary));

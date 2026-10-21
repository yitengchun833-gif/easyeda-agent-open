import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, readFileSync } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { chunkText, textSlices } from '../src/ingest.mjs';
import { freezeContext } from '../src/context-map.mjs';
import { captureEvidence, loadEvidence, referenceFor, readEvidence } from '../src/evidence.mjs';
import { AjvJsonSchemaValidator } from '../../mcp/node_modules/@modelcontextprotocol/sdk/dist/esm/validation/ajv-provider.js';
import { knowledgeContextSchema } from '../src/context-contract.mjs';

const hash=text=>createHash('sha256').update(text).digest('hex');
const root=mkdtempSync(path.join(os.tmpdir(),'kb-context-'));
test.after(()=>rmSync(root,{recursive:true,force:true}));
const validate=new AjvJsonSchemaValidator().getValidator(knowledgeContextSchema);
const source=(patch={})=>({sourceId:'kb-context',revision:'revision1',sourceHash:hash('fixture'),name:'fixture.pdf',type:'datasheet',parseStatus:'success',status:'approved',metadata:{},...patch});
function markdown(text,type='reference'){
  const contentPath=path.join(root,'fixture.md');writeFileSync(contentPath,text);
  const chunks=chunkText(text).map((item,i)=>({...item,chunkId:'c'+i}));
  return captureEvidence(root,source({name:'fixture.md',contentPath,sourceHash:hash(text),type,metadata:{parser:'utf8-text'}}),chunks);
}
function pdf({unknownLevel=false,explicit=true,missing=false}={}){
  const parsedPath=path.join(root,'parsed','fixture.md'),asset=parsedPath+'.assets';mkdirSync(asset,{recursive:true});
  const items={
    '#/texts/0':{label:'section_header',text:'LIMITS',locations:[{page_no:1}]},
    '#/texts/1':{label:'text',text:'At 25 C, VDD=5 V.',locations:[{page_no:1}]},
    '#/tables/0':{label:'table',text:'<table><tr><td>Input (1)</td><td>5.5 V</td></tr></table>',locations:[{page_no:1,bbox:{l:10,t:20}}]},
    '#/texts/2':{label:'footnote',text:'(1) Absolute maximum, not operating conditions.',locations:[{page_no:1}]},
    '#/texts/3':{label:'section_header',text:'NEXT SECTION',locations:[{page_no:1}]},
    '#/texts/4':{label:'text',text:'Unrelated section content.',locations:[{page_no:1}]},
    '#/texts/5':{label:'footnote',text:'(2) Explicit remote condition.',locations:[{page_no:2}]}
  };
  const document={texts:Object.keys(items).filter(anchor=>anchor.startsWith('#/texts')).map(anchor=>({self_ref:anchor,...(items[anchor].label==='section_header'&&!(unknownLevel&&anchor==='#/texts/3')?{level:1}:{})})),tables:[{self_ref:'#/tables/0',footnotes:explicit?[{$ref:'#/texts/2'},{$ref:'#/texts/5'},...(missing?[{$ref:'#/missing'}]:[])]:[]}]};
  writeFileSync(path.join(asset,'manifest.json'),JSON.stringify({source_hash:hash('fixture'),parser:'docling',parser_version:'test',document_version:'v1'}));
  writeFileSync(path.join(asset,'index.json'),JSON.stringify({document_version:'v1',items}));
  writeFileSync(path.join(asset,'document.json'),JSON.stringify(document));
  const chunks=Object.entries(items).map(([anchor,item],i)=>({chunkId:'pdf'+i,anchor,text:item.text,metadata:{locations:item.locations}}));
  const result=captureEvidence(root,source({metadata:{parser:'docling@test',parsedPath}}),chunks);
  return {...result,asset,ref:referenceFor(result.snapshot,{chunkId:'pdf2',anchor:'#/tables/0'})};
}
function drain(snapshot,ref,options){
  let cursor,text='',count=0;
  do{
    const {context}=readEvidence(snapshot,ref,{...options,...(cursor?{cursor}:{})});
    const checked=validate(context);assert.equal(checked.valid,true,checked.errorMessage);
    assert.equal(context.content.text.isWellFormed(),true);
    text+=context.content.text;cursor=context.content.nextCursor;
    assert(++count<200,'cursor must make progress');
  }while(cursor);
  return {text,count};
}

test('Markdown source/experience preserves exact text without chunk overlap; budgets continue losslessly',()=>{
  const text='# Card\n'+('x'.repeat(190)+'😀\n').repeat(17)+'\n## Conditions\n5 V only\n## Evidence\nunknown\n';
  const {snapshot}=markdown(text,'lesson');
  assert.equal(snapshot.structure.basis,'markdown_headings');
  for(const slice of textSlices(text))assert.equal(text.slice(slice.start,slice.end),slice.text);
  const ref=referenceFor(snapshot,{chunkId:'c0',anchor:snapshot.records[0].anchor});
  const first=readEvidence(snapshot,ref,{maxChars:200,limit:1}).context;
  assert.equal(first.content.readScope,'source');assert.equal(first.content.contextCompleteness,'partial');
  assert.equal(drain(snapshot,ref,{maxChars:200,limit:1}).text,text);
  assert.equal(drain(snapshot,ref,{maxChars:30000,limit:2}).text,text);
  assert.throws(()=>readEvidence(snapshot,ref,{maxChars:200,limit:2,cursor:first.content.nextCursor}),error=>error.code==='CURSOR_MISMATCH');
  assert.equal(readEvidence(snapshot,ref,{scope:'chunk',maxChars:30000}).context.content.readScope,'chunk');
  writeFileSync(path.join(root,'fixture.md'),'changed later');
  assert.equal(drain(loadEvidence(root,ref),ref,{maxChars:200,limit:1}).text,text);
});

test('Markdown section retains parent conditions and subheadings, ignores code fence headings, excludes sibling',()=>{
  const text='# Manual\nSupply=5 V\n## Target\nTarget body\n```\n# not a heading\n```\n### Test\nAt 25 C\n## Other\nEXCLUDED';
  const {snapshot}=markdown(text);
  const chunk=snapshot.records.find(item=>item.text.includes('Target body'));
  const ref=referenceFor(snapshot,{chunkId:chunk.chunkId,anchor:chunk.anchor});
  const {context}=readEvidence(snapshot,ref,{scope:'section',maxChars:30000});
  assert.equal(context.content.text,text.slice(0,text.indexOf('## Other')));
  assert.equal(context.content.contextExpansion.relationStatus,'confirmed');
  assert.equal(drain(snapshot,ref,{scope:'section',maxChars:200,limit:1}).text,context.content.text);
});

test('PDF frozen sections include table, conditions and explicit remote footnotes without current parser reads',()=>{
  const {snapshot,ref,asset}=pdf();
  assert.equal(snapshot.structure.basis,'docling_structure');
  assert.equal(freezeContext(root,snapshot.source,snapshot.records).basis,'docling_structure','JSON object key order must not invalidate matching locations');
  const {context}=readEvidence(snapshot,ref,{scope:'section',maxChars:30000});
  assert.match(context.content.text,/LIMITS/);assert.match(context.content.text,/At 25 C/);
  assert.match(context.content.text,/<table>/);assert.match(context.content.text,/Absolute maximum/);assert.match(context.content.text,/Explicit remote/);
  assert.doesNotMatch(context.content.text,/NEXT SECTION|Unrelated/);
  assert.equal(context.content.contextExpansion.relationStatus,'confirmed');
  assert.equal(context.content.contextCompleteness,'complete');
  assert.equal(drain(snapshot,ref,{scope:'section',maxChars:200,limit:1}).text,context.content.text);
  writeFileSync(path.join(asset,'document.json'),'corrupt current parse');
  assert.equal(readEvidence(loadEvidence(root,ref),ref,{scope:'section',maxChars:30000}).context.content.text,context.content.text);
});

test('unlinked/missing notes and unknown heading levels are explicit uncertainty',()=>{
  for(const options of [{explicit:false},{missing:true},{unknownLevel:true}]){
    const {snapshot,ref}=pdf(options);
    const {context}=readEvidence(snapshot,ref,{scope:'section',maxChars:30000});
    assert.equal(context.content.contextExpansion.relationStatus,'unconfirmed');
    assert.equal(context.content.contextCompleteness,'unknown');
    assert(context.content.contextExpansion.warnings.length);
    if(options.unknownLevel)assert.equal(context.content.contextExpansion.basis,'same_page');
  }
});

test('stage 2 snapshots and source cursors remain readable; missing structure only permits bounded fallback',()=>{
  const fresh=pdf(),payload=JSON.parse(readFileSync(path.join(root,'evidence',fresh.ref.evidenceId+'.json')));
  delete payload.structure;
  const canonical=value=>JSON.stringify(value&&typeof value==='object'?sort(value):value);
  function sort(value){return Array.isArray(value)?value.map(v=>v&&typeof v==='object'?sort(v):v):Object.fromEntries(Object.keys(value).sort().map(key=>[key,value[key]&&typeof value[key]==='object'?sort(value[key]):value[key]]));}
  const snapshotHash=hash(canonical(payload)),evidenceId='ev-'+snapshotHash;
  writeFileSync(path.join(root,'evidence',evidenceId+'.json'),canonical(payload));
  const ref={...fresh.ref,evidenceId,snapshotHash},snapshot=loadEvidence(root,ref);
  const oldText=snapshot.records.map(record=>record.text).join('\n\n');
  const binding={evidenceId,bindingHash:hash(canonical({selector:ref.selector,scope:'source',maxChars:200})),scope:'source',maxChars:200,offset:200};
  const cursor=Buffer.from(JSON.stringify({...binding,checksum:hash(canonical(binding)).slice(0,24)})).toString('base64url');
  assert.equal(readEvidence(snapshot,ref,{cursor,maxChars:200}).context.content.text,oldText.slice(200,400));
  assert.equal(drain(snapshot,ref,{scope:'source',maxChars:200}).text,oldText);
  const section=readEvidence(snapshot,ref,{scope:'section',maxChars:30000}).context;
  assert.equal(section.content.contextExpansion.basis,'same_page');assert.equal(section.content.contextExpansion.relationStatus,'unconfirmed');
  const broken=pdf();writeFileSync(path.join(broken.asset,'manifest.json'),'{}');
  const current=captureEvidence(root,source({metadata:{parser:'docling@test',parsedPath:path.join(root,'parsed','fixture.md')}}),broken.snapshot.records);
  assert.equal(current.snapshot.structure.basis,'unavailable');
});

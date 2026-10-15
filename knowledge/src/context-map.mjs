import { readFileSync } from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';
import { chunkText, textSlices } from './ingest.mjs';
import { resolveKnowledgeFile } from './relocation.mjs';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const inside = (root, file) => {
  const relative=path.relative(path.resolve(root),path.resolve(file));
  return relative!==''&&!relative.startsWith('..'+path.sep)&&relative!=='..'&&!path.isAbsolute(relative);
};
const headings = text => {
  const starts=[];let offset=0,fence=null;
  for(const line of text.split(/(?<=\n)/)) {
    const fenced=line.match(/^ {0,3}(`{3,}|~{3,})(.*)/);
    if(fenced){if(!fence)fence=fenced[1];else if(fenced[1][0]===fence[0]&&fenced[1].length>=fence.length&&!fenced[2].trim())fence=null;}
    if(!fence&&!fenced){const heading=line.match(/^ {0,3}(#{1,6})[ \t]+\S/);if(heading)starts.push({start:offset,level:heading[1].length});}
    offset+=line.length;
  }
  return starts;
};

// Freeze only structure that can be matched to this exact indexed representation.
// Missing structure is a bounded fallback, never a reason to read a newer source.
export function freezeContext(root, source, records) {
  const unavailable = reason => ({version:1,basis:'unavailable',reason,units:[],recordUnits:{}});
  const metadata=source.metadata||{};
  if(metadata.parser==='utf8-text'||metadata.parser==='knowledge-card/v1') {
    const contentPath=resolveKnowledgeFile(root,source.contentPath);
    if(!contentPath||!inside(root,contentPath))return unavailable('原文件路径不可确认');
    let raw;try{raw=readFileSync(contentPath);}catch{return unavailable('原文件不可读取，仅保留冻结片段');}
    if(hash(raw)!==source.sourceHash)return unavailable('原文件哈希与当前索引不一致');
    let text;try{text=new TextDecoder('utf-8',{fatal:true}).decode(raw);}catch{return unavailable('原文件不是有效UTF-8');}
    const slices=textSlices(text).filter(item=>item.text);
    if(slices.length!==records.length||slices.some((item,i)=>item.text!==records[i].text))return unavailable('原文件与索引片段不一致');
    const starts=headings(text);if(!starts.length||starts[0].start!==0)starts.unshift({start:0,level:null});
    const units=starts.map((item,i)=>({...item,anchor:`md:${item.start}`,text:text.slice(item.start,starts[i+1]?.start??text.length),end:starts[i+1]?.start??text.length,locations:[],related:[]}));
    const recordUnits=Object.fromEntries(records.map((record,i)=>[record.chunkId,units.flatMap((unit,j)=>slices[i].start<unit.end&&slices[i].end>unit.start?[j]:[])]));
    return {version:1,basis:'markdown_headings',units,recordUnits};
  }
  if(!metadata.parser?.startsWith('docling@'))return unavailable('没有可确认的文档结构');
  const parsed=resolveKnowledgeFile(root,metadata.parsedPath);
  if(typeof parsed!=='string'||!inside(path.join(root,'parsed'),parsed))return unavailable('解析路径不可确认');
  let index,doc,manifest,indexBytes,docBytes;
  try {
    manifest=JSON.parse(readFileSync(parsed+'.assets/manifest.json','utf8'));
    indexBytes=readFileSync(parsed+'.assets/index.json');docBytes=readFileSync(parsed+'.assets/document.json');
    index=JSON.parse(indexBytes);doc=JSON.parse(docBytes);
  }catch{return unavailable('现有解析结构缺失或损坏');}
  if(manifest.source_hash!==source.sourceHash||`${manifest.parser}@${manifest.parser_version}`!==metadata.parser||index.document_version!==manifest.document_version)return unavailable('解析版本与来源不一致');
  const nodes=new Map(['texts','tables','pictures'].flatMap(key=>(doc[key]||[]).map(item=>[item.self_ref,item])));
  const units=[],expected=[];
  for(const [anchor,item] of Object.entries(index.items||{})) {
    const text=item.text||(item.asset?'图表 '+anchor:'');if(!text)continue;
    const node=nodes.get(anchor);
    const heading=['section_header','title'].includes(item.label);
    const related=['captions','footnotes','references'].flatMap(key=>(node?.[key]||[]).map(ref=>ref.$ref).filter(ref=>typeof ref==='string'));
    const unitIndex=units.length;
    units.push({anchor,text,label:item.label,level:heading&&Number.isInteger(node?.level)?node.level:null,heading,locations:item.locations||[],related});
    const parts=item.label==='table'?[{text}]:chunkText(text);
    parts.forEach((part,i)=>expected.push({unitIndex,anchor:parts.length===1?anchor:anchor+'/'+i,text:part.text,locations:item.locations||[]}));
  }
  if(expected.length!==records.length||expected.some((item,i)=>item.anchor!==records[i].anchor||item.text!==records[i].text||!isDeepStrictEqual(item.locations,records[i].locations)))return unavailable('解析正文或位置与冻结索引不一致');
  return {version:1,basis:'docling_structure',indexHash:hash(indexBytes),documentHash:hash(docBytes),units,
    recordUnits:Object.fromEntries(records.map((record,i)=>[record.chunkId,[expected[i].unitIndex]]))};
}

const pages = record => (record.locations||[]).flatMap(loc=>[loc.page_no,...(loc.pages||[])]).filter(Number.isInteger);

export function sectionContext(snapshot, selected) {
  const structure=snapshot.structure;
  if(structure?.basis==='markdown_headings'||structure?.basis==='docling_structure') {
    const {units,recordUnits}=structure;
    const hits=[...new Set(selected.flatMap(record=>recordUnits[record.chunkId]||[]))];
    const included=new Set(),warnings=[];let confirmed=hits.length>0;
    for(const hit of hits) {
      let start=hit;while(start>=0&&units[start].level===null&&!units[start].heading)start--;
      if(start<0||units[start].level===null){confirmed=false;break;}
      const level=units[start].level;let end=start+1;
      while(end<units.length&&(units[end].level===null||units[end].level>level)){
        if(units[end].heading&&units[end].level===null){confirmed=false;break;}
        end++;
      }
      if(!confirmed)break;
      for(let i=start;i<end;i++)included.add(i);
      // Include enclosing titles, not the whole parent section.
      let ancestorLevel=level;
      for(let i=start-1;i>=0;i--)if(units[i].level!==null&&units[i].level<ancestorLevel){included.add(i);ancestorLevel=units[i].level;}
    }
    if(confirmed) {
      const byAnchor=new Map(units.map((unit,i)=>[unit.anchor,i]));
      const linked=new Set();
      for(const i of included)for(const anchor of units[i].related){const related=byAnchor.get(anchor);if(related===undefined)warnings.push('解析关联未包含原文：'+anchor);else {included.add(related);linked.add(related);}}
      if([...included].some(i=>units[i].label==='footnote'&&!linked.has(i)))warnings.push('章节范围已确认，但部分脚注未保存与表格的明确对应；须核对原文');
      const chosen=[...included].sort((a,b)=>a-b);
      const records=snapshot.records.filter(record=>(recordUnits[record.chunkId]||[]).some(i=>included.has(i)));
      return {segments:chosen.map(i=>({text:units[i].text,unit:i})),separator:structure.basis==='markdown_headings'?'':'\n\n',records,
        expansion:{basis:structure.basis,relationStatus:warnings.length?'unconfirmed':'confirmed',warnings},complete:warnings.length===0};
    }
  }
  const hitIndexes=selected.map(record=>snapshot.records.indexOf(record));
  const wantedPages=new Set(selected.flatMap(pages));
  const candidates=snapshot.records.map((record,i)=>({record,i,distance:Math.min(...hitIndexes.map(hit=>Math.abs(i-hit)))}))
    .filter(({record,i})=>hitIndexes.includes(i)||(wantedPages.size?pages(record).some(page=>wantedPages.has(page)):hitIndexes.some(hit=>Math.abs(i-hit)<=2)))
    .sort((a,b)=>a.distance-b.distance||a.i-b.i);
  // ponytail: bounded fallback; larger confirmed sections use the frozen structure.
  const chosen=candidates.slice(0,Math.max(16,selected.length)).sort((a,b)=>a.i-b.i).map(item=>item.record);
  return {segments:chosen.map(record=>({text:record.text,chunkId:record.chunkId})),separator:'\n\n',records:chosen,
    expansion:{basis:wantedPages.size?'same_page':'neighbors',relationStatus:'unconfirmed',warnings:[structure?.reason||'快照未保存可确认的章节结构','邻近内容不证明脚注或测试条件归属',...(candidates.length>chosen.length?['邻近范围已限制，请按原文位置继续核对']:[])]},complete:false};
}

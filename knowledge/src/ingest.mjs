import { createHash } from 'node:crypto';
import { mkdir, writeFile, readFile, access } from 'node:fs/promises';
import path from 'node:path';
import { spawn } from 'node:child_process';

const sha256 = value => createHash('sha256').update(value).digest('hex');
const safeName = name => path.basename(String(name || 'source.txt')).replace(/[^\w.\-\u3400-\u9fff]/g, '_').slice(0, 120) || 'source.txt';
export const textSlices = text => {
  const pieces = [];
  let sectionStart = 0;
  for (const section of String(text).split(/(?=^#{1,4}\s)/m)) {
    for (let offset=0;offset<section.length;offset+=1500) {
      const raw=section.slice(offset,offset+1600),start=sectionStart+offset+raw.length-raw.trimStart().length;
      pieces.push({text:raw.trim(),start,end:start+raw.trim().length});
    }
    sectionStart+=section.length;
  }
  return pieces;
};
export const chunkText = text => textSlices(text).map(({text},index)=>({anchor:`#/text/${index+1}`,text,contentHash:sha256(text),metadata:{}})).filter(item=>item.text);

async function run(program, args) {
  return await new Promise((resolve, reject) => {
    const child = spawn(program, args, { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] }); let stderr = '';
    child.stdout.resume();
    child.stderr.on('data', data => { stderr = (stderr + data).slice(-8000); });
    child.on('error', reject); child.on('exit', code => code === 0 ? resolve() : reject(new Error(`Docling exited ${code}: ${stderr.slice(-1000)}`)));
  });
}

export async function ingest({ root, name, contentBase64, metadata = {} }) {
  if (!name || typeof contentBase64 !== 'string') throw new Error('name and contentBase64 are required');
  const raw = Buffer.from(contentBase64, 'base64'); if (!raw.length || raw.length > 100 * 1024 * 1024) throw new Error('file must be 1 byte..100 MiB');
  const extension = path.extname(name).toLowerCase(); if (!['.md', '.markdown', '.txt', '.pdf', '.png'].includes(extension)) throw new Error('only Markdown, TXT, PDF, PNG are supported');
  if(extension==='.png' && (raw.length<33 || !raw.subarray(0,8).equals(Buffer.from('89504e470d0a1a0a','hex')) || raw.toString('ascii',12,16)!=='IHDR'))throw new Error('Invalid PNG original');
  const hash = sha256(raw); const dir = path.join(root, 'sources', hash); await mkdir(dir, { recursive: true });
  const file = path.join(dir, safeName(name)); await writeFile(file, raw);
  if(extension==='.png'){
    const text=safeName(name)+' — PNG原图，尚未解析文字/引脚/连接；使用前读取原图并确认解读。';
    return {name:safeName(name),sourceHash:hash,contentPath:file,parseStatus:'partial',parser:'image-original/v1',issues:['Image retained without OCR or engineering interpretation'],chunks:[{anchor:'#/image/1',text,contentHash:sha256(text),metadata:{label:'picture',assetPath:file,locations:[]}}]};
  }
  if (extension !== '.pdf') return { name: safeName(name), sourceHash: hash, contentPath: file, parseStatus: 'success', parser:'utf8-text', chunks: chunkText(new TextDecoder('utf-8',{fatal:true}).decode(raw)) };
  const parsed = path.join(root, 'parsed', `${hash}.md`); await mkdir(path.dirname(parsed), { recursive: true });
  const helper = process.env.EASYEDA_DOCLING_CONVERT || 'C:\\Users\\USER\\.codex\\plugins\\cache\\evidence-workflows\\evidence-to-delivery-workflow\\1.0.0+codex.20260916133532\\skills\\document-intake\\scripts\\docling_convert.py';
  const python = process.env.EASYEDA_DOCLING_PYTHON || 'C:\\Users\\USER\\.codex\\tools\\evidence-to-delivery\\docling\\.venv\\Scripts\\python.exe';
  try {
    await access(helper); await access(python);
    let manifest;
    try {manifest=JSON.parse(await readFile(parsed+'.assets/manifest.json','utf8'));}catch(error){if(error.code!=='ENOENT')throw error;}
    if(!manifest || manifest.source_hash!==hash || manifest.parse_status==='failed') {
      await run(python,[helper,file,parsed]);
      manifest=JSON.parse(await readFile(parsed+'.assets/manifest.json','utf8'));
    }
    if(!['success','partial'].includes(manifest.parse_status))throw new Error('Docling did not produce a usable structured result');
    const index=JSON.parse(await readFile(parsed+'.assets/index.json','utf8'));
    const chunks=[];
    for(const [anchor,item] of Object.entries(index.items)) {
      const text=item.text || (item.asset ? '图表 '+anchor : '');
      if(!text)continue;
      const parts=item.label==='table'?[{text}]:chunkText(text);
      for(let i=0;i<parts.length;i++)chunks.push({anchor:parts.length===1?anchor:anchor+'/'+i,text:parts[i].text,contentHash:sha256(parts[i].text),metadata:{label:item.label,modelRef:anchor,locations:item.locations||[],...(item.asset?{assetPath:path.join(parsed+'.assets',item.asset)}:{})}});
    }
    return {name:safeName(name),sourceHash:hash,contentPath:file,parseStatus:manifest.parse_status,chunks,parsedPath:parsed,parser:manifest.parser+'@'+manifest.parser_version,issues:manifest.issues};
  } catch (error) {
    // Never pretend PDF text fallback is structured Docling success.
    return { name: safeName(name), sourceHash: hash, contentPath: file, parseStatus: 'failed', chunks: [], issue: error.message };
  }
}

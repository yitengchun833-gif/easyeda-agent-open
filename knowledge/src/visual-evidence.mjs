import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, realpathSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { resolveKnowledgeFile } from './relocation.mjs';

const MAX_BYTES = 2 * 1024 * 1024, MAX_TOTAL = 4 * 1024 * 1024, MAX_PIXELS = 16_000_000;
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const inside = (root, file) => {
  const relative = path.relative(realpathSync(root), realpathSync(file));
  return relative !== '' && relative !== '..' && !relative.startsWith('..' + path.sep) && !path.isAbsolute(relative);
};
function png(file, root) {
  if (!inside(root, file)) throw Object.assign(Error('Image is outside registered storage'), { status: 'invalid_asset' });
  if (statSync(file).size > MAX_BYTES) throw Object.assign(Error('Image exceeds 2 MiB'), { status: 'too_large' });
  const bytes = readFileSync(file);
  if (bytes.length > MAX_BYTES) throw Object.assign(Error('Image exceeds 2 MiB'), { status: 'too_large' });
  if (bytes.length < 33 || !bytes.subarray(0, 8).equals(Buffer.from('89504e470d0a1a0a', 'hex')) || bytes.toString('ascii', 12, 16) !== 'IHDR')
    throw Object.assign(Error('Registered image is not PNG'), { status: 'invalid_asset' });
  const width = bytes.readUInt32BE(16), height = bytes.readUInt32BE(20);
  if (!width || !height || width * height > MAX_PIXELS) throw Object.assign(Error('Image exceeds pixel limit'), { status: 'too_large' });
  return { bytes, width, height, sha256: hash(bytes) };
}
const failure = error => ({ status: error.status || (error.code === 'ENOENT' ? 'missing' : 'unavailable'), reason: error.message });

// Freeze only index-registered assets belonging to the confirmed source representation.
export function freezeVisuals(root, source, records, structure) {
  const registered = records.filter(record => record.metadata?.assetPath);
  if (!registered.length) return undefined;
  if(source.metadata?.parser==='image-original/v1')return registered.map(record=>{
    const descriptor={assetId:record.chunkId,anchor:record.anchor,locations:record.locations};
    try{
      const file=resolveKnowledgeFile(root,source.contentPath),actual=resolveKnowledgeFile(root,record.metadata.assetPath);
      if(realpathSync(file)!==realpathSync(actual))throw Error('Image does not match original source');
      const image=png(file,path.join(root,'sources'));
      if(image.sha256!==source.sourceHash)throw Error('Original image hash changed');
      const directory=path.join(root,'evidence','assets');mkdirSync(directory,{recursive:true});
      const target=path.join(directory,image.sha256+'.png');
      try{writeFileSync(target,image.bytes,{flag:'wx'});}catch(error){if(error.code!=='EEXIST')throw error;if(hash(readFileSync(target))!==image.sha256)throw Error('Frozen image hash mismatch');}
      return {...descriptor,status:'ready',sha256:image.sha256,mimeType:'image/png',byteLength:image.bytes.length,width:image.width,height:image.height};
    }catch(error){return {...descriptor,...failure(error)};}
  });
  let index, assets;
  try {
    const parsed = resolveKnowledgeFile(root, source.metadata?.parsedPath);
    assets = parsed + '.assets';
    if (structure.basis !== 'docling_structure' || !inside(path.join(root, 'parsed'), assets)) throw Error('Image source structure is unconfirmed');
    const bytes = readFileSync(path.join(assets, 'index.json'));
    if (hash(bytes) !== structure.indexHash) throw Error('Image index changed during capture');
    index = JSON.parse(bytes);
  } catch (error) {
    return registered.map(record => ({ assetId: record.chunkId, anchor: record.anchor, ...failure(error) }));
  }
  const directory = path.join(root, 'evidence', 'assets');
  mkdirSync(directory, { recursive: true });
  const results = [], frozen = new Set(); let total = 0;
  for (const record of registered) {
    const descriptor = { assetId: record.chunkId, anchor: record.anchor, locations: record.locations };
    try {
      const units = structure.recordUnits[record.chunkId] || [];
      const unit = units.length === 1 && structure.units[units[0]];
      const registeredPath = index.items?.[unit?.anchor]?.asset;
      if (typeof registeredPath !== 'string') throw Error('No image registered for this frozen unit');
      const file = path.resolve(assets, registeredPath);
      const actual = resolveKnowledgeFile(root, record.metadata.assetPath);
      if (realpathSync(file) !== realpathSync(actual)) throw Error('Image registration does not match frozen unit');
      const image = png(file, assets);
      // ponytail: bounded source capture; very large documents keep explicit unavailable descriptors.
      if (!frozen.has(image.sha256) && total + image.bytes.length > 32 * 1024 * 1024) throw Object.assign(Error('Source image capture exceeds 32 MiB'), { status: 'too_large' });
      const target = path.join(directory, image.sha256 + '.png');
      try { writeFileSync(target, image.bytes, { flag: 'wx' }); }
      catch (error) { if (error.code !== 'EEXIST') throw error; if (hash(readFileSync(target)) !== image.sha256) throw Error('Frozen image hash mismatch'); }
      if (!frozen.has(image.sha256)) { total += image.bytes.length; frozen.add(image.sha256); }
      results.push({ ...descriptor, status: 'ready', sha256: image.sha256, mimeType: 'image/png', byteLength: image.bytes.length, width: image.width, height: image.height });
    } catch (error) { results.push({ ...descriptor, ...failure(error) }); }
  }
  return results;
}

export function readVisuals(root, snapshot, records, { pinned, assetIds } = {}) {
  const eligible = new Map(records.filter(record => record.metadata?.assetPath).map(record => [record.chunkId, record]));
  const wanted = assetIds ?? [...eligible.keys()];
  const manifest = new Map((snapshot.visuals || []).map(item => [item.assetId, item]));
  let total = 0, count = 0;
  const items = wanted.map(assetId => {
    const record = eligible.get(assetId);
    const base = { assetId, ...(record ? { anchor: record.anchor, locations: record.locations } : {}) };
    if (!record) return { ...base, status: 'not_in_read_scope' };
    if (!pinned) return { ...base, status: 'unpinned', reason: 'Use the returned evidenceRef to request frozen images' };
    const item = manifest.get(assetId);
    if (!item) return { ...base, status: 'unavailable_legacy', reason: 'This evidence snapshot has no frozen image' };
    if (item.status !== 'ready') return item;
    if (count >= 3) return { ...item, status: 'limit_exceeded', reason: 'Select up to three assetIds per call' };
    try {
      if (!/^[a-f0-9]{64}$/.test(item.sha256)) throw Error('Invalid frozen image hash');
      const directory = path.join(root, 'evidence', 'assets');
      const image = png(path.join(directory, item.sha256 + '.png'), directory);
      if (image.sha256 !== item.sha256 || image.width !== item.width || image.height !== item.height || image.bytes.length !== item.byteLength) throw Object.assign(Error('Frozen image changed'), { status: 'integrity_error' });
      if (total + image.bytes.length > MAX_TOTAL) return { ...item, status: 'too_large', reason: 'Combined image bytes exceed 4 MiB' };
      total += image.bytes.length; count++;
      return { ...item, data: image.bytes.toString('base64') };
    } catch (error) { return { ...item, ...failure(error) }; }
  });
  return { evidenceId: snapshot.evidenceId, sourceRevision: snapshot.source.sourceRevision, items, ...(items.length ? {} : { status: 'no_registered_images' }) };
}

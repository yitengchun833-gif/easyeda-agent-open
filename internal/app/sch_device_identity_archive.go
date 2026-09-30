package app

// Old connectors have no bundled ZIP library in debug.exec_js. This bounded,
// read-only helper reads the official epro2 export entirely in the editor. It
// sends only selected footprint provenance records back through the socket.
const schematicIdentityArchiveCode = `
async function identityFootprintsFromProject(file, wanted, documentUuid) {
 const limit = 64 * 1024 * 1024;
 if (!file || typeof file.arrayBuffer !== "function" || !Number.isSafeInteger(file.size) || file.size < 22 || file.size > limit) throw new Error("native project ZIP missing or exceeds 64 MiB");
 const bytes = new Uint8Array(await file.arrayBuffer());
 if (bytes.length !== file.size) throw new Error("native project ZIP size changed");
 const v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
 const u16 = p => v.getUint16(p, true), u32 = p => v.getUint32(p, true);
 let end = -1;
 for (let p = bytes.length - 22; p >= Math.max(0, bytes.length - 65557); p--) {
  if (u32(p) === 0x06054b50 && p + 22 + u16(p + 20) === bytes.length) { end = p; break; }
 }
 if (end < 0 || u16(end+4) || u16(end+6) || u16(end+8) !== u16(end+10)) throw new Error("native project ZIP directory is missing or multi-disk");
 const count = u16(end+10), centralSize = u32(end+12), centralOffset = u32(end+16);
 if (!count || count > 2048 || centralOffset + centralSize !== end) throw new Error("native project ZIP directory exceeds bounds or uses ZIP64");
 let p = centralOffset, target = null;
 const decoder = new TextDecoder("utf-8", {fatal:true});
 for (let i = 0; i < count; i++) {
  if (p + 46 > end || u32(p) !== 0x02014b50) throw new Error("invalid native project ZIP central record");
  const flags = u16(p+8), method = u16(p+10), crc = u32(p+16), compressed = u32(p+20), size = u32(p+24);
  const nameLength = u16(p+28), extraLength = u16(p+30), commentLength = u16(p+32), offset = u32(p+42);
  const next = p+46+nameLength+extraLength+commentLength;
  if (next > end || u16(p+34) || flags & 0x2041 || (method !== 0 && method !== 8)) throw new Error("encrypted, unsupported or malformed native project ZIP entry");
  const nameBytes = bytes.subarray(p+46,p+46+nameLength), name = decoder.decode(nameBytes);
  if (/\.epru$/i.test(name)) {
   if (target || name.includes("/") || name.includes("\\") || !size || size > limit || compressed > limit || offset + 30 > centralOffset) throw new Error("native project ZIP requires one bounded root .epru");
   target = {flags,method,crc,compressed,size,offset,nameBytes};
  }
  p = next;
 }
 if (p !== end || !target) throw new Error("native project ZIP has no unique root .epru");
 const t = target, o = t.offset;
 if (u32(o) !== 0x04034b50 || u16(o+6) !== t.flags || u16(o+8) !== t.method) throw new Error("native project ZIP local header disagrees");
 const localNameLength = u16(o+26), dataStart = o+30+localNameLength+u16(o+28);
 if (localNameLength !== t.nameBytes.length || dataStart > centralOffset || dataStart+t.compressed > centralOffset) throw new Error("native project ZIP payload exceeds bounds");
 for (let i = 0; i < localNameLength; i++) if (bytes[o+30+i] !== t.nameBytes[i]) throw new Error("native project ZIP filename disagrees");
 if (!(t.flags & 8) && (u32(o+14) !== t.crc || u32(o+18) !== t.compressed || u32(o+22) !== t.size)) throw new Error("native project ZIP local lengths disagree");
 let raw;
 const packed = bytes.subarray(dataStart,dataStart+t.compressed);
 if (t.method === 0) raw = packed;
 else {
  const reader = new Blob([packed]).stream().pipeThrough(new DecompressionStream("deflate-raw")).getReader();
  const chunks = []; let length = 0;
  try {
   while (true) {
    const part = await reader.read(); if (part.done) break;
    length += part.value.byteLength;
    if (length > t.size || length > limit) { await reader.cancel(); throw new Error("native project .epru decompression exceeds bounds"); }
    chunks.push(part.value);
   }
  } finally { reader.releaseLock(); }
  raw = new Uint8Array(length); let at = 0;
  for (const chunk of chunks) { raw.set(chunk,at); at += chunk.byteLength; }
 }
 if (raw.length !== t.size) throw new Error("native project .epru length disagrees");
 const table = new Uint32Array(256);
 for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; table[n] = c; }
 let crc = 0xffffffff;
 for (const b of raw) crc = table[(crc ^ b) & 255] ^ (crc >>> 8);
 if (((crc ^ 0xffffffff) >>> 0) !== t.crc) throw new Error("native project .epru CRC disagrees");
 const source = decoder.decode(raw), result = []; let current = null, pages = 0, lines = 0, footprints = 0;
 for (const line of source.split(/\r?\n/)) {
  if (++lines > 200000) throw new Error("native project source exceeds 200000 records");
  const separator = line.indexOf("||"); if (separator < 0) continue;
  const header = JSON.parse(line.slice(0,separator));
  if (header.type !== "DOCHEAD" && !(current && header.type === "META")) continue;
  const tail = line.slice(separator+2).replace(/\|$/, ""), data = JSON.parse(tail);
  if (header.type === "DOCHEAD") {
   current = null;
   if (data.docType === "SCH_PAGE" && data.uuid === documentUuid) pages++;
   if (data.docType === "FOOTPRINT") {
    if (++footprints > 2048) throw new Error("native project source exceeds 2048 footprints");
    if (wanted.has(data.uuid)) {
     current = {footprintUuid:data.uuid,sourceKind:"project-epro2",metadataLines:[JSON.stringify({type:"DOCHEAD"})+"||"+JSON.stringify({docType:data.docType,uuid:data.uuid})+"|"]};
     result.push(current);
    }
   }
  } else {
   if (current.metadataLines.length >= 3) throw new Error("native footprint source has duplicate META records");
   current.metadataLines.push(JSON.stringify({type:"META"})+"||"+JSON.stringify({title:data.title,source:data.source})+"|");
  }
 }
 if (!documentUuid || pages !== 1) throw new Error("native project source does not uniquely contain the current schematic page");
 return result;
}
`

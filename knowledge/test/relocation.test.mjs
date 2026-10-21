import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { resolveKnowledgeFile } from '../src/relocation.mjs';
import { chunkText } from '../src/ingest.mjs';
import { captureEvidence } from '../src/evidence.mjs';

test('relocation preserves frozen identity and maps only the original root', () => {
  const base=mkdtempSync(path.join(os.tmpdir(),'kb-relocation-'));
  try {
    const old=path.join(base,'old'), current=path.join(base,'新目录');
    mkdirSync(old);mkdirSync(current);
    const text='# Example\n\nHardware evidence.', file=path.join(old,'a.md');
    writeFileSync(file,text);
    const source={sourceId:'test',revision:'r1',name:'a.md',contentPath:file,sourceHash:createHash('sha256').update(text).digest('hex'),type:'reference',status:'approved',parseStatus:'success',metadata:{parser:'utf8-text'}};
    const chunks=chunkText(text).map((item,i)=>({...item,chunkId:'c'+i}));
    const before=captureEvidence(old,source,chunks);
    writeFileSync(path.join(current,'a.md'),text);
    writeFileSync(path.join(current,'relocation.json'),JSON.stringify({originalRoot:old}));
    rmSync(old,{recursive:true});
    const after=captureEvidence(current,source,chunks);
    assert.equal(after.snapshot.snapshotHash,before.snapshot.snapshotHash);
    assert.equal(resolveKnowledgeFile(current,file),path.join(current,'a.md'));
    const outside=path.join(base,'other','a.md');
    assert.equal(resolveKnowledgeFile(current,outside),outside);
    assert.equal(resolveKnowledgeFile(current,path.join(base,'old-sibling','a.md')),path.join(base,'old-sibling','a.md'));
  } finally { rmSync(base,{recursive:true,force:true}); }
});

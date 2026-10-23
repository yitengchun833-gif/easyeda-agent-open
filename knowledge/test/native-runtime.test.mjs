import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

// Run only after npm has exited: a native module still being unpacked may be locked.
test('LanceDB native loader, persisted vectors and fresh-process reads', async () => {
  const packageRoot = fileURLToPath(new URL('../', import.meta.url));
  const database = await mkdtemp(path.join(packageRoot, '.native-check-'));
  const run = code => execFileSync(process.execPath, ['--input-type=module', '-e', code, database], { cwd: packageRoot, encoding: 'utf8', timeout: 30000 });
  try {
    run("import * as db from '@lancedb/lancedb'; const c=await db.connect(process.argv[1]); await c.createTable('evidence',[{id:'correct',model:'RS2251',vector:[1,0,0]},{id:'similar',model:'RS22510',vector:[0,1,0]}]);");
    const result = run("import * as db from '@lancedb/lancedb'; const c=await db.connect(process.argv[1]); const t=await c.openTable('evidence'); const hits=await t.vectorSearch([1,0,0]).where(\"model = 'RS2251'\").limit(1).toArray(); console.log(JSON.stringify({count:await t.countRows(),id:hits[0]?.id}));");
    assert.deepEqual(JSON.parse(result.trim()), { count: 2, id: 'correct' });
  } finally {
    assert.equal(path.dirname(database), path.resolve(packageRoot));
    await rm(database, { recursive: true, force: true });
  }
});

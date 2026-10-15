import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtemp, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL, fileURLToPath } from 'node:url';
const root=process.env.EASYEDA_TEST_PACKAGE_ROOT || fileURLToPath(new URL('../../',import.meta.url));
const {ensureDaemon}=await import(pathToFileURL(root+'/mcp/src/daemon-lifecycle.mjs'));
test('rejects remote endpoints and occupied ports without replacement',async()=>{
 await assert.rejects(ensureDaemon({url:'http://example.com:60932'}),/loopback/);
 const server=http.createServer((req,res)=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify({service:'different-app',version:'x',pid:process.pid}));});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 try{await assert.rejects(ensureDaemon({url:`http://127.0.0.1:${server.address().port}`}),/different process/);assert(server.listening);}finally{await new Promise(resolve=>server.close(resolve));}
});
test('missing daemon starts once, persists, and recovers after owned process exits',async()=>{
 const runtimeDir=await mkdtemp(path.join(os.tmpdir(),'eda-daemon-regression-'));
 const reserve=http.createServer();await new Promise(resolve=>reserve.listen(0,'127.0.0.1',resolve));const port=reserve.address().port;await new Promise(resolve=>reserve.close(resolve));
 const options={url:`http://127.0.0.1:${port}`,runtimeDir,binary:root+'/bin/easyeda.exe'};
 let live;
 try{
  const first=await Promise.all([ensureDaemon(options),ensureDaemon(options),ensureDaemon(options)]);live=first[0];assert(first.every(x=>x.pid===live.pid));
  assert.equal(Number(await readFile(path.join(runtimeDir,'daemon.pid'),'utf8')),live.pid);
  assert.equal((await ensureDaemon(options)).pid,live.pid);
  const old=live.pid;process.kill(old);live=undefined;await new Promise(resolve=>setTimeout(resolve,500));
  live=await ensureDaemon(options);assert.notEqual(live.pid,old);assert.equal(live.service,'easyeda-agent');
 }finally{if(live){assert.equal(Number(await readFile(path.join(runtimeDir,'daemon.pid'),'utf8')),live.pid);process.kill(live.pid);}}
});

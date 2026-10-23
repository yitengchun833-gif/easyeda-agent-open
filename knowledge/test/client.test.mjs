import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, unlink, writeFile } from 'node:fs/promises';
import { createServer } from 'node:net';
import { execFile } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import os from 'node:os';
import path from 'node:path';

async function freePort() {
  const server=createServer();
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});
  const {port}=server.address();
  await new Promise(resolve=>server.close(resolve));
  return port;
}
async function stopped(pid) {
  for(let i=0;i<50;i++) {
    try { process.kill(pid,0); } catch { return; }
    await new Promise(resolve=>setTimeout(resolve,20));
  }
}
async function stop(pid) {
  try {process.kill(pid);}catch{}
  await stopped(pid);
  try {await new Promise(resolve=>execFile('taskkill',['/pid',String(pid),'/t','/f'],resolve));}catch{}
  await stopped(pid);
}

test('two clients wait for the starter to replace a stale descriptor after health is ready', async () => {
  const root=await mkdtemp(path.join(os.tmpdir(),'kb-client-'));
  const port=await freePort();
  process.env.EASYEDA_KNOWLEDGE_ROOT=root;
  process.env.EASYEDA_KNOWLEDGE_PORT=String(port);
  process.env.EASYEDA_KNOWLEDGE_ENABLE_VECTOR='0';
  const {ensureKnowledgeService}=await import('../src/client.mjs?client-race='+Date.now());
  const runtimeFile=path.join(root,'.runtime','service.json');
  let timer, service, published, joined;
  try {
    const starting=ensureKnowledgeService();
    published=await new Promise((resolve,reject)=>{
      const deadline=Date.now()+5000;
      timer=setInterval(async()=>{
        try {
          const value=JSON.parse(await readFile(runtimeFile,'utf8'));
          clearInterval(timer);timer=null;
          await writeFile(runtimeFile,JSON.stringify({...value,instance:'stale-instance'}));
          setTimeout(()=>writeFile(runtimeFile,JSON.stringify(value)).catch(reject),180);
          resolve(value);
        } catch(error) {
          if(error.code!=='ENOENT'){clearInterval(timer);timer=null;reject(error);}
          else if(Date.now()>deadline){clearInterval(timer);timer=null;reject(new Error('service descriptor was not published'));}
        }
      },1);
    });
    const joining=ensureKnowledgeService();
    [service,joined]=await Promise.all([starting,joining]);
    assert.equal(service.instance,published.instance);
    assert.equal(joined.instance,service.instance);
    assert.notEqual(service.instance,'stale-instance');
    await writeFile(runtimeFile,JSON.stringify({...service,instance:'stale-instance-2'}));
    const lockPath=path.join(root,'.runtime','start.lock');
    await writeFile(lockPath,'');
    setTimeout(()=>writeFile(lockPath,JSON.stringify({pid:process.pid,servicePid:service.pid})).catch(()=>{}),40);
    setTimeout(()=>writeFile(runtimeFile,JSON.stringify(service)).catch(()=>{}),180);
    const afterBlankLock=await ensureKnowledgeService();
    assert.equal(afterBlankLock.instance,service.instance);
    await writeFile(runtimeFile,'{');
    setTimeout(()=>writeFile(runtimeFile,JSON.stringify(service)).catch(()=>{}),80);
    assert.equal((await ensureKnowledgeService()).pid,service.pid);
    await unlink(lockPath);
    await writeFile(runtimeFile,'{');
    await assert.rejects(ensureKnowledgeService(),SyntaxError);
  } finally {
    if(timer)clearInterval(timer);
    const pid=service?.pid||published?.pid;
    if(pid)await stop(pid);
    await rm(root,{recursive:true,force:true});
  }
});

test('daemon connection helper starts the original service and repeated connects reuse it', async () => {
  const root=await mkdtemp(path.join(os.tmpdir(),'kb-connect-'));
  const env={...process.env,EASYEDA_KNOWLEDGE_ROOT:root,EASYEDA_KNOWLEDGE_PORT:String(await freePort()),EASYEDA_KNOWLEDGE_ENABLE_VECTOR:'0'};
  const helper=new URL('../src/connect.mjs',import.meta.url);
  let service;
  const connect=()=>new Promise((resolve,reject)=>execFile(process.execPath,[fileURLToPath(helper)],{env},(error,stdout)=>error?reject(error):resolve(JSON.parse(stdout))));
  try {
    const first=await connect();
    service=JSON.parse(await readFile(path.join(root,'.runtime','service.json'),'utf8'));
    const next=await connect();
    const after=JSON.parse(await readFile(path.join(root,'.runtime','service.json'),'utf8'));
    assert.equal(first.baseUrl,service.baseUrl);
    assert.equal(first.token,service.token);
    assert.deepEqual(next,first);
    assert.equal(after.pid,service.pid);
    assert.equal(after.instance,service.instance);
    assert.equal(await readFile(path.join(root,'.runtime','node-path'),'utf8'),process.execPath);
  } finally {
    if(service?.pid)await stop(service.pid);
    await rm(root,{recursive:true,force:true});
  }
});

import { spawn } from 'node:child_process';
import { mkdir, readFile, open, rename, unlink, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const packageRoot=fileURLToPath(new URL('../',import.meta.url));
const projectRoot=path.resolve(packageRoot,path.basename(path.dirname(packageRoot))==='source'?'../../..':'../..');
const root=path.resolve(process.env.EASYEDA_KNOWLEDGE_ROOT||path.join(projectRoot,'knowledge'));
const runtimeDir=path.join(root,'.runtime');
const runtimeFile=path.join(runtimeDir,'service.json');
const endpointIdentityError=instance=>Object.assign(new Error('Knowledge endpoint identity mismatch'),{instanceMismatch:instance});
async function existing(startingPid) {
  let descriptor;try{descriptor=JSON.parse(await readFile(runtimeFile,'utf8'));}catch(error){if(error.code==='ENOENT'||error instanceof SyntaxError&&Number.isInteger(startingPid)&&startingPid>0)return null;throw error;}
  if(descriptor.service!=='easyeda-knowledge'||path.resolve(descriptor.root)!==root)throw new Error('Knowledge runtime identity mismatch');
  try {
    const res=await fetch(descriptor.baseUrl+'/health',{signal:AbortSignal.timeout(1000)});
    const info=await res.json();
    if(info.service!=='easyeda-knowledge'||info.root!==descriptor.root)throw endpointIdentityError(false);
    if(info.instance!==descriptor.instance) {
      if(Number.isInteger(startingPid)&&startingPid>0&&info.pid===startingPid)return null;
      throw endpointIdentityError(true);
    }
    return descriptor;
  } catch(error) {if(error.cause?.code==='ECONNREFUSED')return null;throw error;}
}
async function liveStarter(lockPath) {
  let text;try{text=await readFile(lockPath,'utf8');}catch(error){if(error.code==='ENOENT')return null;throw error;}
  let owner;try{owner=JSON.parse(text);}catch{return {pending:true};}
  try{process.kill(owner.pid,0);}catch{return null;}
  return owner;
}
async function activeOrStarting(lockPath) {
  const owner=await liveStarter(lockPath);
  try{return await existing(owner?.servicePid);}
  catch(error) {
    if(owner&&(error instanceof SyntaxError||error.instanceMismatch&&!owner.servicePid))return null;
    throw error;
  }
}
export async function ensureKnowledgeService() {
  await mkdir(runtimeDir,{recursive:true});
  // Reuse this local runtime when the Go daemon is started without MCP/Node on PATH.
  const nodePathFile=path.join(runtimeDir,'node-path');
  if(await readFile(nodePathFile,'utf8').catch(error=>{if(error.code==='ENOENT')return '';throw error;})!==process.execPath)
    await writeFile(nodePathFile,process.execPath);
  const lockPath=path.join(runtimeDir,'start.lock');
  const active=await activeOrStarting(lockPath);if(active)return active;
  let lock;
  try {lock=await open(lockPath,'wx');await lock.writeFile(JSON.stringify({pid:process.pid}));}
  catch(error) {
    if(error.code!=='EEXIST')throw error;
    const owner=await liveStarter(lockPath);
    if(!owner){await unlink(lockPath);return ensureKnowledgeService();}
    for(let i=0;i<100;i++){await new Promise(resolve=>setTimeout(resolve,100));const value=await activeOrStarting(lockPath);if(value)return value;}
    throw new Error('Another client is still starting the knowledge service');
  }
  try {
    const already=await activeOrStarting(lockPath);if(already)return already;
    const log=await open(path.join(runtimeDir,'knowledge-stderr.log'),'a');
    const child=spawn(process.execPath,[fileURLToPath(new URL('./service.mjs',import.meta.url))],{windowsHide:true,detached:true,stdio:['ignore',log.fd,log.fd],env:{...process.env,EASYEDA_KNOWLEDGE_ROOT:root},cwd:runtimeDir});
    let startupError;child.once('error',error=>{startupError=error;});child.unref();await log.close();await lock.close();lock=null;
    const lockTemp=lockPath+'.'+process.pid+'.tmp';await writeFile(lockTemp,JSON.stringify({pid:process.pid,servicePid:child.pid}));await rename(lockTemp,lockPath);
    for(let i=0;i<200;i++) {
      if(startupError)throw startupError;
      if(child.exitCode!==null)throw new Error('Knowledge process exited; see knowledge-stderr.log');
      const value=await existing(child.pid);if(value)return value;
      await new Promise(resolve=>setTimeout(resolve,100));
    }
    throw new Error('Knowledge service startup timed out; see knowledge-stderr.log');
  } finally {if(lock)await lock.close();await unlink(lockPath);}
}
export async function knowledgeRequest(route,{method='GET',body}={}) {
  if(!route.startsWith('/api/'))throw new Error('Invalid knowledge route');
  const service=await ensureKnowledgeService();
  const res=await fetch(service.baseUrl+route,{method,headers:{...(body?{'content-type':'application/json'}:{}),'X-Knowledge-Token':service.token},body:body?JSON.stringify(body):undefined,signal:AbortSignal.timeout(60000)});
  const value=await res.json();
  if(!res.ok)throw Object.assign(new Error(typeof value.error==='string'?value.error:value.error?.message||'Knowledge request failed'),{code:value.code||value.error?.code,status:res.status,...(value.currentCandidate!==undefined?{currentCandidate:value.currentCandidate}:{})});
  return value;
}

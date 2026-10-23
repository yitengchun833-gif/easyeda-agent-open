import {readFile,access} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {isAbsolute} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {toMcpResult} from './core.mjs';

const execute=promisify(execFile);
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
export const pcbConstraintDefinition={
  name:'easyeda_pcb_constraints',
  description:'Run the existing three-type PCB Constraint Compiler: dry-run previews native rules/bindings; apply writes only a ready reviewed IR; review reads current bindings, native DRC, trace corners and declared professional checks. Explicit window/IR target, new evidence output and exact input hashes required. No navigation, layout, routing, save or retry. Fail/unknown is never board acceptance.',
  inputSchema:{type:'object',properties:{
    operation:{type:'string',enum:['dry-run','apply','review']},
    window:{type:'string',minLength:1},
    irFile:{type:'string',description:'Absolute pcb-constraint/v0 JSON path.'},
    checksFile:{type:'string',description:'Absolute pcb-professional-checks/v0 JSON path; required for review.'},
    outputFile:{type:'string',description:'Absolute new raw evidence JSON path; existing files are refused.'},
    expectedIrSha256:{type:'string',pattern:'^[a-f0-9]{64}$'},
    expectedChecksSha256:{type:'string',pattern:'^[a-f0-9]{64}$'}
  },required:['operation','window','irFile','outputFile','expectedIrSha256'],additionalProperties:false},
  annotations:{readOnlyHint:false,destructiveHint:false,idempotentHint:false,openWorldHint:false}
};

export async function pcbConstraintTool(input,run=execute) {
  if(!['dry-run','apply','review'].includes(input.operation)||typeof input.window!=='string'||!input.window.trim())throw Error('Explicit operation and Connector window required');
  for(const key of ['irFile','outputFile',...(input.checksFile?['checksFile']:[])])
    if(typeof input[key]!=='string'||!isAbsolute(input[key]))throw Error('Absolute artifact paths required: '+key);
  if(input.operation==='review'&&!input.checksFile)throw Error('Post-route review requires explicit professional checks');
  if(existsSync(input.outputFile))throw Error('Evidence output already exists; no overwrite or native call');
  for(const [pathKey,hashKey] of [['irFile','expectedIrSha256'],...(input.checksFile?[['checksFile','expectedChecksSha256']]:[])]) {
    if(!/^[a-f0-9]{64}$/.test(input[hashKey]||'')||hash(await readFile(input[pathKey]))!==input[hashKey])
      throw Error('Reviewed input hash missing or changed: '+pathKey);
  }
  const runner=['../../tools/pcb_constraint_apply.mjs','../../../../tools/pcb_constraint_apply.mjs']
    .map(p=>fileURLToPath(new URL(p,import.meta.url))).find(p=>existsSync(p));
  if(!runner)throw Error('Companion PCB compiler not packaged; no native call');
  const pcbBinary=fileURLToPath(new URL('../../bin/easyeda-pcb-review.exe',import.meta.url));
  await access(pcbBinary);
  const args=[runner,'--ir',input.irFile,'--window',input.window,'--output',input.outputFile,'--expected-ir-sha256',input.expectedIrSha256];
  if(input.operation!=='apply')args.push(input.operation==='review'?'--review':'--dry-run');
  if(input.checksFile)args.push('--checks',input.checksFile,'--expected-checks-sha256',input.expectedChecksSha256);
  let receipt;
  try {
    receipt=await run(process.env.EASYEDA_NODE_BIN||process.execPath,args,{encoding:'utf8',windowsHide:true,
      env:{...process.env,EASYEDA_BIN:pcbBinary},maxBuffer:32*1024*1024,timeout:310000,signal:input.signal});
  }catch(error) {
    // A failing checker exits 1 with a real JSON result; transport errors remain unknown, never retried.
    if(error.code===1&&error.stdout)receipt={stdout:error.stdout,stderr:error.stderr};
    else return toMcpResult({ok:false,error:{status:'unknown',message:'PCB execution uncertain; inspect the saved output before continuing',detail:String(error.message),evidence:input.outputFile}});
  }
  let result;try{result=JSON.parse(receipt.stdout);}catch{return toMcpResult({ok:false,error:{status:'unknown',message:'Invalid compiler response',evidence:input.outputFile}});}
  if(!['ready','pass','partial','fail','unknown','unsupported_in_constraint_v0'].includes(result?.status))throw Error('Invalid PCB compiler status');
  return toMcpResult({ok:true,result:{...result,operation:input.operation,compiler:runner,cli:pcbBinary}});
}

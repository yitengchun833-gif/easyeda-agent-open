import {readFileSync,writeFileSync,unlinkSync} from 'node:fs';
import {resolve,dirname,join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {isDeepStrictEqual} from 'node:util';
import {buildConstraintScript,compilePcbConstraints,samePcbSnapshot} from './pcb_constraint_compiler.mjs';
import {inspectProfessionalChecks} from './pcb_professional_checks.mjs';

export function describeDryRun(ir,before) {
  const plan=compilePcbConstraints(ir,before);
  const constraints=(ir.constraints||[]).map((c,index)=>({index,type:c?.type,target:c?.target,
    ...compilePcbConstraints({...ir,constraints:[c]},before)}));
  const rulesToCreate=[],bindings=[];
  for(const entry of constraints.filter(c=>['ready','partial'].includes(c.status))) for(const c of entry.constraints) {
    for(const binding of c.bindings) {
      bindings.push({...binding,type:c.type,constraintIndex:entry.index});
      if(!before.configuration.config[c.category][c.field][binding.ruleName] && !rulesToCreate.some(r=>r.category===c.category&&r.field===c.field&&r.name===binding.ruleName))
        rulesToCreate.push({category:c.category,field:c.field,name:binding.ruleName,values:c.effectiveValues,
          dryRunOnly:c.dryRunOnly,unspecifiedFields:c.unspecifiedFields,retainedNativeValues:c.retainedNativeValues,changedCells:c.changedCells});
    }
  }
  return {status:plan.status,reason:plan.reason??null,plan,
    preview:{rulesToCreate,bindings,conflicts:constraints.filter(c=>c.status==='fail').map(c=>({index:c.index,type:c.type,reason:c.reason})),
      unsupported:constraints.filter(c=>c.status==='unsupported_in_constraint_v0').map(c=>({index:c.index,type:c.type,status:c.status,reason:c.reason})),
      unknown:constraints.filter(c=>c.status==='unknown').map(c=>({index:c.index,type:c.type,reason:c.reason})),
      unresolved:constraints.filter(c=>c.status==='partial').map(c=>({index:c.index,type:c.type,reason:c.reason,
        unspecifiedFields:c.constraints[0].unspecifiedFields,retainedNativeValues:c.constraints[0].retainedNativeValues,changedCells:c.constraints[0].changedCells})),
      batchIssue:plan.status==='ready'?null:plan.reason,candidateOnly:plan.status!=='ready',rulesApplied:false,drcChecked:false}};
}

export function reviewGeometry(geometry,snapshot) {
  if(!geometry||!Array.isArray(geometry.findings)||!Array.isArray(geometry.limitations)||
    !['rightAngles','acuteAngles','junctionAngles','errors'].every(k=>Number.isInteger(geometry.summary?.[k])&&geometry.summary[k]>=0)||
    geometry.trackCount!==snapshot.lines.length||geometry.viaCount!==snapshot.vias.length||geometry.padCount!==snapshot.pads.length)
    return 'unknown';
  for(const [field,type]of [['rightAngles','right-angle'],['acuteAngles','acute-angle'],['junctionAngles','junction-angle']])
    if(geometry.summary[field]!==geometry.findings.filter(f=>f?.type===type).length)return 'unknown';
  if(geometry.summary.errors!==geometry.findings.filter(f=>f?.level==='ERROR').length)return 'unknown';
  return geometry.summary.rightAngles||geometry.summary.acuteAngles||geometry.summary.errors?'fail':'pass';
}

export function applyConstraintFile({irFile,window,port=60932,dryRun=false,review=false,saveReviewed=false,checksFile,outputFile,expectedIrSha256,expectedChecksSha256}) {
  if(dryRun&&review)throw Error('Choose dry-run or post-route review, not both');
  if(saveReviewed&&(!review||dryRun||!checksFile||!outputFile||
    !/^[a-f0-9]{64}$/.test(expectedIrSha256||'')||!/^[a-f0-9]{64}$/.test(expectedChecksSha256||'')))
    throw Error('Saving requires review, professional checks, exact input hashes and a new explicit evidence output');
  const bytes=readFileSync(irFile),ir=JSON.parse(bytes.toString('utf8'));
  const checksBytes=checksFile?readFileSync(checksFile):null;
  const checks=checksBytes?JSON.parse(checksBytes.toString('utf8')):null;
  if(expectedIrSha256&&createHash('sha256').update(bytes).digest('hex')!==expectedIrSha256)throw Error('IR changed since reviewed bytes; no native call');
  if(expectedChecksSha256&&(!checksBytes||createHash('sha256').update(checksBytes).digest('hex')!==expectedChecksSha256))throw Error('Professional checks changed since reviewed bytes; no native call');
  if(checks&&!isDeepStrictEqual(checks.target,ir.target))throw Error('Professional checks target differs from IR');
  const preflight=compilePcbConstraints(ir,null);
  if(preflight.status==='fail' || (!dryRun&&preflight.status==='unsupported_in_constraint_v0')) return preflight;
  if(!window)throw Error('Explicit Connector window required');
  const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
  const out=outputFile?resolve(outputFile):join(root,'evidence','pcb-constraint-v0-'+new Date().toISOString().replace(/[:.]/g,'-')+'.json');
  if(out===resolve(irFile))throw Error('Evidence output cannot overwrite IR');
  if(checksFile&&out===resolve(checksFile))throw Error('Evidence output cannot overwrite professional checks');
  const temp=out+'.exec.js';
  const evidence={irFile:resolve(irFile),irSha256:createHash('sha256').update(bytes).digest('hex'),ir,window,dryRun,review,status:'unknown',receipts:[]};
  if(saveReviewed)evidence.saveRequested=true;
  if(checks)evidence.professionalSpec={path:resolve(checksFile),sha256:createHash('sha256').update(checksBytes).digest('hex'),value:checks};
  writeFileSync(out,JSON.stringify(evidence,null,2),{encoding:'utf8',flag:'wx'});
  let tempCreated=false;
  const save=()=>writeFileSync(out,JSON.stringify(evidence,null,2),'utf8');
  const call=input=>{
    writeFileSync(temp,buildConstraintScript(input),{encoding:'utf8',flag:tempCreated?'w':'wx'});tempCreated=true;
    const r=spawnSync(process.env.EASYEDA_BIN||'easyeda.exe',['--ports',`${port}-${port}`,'debug','exec','--window',window,'--timeout','60','--file',temp],{encoding:'utf8',timeout:70000,maxBuffer:32*1024*1024});
    let receipt;
    try{receipt=JSON.parse(r.stdout);}catch{receipt={ok:false,stdout:r.stdout,stderr:r.stderr,error:String(r.error||'Invalid JSON')};}
    evidence.receipts.push(receipt);save();
    if(r.status!==0||receipt.ok!==true)throw Error('Native execution uncertain; evidence saved; no retry');
    return receipt.result.value;
  };
  try {
    const read=call({mode:'read',ir,onlyRules:dryRun,allObjects:review});evidence.before=read.before;evidence.identity=read.identity;save();
    const described=describeDryRun(ir,read.before);
    evidence.plan=described.plan;evidence.preview=described.preview;save();
    if(evidence.plan.status!=='ready'||dryRun) evidence.status=evidence.plan.status;
    else {
      evidence.result=call({mode:review?'review':'apply',ir,before:read.before,allObjects:review});evidence.status=evidence.result.status;
      if(review&&evidence.result.configurationReadback&&evidence.result.netRulesReadback) {
        const g=spawnSync(process.env.EASYEDA_BIN||'easyeda.exe',['--ports',`${port}-${port}`,'pcb','check','--window',window,'--json'],{encoding:'utf8',timeout:70000,maxBuffer:32*1024*1024});
        evidence.geometryReceipt={exitCode:g.status,stdout:g.stdout,stderr:g.stderr,error:g.error?String(g.error):null};save();
        try{evidence.geometry=JSON.parse(g.stdout);}catch{evidence.geometry=null;}
        const after=call({mode:'read',ir,allObjects:true});evidence.reviewAfter=after;
        const geometry=evidence.geometry;
        evidence.geometryStatus=g.status===0?reviewGeometry(geometry,after.before):'unknown';
        evidence.snapshotStable=samePcbSnapshot(evidence.result.after,after.before,ir.target,isDeepStrictEqual);
        evidence.rawSnapshotsEqual=isDeepStrictEqual(evidence.result.after,after.before);
        if(checks)evidence.professional=inspectProfessionalChecks(checks,after.before);
        if(!evidence.snapshotStable){evidence.status='unknown';evidence.reason='PCB changed during post-route review; read back before continuing';}
        else if(evidence.geometryStatus!=='pass') {evidence.status=evidence.geometryStatus;evidence.reason=evidence.geometryStatus==='fail'?'Post-route geometry requires correction':'Geometry checker coverage unavailable; update the CLI, do not infer pass';}
        else if(checks&&evidence.professional.status!=='pass') {evidence.status=evidence.professional.status;evidence.reason='Required professional checks did not pass';}
      }
    }
    if(saveReviewed) {
      evidence.reviewStatus=evidence.status;
      if(evidence.status==='pass'&&evidence.result?.boardStatus==='pass'&&evidence.geometryStatus==='pass'&&
        evidence.professional?.status==='pass'&&evidence.snapshotStable===true) {
        // Persist intent before dispatch: an uncertain save is inspected, never automatically replayed.
        evidence.saveAttempted=true;save();
        evidence.saveResult=call({mode:'save-reviewed',ir,before:evidence.reviewAfter.before,allObjects:true});
        evidence.status=evidence.saveResult.saved===true&&evidence.saveResult.status==='pass'&&evidence.saveResult.snapshotStable===true?'pass':'unknown';
        if(evidence.status!=='pass')evidence.reason=evidence.saveResult.reason||'Save or post-save state not confirmed; inspect before continuing';
      } else {
        evidence.saveAttempted=false;
        if(evidence.status==='pass')evidence.status=evidence.result?.boardStatus==='fail'?'fail':'unknown';
        evidence.reason=evidence.reason||'Required native DRC, geometry, professional checks or stable readback did not pass; no save';
      }
    }
    save();return {status:evidence.status,evidence:out,reason:evidence.reason||evidence.result?.reason||evidence.plan.reason,preview:evidence.preview,
      ...(saveReviewed?{saved:evidence.saveResult?.saved===true}: {})};
  }catch(e){evidence.status='unknown';evidence.reason=String(e);save();return {status:'unknown',evidence:out,reason:evidence.reason};}
  finally{if(tempCreated)try{unlinkSync(temp);}catch(e){if(e.code!=='ENOENT')throw e;}}
}

if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  const args=process.argv.slice(2),value=key=>args[args.indexOf(key)+1];
  try {
    if(!args.includes('--ir')||!args.includes('--window'))throw Error('Usage: node tools/pcb_constraint_apply.mjs --ir <JSON> --window <ID> [--dry-run|--review] [--checks <JSON>] [--save-reviewed with --review, --checks, --output and both input hashes]');
    const result=applyConstraintFile({irFile:value('--ir'),window:value('--window'),dryRun:args.includes('--dry-run'),review:args.includes('--review'),saveReviewed:args.includes('--save-reviewed'),checksFile:args.includes('--checks')?value('--checks'):undefined,outputFile:args.includes('--output')?value('--output'):undefined,
      expectedIrSha256:args.includes('--expected-ir-sha256')?value('--expected-ir-sha256'):undefined,expectedChecksSha256:args.includes('--expected-checks-sha256')?value('--expected-checks-sha256'):undefined});
    console.log(JSON.stringify(result));
    if(!['ready','pass'].includes(result.status))process.exitCode=1;
  }catch(e){console.error(String(e));process.exitCode=1;}
}

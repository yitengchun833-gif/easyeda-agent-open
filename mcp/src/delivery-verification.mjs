import {createHash} from 'node:crypto';
import {mkdtemp,writeFile,rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {snapshotData} from './project-files.mjs';
import {fullInventory} from './preservation.mjs';

const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
const sameTarget=(actual,target)=>actual?.projectUuid===target.projectUuid&&actual?.documentUuid===target.documentUuid;
export function nativeObservation(receipt,target){
  const {snapshot,context}=snapshotData(receipt),scope=snapshot.readScope;
  fullInventory(snapshot);
  if(!sameTarget(context,target)||scope?.complete!==true||scope.concurrentChange!==false||scope.allPages===true
    ||scope.primitiveIds?.length||scope.missingIds?.length||snapshot.partial===true
    ||snapshot.components.some(part=>part.componentType==='part'&&(part.netlistAvailable!==true||part.pinsAvailable!==true)))
    throw new Error('Delivery needs a complete fresh bound native geometry/netlist inventory');
  return {...snapshot,...target};
}

// This adapter calls the existing Go canonical + drawing diff, never a second comparator or an EDA mutation.
export async function nativeDesignDiff(before,after,target,run){
  let directory;
  try{
    const inputs=[nativeObservation(before,target),nativeObservation(after,target)].map(value=>JSON.stringify(value));
    directory=await mkdtemp(path.join(os.tmpdir(),'easyeda-design-diff-'));
    const files=inputs.map((_,i)=>path.join(directory,`${i}.json`));
    for(let i=0;i<files.length;i++)await writeFile(files[i],inputs[i]);
    const result=await run(['sch','design-diff',...files]);
    return {status:result?.ok&&['synced','different'].includes(result.result?.status)&&result.result.coverage?.drawingCompared===true?result.result.status:'unknown',
      inputSha256:inputs.map(hash),checker:result};
  }catch(error){return {status:'unknown',reason:error.message};}
  finally{if(directory)await rm(directory,{recursive:true,force:true});}
}

// Only the current daemon's successful native close/open receipts can establish a reopen sequence.
export function reopenProof({delivery,state,readback,target,window,closeRequestId,openRequestId}){
  const unknown=reason=>({status:'unknown',reason});
  const saved=delivery.receipt?.finishReceipt?.structuredContent;
  if(delivery.receipt?.saved!==true||saved?.ok!==true||saved.result?.saved!==true||!sameTarget(saved.context,target))return unknown('Native save is unknown');
  const saveId=saved.requestId??saved.id,readId=readback.structuredContent?.requestId??readback.structuredContent?.id;
  const ids=[saveId,closeRequestId,openRequestId,readId];
  if(ids.some(id=>typeof id!=='string'||!id)||new Set(ids).size!==4)return unknown('Distinct save/close/open/readback request IDs are required');
  const rows=ids.map(id=>state?.receipts?.find(row=>row.requestId===id));
  if(rows.some((row,index)=>!row||row.historical===true||row.windowId!==window
    ||(index!==1&&(row.projectUuid!==target.projectUuid||row.documentUuid!==target.documentUuid))
    ||row.status!=='action_finished'||row.errorCode||row.error))return unknown('Successful current native receipts for this window/target are unavailable');
  const [save,close,open,read]=rows;
  if(close.closedDocumentUuid!==target.documentUuid||!sameTarget(close.startedContext,target))return unknown('Close target is not proven by its before-context and native closed UUID');
  if(save.action!=='schematic.save'||close.action!=='document.close'||!['document.open','schematic.page.open'].includes(open.action)
    ||read.action!=='schematic.components.list')return unknown('Reopen action identities do not match');
  const start=row=>Date.parse(row.startedAt),end=row=>start(row)+row.durationMs;
  if(rows.some(row=>!Number.isFinite(start(row))||!Number.isFinite(row.durationMs)||row.durationMs<0)
    ||!Number.isFinite(Date.parse(saved.createdAt))||start(close)<Date.parse(saved.createdAt)
    ||start(close)<end(save)||start(open)<end(close)||start(read)<end(open))return unknown('Native save → close → open → read sequence is not proven');
  const doc=state.documents?.find(doc=>doc.windowId===window&&sameTarget(doc.context,target));
  if(!doc||doc.generation!==read.generation||doc.mutationAttemptedSinceConfirmedSave===true)return unknown('Current generation changed or unsaved writes remain');
  if(state.receipts.some(row=>row.windowId===window&&row.contentWrite===true&&(['queued','running','resuming'].includes(row.status)
    ||start(row)>=start(save)&&!['action_finished','saved'].includes(row.status)||start(row)>=end(open))))return unknown('Content writes are pending, uncertain or occurred after reopen');
  return {status:'verified',requestIds:{save:saveId,close:closeRequestId,open:openRequestId,readback:readId},generation:doc.generation,instance:state.instance};
}

import assert from 'node:assert/strict';
import {test} from 'node:test';
import {createServer} from 'node:http';
import {publicReceipt,runtimeRequest,snapshotSteps} from '../src/open.mjs';
test('snapshot profiles read only requested facts without changing the full default',()=>{
  const geometry=snapshotSteps('schematic',false,false,false,{primitiveIds:['r'],profile:'geometry',includeTexts:true})[1].payload;
  assert.equal(geometry.includePinNets,false);assert.equal(geometry.includeDeviceIdentity,false);
  assert.equal(geometry.includeBBox,true);assert.equal(geometry.includeWires,true);assert.equal(geometry.includeTexts,true);
  const electrical=snapshotSteps('schematic',false,false,false,{profile:'electrical'})[1].payload;
  assert.equal(electrical.includePinNets,true);assert.equal(electrical.includeBBox,false);assert.equal(electrical.includeWires,false);
  assert.equal(snapshotSteps('schematic',false,true)[1].payload.includeDeviceIdentity,true);
  assert.throws(()=>snapshotSteps('schematic',false,true,false,{profile:'typo'}),/Unknown/);
});
test('private net indexes/readbacks do not expand model context',()=>assert.deepEqual(publicReceipt({result:{_readback:[1],_baseline:{},_netIndex:{},_scene:{},rules:{status:'pass'},primitiveId:'r'}}),{result:{rules:{status:'pass'},primitiveId:'r'}}));
test('custom component properties remain intact',()=>assert.deepEqual(publicReceipt({otherProperty:{_scene:'user value'}}),{otherProperty:{_scene:'user value'}}));
test('scoped snapshots use component ids and do not request all page primitives',()=>{const read=snapshotSteps('schematic',false,true,false,{primitiveIds:['r'],includeTexts:true})[1];assert.deepEqual(read.payload.primitiveIds,['r']);assert.equal(read.payload.includePagePrimitives,undefined);assert.equal(read.payload.includeTexts,true)});
test('runtime state consumes cache endpoint once, control uses shared server endpoint',async t=>{const calls=[];const server=createServer(async(req,res)=>{let body='';for await(const chunk of req)body+=chunk;calls.push({url:req.url,header:req.headers['x-easyeda-control'],body});res.setHeader('content-type','application/json');res.end(JSON.stringify({source:'daemon-receipts',edaReads:0}))});await new Promise(r=>server.listen(0,'127.0.0.1',r));t.after(()=>new Promise(r=>server.close(r)));const url=`http://127.0.0.1:${server.address().port}`;assert.equal((await runtimeRequest({window:'w'},url)).isError,false);assert.equal((await runtimeRequest({operation:'pause',window:'w'},url)).isError,false);assert.equal(calls.length,2);assert.match(calls[0].url,/^\/runtime\/state/);assert.equal(new URL(calls[0].url,'http://localhost').searchParams.get('summary'),'1');assert.equal(calls[1].url,'/runtime/control');assert.equal(calls[1].header,'1');assert.equal(JSON.parse(calls[1].body).operation,'pause')});

test('task intent is forwarded once with target and compare revision',async t=>{
  let actual;
  const server=createServer(async(req,res)=>{let body='';for await(const part of req)body+=part;actual=JSON.parse(body);res.setHeader('content-type','application/json');res.end(JSON.stringify({ok:true,persisted:true,taskIsIntent:true}))});
  await new Promise(r=>server.listen(0,'127.0.0.1',r));t.after(()=>new Promise(r=>server.close(r)));
  const input={operation:'task_update',window:'w',target:{projectUuid:'p',documentUuid:'d'},expectedRevision:3,task:{goal:'Move R2 label',primitiveIds:['r2'],remaining:['Inspect local result']}};
  const result=await runtimeRequest(input,`http://127.0.0.1:${server.address().port}`);
  assert.equal(result.isError,false);assert.deepEqual(actual,input);
});

/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, beforeEach } from 'node:test';
import { runAction } from './actions';
import { ActionQueue, type Execution } from './action-queue';

const oldDevice='a'.repeat(32),newDevice='b'.repeat(32),link='source-link';
const primitive=(state:Record<string,any>):any=>new Proxy({}, {get:(_,key)=>String(key).startsWith('getState_')?()=>state[String(key).slice(9)]:undefined});
beforeEach(t=>{
  const globals=globalThis as any,old={eda:globals.eda,window:globals.window,EDMT_EditorDocumentType:globals.EDMT_EditorDocumentType};
  (t as import('node:test').TestContext).after(()=>Object.assign(globals,old));
});
function fixture(t:import('node:test').TestContext,options:{uid?:unknown;attributeConflict?:boolean;duplicate?:boolean;netlistConflict?:boolean;
  modelConflict?:boolean;pinConflict?:boolean;wrongProject?:boolean;sourceDrift?:boolean;restoreThrows?:boolean;finalReadThrows?:boolean;deleteNoop?:boolean;
  createFails?:'throw'|'undefined';rollbackRestoreThrows?:boolean;rollbackUnpublished?:boolean;rollbackReadThrows?:boolean;rollbackFieldMismatch?:boolean}={}){
  const globals=globalThis as any;
  globals.EDMT_EditorDocumentType={HOME:0,BLANK:1,SCHEMATIC_PAGE:2,PCB:3};
  const doc={uuid:'page',tabId:'page@project',documentType:2,profileSetting:{readonlyMode:false}};
  globals.window={SCH:{docMemoryManager:{getActiveDoc:()=>doc},app:{actionRunner:{running:false,currentAction:null}}}};
  const original={PrimitiveId:'old',ComponentType:'part',Designator:'R1',Name:'Old',X:0,Y:0,Rotation:0,Mirror:false,Net:'',SubPartName:'A',
    AddIntoBom:true,AddIntoPcb:true,UniqueId:Object.hasOwn(options,'uid')?options.uid:link,Manufacturer:'',ManufacturerId:'',Supplier:'LCSC',SupplierId:'C1',
    Component:{uuid:oldDevice,libraryUuid:'lib'},Symbol:{uuid:'sym',libraryUuid:'lib'},Footprint:{uuid:'fp',libraryUuid:'lib',name:'FP'},OtherProperty:{}};
  const states=new Map<string,Record<string,any>>([['old',original]]),writes:string[]=[],reads:string[]=[],createdRecords=new Map<string,Record<string,any>>();
  if(options.duplicate)states.set('duplicate',{...original,PrimitiveId:'duplicate'});
  let netlistReads=0,createCount=0;
  const pins=()=>['1','2'].map((n,i)=>primitive({PinNumber:n,PinName:n,X:i*10,Y:0}));
  globals.eda={
    dmt_Project:{getCurrentProjectInfo:async()=>({uuid:'project'})},dmt_SelectControl:{getCurrentDocumentInfo:async()=>({...doc})},
    lib_Device:{getByLcscIds:async()=>{
      if(options.sourceDrift)original.UniqueId='changed-link';
      return [{uuid:oldDevice,libraryUuid:'lib',footprint:original.Footprint}];
    }},
    sch_ManufactureData:{getNetlistFile:async()=>{
      netlistReads++;
      const uid=options.netlistConflict?'official-other':link;
      return new File([JSON.stringify({projectId:options.wrongProject?'other':'project',components:{[uid]:{props:{'Unique ID':uid,Designator:'R1',
        Device:options.modelConflict?'other-device':oldDevice,Symbol:'sym',Footprint:'fp','Channel ID':'channel-1'},
        pinInfoMap:options.pinConflict?{'1':{number:'1'}}:{'1':{number:'1'},'2':{number:'2'}}}}})],'current.enet');
    }},
    sch_PrimitiveAttribute:{getAll:async(id:string)=>{
      const s=states.get(id);return s?[primitive({PrimitiveId:id+'-uid',ParentPrimitiveId:id,Key:'Unique ID',Value:options.attributeConflict?'conflict':s.UniqueId})]:[];
    }},
    sch_PrimitiveComponent:{
      get:async(id:string)=>{
        reads.push(id);
        if(id==='new'&&options.finalReadThrows)throw new Error('fresh SDK read failed');
        if(id==='rollback'&&options.rollbackReadThrows)throw new Error('rollback SDK read failed');
        const s=states.get(id);return s?primitive(s):undefined;
      },getAll:async()=>[...states.values()].map(primitive),getAllPinsByPrimitiveId:async()=>pins(),
      delete:async(id:string)=>{writes.push('delete:'+id);if(!options.deleteNoop)states.delete(id);return !options.deleteNoop;},
      create:async(dev:{uuid:string})=>{
        const id=++createCount===1?'new':'rollback';writes.push('create:'+id);
        if(id==='new'&&options.createFails){if(options.createFails==='throw')throw new Error('original candidate create error');return undefined;}
        const s={...original,PrimitiveId:id,UniqueId:id+'-generated',Designator:'R?',SupplierId:'C2',Component:{uuid:dev.uuid,libraryUuid:'lib'}};
        createdRecords.set(id,s);if(!(id==='rollback'&&options.rollbackUnpublished))states.set(id,s);return primitive(s);
      },
      modify:async(id:string,props:Record<string,unknown>)=>{
        writes.push('modify:'+id);if(id==='new'&&options.restoreThrows)throw new Error('SDK rejected identity restore');
        if(id==='rollback'&&options.rollbackRestoreThrows)throw new Error('SDK rejected rollback identity restore');
        const s=states.get(id)??createdRecords.get(id)!;for(const[k,v]of Object.entries(props))s[k[0].toUpperCase()+k.slice(1)]=v;
        if(id==='rollback'&&options.rollbackFieldMismatch)s.Mirror=true;
        return primitive(s);
      },
    },
  };
  return {writes,reads,states,netlistReads:()=>netlistReads,run:(execution?:Execution)=>runAction('schematic.component.replace',{primitiveId:'old',deviceUuid:newDevice,deviceLibraryUuid:'lib'},false,execution)};
}

test('replace refuses empty/null native links before any library/canvas write; never fills from netlist',async t=>{
  for(const uid of ['',null,undefined,'   ']){
    const f=fixture(t,{uid});await assert.rejects(f.run(),/non-empty native Unique ID/);
    assert.deepEqual(f.writes,[]);assert.equal(f.netlistReads(),0);assert.equal(f.states.get('old')!.UniqueId,uid);
  }
});

test('replace refuses conflicting or duplicate native identity before mutation',async t=>{
  for(const options of [{attributeConflict:true},{duplicate:true}]){
    const f=fixture(t,options);await assert.rejects(f.run(),/disagree|duplicated/);assert.deepEqual(f.writes,[]);
  }
});

test('replace requires current target-bound UID-keyed manufacturing evidence beyond a matching designator',async t=>{
  for(const options of [{netlistConflict:true},{modelConflict:true},{pinConflict:true},{wrongProject:true}]){
    const f=fixture(t,options);await assert.rejects(f.run(),/manufacturing identity/);
    assert.deepEqual(f.writes,[]);assert.equal(f.netlistReads(),1);
  }
});

test('replace rechecks the source after preparation and refuses drift with zero replacement writes',async t=>{
  const f=fixture(t,{sourceDrift:true});await assert.rejects(f.run(),/changed during replacement preparation/);
  assert.deepEqual(f.writes,[]);
});

test('replace exposes native and current official evidence and proves the fresh replacement link',async t=>{
  const f=fixture(t),response:any=await f.run();
  assert.equal(response.result.identityVerified,true);assert.equal(response.result.partial,undefined);
  assert.equal(response.result.component.uniqueId,link);assert.equal(response.result.identityEvidence.before.rawUniqueId,link);
  assert.equal(response.result.componentObservation,'fresh-native-readback');
  assert.equal(response.result.identityEvidence.manufacturing.mapKey,link);
  assert.equal(response.result.identityEvidence.manufacturing.target.documentUuid,'page');
  assert.equal(response.result.identityEvidence.after.primitiveId,'new');assert.equal(f.states.has('old'),false);
  assert.deepEqual(f.writes,['delete:old','create:new','modify:new']);
});

test('replace cannot pass swallowed restore errors or unknown final reads',async t=>{
  for(const options of [{restoreThrows:true},{finalReadThrows:true}]){
    const f=fixture(t,options),response:any=await f.run();
    assert.equal(response.result.identityVerified,false);assert.equal(response.result.partial,true);assert.equal(response.result.verified,false);
    assert.equal(response.result.previousPrimitiveId,'old');assert.equal(response.result.primitiveId,'new');
    if(options.restoreThrows)assert.equal(response.result.component.uniqueId,'new-generated');
    if(options.finalReadThrows)assert.equal(response.result.componentObservation,'last-write-return-unverified');
    assert.match(response.result.reason,/never replay/);assert.deepEqual(f.writes,['delete:old','create:new','modify:new']);
  }
});

test('deletion guard stops false returns and surviving source instances before any create, reusing one fresh read',async t=>{
  for(const [returned,removed] of [[false,false],[false,true],[true,false],[undefined,false]] as const){
    const f=fixture(t),sdk=(globalThis as any).eda,original=sdk.sch_PrimitiveComponent.delete;
    let readsAtDelete=0;
    sdk.sch_PrimitiveComponent.delete=async(id:string)=>{
      readsAtDelete=f.reads.filter(x=>x==='old').length;
      if(removed)await original(id);else f.writes.push('delete:'+id);
      return returned;
    };
    const response:any=await f.run();
    assert.equal(response.result.phase,'delete-verification');assert.equal(response.result.partial,true);assert.equal(response.result.verified,false);
    assert.equal(response.result.deletion.source.present,!removed);assert.equal(response.result.observations.original.present,!removed);
    assert.deepEqual(response.result.deletion.returned,returned===undefined?{type:'void'}:{type:'boolean',value:returned});
    assert.equal(f.reads.filter(x=>x==='old').length-readsAtDelete,1);assert.deepEqual(f.writes,['delete:old']);
  }
});

test('normal deletion accepts true or void only after a fresh absence observation',async t=>{
  for(const returned of [true,undefined]){
    const f=fixture(t),sdk=(globalThis as any).eda,original=sdk.sch_PrimitiveComponent.delete;
    sdk.sch_PrimitiveComponent.delete=async(id:string)=>{await original(id);return returned;};
    const response:any=await f.run();assert.equal(response.result.identityVerified,true);assert.equal(response.result.partial,undefined);
    assert.equal(response.result.deletion.source.present,false);
    assert.deepEqual(f.writes,['delete:old','create:new','modify:new']);
  }
});

test('deletion guard stops unknown fresh source observations with zero create',async t=>{
  const f=fixture(t),sdk=(globalThis as any).eda,originalDelete=sdk.sch_PrimitiveComponent.delete,originalGet=sdk.sch_PrimitiveComponent.get;
  let attempted=false;
  sdk.sch_PrimitiveComponent.delete=async(id:string)=>{await originalDelete(id);attempted=true;return true;};
  sdk.sch_PrimitiveComponent.get=async(id:string)=>{if(attempted&&id==='old')throw new Error('delete publication unknown');return originalGet(id);};
  const response:any=await f.run();assert.equal(response.result.executionState,'unknown');assert.equal(response.result.partial,true);
  assert.equal(response.result.deletion.source.present,null);assert.deepEqual(f.writes,['delete:old']);
});

test('unrelated component layout modification still accepts an empty raw UID without manufacturing reads',async t=>{
  const f=fixture(t,{uid:''});const response:any=await runAction('schematic.component.modify',{primitiveId:'old',patch:{x:10}},false);
  assert.equal(response.result.component.uniqueId,'');assert.equal(response.result.component.x,10);
  assert.equal(f.netlistReads(),0);assert.deepEqual(f.writes,['modify:old']);
});

test('failed replacement creation reports rollback verified only after fresh original identity/property readback',async t=>{
  for(const createFails of ['throw','undefined'] as const){
    const f=fixture(t,{createFails}),response:any=await f.run();
    assert.equal(response.result.ok,false);assert.equal(response.result.partial,true);assert.equal(response.result.verified,false);
    assert.equal(response.result.rollback.status,'verified');assert.equal(response.result.rollback.verified,true);
    assert.equal(response.result.rollback.primitiveId,'rollback');assert.equal(response.result.rollback.component.uniqueId,link);
    assert.ok(f.reads.includes('rollback'));assert.match(response.result.error.message,createFails==='throw'?/original candidate create error/:/returned no primitive/);
    assert.deepEqual(f.writes,['delete:old','create:new','create:rollback','modify:rollback']);
  }
});

test('rollback restore failure and mismatched restored fields remain failed with the actual recovery ID',async t=>{
  for(const options of [{rollbackRestoreThrows:true},{rollbackFieldMismatch:true}]){
    const f=fixture(t,{createFails:'throw',...options}),response:any=await f.run();
    assert.equal(response.result.rollback.status,'failed');assert.equal(response.result.rollback.verified,false);
    assert.equal(response.result.rollback.primitiveId,'rollback');assert.equal(response.result.partial,true);
    assert.match(response.result.error.message,/original candidate create error/);assert.ok(f.reads.includes('rollback'));
    assert.ok(response.result.rollback.differences.includes(options.rollbackRestoreThrows?'uniqueId':'mirror'));
    if(options.rollbackRestoreThrows)assert.match(response.result.rollback.restoreError.message,/SDK rejected rollback identity restore/);
    assert.deepEqual(f.writes,['delete:old','create:new','create:rollback','modify:rollback']);
  }
});

test('unpublished or unreadable rollback objects are attempted recovery with unknown status, never claimed restored',async t=>{
  for(const options of [{rollbackUnpublished:true},{rollbackReadThrows:true}]){
    const f=fixture(t,{createFails:'throw',...options}),response:any=await f.run();
    assert.equal(response.result.rollback.status,'unknown');assert.equal(response.result.rollback.verified,false);
    assert.equal(response.result.rollback.primitiveId,'rollback');assert.equal(response.result.partial,true);assert.equal(response.result.verified,false);
    assert.match(response.result.error.message,/original candidate create error/);assert.match(response.result.reason,/rollback unknown/);
    assert.ok(f.reads.includes('rollback'));assert.deepEqual(f.writes,['delete:old','create:new','create:rollback','modify:rollback']);
  }
});

test('queue cancellation while replacement preparation awaits cannot resume later writes',async t=>{
  const f=fixture(t),sdk=(globalThis as any).eda,original=sdk.lib_Device.getByLcscIds;
  let release:(()=>void)|undefined,inner:Promise<any>|undefined;
  sdk.lib_Device.getByLcscIds=(...args:any[])=>new Promise(resolve=>{release=()=>resolve(original(...args));});
  const queue=new ActionQueue({graceMs:0}),pending=queue.submit({id:'replace-cancel',timeoutMs:60000,run:execution=>{
    inner=f.run(execution);return inner;
  }});
  for(let i=0;i<20&&!release;i++)await new Promise(setImmediate);
  assert.equal(typeof release,'function');queue.control({operation:'cancel',requestId:'replace-cancel'});
  assert.equal((await pending).status,'error');assert.deepEqual(f.writes,[]);
  release!();await assert.rejects(inner!,/cancel|abort/i);assert.deepEqual(f.writes,[]);
});

test('cancellation at each replacement write boundary stops remaining writes and does not launch rollback',async t=>{
  for(const boundary of ['delete','create','modify']){
    const f=fixture(t),sdk=(globalThis as any).eda,controller=new AbortController(),original=sdk.sch_PrimitiveComponent[boundary];
    sdk.sch_PrimitiveComponent[boundary]=async(...args:any[])=>{
      const value=await original(...args);controller.abort(new Error('cancel after '+boundary));return value;
    };
    const response:any=await f.run({signal:controller.signal,progress:()=>{}});
    assert.equal(response.result.partial,true);assert.equal(response.result.cancelled,true);assert.equal(response.result.rollback.attempted,false);
    assert.equal(response.result.observations.original.present,false);
    assert.deepEqual(f.writes,['delete:old','create:new','modify:new'].slice(0,['delete','create','modify'].indexOf(boundary)+1));
    if(boundary!=='delete'){assert.equal(response.result.attemptedReplacementId,'new');assert.equal(response.result.observations.replacement.present,true);}
  }
});

test('cancellation during supplier lookup also blocks its deferred component write',async t=>{
  const f=fixture(t),sdk=(globalThis as any).eda,controller=new AbortController(),original=sdk.sch_PrimitiveComponent.modify;
  sdk.sch_PrimitiveComponent.modify=async(...args:any[])=>{const value=await original(...args);f.states.get('new')!.SupplierId='pending';return value;};
  sdk.lib_Device.get=async()=>{controller.abort(new Error('cancel supplier lookup'));return {property:{supplierId:'C2'}};};
  const response:any=await f.run({signal:controller.signal,progress:()=>{}});
  assert.equal(response.result.cancelled,true);assert.equal(response.result.partial,true);
  assert.deepEqual(f.writes,['delete:old','create:new','modify:new']);
});

test('delete exception reports fresh source presence or unknown without claiming unchanged or retrying',async t=>{
  for(const mode of ['present','absent','unknown']){
    const f=fixture(t),sdk=(globalThis as any).eda,originalDelete=sdk.sch_PrimitiveComponent.delete,originalGet=sdk.sch_PrimitiveComponent.get;
    let attempted=false;
    sdk.sch_PrimitiveComponent.delete=async(id:string)=>{
      if(mode==='present')f.writes.push('delete:'+id);else await originalDelete(id);
      attempted=true;throw new Error('native delete rejected after attempt');
    };
    sdk.sch_PrimitiveComponent.get=async(id:string)=>{if(attempted&&mode==='unknown')throw new Error('source read unavailable');return originalGet(id);};
    const response:any=await f.run();
    assert.equal(response.result.phase,'delete-original');assert.equal(response.result.partial,true);assert.equal(response.result.verified,false);
    assert.equal(response.result.observations.original.present,mode==='present'?true:mode==='absent'?false:null);
    assert.equal(response.result.executionState,mode==='unknown'?'unknown':'partial');
    assert.match(response.result.error.message,/native delete rejected/);assert.doesNotMatch(response.result.reason,/canvas unchanged/);
    assert.deepEqual(f.writes,['delete:old']);assert.equal(response.result.rollback.attempted,false);
  }
});

test('restore that applies before throwing keeps fresh identity evidence but remains an overall partial failure',async t=>{
  const f=fixture(t),sdk=(globalThis as any).eda,original=sdk.sch_PrimitiveComponent.modify;
  sdk.sch_PrimitiveComponent.modify=async(...args:any[])=>{await original(...args);throw new Error('native restore rejected after applying');};
  const response:any=await f.run();
  assert.equal(response.result.identityVerified,true);assert.equal(response.result.component.uniqueId,link);
  assert.equal(response.result.partial,true);assert.equal(response.result.verified,false);
  assert.match(response.result.restoreError.message,/native restore rejected after applying/);
  assert.deepEqual(f.writes,['delete:old','create:new','modify:new']);
});

test('rollback exception cannot certify recovery even when its freshly read fields match',async t=>{
  const f=fixture(t,{createFails:'throw'}),sdk=(globalThis as any).eda,original=sdk.sch_PrimitiveComponent.modify;
  sdk.sch_PrimitiveComponent.modify=async(...args:any[])=>{await original(...args);throw new Error('rollback restore rejected after applying');};
  const response:any=await f.run();
  assert.equal(response.result.rollback.component.uniqueId,link);assert.deepEqual(response.result.rollback.differences,[]);
  assert.equal(response.result.rollback.status,'unknown');assert.equal(response.result.rollback.verified,false);
  assert.match(response.result.rollback.restoreError.message,/rollback restore rejected after applying/);
  assert.match(response.result.error.message,/original candidate create error/);assert.equal(response.result.partial,true);
});

test('cancellation after rollback creation preserves its ID and blocks rollback restoration writes',async t=>{
  const f=fixture(t,{createFails:'throw'}),sdk=(globalThis as any).eda,controller=new AbortController(),original=sdk.sch_PrimitiveComponent.create;
  sdk.sch_PrimitiveComponent.create=async(...args:any[])=>{
    const value=await original(...args);if(value?.getState_PrimitiveId()==='rollback')controller.abort(new Error('cancel rollback restore'));return value;
  };
  const response:any=await f.run({signal:controller.signal,progress:()=>{}});
  assert.equal(response.result.cancelled,true);assert.equal(response.result.partial,true);assert.equal(response.result.rollback.status,'unknown');
  assert.equal(response.result.rollback.primitiveId,'rollback');assert.ok(f.reads.includes('rollback'));
  assert.match(response.result.error.message,/original candidate create error/);
  assert.deepEqual(f.writes,['delete:old','create:new','create:rollback']);
});

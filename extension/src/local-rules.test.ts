import assert from 'node:assert/strict';
import { test } from 'node:test';
import { displayOnlyAction, includesNativeScript, localRules, localObservationKey } from './local-rules';
import { runAction } from './actions';
import { observeSceneChanges, sceneObservation } from './scene-observation';
const facts=(net:string|null='GND',rotation=270)=>({components:[{primitiveId:'r',componentType:'part',uniqueId:'stable',designator:'R1',pinsAvailable:true,netlistAvailable:true,bbox:{minX:0,minY:0,maxX:10,maxY:10},pins:[{pinNumber:'1',net,x:5,y:0,rotation}]}],wiresAvailable:true,wires:[{primitiveId:'w',x0:5,y0:0,x1:5,y1:-10}],textsAvailable:true,texts:[]});
test('null net is unknown and changed net is a failure',()=>{assert.equal(localRules(facts(),facts(null),'layout').find(r=>r.id==='pin_nets')?.status,'unknown');assert.equal(localRules(facts(),facts('VCC'),'layout').find(r=>r.id==='pin_nets')?.status,'fail')});
test('perpendicular pin exit is detected, native direct marker does not force a stub',()=>{assert.equal(localRules(facts(),facts('GND',0),'layout').find(r=>r.id==='pin_exit')?.status,'fail');const direct=facts();direct.wires=[];assert.equal(localRules(direct,direct,'layout').find(r=>r.id==='pin_exit')?.status,'pass')});
test('visible label overlapping a foreign component is WARN, missing geometry is unknown',()=>{const f:any=facts();f.texts=[{primitiveId:'label',parentId:'w',visible:true,bbox:{minX:2,minY:2,maxX:4,maxY:4}}];const r=localRules(f,f,'layout').find(r=>r.id==='text_overlap');assert.equal(r?.status,'fail');assert.equal(r?.severity,'warn');delete f.texts[0].bbox;assert.equal(localRules(f,f,'layout').find(r=>r.id==='text_overlap')?.status,'unknown')});
test('design edits compare expected new nets instead of forcing old nets',()=>{assert.equal(localRules(facts(),facts('VCC'),'design',{'R1.1':'VCC'}).find(r=>r.id==='pin_nets')?.status,'pass')});

test('own label intersections are WARN candidates; measured non-rendered attributes do not hide them',()=>{
	const f:any=facts();f.texts=[{primitiveId:'label',parentId:'r',visible:true,bbox:{minX:2,minY:2,maxX:4,maxY:4}},{primitiveId:'metadata',parentId:'r',rendered:false,bbox:{minX:0,minY:0,maxX:0,maxY:0}}];
	const r=localRules(f,f,'layout').find(r=>r.id==='text_overlap');assert.equal(r?.status,'fail');assert.equal(r?.severity,'warn');assert.deepEqual(r?.targets,['label:r']);
	f.texts[0].bbox={minX:12,minY:2,maxX:14,maxY:4};assert.equal(localRules(f,f,'layout').find(r=>r.id==='text_overlap')?.status,'pass');
	delete f.texts[1].rendered;assert.equal(localRules(f,f,'layout').find(r=>r.id==='text_overlap')?.status,'unknown','unmeasured visibility still cannot pass');
});

test('local observation comparison catches manual text moves and ignores unrelated wires',()=>{
	const f:any=facts();f.texts=[{primitiveId:'a',parentId:'r',visible:true,x:20,y:20,bbox:{minX:20,minY:20,maxX:24,maxY:24}}];
	const changed=structuredClone(f);changed.texts[0].x=30;
	assert.notEqual(localObservationKey(f),localObservationKey(changed));
	changed.texts[0].x=20;changed.wires.push({primitiveId:'elsewhere',x0:100,y0:100,x1:200,y1:100});
	assert.equal(localObservationKey(f),localObservationKey(changed));
});

test('explicit NC is preserved, removing it fails, and null alone never means NC',()=>{
	const nc:any=facts(null);nc.components[0].pins[0].noConnected=true;
	const removed:any=facts(null);removed.components[0].pins[0].noConnected=false;
	const status=(before:any,after:any,mode:'layout'|'design'='layout',expected?:Record<string,string|null>)=>localRules(before,after,mode,expected).find(r=>r.id==='pin_nets')?.status;
	assert.equal(status(nc,nc),'pass');
	assert.equal(status(nc,removed),'fail');
	assert.equal(status(removed,removed),'unknown');
	assert.equal(status(facts(),nc,'design',{'R1.1':null}),'pass');
	assert.equal(status(facts(),removed,'design',{'R1.1':null}),'unknown');
});

test('missing identity is unknown, unrelated zero wires and sheet frames are excluded',()=>{
	const f:any=facts();
	f.wires.push({primitiveId:'unrelated',x0:100,y0:100,x1:100,y1:100});
	f.components.push({primitiveId:'sheet',componentType:'drawing',bbox:{minX:-100,minY:-100,maxX:200,maxY:200}});
	f.texts=[{primitiveId:'label',parentId:'w',visible:true,bbox:{minX:20,minY:20,maxX:24,maxY:24}}];
	const checks=localRules(f,f,'layout');
	assert.equal(checks.find(r=>r.id==='identity')?.status,'unknown');
	assert.equal(checks.find(r=>r.id==='valid_wires')?.status,'pass');
	assert.equal(checks.find(r=>r.id==='text_overlap')?.status,'pass');
	assert.equal(localRules(f,f,'layout',undefined,{wireIds:['unrelated']}).find(r=>r.id==='valid_wires')?.status,'fail');
});

test('only official display fields qualify for skipping netlist checks',()=>{
	assert.equal(displayOnlyAction('schematic.attribute.modify',{props:{x:0,valueVisible:false,bold:true}}),true);
	assert.equal(displayOnlyAction('schematic.attribute.modify',{props:{value:'GND'}}),false);
	assert.equal(displayOnlyAction('schematic.attribute.modify',{props:{lineWidth:1}}),false);
	assert.equal(displayOnlyAction('debug.batch',{steps:[{action:'debug.exec_js',payload:{code:'return 1'}}]}),false);
	assert.equal(includesNativeScript('debug.batch',{steps:[{action:'debug.batch',payload:{steps:[{action:'debug.exec_js'}]}}]}),true);
});

test('changes observed during readback cannot pass acceptance',()=>{
	const f:any=facts();f.readScope={concurrentChange:true};
	const checks=localRules(facts(),f,'layout');
	assert.equal(checks.find(r=>r.id==='pin_nets')?.status,'unknown');
	assert.equal(checks.find(r=>r.id==='observation')?.severity,'error');
});

test('automatic action wrapper uses native attribute readback and performs zero netlist or full-page component reads for a display edit',async t=>{
	const globals=globalThis as any,old=globals.eda,oldTypes=globals.EDMT_EditorDocumentType;
	t.after(()=>{globals.eda=old;globals.EDMT_EditorDocumentType=oldTypes;});
	globals.EDMT_EditorDocumentType={SCHEMATIC_PAGE:1};
	let x=20,attributeReads=0,componentReads=0,netlistReads=0,pinReads=0,wireReads=0;
	const state:Record<string,unknown>={PrimitiveId:'r',ComponentType:'part',UniqueId:'uid',Designator:'R1',Component:{uuid:'instance'},Footprint:{uuid:'fp'},SupplierId:'C1'};
	const part=new Proxy({}, {get:(_,key)=>()=>state[String(key).replace('getState_','')]??''});
	const attr=()=>({getState_PrimitiveId:()=> 'a',getState_ParentPrimitiveId:()=> 'r',getState_Key:()=> 'Name',getState_Value:()=> 'GND',getState_X:()=>x,getState_Y:()=>20,getState_KeyVisible:()=>false,getState_ValueVisible:()=>true});
	globals.eda={
		dmt_Project:{getCurrentProjectInfo:async()=>({uuid:'project'})},
		dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:'page',documentType:1})},
		sch_PrimitiveComponent:{get:async()=>{componentReads++;return [part]},getAll:async()=>{throw new Error('unexpected page inventory')},getAllPinsByPrimitiveId:async()=>{pinReads++;return []}},
		sch_PrimitiveAttribute:{get:async()=>{attributeReads++;return attr()},getAll:async()=>[attr(),{...attr(),getState_PrimitiveId:()=> 'metadata',getState_KeyVisible:()=>null,getState_ValueVisible:()=>null}],modify:async(_id:string,props:any)=>{x=props.x;return {getState_PrimitiveId:()=> 'a',getState_X:()=> -999}}},
		sch_PrimitiveWire:{getAll:async()=>{wireReads++;return []}},
		sch_Primitive:{getPrimitivesBBox:async(ids:string[])=>({minX:0,minY:0,maxX:ids[0]==='metadata'?0:10,maxY:ids[0]==='metadata'?0:10})},
		sch_ManufactureData:{getNetlistFile:async()=>{netlistReads++;throw new Error('unexpected netlist')}}
	};
	const result:any=await runAction('schematic.attribute.modify',{primitiveId:'a',props:{x:25},_mutation:true,_displayOnly:true,_scope:['r']});
	assert.equal(result.result.attribute.X,25);
	assert.equal(result.result.verified,true);
	assert.equal(attributeReads,2,'read actual native state before and after, not modify return');
	assert.equal(componentReads,2);
	assert.equal(netlistReads,0);
	assert.equal(pinReads,1,'display-only baseline needs identity, final visual neighborhood still reads pins');
	assert.equal(wireReads,1,'no before-write wire inventory for a display-only change');
	assert.equal(result.result.rules.status,'pass');
	assert.equal(result.result.rules.items.find((r:any)=>r.id==='pin_nets').coverage,'not_applicable');
	assert.equal(result.result._readback[0].result.texts[0].x,25);
	assert.equal(result.result._readback[0].result.textsAvailable,true,'zero-area native metadata does not make rendered text unavailable');
	assert.equal(result.result._readback[0].result.texts[1].rendered,false);
	const expected=result.result._readback[0].result;
	x=50; // A manual move with no event, while the batch is paused.
	const guarded:any=await runAction('debug.batch',{steps:[{action:'schematic.attribute.modify',payload:{primitiveId:'a',props:{x:60}}}],_mutation:true,_displayOnly:true,_scope:['r'],_resumeBaseline:expected,_expectedObserved:expected});
	assert.equal(guarded.result.partial,true);assert.equal(guarded.result.completed,0);assert.equal(x,50,'stale continuation must not overwrite the manual edit');
	assert.equal(guarded.result._readback[0].result.texts[0].x,50,'return refreshed local facts instead of requiring another read');
	const saveGuard:any=await runAction('schematic.save',{_scope:['r'],_expectedObserved:expected},false);
	assert.equal(saveGuard.result.partial,true,'checked finish cannot save from a stale observation');assert.equal(x,50);
	const current=guarded.result._readback[0].result;
	const resumed:any=await runAction('debug.batch',{steps:[{action:'schematic.attribute.modify',payload:{primitiveId:'a',props:{x:60}}}],_mutation:true,_displayOnly:true,_scope:['r'],_resumeBaseline:current,_expectedObserved:current});
	assert.equal(resumed.result.completed,1);assert.equal(x,60,'unchanged confirmed scope continues');
	const failed:any=await runAction('schematic.attribute.modify',{primitiveId:'a',props:{x:30,y:999}},false);
	assert.equal(failed.result.verified,false,'native readback must detect ignored requested fields');
	assert.deepEqual(failed.result.mismatch,['y']);
	const raw:any=await runAction('debug.batch',{steps:[{action:'debug.exec_js',payload:{code:'eda.outsideScope = 1; return true;'}}],_mutation:true,_scope:['r'],_displayOnly:true});
	assert.equal(globals.eda.outsideScope,1,'native code stays executable');
	assert.notEqual(raw.result.rules.status,'pass');
	assert.equal(raw.result.rules.items.find((r:any)=>r.id==='required_facts').status,'unknown');
	assert.match(raw.result.rules.items.find((r:any)=>r.id==='required_facts').evidence,/outside the declared scope/);
});

test('scene events send one delta and retain a bounded response history',()=>{
	const globals=globalThis as any,old=globals.eda;
	let listener:any; const received:any[]=[];
	globals.eda={sch_Event:{addPrimitiveEventListener:(_id:string,_events:string,fn:any)=>{listener=fn}}};
	try {
		observeSceneChanges(stamp=>received.push(stamp));
		listener('modify',{primitiveIds:['a']}); listener('modify',{primitiveIds:['b']});
		assert.equal(received[1].delta,true);assert.equal(received[1].changes.length,1);
		assert.deepEqual(received[1].changes[0].primitiveIds,['b']);
		assert.equal(sceneObservation().changes.length,2);
	} finally {observeSceneChanges(undefined);globals.eda=old;}
});

test('batch yields between native steps on pause and reports only completed work',async t=>{
	const globals=globalThis as any,old=globals.eda;t.after(()=>{globals.eda=old});
	let writes=0,paused=false;
	globals.eda={mutate:async()=>{writes++;paused=true;return writes}};
	const result:any=await runAction('debug.batch',{steps:[1,2,3].map(()=>({action:'debug.exec_js',payload:{code:'return eda.mutate();'}}))},false,
		{signal:new AbortController().signal,progress:()=>{},isPaused:()=>paused});
	assert.equal(writes,1);assert.equal(result.result.completed,1);assert.equal(result.result.paused,true);
	assert.equal(result.result.partial,true);assert.equal(result.result.results[0].ok,true);
});

test('view.capture waits for canvas repaint with and without fitting',async t=>{
	const globals=globalThis as any,oldEda=globals.eda,oldRaf=globals.requestAnimationFrame;
	t.after(()=>{globals.eda=oldEda;globals.requestAnimationFrame=oldRaf});
	for(const fit of [false,true]) {
		let frames=0;const calls:string[]=[];
		globals.requestAnimationFrame=(callback:()=>void)=>{frames++;callback();return frames};
		globals.eda={dmt_EditorControl:{zoomToAllPrimitives:async()=>{calls.push('fit')},getCurrentRenderedAreaImage:async()=>{assert.equal(frames,2,'capture must follow two animation frames');calls.push('capture');return new Blob(['painted'],{type:'image/png'})}}};
		const result:any=await runAction('view.capture',{fit},false);
		assert.deepEqual(calls,fit?['fit','capture']:['capture']);
		assert.equal(result.result.value.mimeType,'image/png');
		assert.ok(result.result.value.base64);
	}
});

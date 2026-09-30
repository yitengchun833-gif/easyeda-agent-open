import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

function host(t: any) {
  const g = globalThis as any;
  const previous = {eda:g.eda,window:g.window,types:g.EDMT_EditorDocumentType};
  t.after(()=>{g.eda=previous.eda;g.window=previous.window;g.EDMT_EditorDocumentType=previous.types});
  const counts={netlists:0,pins:0,all:0,wires:0};
  let x=20,net='GND',failPins=false;
  const doc={uuid:'page',tabId:'tab',documentType:1,profileSetting:{readonlyMode:false}};
  g.EDMT_EditorDocumentType={SCHEMATIC_PAGE:1};
  g.window={SCH:{app:{actionRunner:{running:false,currentAction:null}},docMemoryManager:{getActiveDoc:()=>doc}}};
  const primitive=(state:Record<string,unknown>)=>new Proxy({}, {get:(_,key)=>String(key).startsWith('getState_')?()=>state[String(key).replace('getState_','')]??'':undefined});
  const parts=['r','other'].map((id,i)=>primitive({PrimitiveId:id,ComponentType:'part',UniqueId:id,Designator:`R${i+1}`,Name:'R',Component:{uuid:id},Footprint:{uuid:'fp'},SupplierId:'C1'}));
  const attribute=()=>primitive({PrimitiveId:'a',ParentPrimitiveId:'r',Key:'Name',Value:'GND',X:x,Y:20,KeyVisible:false,ValueVisible:true});
  g.eda={
    dmt_Project:{getCurrentProjectInfo:async()=>({uuid:'project'})},dmt_SelectControl:{getCurrentDocumentInfo:async()=>doc},
    sch_PrimitiveComponent:{get:async()=>[parts[0]],getAll:async()=>{counts.all++;return parts},getAllPinsByPrimitiveId:async()=>{counts.pins++;if(failPins)throw new Error('missing pins');return [primitive({PinNumber:'1',X:0,Y:0,Rotation:0,NoConnected:false})]}},
    sch_PrimitiveAttribute:{get:async()=>attribute(),getAll:async()=>[attribute()],modify:async(_id:string,p:any)=>{x=p.x;return attribute()}},
    sch_Primitive:{getPrimitivesBBox:async()=>({minX:0,minY:0,maxX:10,maxY:10})},
    sch_PrimitiveWire:{getAll:async()=>{counts.wires++;return []}},
    sch_ManufactureData:{getNetlistFile:async()=>{
      counts.netlists++;
      return {text:async()=>JSON.stringify({components:{
        a:{props:{Designator:'R1'},pinInfoMap:{p:{number:'1',net}}},
        b:{props:{Designator:'R2'},pinInfoMap:{p:{number:'1',net:'GND'}}},
      }})};
    }},
  };
  return {counts,g,setNet:(n:string)=>{net=n},failPins:()=>{failPins=true}};
}

test('combined full preservation and scoped checks share each read phase, never across writes',async t=>{
  const h=host(t);
  const payload={steps:[{action:'schematic.attribute.modify',payload:{primitiveId:'a',props:{x:25}}}],verifyPreservedSchematic:true,_mutation:true,_scope:['r']};
  const result:any=await runAction('debug.batch',payload);
  assert.equal(result.result.verification.status,'pass');
  assert.equal(result.result.rules.status,'pass');
  t.diagnostic(JSON.stringify(h.counts));
  assert.equal(h.counts.netlists,2,'one export before and one after; no duplicated full export');
  assert.equal(h.counts.pins,4,'each of the two components read once per phase');
  const modify=h.g.eda.sch_PrimitiveAttribute.modify;
  h.g.eda.sch_PrimitiveAttribute.modify=async(...args:any[])=>{h.setNet('VCC');return modify(...args)};
  const changed:any=await runAction('debug.batch',payload);
  assert.equal(changed.result.verification.status,'fail','after facts must see the new network');
  assert.equal(changed.result.rules.status,'fail');
});

test('geometry reads omit electrical export without manufacturing known pin nets',async t=>{
  const h=host(t);
  const result:any=await runAction('schematic.components.list',{primitiveIds:['r'],includePins:true,includePinNets:false,includeBBox:true,includeWires:true},false);
  assert.equal(h.counts.netlists,0);assert.equal(h.counts.all,0);
  assert.equal(result.result.components[0].pins[0].net,null);
  assert.equal(result.result.readScope.netRead,'not-requested');
});

test('missing cross-page identity evidence keeps electrical nets unknown',async t=>{
  const h=host(t);
  h.g.eda.sch_PrimitiveComponent.getAll=async()=>{throw new Error('identity inventory unavailable')};
  const result:any=await runAction('schematic.components.list',{primitiveIds:['r'],includePins:true,includeNetIndex:true},false);
  assert.equal(result.result.pinNetsAvailable,false);
  assert.equal(result.result.components[0].pins[0].net,null);
  assert.equal(result.result._netIndex,undefined);
});

test('combined preservation refuses missing pins before executing a batch',async t=>{
  const h=host(t);h.failPins();let writes=0;
  h.g.eda.sch_PrimitiveAttribute.modify=async()=>{writes++;return {}};
  await assert.rejects(runAction('debug.batch',{steps:[{action:'schematic.attribute.modify',payload:{primitiveId:'a',props:{x:25}}}],verifyPreservedSchematic:true,_mutation:true,_scope:['r']}),/baseline pins/);
  assert.equal(writes,0);
});

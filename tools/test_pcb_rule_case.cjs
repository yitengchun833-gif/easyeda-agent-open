// Offline trust-boundary tests; these do not certify EasyEDA behavior.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const source=fs.readFileSync(require('node:path').join(__dirname,'pcb_rule_case.js'),'utf8');
const AsyncFunction=Object.getPrototypeOf(async function(){}).constructor;
const run=new AsyncFunction('eda','CASE_INPUT',source);
const target={projectUuid:'test-project',documentUuid:'test-pcb'};
const copy=x=>JSON.parse(JSON.stringify(x));
async function experiment(change={}) {
  let id=0, config={Physics:{'Via Size':{viaSize:{editName:'viaSize',unit:'mm',isSetDefault:true,form:{}}}}};
  let rows=[],classes=[],pads=[],vias=[];
  const primitive=(list,values)=>{const o={primitiveId:String(++id),...values};list.push(o);return {...o,getState_PrimitiveId:()=>o.primitiveId};};
  const eda={dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:change.wrongIdentity?'wrong':target.documentUuid,parentProjectUuid:target.projectUuid,documentType:3})}};
  const d=eda.pcb_Drc={
    getCurrentRuleConfiguration:async()=>copy({name:'test',config}),
    overwriteCurrentRuleConfiguration:async c=>{config=copy(c);return true;},
    getNetRules:async()=>copy(rows),overwriteNetRules:async r=>{if(!change.rejectBinding)rows=copy(r);return true;},
    getAllNetClasses:async()=>copy(classes),getNetByNetRules:async()=>({'Safe Spacing':[]}),getRegionRules:async()=>[],
    getAllDifferentialPairs:async()=>[],getAllEqualLengthNetGroups:async()=>[],
    createNetClass:async(name,nets,color)=>{classes.push({name,nets,color});rows.push({name,type:'netClass',sub:nets.map(name=>({name,type:'net','Via Size':'default'}))});return true;},
    deleteNetClass:async n=>{classes=classes.filter(v=>v.name!==n);rows=rows.filter(v=>v.name!==n);return true;},
    check:async()=>{
      const bad=vias.find(v=>v.diameter===20);if(!bad)return [];
      const ruleName=Object.keys(config.Physics['Via Size']).find(k=>k!=='viaSize');
      return [{list:[{list:[{objs:[change.wrongId?'unrelated':bad.primitiveId],ruleName:change.wrongRule?'other':ruleName,
        errorType:'Physical Error',errorObjType:change.wrongType?'Track':'Via Size',
        explanation:{param:{type:'RadiusError',diameter:change.wrongValue?'0.6mm':'0.508mm'}}}]}]}];
    }
  };
  eda.pcb_PrimitivePad={getAll:async()=>copy(pads),create:async(layer,num,x,y,rot,shape,net)=>primitive(pads,{layer,x,y,net}),delete:async ids=>{pads=pads.filter(v=>!ids.includes(v.primitiveId));return true;}};
  eda.pcb_PrimitiveVia={getAll:async()=>copy(vias),create:async(net,x,y,holeDiameter,diameter)=>primitive(vias,{net,x,y,holeDiameter,diameter:change.wrongCompliant && diameter===40?30:diameter}),delete:async ids=>{if(change.cleanupFail)throw Error('delete failed');vias=vias.filter(v=>!ids.includes(v.primitiveId));return true;}};
  eda.pcb_PrimitiveLine={getAll:async()=>[]};eda.pcb_PrimitiveRegion={getAll:async()=>[]};
  const before=await run(eda,{mode:'read',target});
  const result=await run(eda,{target,mode:'run',case:'via-size',before:before.snapshot,prefix:'TEST_RULE',x:5000,y:5000});
  return result;
}
(async()=>{
  const good=await experiment();assert.equal(good.status,'supported');assert.equal(good.checks.restored,true);
  for(const key of ['wrongId','wrongRule','wrongType','wrongValue','wrongCompliant','rejectBinding','cleanupFail']) {
    const result=await experiment({[key]:true});assert.notEqual(result.status,'supported',key);
    if(key==='cleanupFail')assert.equal(result.checks.restored,false);
  }
  await assert.rejects(()=>experiment({wrongIdentity:true}),/identity mismatch/);
  console.log('9 offline tests passed: success, wrong ID/rule/type/value/compliant geometry, rejected binding, cleanup failure, wrong target.');
})().catch(e=>{console.error(e);process.exitCode=1;});

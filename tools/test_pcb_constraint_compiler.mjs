import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync,mkdtempSync,writeFileSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {compilePcbConstraints as compile,executePcbConstraints as execute,buildConstraintScript} from './pcb_constraint_compiler.mjs';
import {reviewGeometry,applyConstraintFile} from './pcb_constraint_apply.mjs';
const copy=x=>JSON.parse(JSON.stringify(x));
test('changed reviewed input bytes are refused before any native call',()=>{
  const dir=mkdtempSync(join(tmpdir(),'pcb-input-hash-'));
  try {
    const irFile=join(dir,'ir.json');writeFileSync(irFile,'{}');
    assert.throws(()=>applyConstraintFile({irFile,window:'w',expectedIrSha256:'0'.repeat(64)}),/IR changed/);
    const checksFile=join(dir,'checks.json');writeFileSync(checksFile,'{}');
    assert.throws(()=>applyConstraintFile({irFile,checksFile,window:'w',expectedChecksSha256:'0'.repeat(64)}),/checks changed/);
  }finally{rmSync(dir,{recursive:true,force:true});}
});
const target={projectUuid:'p',documentUuid:'pcb'};
const net={kind:'net',name:'P'};
const constraints=[{type:'track_width',target:net,min:0.254,preferred:0.508,max:0.762},
  {type:'via_size',target:net,hole:0.508,diameterMin:0.9144,diameterPreferred:1.016,diameterMax:1.1176},
  {type:'safe_spacing',target:net,clearance:0.254}];
const ir=cs=>({version:'pcb-constraint/v0',unit:'mm',target,constraints:copy(cs)});
function fixture(){
  const base={editName:'base',isSetDefault:true,unit:'mm'};
  const config={Physics:{Track:{},'Via Size':{}},Spacing:{'Safe Spacing':{}}};
  config.Physics.Track.base={...base,form:{status:1,data:{1:{minValue:0.127,defaultValue:0.254,maxValue:2.54}}}};
  config.Physics['Via Size'].base={...base,form:{viaInnerdiameterMin:0.3,viaInnerdiameterDefault:0.3,viaInnerdiameterMax:6,viaOuterdiameterMin:0.5,viaOuterdiameterDefault:0.6,viaOuterdiameterMax:10}};
  config.Spacing['Safe Spacing'].base={...base,status:0,row:['Track','Via'],column:['Track','Via'],tables:{1:{status:1,content:[[0.15],[0.15,0.15]]}}};
  return {
    configuration:{name:'test',config},
    netRules:[{type:'netClass',name:'POWER',Track:'class-only-rule',sub:[{type:'net',name:'P',Track:'default','Via Size':'default','Safe Spacing':'default'}]}],
    classes:[{name:'POWER',nets:['P']}],lines:[{primitiveId:'line',net:'P'}],vias:[{primitiveId:'via',net:'P'}],pads:[{primitiveId:'pad',net:'P'}]
  };
}
function mock(options={}) {
  let state=fixture();const writes=[];
  const eda={dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:options.wrongIdentity?'wrong':'pcb',parentProjectUuid:'p',documentType:3})}};
  eda.sys_FileManager={getDocumentSource:async()=>JSON.stringify(state)};
  eda.pcb_Drc={getCurrentRuleConfiguration:async()=>copy(state.configuration),getNetRules:async()=>copy(state.netRules),getAllNetClasses:async()=>copy(state.classes),
    overwriteCurrentRuleConfiguration:async config=>{writes.push('config');if(!options.rejectConfig)state.configuration.config=copy(config);return true;},
    overwriteNetRules:async rows=>{writes.push('bindings');if(!options.rejectBinding)state.netRules=copy(rows);return true;},
    check:async()=>options.drc!==undefined?options.drc:[]};
  for(const [kind,key]of[['Line','lines'],['Via','vias'],['Pad','pads']])eda['pcb_Primitive'+kind]={getAll:async()=>copy(state[key])};
  return {eda,writes,before:copy(state),state:()=>copy(state)};
}

test('post-route review refuses missing rules without any writes',async()=>{
  const m=mock(),read=await execute(m.eda,{mode:'read',ir:ir(constraints),allObjects:true},compile);
  const result=await execute(m.eda,{mode:'review',ir:ir(constraints),allObjects:true,before:read.before},compile);
  assert.equal(result.status,'unknown');assert.match(result.reason,/read-only/);assert.deepEqual(m.writes,[]);
});
test('post-route review rereads an existing binding and fresh failing DRC without writes',async()=>{
  const opts={},m=mock(opts);await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  const writes=[...m.writes];opts.drc=[{objs:['line'],errorType:'Clearance Error',ruleName:'existing'}];
  const read=await execute(m.eda,{mode:'read',ir:ir(constraints),allObjects:true},compile);
  const result=await execute(m.eda,{mode:'review',ir:ir(constraints),allObjects:true,before:read.before},compile);
  assert.equal(result.boardStatus,'fail');assert.equal(result.configurationReadback,true);assert.equal(result.netRulesReadback,true);
  assert.deepEqual(m.writes,writes);assert.deepEqual(result.operations.map(o=>o.label),['pcb_Drc.check(true,false,true)']);
});
test('geometry review distinguishes ordinary corners, junction review and missing coverage',()=>{
  const snapshot={lines:[{}],vias:[],pads:[]},base={trackCount:1,viaCount:0,padCount:0,summary:{rightAngles:0,acuteAngles:0,junctionAngles:0,errors:0},findings:[],limitations:['line centers only']};
  assert.equal(reviewGeometry(base,snapshot),'pass');
  const junction=copy(base);junction.summary.junctionAngles=1;junction.findings=[{type:'junction-angle',level:'INFO'}];
  assert.equal(reviewGeometry(junction,snapshot),'pass');
  for(const [field,type]of [['rightAngles','right-angle'],['acuteAngles','acute-angle']]){
    const r=copy(base);r.summary[field]=1;r.findings=[{type,level:'WARN'}];assert.equal(reviewGeometry(r,snapshot),'fail');
  }
  const old=copy(base);delete old.summary.rightAngles;assert.equal(reviewGeometry(old,snapshot),'unknown');
  const drift=copy(base);drift.trackCount=2;assert.equal(reviewGeometry(drift,snapshot),'unknown');
  const fabricated=copy(base);fabricated.findings=[{type:'right-angle',level:'WARN'}];assert.equal(reviewGeometry(fabricated,snapshot),'unknown');
});
test('three native mappings preserve defaults, class fields and source snapshot',()=>{
  const before=fixture(),original=copy(before),plan=compile(ir(constraints),before);
  assert.equal(plan.status,'ready');assert.deepEqual(before,original);
  assert.equal(plan.netRules[0].Track,'class-only-rule');
  assert.notEqual(plan.netRules[0].sub[0].Track,'default');
  for(const c of plan.constraints)assert.equal(c.bindings[0].net,'P');
  assert.deepEqual(plan.configuration.config.Spacing['Safe Spacing'][plan.constraints[2].bindings[0].ruleName].tables[1].content,[[0.254],[0.254,0.254]]);
});
test('NetClass expands explicit members without changing class rule',()=>{
  const cs=copy(constraints);cs[0].target={kind:'netClass',name:'POWER',nets:['p']};
  assert.equal(compile(ir(cs),fixture()).status,'ready');
  cs[0].target.nets=['missing'];assert.equal(compile(ir(cs),fixture()).status,'fail');
});
for(const type of ['net_to_net_safe_spacing','differential_pair','equal_length_group','region_rule'])test('reject unverified type '+type,()=>{
  const r=compile(ir([...constraints,{type,target:net}]),fixture());
  assert.equal(r.status,'unsupported_in_constraint_v0');assert.equal(r.reason,'native mapping not yet verified');assert.deepEqual(r.operations,[]);
});
for(const [field,value]of[['min',NaN],['min',-1],['preferred',5],['max',0.1]])test('invalid numeric '+field+' '+value,()=>{
  const cs=copy(constraints);cs[0][field]=value;assert.equal(compile(ir(cs),fixture()).status,'fail');
});
test('unknown unit, missing nets and conflicting input stop compilation',()=>{
  const input=ir(constraints);input.unit='mil';assert.equal(compile(input,fixture()).status,'fail');
  input.unit='mm';input.constraints[0].target.name='missing';assert.equal(compile(input,fixture()).status,'fail');
  const different={...constraints[0],min:0.508};assert.equal(compile(ir([constraints[0],different]),fixture()).status,'fail');
});
test('preserve explicit incompatible rule, never overwrite it',()=>{
  const b=fixture();b.netRules[0].sub[0].Track='base';const original=copy(b);
  assert.equal(compile(ir(constraints),b).status,'fail');assert.deepEqual(b,original);
});
test('equivalent explicit rules are reused and repeat apply is idempotent',()=>{
  const first=compile(ir(constraints),fixture());
  const second=compile(ir(constraints),{...fixture(),configuration:first.configuration,netRules:first.netRules});
  assert.equal(second.status,'ready');assert.deepEqual(second.changes,{configuration:false,netRules:false});
});
test('unknown native schema does not fabricate rule forms',()=>{
  const b=fixture();delete b.configuration.config.Physics.Track.base.form;
  assert.equal(compile(ir(constraints),b).status,'unknown');
});
test('native apply reads explicit bindings, DRC and returns pass',async()=>{
  const m=mock(),r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  assert.equal(r.status,'pass');assert.equal(r.netRulesReadback,true);assert.deepEqual(m.writes,['config','bindings']);
});
test('existing unrelated DRC is separate from constraint status',async()=>{
  const m=mock({drc:[{objs:['other'],ruleName:'other-rule',errorType:'Netlist Error'}]});
  const r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  assert.equal(r.status,'pass');assert.equal(r.boardStatus,'fail');
});
test('target native violation returns fail with raw IDs and explanation',async()=>{
  const name=compile(ir(constraints),fixture()).constraints[0].bindings[0].ruleName;
  const leaf={objs:['line'],ruleName:name,errorType:'Physical Error',explanation:{param:{width:'0.127mm'}}};
  const m=mock({drc:[{list:[{list:[leaf]}]}]});
  const r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  assert.equal(r.status,'fail');assert.deepEqual(r.constraints[0].violations,[leaf]);
});
for(const [name,options]of[['wrong identity',{wrongIdentity:true}],['configuration readback',{rejectConfig:true}],['binding readback',{rejectBinding:true}],['boolean DRC',{drc:true}],['malformed DRC',{drc:[{count:1,list:[]}]}]])test(name+' cannot become pass',async()=>{
  const m=mock(options),r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  assert.equal(r.status,'unknown');if(options.wrongIdentity)assert.deepEqual(m.writes,[]);
});
test('stale durable before refuses all writes',async()=>{
  const m=mock();m.before.classes=[];const r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  assert.equal(r.status,'unknown');assert.deepEqual(m.writes,[]);
});
test('unsupported mixed input never writes a supported subset',async()=>{
  const m=mock(),r=await execute(m.eda,{mode:'apply',ir:ir([...constraints,{type:'region_rule'}]),before:m.before},compile);
  assert.equal(r.status,'unsupported_in_constraint_v0');assert.deepEqual(m.writes,[]);
});
test('script builder uses the exact compiler and executor',async()=>{
  const m=mock(),AsyncFunction=Object.getPrototypeOf(async()=>{}).constructor;
  const r=await new AsyncFunction('eda',buildConstraintScript({mode:'apply',ir:ir(constraints),before:m.before}))(m.eda);
  assert.equal(r.status,'pass');
});

test('cancelled Connector execution never starts writes',async()=>{
  const m=mock(),r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile,{aborted:true});
  assert.equal(r.status,'unknown');assert.deepEqual(m.writes,[]);
});

test('native before evidence contains only target geometry and all rule bindings',async()=>{
  const m=mock();m.eda.pcb_PrimitivePad.getAll=async()=>[{primitiveId:'target',net:'P'},{primitiveId:'unrelated',net:'OTHER'}];
  const r=await execute(m.eda,{mode:'read',ir:ir(constraints)},compile);
  assert.deepEqual(r.before.pads,[{primitiveId:'target',net:'P'}]);assert.deepEqual(r.before.netRules,m.before.netRules);
});

test('cancellation after configuration stops remaining binding write',async()=>{
  const m=mock(),signal={aborted:false},write=m.eda.pcb_Drc.overwriteCurrentRuleConfiguration;
  m.eda.pcb_Drc.overwriteCurrentRuleConfiguration=async c=>{const value=await write(c);signal.aborted=true;return value;};
  const r=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile,signal);
  assert.equal(r.status,'unknown');assert.deepEqual(m.writes,['config']);
});

test('compile each v0 mapping against captured native before schema without EDA',()=>{
  const evidence=JSON.parse(readFileSync(new URL('./testdata/native-rule-schema.json',import.meta.url),'utf8'));
  const before=evidence.before.snapshot;
  for(const c of constraints) {
    const input={version:'pcb-constraint/v0',unit:'mm',target:evidence.target,constraints:[{...c,target:{kind:'net',name:'GND'}}]};
    const plan=compile(input,before);assert.equal(plan.status,'ready');
    assert.equal(plan.constraints[0].bindings[0].net,'GND');
  }
});

test('source minimum compiles Track without inventing preferred/max or changing the snapshot',()=>{
  const before=fixture(),original=copy(before);
  before.configuration.config.Physics.Track.base.form.data[2]={minValue:0.127,defaultValue:0.381,maxValue:3.81};
  original.configuration=copy(before.configuration);
  const plan=compile(ir([{type:'track_width',target:net,min:0.1778}]),before),c=plan.constraints[0];
  assert.equal(plan.status,'ready');assert.equal(c.dryRunOnly,false);
  assert.deepEqual(c.unspecifiedFields,['preferred','max']);assert.ok(Math.abs(c.effectiveValues[0]-0.1778)<1e-12);
  assert.deepEqual(c.effectiveValues.slice(1),[null,null]);
  const form=plan.configuration.config.Physics.Track[c.bindings[0].ruleName].form;
  for(const layer of ['1','2']) for(const key of ['defaultValue','maxValue'])
    assert.equal(form.data[layer][key],before.configuration.config.Physics.Track.base.form.data[layer][key]);
  assert.equal(c.retainedNativeValues.length,4);assert.deepEqual(before,original);
});
test('one optional Track bound is retained only when not supplied',()=>{
  const plan=compile(ir([{type:'track_width',target:net,min:0.1778,max:0.762}]),fixture());
  assert.equal(plan.status,'ready');assert.deepEqual(plan.constraints[0].unspecifiedFields,['preferred']);
  assert.equal(plan.constraints[0].effectiveValues[1],null);assert.ok(Math.abs(plan.constraints[0].effectiveValues[2]-0.762)<1e-12);
  for(const invalid of [{preferred:null},{max:0.1},{preferred:0}])
    assert.equal(compile(ir([{type:'track_width',target:net,min:0.1778,...invalid}]),fixture()).status,'fail');
});
test('Track lower bound incompatible with native preference stays unknown without guessing',()=>{
  const before=fixture(),original=copy(before);
  const plan=compile(ir([{type:'track_width',target:net,min:0.508}]),before);
  assert.equal(plan.status,'unknown');assert.match(plan.reason,/no automatic replacement/);assert.deepEqual(before,original);
});
test('Via drill lower bound changes only min and retains five actual native fields',()=>{
  const before=fixture(),original=copy(before),plan=compile(ir([{type:'via_size',target:net,holeMin:0.299}]),before),c=plan.constraints[0];
  assert.equal(plan.status,'ready');assert.equal(c.retainedNativeValues.length,5);
  const form=plan.configuration.config.Physics['Via Size'][c.bindings[0].ruleName].form;
  assert.ok(form.viaInnerdiameterMin>=0.299);
  for(const {field,value} of c.retainedNativeValues)assert.equal(value,original.configuration.config.Physics['Via Size'].base.form[field.slice(5)]);
  assert.deepEqual(before,original);
});
test('Via lower bound rejects mixed fixed-hole fields and incompatible native dimensions',()=>{
  for(const extra of [{hole:0.299},{diameterMin:0.5},{holeMin:null}])
    assert.equal(compile(ir([{type:'via_size',target:net,holeMin:0.299,...extra}]),fixture()).status,'fail');
  assert.equal(compile(ir([{type:'via_size',target:net,holeMin:0.508}]),fixture()).status,'unknown');
});

test('native one-step quantization is reused only when the requested minimum still holds',()=>{
  const before=fixture();before.configuration.config.Physics['Via Size'].base.form.viaInnerdiameterDefault=0.3048;
  const input=ir([{type:'via_size',target:net,holeMin:0.3}]),first=compile(input,before);
  assert.equal(first.constraints[0].effectiveValues[0],0.30000194);
  const name=first.constraints[0].bindings[0].ruleName,observed={...before,configuration:first.configuration,netRules:first.netRules};
  observed.configuration.config.Physics['Via Size'][name].form.viaInnerdiameterMin=0.30000448;
  assert.equal(compile(input,observed).status,'ready');
  observed.configuration.config.Physics['Via Size'][name].form.viaInnerdiameterMin=0.2999994;
  assert.equal(compile(input,observed).status,'fail');
});

test('native configuration rename is metadata and does not block verified rule bindings',async()=>{
  const m=mock(),write=m.eda.pcb_Drc.overwriteCurrentRuleConfiguration;
  m.eda.pcb_Drc.overwriteCurrentRuleConfiguration=async c=>{await write(c);return true;};
  const read=m.eda.pcb_Drc.getCurrentRuleConfiguration;
  m.eda.pcb_Drc.getCurrentRuleConfiguration=async()=>{const c=await read();if(m.writes.length)c.name='custom';return c;};
  const result=await execute(m.eda,{mode:'apply',ir:ir(constraints),before:m.before},compile);
  assert.equal(result.status,'pass');assert.equal(result.configurationReadback,true);
});
test('lower-bound batch applies with retained fields and DRC, including Connector script',async()=>{
  const cs=[constraints[2],{type:'track_width',target:net,min:0.1778},{type:'via_size',target:net,holeMin:0.299}];
  for(const built of [false,true]) {
    const m=mock();let checks=0;m.eda.pcb_Drc.check=async()=>{checks++;return [];};
    const input={mode:'apply',ir:ir(cs),before:m.before};
    const r=built?await new (Object.getPrototypeOf(async()=>{}).constructor)('eda',buildConstraintScript(input))(m.eda):await execute(m.eda,input,compile);
    assert.equal(r.status,'pass');assert.deepEqual(m.writes,['config','bindings']);assert.equal(checks,1);
    assert.equal(r.configurationReadback,true);assert.equal(r.netRulesReadback,true);
    for(const c of r.constraints)assert.equal(c.dryRunOnly,false);
  }
});

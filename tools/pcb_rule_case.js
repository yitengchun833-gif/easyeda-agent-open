// Executed by pcb_rule_verify.py inside the existing Connector; no save or UI calls.
const I = CASE_INPUT;
const clone = x => JSON.parse(JSON.stringify(x));
const canon = x => JSON.stringify(x, function(k, v) {
  return v && typeof v === 'object' && !Array.isArray(v)
    ? Object.fromEntries(Object.keys(v).sort().map(k => [k, v[k]])) : v;
});
function equal(a,b) {
  if(typeof a==='number' && typeof b==='number') return Math.abs(a-b)<=1e-12;
  if(a===null || b===null || typeof a!=='object' || typeof b!=='object') return a===b;
  if(Array.isArray(a)!==Array.isArray(b)) return false;
  const ak=Object.keys(a),bk=Object.keys(b);
  return ak.length===bk.length && ak.every(k=>k in b && equal(a[k],b[k]));
}
function violations(x) {
  if(Array.isArray(x)) return x.flatMap(violations);
  if(x && typeof x==='object') return Array.isArray(x.objs)?[x]:violations(x.list||[]);
  return [];
}
async function identity() {
  const d = await eda.dmt_SelectControl.getCurrentDocumentInfo();
  if (d?.uuid !== I.target.documentUuid || d?.parentProjectUuid !== I.target.projectUuid || d?.documentType !== 3)
    throw new Error('Dedicated test PCB identity mismatch; no navigation permitted');
  return d;
}
async function snapshot() {
  await identity();
  const d = eda.pcb_Drc;
  return clone({configuration:await d.getCurrentRuleConfiguration(), netRules:await d.getNetRules(),
    netByNet:await d.getNetByNetRules(), regionRules:await d.getRegionRules(),
    classes:await d.getAllNetClasses(), pairs:await d.getAllDifferentialPairs(),
    groups:await d.getAllEqualLengthNetGroups(), lines:await eda.pcb_PrimitiveLine.getAll(),
    vias:await eda.pcb_PrimitiveVia.getAll(), pads:await eda.pcb_PrimitivePad.getAll(),
    regions:await eda.pcb_PrimitiveRegion.getAll()});
}
if (I.mode === 'read') return {identity:await identity(), snapshot:await snapshot()};
const E = {case:I.case, apiBeta:true, status:'unknown', startedAt:new Date().toISOString(),
  before:I.before, operations:[], objects:[], cleanup:[], checks:{}, limitations:[]};
const D = eda.pcb_Drc, name = I.prefix, nets = [name+'_P', name+'_N', name+'_P2', name+'_N2'].map(n=>n.toUpperCase());
const owned = [], pairNames = [], groupNames = [], classNames=[];
let wroteConfig=false, wroteNets=false, wroteRegion=false;
async function call(label, fn) {
  if(typeof signal!=='undefined' && signal?.aborted) throw new Error('Execution cancelled; cleanup only');
  await identity();
  try { const v=await fn(); E.operations.push({label,return:clone(v===undefined?null:v)}); return v; }
  catch(e) { E.operations.push({label,error:String(e)}); throw e; }
}
async function object(kind, role, fn) {
  const o=await call('create '+kind+' '+role, fn);
  const id=o?.getState_PrimitiveId();
  if (!id) throw new Error('Creation returned no ID; stop and inspect delta');
  owned.push({kind,id}); E.objects.push({kind,role,id,created:clone(o)}); return id;
}
const x=I.x, y=I.y;
async function pad(net, px, py) {
  return object('Pad','net endpoint',()=>eda.pcb_PrimitivePad.create(1,'1',px,py,0,['ELLIPSE',20,20],net,null,0,0,0,false,0));
}
async function line(net, px, py, len, role, width=10) {
  return object('Line',role,()=>eda.pcb_PrimitiveLine.create(net,1,px,py,px+len,py,width,false));
}
const compilerBefore=s=>{
  const wanted=new Set(E.compilerIR.constraints.flatMap(c=>c.target.kind==='net'?[c.target.name]:c.target.nets).map(n=>n.toUpperCase()));
  return Object.fromEntries(['configuration','netRules','classes','lines','vias','pads'].map(k=>[k,['lines','vias','pads'].includes(k)?s[k].filter(o=>typeof o.net==='string'&&wanted.has(o.net.toUpperCase())):s[k]]));
};
function entries(rows) { return rows.flatMap(r=>[r,...entries(r.sub||[])]); }
async function rule(category, type, source, change) {
  const c=await D.getCurrentRuleConfiguration();
  const r=clone(c.config[category]?.[type]?.[source]);
  if(!r || r.unit!=='mm') throw new Error('Observed rule schema/unit unavailable');
  if(c.config[category][type][name]) throw new Error('Rule name collision');
  r.editName=name; r.isSetDefault=false; change(r);
  if(I.compilerV0) {
    const target={kind:'netClass',name:name+'_CLASS',nets:I.case==='safe-spacing'?nets:[nets[0]]};
    const constraint=type==='Track'?{type:'track_width',target,min:r.form.data[1].minValue,preferred:r.form.data[1].defaultValue,max:r.form.data[1].maxValue}:
      type==='Via Size'?{type:'via_size',target,hole:r.form.viaInnerdiameterDefault,diameterMin:r.form.viaOuterdiameterMin,diameterPreferred:r.form.viaOuterdiameterDefault,diameterMax:r.form.viaOuterdiameterMax}:
      {type:'safe_spacing',target,clearance:r.tables[1].content[0][0]};
    E.compilerIR={version:'pcb-constraint/v0',unit:'mm',target:I.target,constraints:[constraint]};
    const compilerBaseline=compilerBefore(await snapshot());
    const compilerPlan=compilePcbConstraints(E.compilerIR,compilerBaseline);
    const plannedBinding=compilerPlan.constraints?.[0]?.bindings?.[0];
    if(compilerPlan.status!=='ready'||!plannedBinding)throw new Error('Compiler preflight failed: '+compilerPlan.reason);
    E.rule={category,type,name:plannedBinding.ruleName,requested:compilerPlan.configuration.config[category][type][plannedBinding.ruleName]};
    wroteConfig=compilerPlan.changes.configuration;wroteNets=compilerPlan.changes.netRules;
    E.compilerSetup=await executePcbConstraints(eda,{mode:'apply',ir:E.compilerIR,before:compilerBaseline},compilePcbConstraints,typeof signal==='undefined'?undefined:signal);
    E.operations.push({label:'compiler v0 setup',return:{status:E.compilerSetup.status,operations:E.compilerSetup.operations}});
    const binding=E.compilerSetup.plan?.constraints?.[0]?.bindings?.[0];
    if(!binding || !E.compilerSetup.configurationReadback || !E.compilerSetup.netRulesReadback) throw new Error('Compiler rule/binding setup failed');
    E.rule={category,type,name:binding.ruleName,readback:E.compilerSetup.after.configuration.config[category][type][binding.ruleName]};
    E.rule.requested=E.compilerSetup.plan.configuration.config[category][type][binding.ruleName];
    E.checks.ruleCreated=true;wroteConfig=true;wroteNets=true;
    return;
  }
  c.config[category][type][name]=r; E.rule={category,type,name,requested:clone(r)};
  wroteConfig=true; await call('overwriteCurrentRuleConfiguration',()=>D.overwriteCurrentRuleConfiguration(c.config));
  const back=await D.getCurrentRuleConfiguration(); E.rule.readback=back.config[category]?.[type]?.[name];
  E.checks.ruleCreated=equal(E.rule.readback,r);
  if(!E.checks.ruleCreated) throw new Error('Rule configuration readback differs');
}
async function bind(type, targetNames) {
  const rows=await D.getNetRules(), all=entries(rows);
  E.bindingBefore=clone(rows);
  const ruleName=E.rule.name;
  E.bindingRequest={type,targetNames,ruleName};
  for(const n of targetNames) {
    const found=all.filter(r=>r.name===n);
    if(found.length!==1 || !(type in found[0])) throw new Error('No unambiguous observed binding field: '+n+' / '+type);
    found[0][type]=ruleName;
  }
  if(!I.compilerV0) {wroteNets=true; await call('overwriteNetRules explicit binding',()=>D.overwriteNetRules(rows));}
  E.bindingAfter=await D.getNetRules();
  E.checks.bound=targetNames.every(n=>entries(E.bindingAfter).some(r=>r.name===n && r[type]===ruleName));
  if(!E.checks.bound) throw new Error('Explicit binding readback failed');
}
async function netClass(members) {
  const cn=name+'_CLASS'; classNames.push(cn);
  await call('createNetClass',()=>D.createNetClass(cn,members,{r:0,g:255,b:0,alpha:1}));
  E.classReadback=await D.getAllNetClasses();
}
try {
  const current=await snapshot();
  if(canon(current)!==canon(I.before)) throw new Error('State changed after durable before snapshot; no writes');
  E.drcBefore=await call('baseline DRC',()=>D.check(true,false,true));
  if(I.case==='net-to-net-safe-spacing') {
    E.limitations.push('Native getNetByNetRules returned empty arrays; Record<string,any> documentation gives no item schema. No guessed selector was written. Positive/negative case is blocked.');
  } else if(I.case==='track-width') {
    await pad(nets[0],x-100,y);await netClass([nets[0]]);
    await rule('Physics','Track','copperThickness2oz',r=>{
      for(const v of Object.values(r.form.data))Object.assign(v,{minValue:0.254,defaultValue:0.508,maxValue:0.762});
    });
    await bind('Track',[nets[0]]);
    await line(nets[0],x,y,100,'compliant',20);
    await line(nets[0],x,y+200,100,'violating',5);
  } else if(I.case==='via-size') {
    await pad(nets[0],x-100,y);
    await netClass([nets[0]]);
    await rule('Physics','Via Size','viaSize',r=>Object.assign(r.form,{
      viaInnerdiameterMin:0.4572,viaInnerdiameterDefault:0.508,viaInnerdiameterMax:0.5588,
      viaOuterdiameterMin:0.9144,viaOuterdiameterDefault:1.016,viaOuterdiameterMax:1.1176}));
    await bind('Via Size',[nets[0]]);
    await object('Via','compliant',()=>eda.pcb_PrimitiveVia.create(nets[0],x,y,20,40));
    await object('Via','violating',()=>eda.pcb_PrimitiveVia.create(nets[0],x+200,y,20,20));
  } else if(I.case==='safe-spacing') {
    for(let i=0;i<4;i++) await pad(nets[i],x-100,y+i*200);
    await netClass(nets);
    await rule('Spacing','Safe Spacing','copperThickness1oz',r=>{
      for(const t of Object.values(r.tables)) t.content[0][0]=0.254;
    });
    await bind('Safe Spacing',nets);
    await line(nets[0],x,y,100,'compliant'); await line(nets[1],x,y+30,100,'compliant reference');
    await line(nets[2],x,y+200,100,'violating'); await line(nets[3],x,y+215,100,'violating reference');
  } else if(I.case==='differential-pair' || I.case==='equal-length-group') {
    for(let i=0;i<4;i++) {
      const py=y+(i<2?i*30:400+(i-2)*30), len=i===3?200:100;
      await pad(nets[i],x,py); await pad(nets[i],x+len,py);
      await line(nets[i],x,py,len,i<2?'compliant':'violating');
    }
    for(let j=0;j<2;j++) {
      const gn=name+'_'+j;
      if(I.case==='differential-pair') {
        pairNames.push(gn); await call('createDifferentialPair',()=>D.createDifferentialPair(gn,nets[j*2],nets[j*2+1]));
      } else {
        groupNames.push(gn); await call('createEqualLengthNetGroup',()=>D.createEqualLengthNetGroup(gn,nets.slice(j*2,j*2+2),{r:0,g:255,b:0,alpha:1}));
      }
    }
    E.createdGroups={pairs:await D.getAllDifferentialPairs(),groups:await D.getAllEqualLengthNetGroups(),netRules:await D.getNetRules()};
    if(I.case==='differential-pair') {
      await rule('Physics','Differential Pair','differentialPair',r=>{
        r.form.differentailPairLenTolerMax=0.254;
        for(const v of Object.values(r.form.diffPairSpacingTables.data)) Object.assign(v,{minValue:0.508,defaultValue:0.508,maxValue:0.6096});
        for(const v of Object.values(r.form.strokeWidthTables.data)) Object.assign(v,{minValue:0.254,defaultValue:0.254,maxValue:0.254});
      });
      await bind('Differential Pair',nets);
    } else {
      await rule('Physics','Net Length Tolerance','netLengthTolerance',r=>r.form.netLengthTolerance=0.254);
      await bind('Net Length Tolerance',[...groupNames,...nets]);
    }
  } else if(I.case==='region-rule') {
    await pad(nets[0],x-100,y);
    await object('Region','test region',()=>eda.pcb_PrimitiveRegion.create(1,eda.pcb_MathPolygon.createPolygon(['R',x-50,y-50,400,150,0,0]),[5],name,1,false));
    E.regionCreated=await D.getRegionRules();
    E.limitations.push('Region selector must be confirmed from native readback before binding; no guessed Record<string,any> fields.');
    await line(nets[0],x,y+300,100,'compliant');
    await line(nets[0],x,y,100,'violating');
  } else throw new Error('Unknown case');
  E.after=await snapshot();
  if(I.compilerV0) {
    E.compilerValidation=await executePcbConstraints(eda,{mode:'apply',ir:E.compilerIR,before:compilerBefore(E.after)},compilePcbConstraints,typeof signal==='undefined'?undefined:signal);
    E.drc=E.compilerValidation.drc;
    E.operations.push({label:'test DRC',return:E.drc});
  } else E.drc=await call('test DRC',()=>D.check(true,false,true));
  E.bindingFinal={netRules:await D.getNetRules(),netByNet:await D.getNetByNetRules(),regionRules:await D.getRegionRules()};
  const ids=E.objects.filter(o=>o.role!=='net endpoint').map(o=>o.id);
  E.targetViolations=violations(E.drc).filter(v=>v.objs.some(id=>ids.includes(id)));
  E.namedViolations=E.targetViolations.filter(v=>v.ruleName===E.rule?.name);
  E.compliantIds=E.objects.filter(o=>o.role.startsWith('compliant')).map(o=>o.id);
  E.violatingIds=E.objects.filter(o=>o.role.startsWith('violating')).map(o=>o.id);
  E.checks.compliantHasNoTargetViolation=Array.isArray(E.drc) && !E.namedViolations.some(v=>v.objs.some(id=>E.compliantIds.includes(id)));
  E.checks.violatingHasNamedViolation=E.namedViolations.some(v=>v.objs.some(id=>E.violatingIds.includes(id)));
  E.checks.actualValue=false;
  if(I.case==='track-width') E.checks.actualValue=E.namedViolations.some(v=>v.errorType==='Physical Error'&&v.errorObjType==='Track'&&v.explanation?.param?.type==='TrackError'&&v.explanation?.param?.width==='0.127mm'&&v.objs.length===1&&E.violatingIds.includes(v.objs[0]));
  if(I.case==='via-size') E.checks.actualValue=E.namedViolations.some(v=>v.errorType==='Physical Error' && v.errorObjType==='Via Size' && v.explanation?.param?.type==='RadiusError' && v.explanation?.param?.diameter==='0.508mm' && v.objs.length===1 && E.violatingIds.includes(v.objs[0]));
  if(I.case==='safe-spacing') E.checks.actualValue=E.namedViolations.some(v=>v.errorType==='Clearance Error' && v.errorObjType==='Track to Track' && v.explanation?.param?.type==='ClearanceError' && v.explanation?.param?.minDistance==='0.127mm' && v.explanation?.param?.shouldBe==='>= 0.254mm' && v.objs.length===2 && v.objs.every(id=>E.violatingIds.includes(id)));
  E.objectReadback=E.objects.map(o=>({...o,observed:E.after[{Line:'lines',Via:'vias',Pad:'pads',Region:'regions'}[o.kind]].find(v=>v.primitiveId===o.id)}));
  E.checks.objectsRead=E.objectReadback.every(o=>o.observed);
  if(I.compilerV0 && E.checks.actualValue && E.checks.compliantHasNoTargetViolation) {
    for(const kind of ['Line','Via']) {
      const ids=owned.filter(o=>o.kind===kind && E.violatingIds.includes(o.id)).map(o=>o.id);
      if(ids.length) {
        await call('remove negative objects for positive Compiler DRC',()=>eda['pcb_Primitive'+kind].delete(ids));
        for(let n=owned.length-1;n>=0;n--)if(ids.includes(owned[n].id))owned.splice(n,1);
      }
    }
    const positiveBefore=compilerBefore(await snapshot());
    const positive=await executePcbConstraints(eda,{mode:'apply',ir:E.compilerIR,before:positiveBefore},compilePcbConstraints,typeof signal==='undefined'?undefined:signal);
    E.compilerPositiveValidation={status:positive.status,boardStatus:positive.boardStatus,configurationReadback:positive.configurationReadback,netRulesReadback:positive.netRulesReadback,
      beforeObjects:{lines:positiveBefore.lines,vias:positiveBefore.vias,pads:positiveBefore.pads},constraints:positive.constraints,drc:positive.drc,operations:positive.operations};
    E.checks.compilerPositivePass=positive.status==='pass';
    E.checks.compilerNegativeFail=E.compilerValidation.status==='fail';
  }
  E.status=E.objects.length?'partial':'unknown';
} catch(e) {
  E.error=String(e); E.status=E.operations.length?'partial':'unknown';
  try { E.after=await snapshot(); E.drc=await call('DRC after partial setup',()=>D.check(true,false,true)); }
  catch(e2) { E.readError=String(e2); }
} finally {
  // Independent cleanup attempts; a failed operation must not skip the remaining cleanup.
  async function clean(label,fn) { try {await identity(); E.cleanup.push({label,return:await fn()});} catch(e) {E.cleanup.push({label,error:String(e)});} }
  for(const n of pairNames) await clean('delete pair '+n,()=>D.deleteDifferentialPair(n));
  for(const n of groupNames) await clean('delete group '+n,()=>D.deleteEqualLengthNetGroup(n));
  for(const n of classNames) await clean('delete class '+n,()=>D.deleteNetClass(n));
  for(const kind of ['Line','Via','Region','Pad']) {
    const ids=owned.filter(o=>o.kind===kind).map(o=>o.id);
    if(ids.length) await clean('delete owned '+kind,()=>eda['pcb_Primitive'+kind].delete(ids));
  }
  if(wroteNets) await clean('restore net bindings',async()=>{
    const current=await D.getNetRules();
    if(equal(current,I.before.netRules)) return {alreadyRestored:true};
    const withoutOwned=current.filter(r=>!r.name.startsWith(name));
    if(!equal(withoutOwned,I.before.netRules)) throw new Error('Unrelated net bindings changed; refuse baseline overwrite');
    return D.overwriteNetRules(withoutOwned);
  });
  if(wroteRegion) await clean('restore region bindings',()=>D.overwriteRegionRules(I.before.regionRules));
  if(wroteConfig) await clean('remove test rule / restore configuration',async()=>{
    const current=await D.getCurrentRuleConfiguration();
    delete current.config[E.rule.category][E.rule.type][E.rule.name];
    if(!equal(current.config,I.before.configuration.config)) throw new Error('Unrelated configuration changed; refuse baseline overwrite');
    return D.overwriteCurrentRuleConfiguration(I.before.configuration.config);
  });
  try {
    E.restored=await snapshot();
    E.restorationStrict=Object.fromEntries(Object.keys(I.before).map(k=>[k,canon(I.before[k])===canon(E.restored[k])]));
    E.restoration=Object.fromEntries(Object.keys(I.before).map(k=>[k,equal(I.before[k],E.restored[k])]));
    E.numericComparisonTolerance=1e-12;
    E.checks.restored=Object.values(E.restoration).every(Boolean);
  } catch(e) {E.restoreReadError=String(e); E.checks.restored=false;}
}
E.finishedAt=new Date().toISOString();
E.targetViolations=violations(E.drc).filter(v=>v.objs.some(id=>E.objects.some(o=>o.id===id)));
E.measurements=[];
if(E.after) {
  for(const o of E.objects.filter(o=>['Via','Line'].includes(o.kind))) {
    const r=E.after[o.kind==='Via'?'vias':'lines'].find(v=>v.primitiveId===o.id);
    if(r) E.measurements.push({id:o.id,role:o.role,net:r.net,unit:'mm',
      ...(o.kind==='Via'?{diameter:r.diameter*0.0254,holeDiameter:r.holeDiameter*0.0254}:
        {length:Math.hypot(r.endX-r.startX,r.endY-r.startY)*0.0254,width:r.lineWidth*0.0254})});
  }
}
E.checks.bindingFinal=!!E.bindingRequest && !!E.bindingFinal && E.bindingRequest.targetNames.every(n=>entries(E.bindingFinal.netRules).some(r=>r.name===n && r[E.bindingRequest.type]===E.rule.name));
E.checks.objectValues=false;
if(I.case==='track-width') E.checks.objectValues=['compliant','violating'].every(role=>{
  const m=E.measurements.find(v=>v.role===role);return m&&equal(m.width,role==='compliant'?0.508:0.127)&&m.net===nets[0];
});
if(I.case==='via-size') E.checks.objectValues=['compliant','violating'].every(role=>{
  const m=E.measurements.find(v=>v.role===role);
  return m && equal(m.diameter,role==='compliant'?1.016:0.508) && equal(m.holeDiameter,0.508) && m.net===nets[0];
});
if(I.case==='safe-spacing' && E.after) {
  const tracks=E.objects.filter(o=>o.kind==='Line').map(o=>({role:o.role,...E.after.lines.find(v=>v.primitiveId===o.id)}));
  const distance=(a,b)=>(Math.abs(a.startY-b.startY)-(a.lineWidth+b.lineWidth)/2)*0.0254;
  if(tracks.length===4) {
    E.spacingMeasurements={unit:'mm',compliant:distance(tracks[0],tracks[1]),violating:distance(tracks[2],tracks[3])};
    E.checks.objectValues=tracks.every(t=>t.lineWidth===10) && equal(E.spacingMeasurements.compliant,0.508) && equal(E.spacingMeasurements.violating,0.127);
  }
}
E.steps={beforeSaved:true,ruleCreated:!!E.checks.ruleCreated,bound:!!E.checks.bound,
  positiveAndNegativeObjects:E.objects.some(o=>o.role==='compliant')&&E.objects.some(o=>o.role==='violating'),
  drcCalled:E.operations.some(o=>['test DRC','DRC after partial setup'].includes(o.label) && 'return' in o),
  violationVerified:!!E.checks.actualValue,bindingReRead:!!E.checks.bindingFinal,
  cleanupAttempted:E.cleanup.length>0 || !E.objects.length,restorationVerified:!!E.checks.restored,
  independentEvidence:true};
if(!E.error && (!I.compilerV0 || (E.checks.compilerPositivePass && E.checks.compilerNegativeFail)) && E.checks.bound && E.checks.bindingFinal && E.checks.ruleCreated && E.checks.actualValue && E.checks.objectValues && E.checks.objectsRead && E.checks.compliantHasNoTargetViolation && E.checks.violatingHasNamedViolation && E.checks.restored) E.status='supported';
if(E.status==='partial' && !E.error) E.limitations.push('The ten-step acceptance chain is incomplete; absence of a target DRC error is not evidence of platform-wide unsupported behavior.');
return I.compilerV0 ? JSON.stringify(E) : E;

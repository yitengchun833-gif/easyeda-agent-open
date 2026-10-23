import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {describeDryRun} from './pcb_constraint_apply.mjs';
import {buildConstraintScript} from './pcb_constraint_compiler.mjs';
const captured=JSON.parse(readFileSync(new URL('./testdata/native-rule-schema.json',import.meta.url),'utf8'));
const before=captured.before.snapshot;
const track={type:'track_width',target:{kind:'net',name:'GND'},min:0.254,preferred:0.508,max:0.762};
const ir=constraints=>({version:'pcb-constraint/v0',unit:'mm',target:captured.target,constraints});

test('dry-run lists native rules and explicit member bindings without changing input',()=>{
  const bytes=JSON.stringify(before),r=describeDryRun(ir([track]),before);
  assert.equal(r.status,'ready');assert.equal(r.preview.rulesToCreate.length,1);
  assert.equal(r.preview.bindings[0].net,'GND');assert.equal(r.preview.rulesApplied,false);
  assert.equal(JSON.stringify(before),bytes);
});
test('mixed unsupported batch retains supported preview and unsupported reasons',()=>{
  const r=describeDryRun(ir([track,{type:'region_rule',target:track.target}]),before);
  assert.equal(r.status,'unsupported_in_constraint_v0');assert.equal(r.preview.rulesToCreate.length,1);
  assert.equal(r.preview.unsupported[0].reason,'native mapping not yet verified');assert.equal(r.preview.candidateOnly,true);
});
test('existing explicit conflict and missing net are returned for review',()=>{
  const r=describeDryRun(ir([{...track,target:{kind:'net',name:'VCC_DUT'}},{...track,target:{kind:'net',name:'missing'}}]),before);
  assert.equal(r.status,'fail');assert.equal(r.preview.conflicts.length,2);assert.equal(r.preview.rulesApplied,false);
});
test('contradictory module constraints remain a blocked batch even if each is valid',()=>{
  const r=describeDryRun(ir([track,{...track,min:0.508}]),before);
  assert.equal(r.status,'fail');assert.match(r.preview.batchIssue,/Conflicting constraints/);
});
test('native read mode never calls overwrite, create, DRC or save APIs',async()=>{
  const reads=[];
  const eda={dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:captured.target.documentUuid,parentProjectUuid:captured.target.projectUuid,documentType:3})},
    pcb_Drc:{getCurrentRuleConfiguration:async()=>{reads.push('configuration');return before.configuration;},getNetRules:async()=>before.netRules,getAllNetClasses:async()=>before.classes}};
  const fn=Object.getPrototypeOf(async()=>{}).constructor;
  const r=await new fn('eda',buildConstraintScript({mode:'read',ir:ir([track]),onlyRules:true}))(eda);
  assert.equal(describeDryRun(ir([track]),r.before).status,'ready');assert.deepEqual(reads,['configuration']);
});
test('lower-bound dry-run exposes omitted fields and native retained values without blocking apply',()=>{
  const bytes=JSON.stringify(before),r=describeDryRun(ir([{type:'track_width',target:track.target,min:0.1778}]),before);
  assert.equal(r.status,'ready');assert.equal(r.preview.candidateOnly,false);
  assert.equal(r.preview.rulesToCreate.length,1);assert.equal(r.preview.bindings.length,1);
  assert.deepEqual(r.preview.rulesToCreate[0].unspecifiedFields,['preferred','max']);
  assert.ok(r.preview.rulesToCreate[0].retainedNativeValues.length>0);
  assert.equal(r.preview.rulesToCreate[0].dryRunOnly,false);assert.equal(JSON.stringify(before),bytes);
});

const pairsConstraint={type:'safe_spacing',target:track.target,pairs:[
  {objects:['Track','SMD Pad'],clearance:0.1778},{objects:['Via','Via'],clearance:0.1778}]};
test('pair preview changes only labelled cells across layers and preserves all other native cells',()=>{
  const snapshot=structuredClone(before);
  const map=snapshot.configuration.config.Spacing['Safe Spacing'];
  const base=Object.values(map).find(v=>v.isSetDefault);base.tables[2]=structuredClone(base.tables[1]);
  const original=structuredClone(snapshot),r=describeDryRun(ir([pairsConstraint]),snapshot);
  assert.equal(r.status,'ready');assert.equal(r.preview.candidateOnly,false);
  const c=r.plan.constraints[0],rule=r.plan.configuration.config.Spacing['Safe Spacing'][c.bindings[0].ruleName];
  assert.equal(c.changedCells.length,4);assert.ok(c.retainedNativeValues.length>0);
  for(const [layer,table] of Object.entries(base.tables)) for(const [row,values] of table.content.entries()) for(const [col,value] of values.entries()) {
    const expected=(row===1&&col===0)||(row===5&&col===5)?0.1778:value;
    assert.ok(Math.abs(rule.tables[layer].content[row][col]-expected)<1e-12);
  }
  assert.deepEqual(snapshot,original);assert.equal(r.preview.rulesToCreate[0].changedCells.length,4);
});
test('pair ordering and reversal are canonical but duplicate or conditional pairs fail',()=>{
  const first=describeDryRun(ir([pairsConstraint]),before);
  const reversed={...pairsConstraint,pairs:[...pairsConstraint.pairs].reverse().map(p=>({...p,objects:[...p.objects].reverse()}))};
  assert.equal(first.plan.constraints[0].bindings[0].ruleName,describeDryRun(ir([reversed]),before).plan.constraints[0].bindings[0].ruleName);
  for(const c of [
    {...pairsConstraint,clearance:0.2}, {...pairsConstraint,pairs:[]},
    {...pairsConstraint,pairs:[pairsConstraint.pairs[0],{...pairsConstraint.pairs[0],objects:['SMD Pad','Track']}]},
    {...pairsConstraint,pairs:[{objects:['Hole','Track'],clearance:0.2}]},
    {...pairsConstraint,pairs:[{objects:['Via','Via'],clearance:0.2032,sameLayer:true}]},
    {...pairsConstraint,pairs:[{objects:['Via','Via'],clearance:NaN}]}])
    assert.equal(describeDryRun(ir([c]),before).status,'fail');
});
test('missing or ambiguous matrix labels/cells are unknown without guessing orientation',()=>{
  for(const alter of [
    r=>{r.row[1]='unknown';},r=>{r.column[2]='SMD Pad';},
    r=>{r.tables[1].content[0].push(0.2);},r=>{r.tables[1].content[5].pop();}]) {
    const b=structuredClone(before),r=Object.values(b.configuration.config.Spacing['Safe Spacing']).find(v=>v.isSetDefault);alter(r);
    assert.equal(describeDryRun(ir([pairsConstraint]),b).status,'unknown');
  }
});
test('pair-only batch applies through the same Connector executor with readback and DRC',async()=>{
  const state=structuredClone({configuration:before.configuration,netRules:before.netRules,classes:before.classes,
    lines:[],vias:[],pads:[{primitiveId:'pad',net:'GND'}]});
  const initial=structuredClone(state),writes=[];
  const eda={dmt_SelectControl:{getCurrentDocumentInfo:async()=>({uuid:captured.target.documentUuid,parentProjectUuid:captured.target.projectUuid,documentType:3})},
    pcb_Drc:{getCurrentRuleConfiguration:async()=>state.configuration,getNetRules:async()=>state.netRules,getAllNetClasses:async()=>state.classes,
      overwriteCurrentRuleConfiguration:async config=>{writes.push('config');state.configuration.config=structuredClone(config);return true;},
      overwriteNetRules:async rows=>{writes.push('bindings');state.netRules=structuredClone(rows);return true;},
      check:async()=>{writes.push('drc');return [];}}};
  for(const [kind,key]of[['Line','lines'],['Via','vias'],['Pad','pads']])eda['pcb_Primitive'+kind]={getAll:async()=>state[key]};
  const fn=Object.getPrototypeOf(async()=>{}).constructor;
  const result=await new fn('eda',buildConstraintScript({mode:'apply',ir:ir([pairsConstraint]),before:initial}))(eda);
  assert.equal(result.status,'pass');assert.deepEqual(writes,['config','bindings','drc']);
  assert.equal(result.configurationReadback,true);assert.equal(result.netRulesReadback,true);
});

// Pure compiler and native executor. The same functions run in Node and the existing Connector.
export function compilePcbConstraints(ir, before) {
  const copy=x=>JSON.parse(JSON.stringify(x));
  const same=(a,b,tolerance=1e-12)=>{
    if(typeof a==='number'&&typeof b==='number')return Math.abs(a-b)<=tolerance;
    if(a===null||b===null||typeof a!=='object'||typeof b!=='object')return a===b;
    return Array.isArray(a)===Array.isArray(b)&&Object.keys(a).length===Object.keys(b).length&&Object.keys(a).every(k=>k in b&&same(a[k],b[k],tolerance));
  };
  const bad=reason=>({status:'fail',reason,operations:[]});
  if(!ir || ir.version!=='pcb-constraint/v0' || ir.unit!=='mm' || !['projectUuid','documentUuid'].every(k=>typeof ir.target?.[k]==='string'&&ir.target[k].trim()) || !Array.isArray(ir.constraints) || !ir.constraints.length)
    return bad('Invalid PcbConstraintIR v0 envelope; explicit target and mm required');
  const supported=['track_width','via_size','safe_spacing'];
  const unsupported=ir.constraints.filter(c=>!supported.includes(c?.type)).map(c=>({type:c?.type??null,status:'unsupported_in_constraint_v0',reason:'native mapping not yet verified'}));
  if(unsupported.length) return {status:'unsupported_in_constraint_v0',reason:'native mapping not yet verified',unsupported,operations:[]};
  if(!before?.configuration?.config || !Array.isArray(before.netRules) || !Array.isArray(before.classes)) return {status:'unknown',reason:'Native rule snapshot unavailable',operations:[]};
  const configuration=copy(before.configuration), netRules=copy(before.netRules);
  const entries=rows=>rows.flatMap(r=>[r,...entries(r.sub||[])]);
  const nets=entries(netRules).filter(r=>r.type==='net');
  const results=[], assignments=new Map();
  // Observed native storage uses 0.0001 mil. Preserve original numbers and report effective values.
  const q=(v,mode='round')=>Number((Math[mode](Number((v/0.00000254).toFixed(8)))*0.00000254).toFixed(8));
  const numeric=v=>typeof v==='number' && Number.isFinite(v) && v>0;
  const resolve=n=>[...new Set(nets.filter(r=>r.name.toUpperCase()===n.toUpperCase()).map(r=>r.name))];
  for(const [index,c] of ir.constraints.entries()) {
    const selector=c.target;
    if(!selector || !['net','netClass'].includes(selector.kind) || typeof selector.name!=='string' || !selector.name.trim()) return bad(`Invalid selector at ${index}`);
    if(c.evidenceRef!==undefined && (typeof c.evidenceRef!=='string' || !c.evidenceRef.trim())) return bad(`Invalid evidenceRef at ${index}`);
    const requested=selector.kind==='net'?[selector.name]:selector.nets;
    if(!Array.isArray(requested) || !requested.length || requested.some(n=>typeof n!=='string' || !n.trim())) return bad(`Explicit member nets required at ${index}`);
    const selected=[];
    for(const n of requested) {
      const found=resolve(n);
      if(found.length!==1) return bad(`Missing or ambiguous target Net: ${n}`);
      if(!selected.includes(found[0])) selected.push(found[0]);
    }
    if(selector.kind==='netClass') {
      const classes=before.classes.filter(v=>v.name===selector.name);
      if(classes.length>1) return bad(`Ambiguous NetClass: ${selector.name}`);
      if(classes.length && !same([...classes[0].nets].sort(),[...selected].sort())) return bad(`NetClass membership differs from declared nets: ${selector.name}`);
    }
    let category,field,values,preservesNative=false,pairs;
    const unspecifiedFields=[],retainedNativeValues=[],changedCells=[];
    const provided=k=>Object.prototype.hasOwnProperty.call(c,k);
    if(c.type==='track_width') {
      if(!numeric(c.min) || ['preferred','max'].some(k=>provided(k)&&!numeric(c[k])) ||
        (provided('preferred')&&c.min>c.preferred) || (provided('max')&&c.min>c.max) ||
        (provided('preferred')&&provided('max')&&c.preferred>c.max)) return bad(`Invalid track_width range at ${index}`);
      unspecifiedFields.push(...['preferred','max'].filter(k=>!provided(k)));
      preservesNative=unspecifiedFields.length>0;
      category='Physics';field='Track';values=[q(c.min,'ceil'),provided('preferred')?q(c.preferred):null,provided('max')?q(c.max,'floor'):null];
    } else if(c.type==='via_size') {
      category='Physics';field='Via Size';
      if(provided('holeMin')) {
        if(!numeric(c.holeMin) || ['hole','diameterMin','diameterPreferred','diameterMax'].some(provided)) return bad(`Invalid via_size lower-bound input at ${index}`);
        preservesNative=true;unspecifiedFields.push('holePreferred','holeMax','diameterMin','diameterPreferred','diameterMax');
        values=[q(c.holeMin,'ceil')];
      } else {
        if(![c.hole,c.diameterMin,c.diameterPreferred,c.diameterMax].every(numeric) || !(c.hole<c.diameterMin && c.diameterMin<=c.diameterPreferred && c.diameterPreferred<=c.diameterMax)) return bad(`Invalid via_size range at ${index}`);
        values=[q(c.hole),q(c.diameterMin,'ceil'),q(c.diameterPreferred),q(c.diameterMax,'floor')];
      }
    } else {
      category='Spacing';field='Safe Spacing';
      if(provided('pairs')) {
        if(provided('clearance') || !Array.isArray(c.pairs) || !c.pairs.length) return bad(`Invalid safe_spacing pairs at ${index}`);
        const allowed=['Track','SMD Pad','TH Pad','Via'],seen=new Set();
        pairs=[];
        for(const pair of c.pairs) {
          if(!Array.isArray(pair?.objects) || pair.objects.length!==2 || pair.objects.some(o=>!allowed.includes(o)) || !numeric(pair.clearance) || Object.keys(pair).some(k=>!['objects','clearance'].includes(k)))
            return bad(`Invalid safe_spacing pair at ${index}`);
          const objects=[...pair.objects].sort(),key=objects.join('\0');
          if(seen.has(key)) return bad(`Duplicate safe_spacing pair at ${index}`);
          seen.add(key);pairs.push({objects,clearance:q(pair.clearance,'ceil')});
        }
        pairs.sort((a,b)=>a.objects.join('\0')<b.objects.join('\0')?-1:1);
        values=pairs.map(p=>p.clearance);preservesNative=true;
      } else {
        if(!numeric(c.clearance)) return bad(`Invalid safe_spacing at ${index}`);
        values=[q(c.clearance,'ceil')];
      }
    }
    const range=values.slice(c.type==='via_size'?1:0);
    if(values.some(v=>v!==null&&v<=0) || (!preservesNative&&range.length===3 && !(range[0]<=range[1] && range[1]<=range[2])) || (!preservesNative&&c.type==='via_size'&&values[0]>=values[1])) return bad(`Range below native numeric resolution at ${index}`);
    const map=configuration.config[category]?.[field];
    if(!map || typeof map!=='object' || Array.isArray(map)) return {status:'unknown',reason:`Native ${field} schema unavailable`,operations:[]};
    const defaults=Object.values(map).filter(r=>r?.isSetDefault===true && r.unit==='mm');
    if(defaults.length!==1) return {status:'unknown',reason:`Native ${field} default template ambiguous`,operations:[]};
    const rule=copy(defaults[0]);
    if(c.type==='track_width') {
      if(!rule.form?.data || !Object.keys(rule.form.data).length) return {status:'unknown',reason:'Track table unavailable',operations:[]};
      for(const [layer,v] of Object.entries(rule.form.data)) {
        if(!v || !['minValue','defaultValue','maxValue'].every(k=>k in v)) return {status:'unknown',reason:'Track table shape unknown',operations:[]};
        for(const [i,key] of ['minValue','defaultValue','maxValue'].entries()) {
          if(values[i]!==null) v[key]=values[i];
          else retainedNativeValues.push({field:`form.data.${layer}.${key}`,value:v[key]});
        }
        if(preservesNative && (!['minValue','defaultValue','maxValue'].every(k=>numeric(v[k])) || !(v.minValue<=v.defaultValue&&v.defaultValue<=v.maxValue)))
          return {status:'unknown',reason:'Track minimum incompatible with retained native preferred/max; no automatic replacement',operations:[]};
      }
    } else if(c.type==='via_size') {
      const keys=['viaInnerdiameterMin','viaInnerdiameterDefault','viaInnerdiameterMax','viaOuterdiameterMin','viaOuterdiameterDefault','viaOuterdiameterMax'];
      if(!rule.form || keys.some(k=>!(k in rule.form))) return {status:'unknown',reason:'Via Size form unknown',operations:[]};
      if(preservesNative) {
        rule.form.viaInnerdiameterMin=values[0];
        retainedNativeValues.push(...keys.slice(1).map(field=>({field:'form.'+field,value:rule.form[field]})));
        const f=rule.form;
        if(!keys.every(k=>numeric(f[k])) || !(f.viaInnerdiameterMin<=f.viaInnerdiameterDefault&&f.viaInnerdiameterDefault<=f.viaInnerdiameterMax&&
          f.viaOuterdiameterMin<=f.viaOuterdiameterDefault&&f.viaOuterdiameterDefault<=f.viaOuterdiameterMax&&f.viaInnerdiameterMin<f.viaOuterdiameterMin))
          return {status:'unknown',reason:'Via minimum incompatible with retained native dimensions; no automatic replacement',operations:[]};
      } else Object.assign(rule.form,Object.fromEntries(keys.map((k,i)=>[k,i<3?values[0]:values[i-2]])));
    } else {
      if(!Array.isArray(rule.row) || !Array.isArray(rule.column) || !rule.tables || !Object.keys(rule.tables).length) return {status:'unknown',reason:'Safe Spacing table unavailable',operations:[]};
      for(const table of Object.values(rule.tables)) {
        if(!Array.isArray(table.content) || table.content.length!==rule.row.length || table.content.some(row=>!Array.isArray(row)||!row.length||row.some(v=>typeof v!=='number'||!Number.isFinite(v)))) return {status:'unknown',reason:'Safe Spacing matrix shape unknown',operations:[]};
        if(!pairs) table.content=table.content.map(row=>row.map(()=>values[0]));
      }
      if(pairs) for(const [layer,table] of Object.entries(rule.tables)) {
        const touched=new Set();
        for(const pair of pairs) {
          for(const label of pair.objects) if(rule.row.filter(v=>v===label).length!==1 || rule.column.filter(v=>v===label).length!==1)
            return {status:'unknown',reason:`Safe Spacing pair labels missing or ambiguous: ${label}`,operations:[]};
          const candidates=pair.objects.map((label,i)=>[rule.row.indexOf(label),rule.column.indexOf(pair.objects[1-i])]);
          const cells=[...new Map(candidates.filter(([r,c])=>typeof table.content[r]?.[c]==='number').map(cell=>[cell.join(','),cell])).values()];
          if(cells.length!==1) return {status:'unknown',reason:'Safe Spacing pair cell missing or ambiguous; no matrix orientation guessed',operations:[]};
          const [r,c]=cells[0],path=`tables.${layer}.content.${r}.${c}`;
          changedCells.push({field:path,objects:pair.objects,before:table.content[r][c],after:pair.clearance});
          table.content[r][c]=pair.clearance;touched.add(r+','+c);
        }
        table.content.forEach((row,r)=>row.forEach((value,c)=>{
          if(!touched.has(r+','+c))retainedNativeValues.push({field:`tables.${layer}.content.${r}.${c}`,value});
        }));
      }
    }
    let pairHash=2166136261;
    if(pairs) for(const char of JSON.stringify(pairs)) pairHash=Math.imul(pairHash^char.charCodeAt(0),16777619)>>>0;
    const generatedName=pairs?'CCv0_safe_spacing_pairs_'+pairHash.toString(16):
      preservesNative?'CCv0_'+c.type+(c.type==='via_size'?'_min_':'_')+values.map(v=>v===null?'unspecified':Number(v.toFixed(8))).join('_'):
      'CCv0_'+c.type+'_'+values.map(v=>Number(v.toFixed(8))).join('_');
    rule.editName=generatedName;rule.isSetDefault=false;
    const parameters=r=>{const v=copy(r);delete v.editName;delete v.isSetDefault;return v;};
    // Native conversion can move a length by one storage step; design bounds still hold exactly.
    const nativeTolerance=0.00000254+1e-12;
    const meets=r=>{
      if(c.type==='track_width') return Object.values(r.form.data).every(v=>v.minValue>=c.min-1e-12&&
        (!provided('max')||v.maxValue<=c.max+1e-12)&&(!provided('preferred')||Math.abs(v.defaultValue-c.preferred)<=nativeTolerance)&&
        v.minValue<=v.defaultValue&&v.defaultValue<=v.maxValue);
      if(c.type==='via_size') {
        const f=r.form;
        return provided('holeMin')?f.viaInnerdiameterMin>=c.holeMin-1e-12:
          ['viaInnerdiameterMin','viaInnerdiameterDefault','viaInnerdiameterMax'].every(k=>Math.abs(f[k]-c.hole)<=nativeTolerance)&&
          f.viaOuterdiameterMin>=c.diameterMin-1e-12&&f.viaOuterdiameterMax<=c.diameterMax+1e-12&&Math.abs(f.viaOuterdiameterDefault-c.diameterPreferred)<=nativeTolerance;
      }
      if(pairs) return changedCells.every(cell=>{
        const [,layer,,row,col]=cell.field.split('.');return r.tables[layer].content[row][col]>=cell.after-1e-12;
      });
      return Object.values(r.tables).every(t=>t.content.every(row=>row.every(v=>v>=c.clearance-1e-12)));
    };
    const bindings=[];
    for(const net of selected) {
      const rows=nets.filter(r=>r.name===net);
      if(rows.some(r=>!(field in r))) return {status:'unknown',reason:`Explicit ${field} binding field unavailable for ${net}`,operations:[]};
      const key=net+'\0'+field, previous=assignments.get(key);
      if(previous && !same(previous.rule,parameters(rule))) return bad(`Conflicting constraints on ${net} / ${field}`);
      const explicit=[...new Set(rows.map(r=>r[field]).filter(v=>v!==null && v!==undefined && v!=='default'))];
      if(explicit.length>1) return bad(`Conflicting existing member bindings on ${net} / ${field}`);
      const existing=explicit[0];
      if(existing && (!map[existing] || !same(parameters(map[existing]),parameters(rule),nativeTolerance)||!meets(map[existing]))) return bad(`Existing explicit rule conflicts: ${net} / ${field} = ${existing}`);
      const name=existing || previous?.name || generatedName;
      if(name===generatedName && map[name] && (!same(map[name],rule,nativeTolerance)||!meets(map[name]))) return bad(`Native rule name collision: ${name}`);
      if(!map[name]) map[name]=copy(rule);
      rows.forEach(r=>r[field]=name);
      assignments.set(key,{name,rule:parameters(rule)});
      bindings.push({net,field,ruleName:name,reused:!!existing});
    }
    results.push({index,type:c.type,target:copy(selector),nets:selected,evidenceRef:c.evidenceRef??null,
      requested:copy(c),effectiveValues:values,category,field,bindings,dryRunOnly:false,unspecifiedFields,retainedNativeValues,changedCells});
  }
  return {status:'ready',target:copy(ir.target),constraints:results,configuration,netRules,
    changes:{configuration:!same(configuration,before.configuration),netRules:!same(netRules,before.netRules)},
    inheritance:'explicit_member_nets_only',unit:'mm',nativeResolutionMm:0.00000254};
}

export function samePcbSnapshot(before,current,target,equal) {
  if(equal(before,current)) return true;
  if(typeof before?.source!=='string' || typeof current?.source!=='string') return false;
  const {source:a,...factsA}=before,{source:b,...factsB}=current;
  if(!equal(factsA,factsB)) return false;
  const i=a.indexOf('\n'),j=b.indexOf('\n');
  if(i<0 || j<0 || a.slice(i)!==b.slice(j)) return false;
  const header=s=>{
    const divider=s.indexOf('||');
    if(divider<0 || !s.endsWith('|')) throw Error('Unknown document header');
    const meta=JSON.parse(s.slice(0,divider)),data=JSON.parse(s.slice(divider+2,-1));
    if(meta.type!=='DOCHEAD' || data.docType!=='PCB' || data.uuid!==target.documentUuid) throw Error('Document header identity differs');
    if(typeof data.client!=='string' || !Number.isFinite(data.updateTime) || typeof data.version!=='string') throw Error('Unknown header metadata structure');
    for(const key of ['client','updateTime','version']) delete data[key];
    return {meta,data};
  };
  // Header revision semantics are unknown; require the complete body and all other observed facts to match.
  try{return equal(header(a.slice(0,i)),header(b.slice(0,j)));}catch{return false;}
}

export async function executePcbConstraints(eda,input,compile,signal,sameSnapshot=samePcbSnapshot) {
  if(!['read','apply','review','save-reviewed'].includes(input.mode)) throw Error('Unknown PCB execution mode; no action');
  const clone=x=>JSON.parse(JSON.stringify(x));
  const equal=(a,b,tolerance=1e-12)=>{
    if(typeof a==='number' && typeof b==='number') return Math.abs(a-b)<=tolerance;
    if(a===null || b===null || typeof a!=='object'||typeof b!=='object') return a===b;
    return Array.isArray(a)===Array.isArray(b) && Object.keys(a).length===Object.keys(b).length && Object.keys(a).every(k=>k in b && equal(a[k],b[k],tolerance));
  };
  const difference=(a,b,path=[])=>{
    if(equal(a,b)) return null;
    if(a!==null && b!==null && typeof a==='object' && typeof b==='object' && Array.isArray(a)===Array.isArray(b)) {
      for(const k of new Set([...Object.keys(a),...Object.keys(b)])) {
        if(!(k in a) || !(k in b)) return {path:[...path,k],missingFrom:k in a?'current':'before'};
        if(!equal(a[k],b[k])) return difference(a[k],b[k],[...path,k]);
      }
    }
    const summary=x=>typeof x==='string'?{length:x.length,preview:x.slice(0,200)}:
      x!==null && typeof x==='object'?{kind:Array.isArray(x)?'array':'object',keys:Object.keys(x).length}:x;
    return {path,before:summary(a),current:summary(b)};
  };
  const identity=async()=>{
    if(signal?.aborted) throw Error('Execution cancelled; stop remaining operations');
    const d=await eda.dmt_SelectControl.getCurrentDocumentInfo();
    if(signal?.aborted) throw Error('Execution cancelled; stop remaining operations');
    if(d?.uuid!==input.ir.target.documentUuid || d?.parentProjectUuid!==input.ir.target.projectUuid || d?.documentType!==3) throw Error('PCB identity mismatch; no navigation or write');
    return d;
  };
  const D=eda.pcb_Drc;
  const wanted=new Set((input.ir.constraints||[]).flatMap(c=>c?.target?.kind==='net'?[c.target.name]:c?.target?.nets||[]).filter(n=>typeof n==='string').map(n=>n.toUpperCase()));
  const scoped=rows=>input.allObjects?rows:rows.filter(o=>typeof o.net==='string'&&wanted.has(o.net.toUpperCase()));
  const read=async()=>{
    await identity();
    return clone({configuration:await D.getCurrentRuleConfiguration(),netRules:await D.getNetRules(),classes:await D.getAllNetClasses(),
      ...(input.onlyRules?{}:{lines:scoped(await eda.pcb_PrimitiveLine.getAll()),vias:scoped(await eda.pcb_PrimitiveVia.getAll()),pads:scoped(await eda.pcb_PrimitivePad.getAll())}),
      ...(input.allObjects?{source:await eda.sys_FileManager.getDocumentSource()}: {})});
  };
  if(input.mode==='read') return {identity:await identity(),before:await read()};
  const report={status:'unknown',target:input.ir.target,before:input.before,operations:[],after:null,drc:null,boardStatus:'unknown',constraints:[],saved:false};
  try {
    const current=await read();
    if(!equal(current,input.before)) {
      report.current=current;report.snapshotDifference=difference(input.before,current);
      if(!sameSnapshot(input.before,current,input.ir.target,equal)) return {...report,
        reason:'State changed since durable before snapshot; no writes'};
      report.snapshotComparison={stable:true,basis:'Complete document body and other snapshot facts unchanged',headerFields:['client','updateTime','version']};
    }
    const plan=compile(input.ir,current);report.plan=plan;
    if(plan.status!=='ready') return {...report,status:plan.status,reason:plan.reason};
    if(input.mode==='save-reviewed') {
      report.current=current;
      if(input.allObjects!==true||typeof input.before?.source!=='string'||plan.changes.configuration||plan.changes.netRules)
        return {...report,reason:'Save requires the complete reviewed snapshot and unchanged explicit rule bindings; no save'};
      await identity();
      const saved=await eda.pcb_Document.save();
      report.operations.push({label:'pcb_Document.save()',return:saved===undefined?null:saved});
      report.saved=saved===true;
      report.after=await read();
      report.snapshotStable=sameSnapshot(current,report.after,input.ir.target,equal);
      report.status=report.saved&&report.snapshotStable?'pass':'unknown';
      if(report.status!=='pass')report.reason=report.saved?'PCB changed during save; inspect actual state, do not replay':'Native save did not return true; inspect actual state, do not replay';
      return report;
    }
    if(input.mode==='review' && (plan.changes.configuration||plan.changes.netRules))
      return {...report,reason:'Post-route review is read-only; required rules or bindings are absent'};
    for(const [label,needed,fn] of [
      ['overwriteCurrentRuleConfiguration',plan.changes.configuration,()=>D.overwriteCurrentRuleConfiguration(plan.configuration.config)],
      ['overwriteNetRules',plan.changes.netRules,()=>D.overwriteNetRules(plan.netRules)]]) {
      if(!needed) continue;
      await identity();
      // A second full read avoids overwriting an intervening user change after the first write.
      const observed={configuration:await D.getCurrentRuleConfiguration(),netRules:await D.getNetRules()};
      const expectedConfig=label==='overwriteNetRules'?plan.configuration:current.configuration;
      if(!equal(observed.configuration.config,expectedConfig.config,0.00000254+1e-12)||!equal(observed.netRules,current.netRules)) throw Error('Rules changed during apply; stop without replay');
      if(compile(input.ir,{...current,configuration:observed.configuration}).status!=='ready') throw Error('Native rule values violate requested bounds; stop before binding');
      if(signal?.aborted) throw Error('Execution cancelled; stop remaining operations');
      const value=await fn();report.operations.push({label,return:value===undefined?null:value});
      if(value!==true) throw Error(label+' returned no confirmed success; read back before retry');
    }
    report.after=await read();
    report.configurationReadback=equal(report.after.configuration.config,plan.configuration.config,0.00000254+1e-12);
    report.netRulesReadback=equal(report.after.netRules,plan.netRules);
    if(!report.configurationReadback || !report.netRulesReadback) throw Error('Rule or explicit binding readback differs');
    if(compile(input.ir,report.after).status!=='ready') throw Error('Native rule values violate requested bounds after binding');
    await identity();report.drc=await D.check(true,false,true);
    report.operations.push({label:'pcb_Drc.check(true,false,true)',return:report.drc});
    let known=Array.isArray(report.drc);
    const leaves=[];
    const walk=x=>{
      if(Array.isArray(x)){x.forEach(walk);return;}
      if(!x || typeof x!=='object'){known=false;return;}
      if(Array.isArray(x.objs) && typeof x.errorType==='string' && typeof x.ruleName==='string') {leaves.push(x);return;}
      if(Array.isArray(x.list)){if(x.count>0&&!x.list.length)known=false;walk(x.list);return;}
      known=false;
    };
    if(known)walk(report.drc);
    report.boardStatus=known?(leaves.length?'fail':'pass'):'unknown';
    report.constraints=plan.constraints.map(c=>{
      const objects=[...report.after.lines,...report.after.vias,...report.after.pads].filter(o=>c.nets.includes(o.net));
      const ids=objects.map(o=>o.primitiveId),names=c.bindings.map(b=>b.ruleName);
      const errors=leaves.filter(v=>names.includes(v.ruleName)&&v.objs.some(id=>ids.includes(id)));
      const unlocated=leaves.filter(v=>names.includes(v.ruleName)&&!v.objs.some(id=>[...report.after.lines,...report.after.vias,...report.after.pads].some(o=>o.primitiveId===id)));
      const relevant=c.type==='track_width'?report.after.lines:c.type==='via_size'?report.after.vias:objects;
      const coverage=relevant.filter(o=>c.nets.includes(o.net)).map(o=>o.primitiveId);
      return {...c,status:errors.length?'fail':(!known||unlocated.length||!coverage.length?'unknown':'pass'),
        explicitBindings:c.bindings,objectIds:coverage,violations:errors,unlocatedViolations:unlocated};
    });
    report.status=report.constraints.some(c=>c.status==='fail')?'fail':report.constraints.some(c=>c.status==='unknown')?'unknown':'pass';
  } catch(e) {
    report.reason=String(e);
    try {report.after=await read();}catch(readError){report.readbackError=String(readError);}
  }
  return report;
}

export function buildConstraintScript(input) {
  return 'return await ('+executePcbConstraints.toString()+')(eda,'+JSON.stringify(input)+',('+compilePcbConstraints.toString()+'),typeof signal===\'undefined\'?undefined:signal,('+samePcbSnapshot.toString()+'));';
}

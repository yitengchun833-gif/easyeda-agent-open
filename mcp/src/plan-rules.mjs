import { isDeepStrictEqual } from 'node:util';
// Bind existing predicates to their actual receipts; check names never execute arbitrary actions.
const localChecks = new Set(['identity', 'pin_nets', 'valid_wires', 'pin_exit', 'text_overlap', 'network_scope', 'target']);
const targetChecks = new Set(['schematic.target.check', 'identity', 'pin_nets']);

// Mandatory task checks use the existing requirement protocol and predicates, including on reloaded plans.
export function taskRequirements(plan) {
  if (!plan.taskKind) return plan.requirements;
  if (!['new_module','incremental_module','local_edit'].includes(plan.taskKind)) throw new Error('Unsupported schematic task kind');
  const requirements=[...(plan.requirements??[])], sourceId=plan.sources[0]?.id;
  const add=(stage,when,method,check,componentKeys=[])=>{
    const rule={id:`automatic:${stage.id}:${check}`,sourceId,stageId:stage.id,when,method,check,severity:'error',componentKeys,netNames:[]};
    const old=requirements.find(item=>item.id===rule.id);
    if(old&&!isDeepStrictEqual(old,rule))throw new Error('Mandatory task requirement cannot be downgraded or replaced');
    if(!old)requirements.push(rule);
  };
  for(const stage of plan.stages??[]){
    add(stage,'before_write','checker','source_binding');
    if(stage.check==='final'){
      add(stage,'delivery','checker','schematic.target.check'); add(stage,'delivery','native','drc'); continue;
    }
    const actions=stage.steps.map(step=>step.action);
    if(!stage.routeFrom&&!stage.labelsFrom&&!actions.some(action=>action==='schematic.component.modify'||action==='schematic.attribute.modify'))continue;
    const keys=stage.componentKeys??plan.semanticTarget.components.filter(part=>stage.primitiveIds?.includes(plan.semanticTarget.idMap?.[part.key])).map(part=>part.key);
    for(const check of stage.routeFrom?['pin_nets','valid_wires','pin_exit']:stage.labelsFrom||displayOnlyStage(stage)?['identity']:['identity','pin_nets'])add(stage,'after_edit','checker',check,keys);
  }
  return requirements;
}

export function receiptFacts(receipt) {
  let value = receipt, context = {}, ok = true;
  while (value && typeof value === 'object' && !Array.isArray(value)) {
    if (value.isError === true || value.ok === false) ok = false;
    if (value.context && typeof value.context === 'object' && !Array.isArray(value.context)) context = value.context;
    const child = value.structuredContent ?? value.result;
    if (!child || typeof child !== 'object' || Array.isArray(child)) break;
    value = child;
  }
  return { value: value && typeof value === 'object' && !Array.isArray(value) ? value : {}, context, ok };
}
const sameTarget = (context, target) => ['projectUuid', 'documentUuid'].every(key => context?.[key] === target[key]);

export function localRuleFacts(report) {
  if (!report.designCheck) return receiptFacts(report.execution);
  const original = receiptFacts(report.execution);
  const unresolved = original.value.rules?.items?.filter(item => item.severity === 'error' && item.coverage !== 'not_applicable' && item.status !== 'pass') ?? [];
  const checked = receiptFacts(report.designCheck), binding = checked.value.designVerification, edit = report.edit;
  const same = isDeepStrictEqual;
  if (!original.ok || !sameTarget(original.context, report.target) || original.value.ok !== true || original.value.partial === true || !unresolved.length
    || unresolved.some(item => item.id !== 'pin_nets' || !['unknown', 'fail'].includes(item.status))
    || edit?.mode !== 'design' || edit.complete === false || binding?.mode !== 'design'
    || !same(binding.primitiveIds, edit.primitiveIds) || !same(binding.expectedPins, edit.expectedPins)
    || !same(binding.expectedIdentities, edit.expectedIdentities) || !sameTarget(checked.context, report.target)) {
    return { ok: false, value: {}, context: checked.context };
  }
  return checked;
}
const inStage = (rule, stage) => !rule.stageId || rule.stageId === stage.id;
const due = (rule, stage) => inStage(rule, stage) && (rule.when !== 'delivery' || stage.check === 'final');

function ruleScope(plan, rule, stage, stageIds) {
  const keys = new Set(rule.componentKeys);
  for (const name of rule.netNames) for (const member of plan.semanticTarget.nets.find(net => net.name === name).members) keys.add(member.componentKey);
  if (!keys.size) return { kind: rule.stageId ? 'local' : 'page', primitiveIds: rule.stageId ? [...stageIds] : [], componentKeys: [], available: !rule.stageId || stageIds.length > 0 };
  const ids = [...keys].map(key => plan.semanticTarget.idMap?.[key]);
  return { kind: 'local', primitiveIds: ids.filter(Boolean), componentKeys: [...keys], available: ids.every(id => typeof id === 'string' && id.length > 0) };
}

function observedScope(plan, scope, readback) {
  const { value, context, ok } = receiptFacts(readback), read = value.readScope;
  if (!ok || !sameTarget(context, plan.target) || value.partial === true || read?.concurrentChange !== false
    || !Array.isArray(read.missingIds) || read.missingIds.length || !Array.isArray(read.primitiveIds) || !Array.isArray(value.components)
    || !scope.primitiveIds.every(id => read.primitiveIds.includes(id) && value.components.some(part => part?.primitiveId === id))) return false;
  const ids = value.components.map(part => part?.primitiveId);
  if (ids.some(id => typeof id !== 'string' || !id) || new Set(ids).size !== ids.length) return false;
  return scope.componentKeys.every(key => {
    const expected = plan.semanticTarget.components.find(part => part.key === key);
    const actual = value.components.find(part => part?.primitiveId === plan.semanticTarget.idMap[key]);
    return expected && actual && actual.designator === expected.ref && (!expected.uniqueId || actual.uniqueId === expected.uniqueId);
  });
}

export function designPins(plan, ids) {
  const pins = {};
  for (const id of ids) {
    const part = plan.semanticTarget.components.find(part => plan.semanticTarget.idMap?.[part.key] === id);
    if (!part || !part.pins.length || part.pins.some(pin => !['connected', 'nc', 'unconnected'].includes(pin.state))) throw new Error('Design edit needs mapped independent pin targets for its complete component scope');
    for (const pin of part.pins) {
      const net = plan.semanticTarget.nets.find(net => net.name === pin.net);
      if (pin.state === 'connected' && net?.match === 'members') {
        const members = net.members.map(member => {
          const owner = plan.semanticTarget.components.find(c => c.key === member.componentKey);
          if (!owner || !ids.includes(plan.semanticTarget.idMap?.[owner.key])) throw new Error('Internal net members must all be in the design edit scope');
          return `${owner.ref}.${member.pinNumber}`;
        }).sort();
        pins[`${id}.${pin.number}`] = { members };
      } else pins[`${id}.${pin.number}`] = pin.state === 'nc' ? null : pin.net;
    }
  }
  return pins;
}

export function requiredEdit(plan, stage, edit) {
  const after = (plan.requirements ?? []).filter(rule => due(rule, stage) && rule.when === 'after_edit');
  const hard = after.filter(rule => rule.severity === 'error');
  if (hard.some(rule => rule.method !== 'checker' || !localChecks.has(rule.check))) throw new Error('Required after-edit check is unsupported; no EDA action sent');
  if (edit?.mode === 'design' && hard.some(rule => rule.check === 'identity')) throw new Error('Design identity requires the independent delivery target, not the layout-only after-edit identity predicate');
  if (hard.length && edit?.complete === false) throw new Error('A hard after-edit requirement must be bound to its complete modification stage');
  const rules = after.filter(rule => rule.method === 'checker' && localChecks.has(rule.check));
  const memberDesign = edit?.mode === 'design' && plan.semanticTarget.nets.some(net => net.match === 'members');
  if ((!rules.length && !memberDesign) || edit?.complete === false || !stage.steps.length) return edit;
  const ids = edit?.primitiveIds ?? stage.primitiveIds ?? stage.steps.filter(step => step.action === 'schematic.component.modify').map(step => step.payload?.primitiveId).filter(Boolean);
  if (!ids.length) {
    if (memberDesign || rules.some(rule => rule.severity === 'error')) throw new Error('Local requirements need the affected component IDs before writing; read/map the scope first');
    return edit;
  }
  for (const rule of rules.filter(rule => rule.severity === 'error')) {
    const scope = ruleScope(plan, rule, stage, ids);
    if (!scope.available || scope.kind === 'page' || scope.primitiveIds.some(id => !ids.includes(id))) throw new Error(`Local requirement scope is not bound to this edit: ${rule.id}`);
  }
  // Preserve caller intent; default to layout invariants, never guess a changed electrical design.
  const result = { mode: 'layout', ...edit, primitiveIds: [...new Set(ids)] };
  const identities = { ...edit?.expectedIdentities };
  for (const part of plan.semanticTarget.components) {
    const id = plan.semanticTarget.idMap?.[part.key];
    if (!result.primitiveIds.includes(id)) continue;
    const expected = { designator: part.ref, ...(part.uniqueId ? { uniqueId: part.uniqueId } : {}) };
    if (identities[id] && (identities[id].designator !== expected.designator || identities[id].uniqueId !== expected.uniqueId)) throw new Error('Edit expectedIdentities conflicts with the independent plan target');
    identities[id] = expected;
  }
  if (Object.keys(identities).length) result.expectedIdentities = identities;
  if (result.mode === 'design') {
    const expected = designPins(plan, result.primitiveIds);
    for (const [key, value] of Object.entries(edit.expectedPins ?? {})) {
      const [part, number] = key.split(/\.(?=[^.]+$)/), ref = plan.semanticTarget.components.find(component => component.ref === part);
      const actualKey = Object.hasOwn(expected, key) ? key : `${plan.semanticTarget.idMap?.[ref?.key]}.${number}`;
      if (!Object.hasOwn(expected, actualKey) || JSON.stringify(expected[actualKey]) !== JSON.stringify(value)) throw new Error('Design expectedPins conflicts with the independent plan target');
    }
    result.expectedPins = expected;
  }
  return result;
}

export function needsDrc(plan, stage) {
  return (plan.requirements ?? []).some(rule => due(rule, stage) && rule.when === 'delivery' && rule.method === 'native' && rule.check === 'drc');
}

export function requirementResults(plan, stage, report, { before = false, complete = true, executed = false, previousReceipts = [] } = {}) {
  const execution = localRuleFacts(report), target = receiptFacts(report.targetCheck), drc = receiptFacts(report.drcCheck);
  const items = (plan.requirements ?? []).filter(rule => !before || due(rule, stage) && rule.when === 'before_write').map(rule => {
    const declared = ruleScope(plan, rule, stage, report.primitiveIds ?? stage.primitiveIds ?? []);
    const row = { requirement: rule, status: 'unknown', severity: rule.severity, coverage: 'unavailable',
      scope: { kind: declared.kind, primitiveIds: declared.primitiveIds }, evidence: { kind: 'unavailable' }, note: 'No supported current evidence for this requirement.' };
    if (!due(rule, stage)) return { ...row, coverage: 'deferred', note: 'Outside the current stage/trigger; not a pass.' };
    if (rule.method === 'checker' && rule.check === 'source_binding' && rule.when === 'before_write') {
      return { ...row, status: 'pass', coverage: 'checked', evidence: { kind: 'source_binding', sourceId: rule.sourceId, sourceSha256: plan.sources.find(source => source.id === rule.sourceId).sha256 }, note: 'loadPlan verified this source file hash before dispatch; not proof of its engineering content.' };
    }
    if (before || rule.when === 'before_write') return row;
    if (!complete && rule.when === 'after_edit') return { ...row, coverage: 'deferred', note: 'Incomplete modification unit; electrical/visual acceptance deferred.' };
    if (!executed) return { ...row, note: 'Execution is partial or unknown; no postcondition can pass.' };
    if (rule.method === 'checker' && rule.when === 'delivery' && targetChecks.has(rule.check) && sameTarget(target.context, plan.target)) {
      const verified = target.ok && target.value.status === 'pass' && target.value.verified === true && target.value.partial === false;
      return { ...row, status: verified ? 'pass' : rule.check === 'schematic.target.check' && target.value.status === 'fail' ? 'fail' : 'unknown',
        coverage: verified ? 'checked' : 'unavailable', scope: { kind: 'page', primitiveIds: [] }, evidence: { kind: 'target_check' }, note: 'Current independent page target; a composite failure is not attributed to an individual predicate.' };
    }
    if (rule.method === 'checker' && rule.when === 'after_edit' && localChecks.has(rule.check)) {
      const rules = execution.value.rules, matching = Array.isArray(rules?.items) ? rules.items.filter(item => item?.id === rule.check) : [];
      if (!execution.ok || !sameTarget(execution.context, plan.target) || !declared.available || declared.kind !== 'local'
        || !Array.isArray(rules?.scope) || !declared.primitiveIds.length || !declared.primitiveIds.every(id => rules.scope.includes(id)) || matching.length !== 1
        || !observedScope(plan, declared, report.readback)) return row;
      if (rule.check === 'pin_nets' && rules.mode === 'design') {
        try {
          const ids = report.edit?.primitiveIds;
          if (!Array.isArray(ids) || !ids.length || !declared.primitiveIds.every(id => ids.includes(id))) return row;
          const pins = designPins(plan, ids), supplied = report.edit?.expectedPins;
          if (!supplied || Object.keys(pins).length !== Object.keys(supplied).length || Object.entries(pins).some(([key, value]) => JSON.stringify(supplied[key]) !== JSON.stringify(value))) return row;
        } catch { return row; }
      }
      const checked = matching[0];
      const status = ['pass', 'fail', 'unknown'].includes(checked.status) ? checked.status : 'unknown';
      const coverage = ['checked', 'unavailable', 'not_applicable'].includes(checked.coverage) ? checked.coverage : 'unavailable';
      return { ...row, severity: checked.severity === 'error' ? 'error' : row.severity, status: coverage === 'checked' ? status : 'unknown', coverage, evidence: { kind: 'local_rule', check: rule.check }, note: 'Named Connector predicate within its observed component scope; no unobserved page or source-text compliance claim.' };
    }
    if (rule.method === 'native' && rule.when === 'delivery' && rule.check === 'drc' && drc.ok && sameTarget(drc.context, plan.target)) {
      const summary = drc.value.summary, counts = ['error', 'fatal', 'unknown'].map(key => summary?.[key]);
      if (drc.value.countsAvailable !== true || drc.value.partial === true || !counts.every(count => Number.isInteger(count) && count >= 0)) return row;
      return { ...row, severity: 'error', status: counts[0] || counts[1] ? 'fail' : counts[2] ? 'unknown' : 'pass', coverage: counts[2] ? 'unavailable' : 'checked',
        scope: { kind: 'page', primitiveIds: [] }, evidence: { kind: 'drc' }, note: 'Native DRC counts; warnings do not block and DRC does not prove design or visual quality.' };
    }
    return row;
  });
  for (const row of items) {
    if (row.coverage !== 'deferred' || !row.requirement.stageId || row.requirement.when === 'delivery') continue;
    const index = plan.stages.findIndex(candidate => candidate.id === row.requirement.stageId);
    const currentIndex = plan.stages.findIndex(candidate => candidate.id === stage.id);
    if (index < 0 || index >= currentIndex) continue;
    const prior = previousReceipts.find(({ packet }) => packet.stage?.id === row.requirement.stageId);
    if (!prior) continue;
    const previousReport = prior.packet.receipt?.result ?? prior.packet.receipt;
    const old = requirementResults(plan, plan.stages[index], previousReport, { complete: previousReport.edit?.complete !== false, executed: true }).items
      .find(item => item.requirement.id === row.requirement.id && item.requirement.when === row.requirement.when && item.requirement.stageId === row.requirement.stageId);
    if (!old || old.coverage === 'deferred') continue;
    const touched = row.requirement.when === 'after_edit' && plan.stages.slice(index + 1, currentIndex + 1).some(candidate => {
      const observed = candidate.id === stage.id ? report : previousReceipts.find(({ packet }) => packet.stage?.id === candidate.id)?.packet.receipt;
      return stageInvalidatesRequirement(candidate, row.requirement.check, observed, plan.target);
    });
    if (touched) {

      let refreshed = false;

      for (let freshIndex = currentIndex; freshIndex > index; freshIndex--) {

        const candidate = plan.stages[freshIndex];

        const packet = previousReceipts.find(({ packet }) => packet.stage?.id === candidate.id);

        const current = candidate.id === stage.id;

        const observed = current ? report : packet?.packet.receipt?.result ?? packet?.packet.receipt;

        if (!observed || !current && observed.ok !== true) continue;

        if (plan.stages.slice(freshIndex + 1, currentIndex + 1).some(later => stageInvalidatesRequirement(later, row.requirement.check,

          later.id === stage.id ? report : previousReceipts.find(({ packet }) => packet.stage?.id === later.id)?.packet.receipt, plan.target))) continue;

        const fresh = requirementResults({ ...plan, requirements: [{ ...row.requirement, stageId: candidate.id }] }, candidate, observed,

          { complete: observed.edit?.complete !== false, executed: current ? executed : true }).items[0];

        if (fresh.coverage !== 'checked' || old.scope.kind !== 'local' || !old.scope.primitiveIds.length

          || !old.scope.primitiveIds.every(id => fresh.scope.primitiveIds.includes(id))) continue;

        Object.assign(row, fresh, { requirement: row.requirement, scope: old.scope,

          evidence: { ...fresh.evidence, stageId: candidate.id, ...(packet ? { receiptFile: packet.file, receiptSha256: packet.sha256 } : {}), validity: 'fresh_covering_check' },

          note: 'The same predicate was re-evaluated on current native evidence covering the original scope; prior failure/history was not rewritten.' });

        refreshed = true; break;

      }

      if (!refreshed) { row.coverage = 'unavailable'; row.note = 'A later stage changes or cannot prove preservation of this check dependency; bind a fresh check.'; }

      continue;

    }

    Object.assign(row, old, { evidence: { ...old.evidence, stageId: prior.packet.stage.id, receiptFile: prior.file, receiptSha256: prior.sha256, validity: 'post_stage' },
      note: old.note + ' Recomputed from the completed predecessor; later stages preserve this predicate. This is historical stage evidence, not a new electrical read.' });
  }
  const errors = items.filter(item => item.severity === 'error' && (item.coverage !== 'deferred' || stage.check === 'final'));
  return { items, status: errors.some(item => item.status === 'fail') ? 'fail' : errors.some(item => item.status !== 'pass') ? 'unknown' : 'pass',
    blocking: errors.some(item => item.status !== 'pass') };
}

export function stageInvalidatesRequirement(stage, check, report, target) {
  if (!stage.steps?.length && !stage.routeFrom) return false;
  const observed = receiptFacts(report), actual = observed.value;
  const native = receiptFacts(actual.execution), rules = native.value.rules;
  const displayVerified = displayOnlyStage(stage) && observed.ok && actual.stage?.completed === true
    && actual.edit?.complete !== false && native.ok && sameTarget(native.context, target)
    && rules?.status === 'pass' && Array.isArray(rules.items) && rules.items.some(item => item?.id === 'identity' && item.status === 'pass' && item.coverage === 'checked')
    && displayAttributesVerified(stage.steps, actual.execution, stage.primitiveIds, actual.readback);
  if (displayVerified) return !['identity', 'pin_nets', 'valid_wires', 'pin_exit', 'network_scope', 'target'].includes(check);
  // Unknown writes and connectivity changes can affect remote members of the same net.
  return true;
}

export function displayOnlyStage(stage) {
  const fields = ['x', 'y', 'rotation', 'color', 'fontName', 'fontSize', 'bold', 'italic', 'underLine', 'alignMode', 'fillColor', 'keyVisible', 'valueVisible'];
  return stage?.check === 'geometry' && Array.isArray(stage.steps) && stage.steps.length > 0 && stage.steps.every(step => {
    const payload = step?.payload, props = payload?.props;
    return step?.action === 'schematic.attribute.modify' && payload && typeof payload.primitiveId === 'string' && payload.primitiveId.trim()
      && Object.keys(payload).every(key => ['primitiveId', 'props', 'fontSizeUnit'].includes(key))
      && (payload.fontSizeUnit === undefined || ['native', 'display'].includes(payload.fontSizeUnit))
      && props && typeof props === 'object' && !Array.isArray(props) && Object.keys(props).length > 0
      && Object.keys(props).every(key => fields.includes(key));
  });
}

export function displayAttributesVerified(steps, execution, ids, readback) {
  const { value: batch, ok } = receiptFacts(execution);
  return Array.isArray(steps) && steps.length > 0 && Array.isArray(ids) && ids.length > 0
    && ok && Array.isArray(batch.results) && batch.results.length === steps.length && batch.results.every((result, index) => {
    const step = steps[index], { value: actual, ok: readOk } = receiptFacts(result), attribute = actual.attribute;
    return result?.ok === true && result.action === step.action && readOk && actual.verified === true && actual.partial !== true
      && actual.primitiveId === step.payload.primitiveId && attribute?.primitiveId === step.payload.primitiveId
      && displayOwnerVerified(attribute, ids, readback) && typeof attribute.Key === 'string' && attribute.Key.trim() && typeof attribute.Value === 'string'
      && Object.entries(step.payload.props).every(([key, expected]) => {
        const observed = attribute[key[0].toUpperCase() + key.slice(1)];
        if (expected === null || observed === undefined) return false;
        if (typeof expected === 'number' && (!Number.isFinite(expected) || !Number.isFinite(observed))) return false;
        if (key === 'fontSize' && step.payload.fontSizeUnit === 'display' && typeof expected === 'number') {
          return Number.isFinite(expected) && Number.isFinite(observed) && Math.abs(observed - expected) <= 1e-6;
        }
        return JSON.stringify(observed) === JSON.stringify(expected);
      });
  });
}


// A wire Name belongs to this scope only when actual geometry touches an observed scoped pin.
function displayOwnerVerified(attribute, ids, readback) {
  if (ids.includes(attribute?.ParentPrimitiveId)) return true;
  const { value, ok } = receiptFacts(readback);
  if (!ok || attribute?.Key !== 'Name' || value.wiresAvailable !== true || !Array.isArray(value.wires)
    || !Array.isArray(value.components) || value.readScope?.concurrentChange !== false || value.readScope?.missingIds?.length !== 0) return false;
  const wires = value.wires.filter(w => w.primitiveId === attribute.ParentPrimitiveId);
  const pins = value.components.filter(c => ids.includes(c.primitiveId) && c.pinsAvailable === true).flatMap(c => c.pins ?? []);
  return wires.length > 0 && wires.every(w => w.net === attribute.Value) && wires.some(w => pins.some(p => {
    if (![w.x0,w.y0,w.x1,w.y1,p.x,p.y].every(Number.isFinite)) return false;
    return Math.abs((p.x-w.x0)*(w.y1-w.y0)-(p.y-w.y0)*(w.x1-w.x0)) <= 1e-6
      && p.x >= Math.min(w.x0,w.x1)-1e-6 && p.x <= Math.max(w.x0,w.x1)+1e-6
      && p.y >= Math.min(w.y0,w.y1)-1e-6 && p.y <= Math.max(w.y0,w.y1)+1e-6
      && Math.hypot(w.x1-w.x0,w.y1-w.y0)>1e-6;
  }));
}

// The confirmed five-stage module is the first supported continuous slice, not an auto-designer.
export function continuousInputs(plan,catalog,{resuming=false}={}) {
  const missing = [], add=(path,reason)=>missing.push({path,reason});
  const stages=plan.stages??[],[place,measure,route,labels,final]=stages;
  const boundMeasurement = resuming && Array.isArray(measure?.primitiveIds) && measure.primitiveIds.length > 0
    && isDeepStrictEqual(measure.steps, [{ action:'schematic.components.list', payload:{primitiveIds:measure.primitiveIds,
      includePins:true, includePinNets:true, includeBBox:true, includeDeviceIdentity:false, includeTexts:false, includeWires:false} }]);
  if (stages.length!==5 || place?.check!=='geometry' || !place.steps.length
    || place.steps.some(step=>step.action!=='schematic.component.place') || measure?.check!=='geometry'
    || (measure.steps.length && !boundMeasurement) || measure.routeFrom || measure.labelsFrom || route?.routeFrom!==measure?.id
    || labels?.labelsFrom!==route?.id || final?.check!=='final' || final.steps.length)
    add('stages','Declare exactly place, empty logical measurement, routeFrom measurement, labelsFrom route, and final; arbitrary pipelines are not supported by continuous v0.');
  if (missing.length) return missing;
  for (const required of ['schematic.component.place','schematic.component.modify','schematic.components.list','schematic.wire.create','schematic.wire.labels.apply','schematic.target.check','schematic.export.image'])
    if (catalog && !catalog.has(required)) add('actionCatalog.'+required,'Required existing Connector action is unavailable.');
  if ((plan.missing??[]).length) add('missing','Resolve the plan\'s declared missing design evidence first.');
  for (const rule of plan.requirements??[]) if (rule.severity==='error' && rule.when==='delivery'
    && !(rule.method==='native'&&rule.check==='drc')
    && !(rule.method==='checker'&&['schematic.target.check','identity','pin_nets'].includes(rule.check)))
    add('requirements.'+rule.id,'This hard delivery requirement has no supported automatic predicate in continuous v0.');
  const parts=place.steps.map(step=>plan.semanticTarget.components.find(part=>part.ref===step.payload?.designator));
  const keys=parts.filter(Boolean).map(part=>part.key), unique=new Set(keys);
  if (parts.some(part=>!part) || unique.size!==place.steps.length || keys.length>=100) add('stages.'+place.id,'Placements need distinct declared parts; continuous v0 supports fewer than 100 new parts.');
  const fields=['name','manufacturer','manufacturerId','supplier','supplierId','addIntoBom','addIntoPcb','otherProperty'];
  for (let i=0;i<parts.length;i++) {
    const part=parts[i],payload=place.steps[i].payload,prefix='components.'+(part?.key??i);
    if (!part) continue;
    if (!resuming && plan.semanticTarget.idMap?.[part.key]) add(prefix+'.idMap','Already observed parts must not be placed again.');
    if (!part.uniqueId || payload.uniqueId!==part.uniqueId || payload.uuid!==part.device?.uuid
      || payload.libraryUuid!==part.device?.libraryUuid || !/^[a-f0-9]{32}$/i.test(payload.uuid??'')
      || !part.footprint?.uuid || !part.footprint?.libraryUuid) add(prefix+'.identity','Need exact device/library, footprint and matching unique identity.');
    if (!Number.isFinite(payload.x)||!Number.isFinite(payload.y)||!Number.isFinite(payload.rotation)
      || typeof payload.mirror!=='boolean') add(prefix+'.layout','Placement x/y/rotation/mirror must be explicit.');
    if (!part.pins.length || part.pins.some(pin=>!['connected','unconnected'].includes(pin.state))) add(prefix+'.pins','Resolve every pin; NC markers are outside this first continuous slice.');
    if (!part.properties || typeof part.properties!=='object' || Array.isArray(part.properties)
      || Object.keys(part.properties).some(key=>!fields.includes(key))) add(prefix+'.properties','Supply only supported instance values; geometry/identity belong in placement.');
    else for (const [key,value] of Object.entries(part.properties)) {
      if (key==='otherProperty' ? !value||typeof value!=='object'||Array.isArray(value)||Object.values(value).some(v=>!['string','number','boolean'].includes(typeof v))
        : ['addIntoBom','addIntoPcb'].includes(key)?typeof value!=='boolean':typeof value!=='string') add(prefix+'.properties.'+key,'Unsupported native property value; resolve before placement.');
    }
  }
  for (const stage of [measure,route,labels]) {
    if ((!resuming && stage.primitiveIds) || !Array.isArray(stage.componentKeys)||stage.componentKeys.length!==unique.size
      || stage.componentKeys.some(key=>!unique.has(key))) add('stages.'+stage.id+'.componentKeys','Use exactly the new module logical keys; native IDs are bound after placement.');
  }
  if (!Array.isArray(plan.layoutHints?.groups)||!keys.every(key=>plan.layoutHints.groups.some(group=>group.componentKeys.includes(key)))) add('layoutHints.groups','Every module part needs a confirmed functional group.');
  if (!Array.isArray(plan.layoutHints?.visibleNetNames)) add('layoutHints.visibleNetNames','Declare the visible interface names explicitly; internal direct nets remain unnamed.');
  const nets=plan.semanticTarget.nets.filter(net=>net.members.some(member=>unique.has(member.componentKey)));
  if (!nets.length) add('nets','This continuous slice needs an explicit connection; do not place a test module with no routing goal.');
  for (const net of nets) {
    const local=net.members.filter(member=>unique.has(member.componentKey));
    const stubs=(plan.layoutHints?.interfaceStubs??[]).filter(stub=>local.some(member=>member.componentKey===stub.componentKey&&member.pinNumber===stub.pinNumber));
    const named=local.length===1&&stubs.length===1&&net.match!=='members'&&plan.layoutHints?.visibleNetNames?.includes(net.name);
    if (plan.layoutHints?.netPolicies?.[net.name]!=='direct') add('layoutHints.netPolicies.'+net.name,'Only direct and previously supported named short-wire interfaces are supported.');
    if ((net.members.length!==local.length||stubs.length||local.length<2)&&!named) add('nets.'+net.name,'Include all internal endpoints or declare a single visible named interface stub.');
    if (local.length>=2&&net.members.length===local.length&&net.match!=='members') add('nets.'+net.name+'.match','Internal direct nets require exact members and no visible Name.');
  }
  // Validate fixed rule bindings before placement, using logical stand-ins only for validation, never EDA input.
  const mapped={...plan.semanticTarget.idMap,...Object.fromEntries(keys.map(key=>[key,'logical:'+key]))};
  const shadow={...plan,semanticTarget:{...plan.semanticTarget,idMap:mapped}};
  for (const stage of stages) {
    const before=requirementResults(shadow,stage,{}, {before:true});
    if (before.blocking) add('stages.'+stage.id+'.requirements','Required before-write predicate is unavailable.');
    try {
      const ids=stage.componentKeys?.map(key=>mapped[key]);
      requiredEdit(shadow,{...stage,...(ids?{primitiveIds:ids}:{}),
        steps:stage.routeFrom?[{action:'schematic.wire.create'}]:stage.labelsFrom?[{action:'schematic.wire.labels.apply'}]:stage.steps},
        ids?{mode:stage.routeFrom?'design':'layout',primitiveIds:ids}:undefined);
    } catch (error) { add('stages.'+stage.id+'.edit',error.message); }
    if (needsDrc(plan,stage)&&catalog&&!catalog.has('schematic.drc.check')) add('actionCatalog.schematic.drc.check','Required delivery DRC is unavailable.');
  }
  return missing;
}


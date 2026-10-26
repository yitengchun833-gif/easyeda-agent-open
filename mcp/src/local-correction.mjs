import { isDeepStrictEqual } from 'node:util';
import { fullInventory, preservationDiff } from './preservation.mjs';
import { displayOnlyStage } from './plan-rules.mjs';

const boxValid = box => box && ['minX','minY','maxX','maxY'].every(key => Number.isFinite(box[key]))
  && box.minX < box.maxX && box.minY < box.maxY;

// A region selects complete existing components; it never authorizes every
// primitive that happens to cross the rectangle or changes electrical values.
export function correctionScope(snapshot, input) {
  fullInventory(snapshot);
  if ((input.primitiveIds !== undefined) === (input.region !== undefined)) throw new Error('Choose exactly one of primitiveIds or region');
  if (input.region !== undefined && !boxValid(input.region)) throw new Error('region requires finite minX/minY/maxX/maxY');
  let ids = input.primitiveIds;
  if (input.region) ids = snapshot.components.filter(part => part.componentType === 'part' && boxValid(part.bbox)
    && part.bbox.minX >= input.region.minX && part.bbox.maxX <= input.region.maxX
    && part.bbox.minY >= input.region.minY && part.bbox.maxY <= input.region.maxY).map(part => part.primitiveId);
  if (!Array.isArray(ids) || !ids.length || ids.length > 500 || new Set(ids).size !== ids.length
    || ids.some(id => typeof id !== 'string' || !id.trim()
      || snapshot.components.filter(part => part.componentType === 'part' && part.primitiveId === id).length !== 1))
    throw new Error('Correction scope needs unique measured component IDs');
  if (!Array.isArray(input.corrections) || !input.corrections.length || input.corrections.length > 500) throw new Error('corrections requires 1..500 explicit attribute edits');
  const attributes = new Set();
  for (const payload of input.corrections) {
    if (!payload || Object.keys(payload).some(key => !['primitiveId','props','fontSizeUnit'].includes(key))) throw new Error('Correction accepts only primitiveId, props and fontSizeUnit');
    if (!displayOnlyStage({check:'geometry',steps:[{action:'schematic.attribute.modify',payload}]})) throw new Error('Only display attribute corrections are supported; electrical values and topology stay unchanged');
    for (const [key,value] of Object.entries(payload.props)) {
      const numeric = ['x','y','rotation','fontSize'].includes(key), boolean = ['bold','italic','underLine','keyVisible','valueVisible'].includes(key);
      if (numeric ? !Number.isFinite(value) || key === 'fontSize' && value <= 0
        : boolean ? typeof value !== 'boolean' : typeof value !== 'string' || !value.trim()) throw new Error(`Invalid display property ${key}`);
    }
    const records = snapshot.pagePrimitives.attributes.filter(attr => attr.primitiveId === payload.primitiveId);
    if (attributes.has(payload.primitiveId) || records.length !== 1 || !ids.includes(records[0].ParentPrimitiveId)
      || typeof records[0].Key !== 'string' || typeof records[0].Value !== 'string') throw new Error('Attribute identity/owner is ambiguous, repeated or outside the selected components');
    attributes.add(payload.primitiveId);
  }
  return { componentIds: [...ids], attributeIds: [...attributes] };
}

export function validateLocalCorrection(plan, snapshot) {
  if (!plan.localCorrection) return;
  const stages = plan.stages;
  if (plan.taskKind !== 'local_edit' || plan.acceptanceScope !== 'local_display' || stages?.length !== 1
    || !plan.executionReceipts?.directory || stages[0].id !== 'correct' || !displayOnlyStage(stages[0])) throw new Error('Local correction must remain one display-only scoped stage');
  const scope = correctionScope(snapshot, {primitiveIds: stages[0].primitiveIds, corrections: stages[0].steps.map(step => step.payload)});
  if (!isDeepStrictEqual(scope, plan.localCorrection)) throw new Error('Local correction scope differs from its bound attribute edits');
}

export function correctionPreservation(before, after, edits = []) {
  const attributeIds = edits.map(edit => edit.primitiveId);
  const preserved = preservationDiff(before, after, null, {attributes:attributeIds});
  // Even on an edited attribute, every field other than the explicitly requested
  // display properties stays protected, including owner, Key and electrical Value.
  for (const edit of edits) {
    const id = edit.primitiveId, requested = new Set(Object.keys(edit.props).map(key => key[0].toUpperCase()+key.slice(1)));
    const original = before.pagePrimitives.attributes.find(attr => attr.primitiveId === id);
    const actual = after.pagePrimitives.attributes.filter(attr => attr.primitiveId === id);
    const unchanged = attr => Object.fromEntries(Object.entries(attr).filter(([key]) => !requested.has(key)));
    if (!original || actual.length !== 1 || !isDeepStrictEqual(unchanged(original),unchanged(actual[0]))) preserved.changed.push(id);
  }
  preserved.changed = [...new Set(preserved.changed)];
  preserved.status = preserved.changed.length ? 'fail' : 'pass';
  return preserved;
}

// Read-only supplement to schematic.components.list: measure every visible attribute.
const context = await eda.dmt_SelectControl.getCurrentDocumentInfo();
const components = await eda.sch_PrimitiveComponent.getAll();
const wires = await eda.sch_PrimitiveWire.getAll();
const texts = [], errors = [];
const state = (p, key) => typeof p['getState_' + key] === 'function' ? (p['getState_' + key]() ?? null) : null;
async function bounds(id) {
  try { return await eda.sch_Primitive.getPrimitivesBBox([id]); }
  catch (e) { errors.push({id, kind: 'bbox_unavailable', message: String(e)}); return null; }
}
const seen = new Set();
async function attribute(a, parent) {
  const id = a.getState_PrimitiveId();
  if (seen.has(id)) return;
  seen.add(id);
  let keyVisible = state(a, 'KeyVisible'), valueVisible = state(a, 'ValueVisible');
  if (keyVisible === null || valueVisible === null) {
    try {
      const fresh = await eda.sch_PrimitiveAttribute.get(id);
      if (fresh && fresh.getState_PrimitiveId() === id) a = fresh;
    } catch (e) { errors.push({id, kind:'attribute_reread_unavailable', message:String(e)}); }
    keyVisible = state(a, 'KeyVisible'); valueVisible = state(a, 'ValueVisible');
  }
  const key = state(a, 'Key'), value = state(a, 'Value'), visible = state(a, 'Visible');
  const visibility = visible === false || (keyVisible === false && valueVisible === false)
    ? 'hidden' : (keyVisible === true || valueVisible === true) ? 'visible' : 'unknown';
  if (visibility === 'hidden') return;
  const bbox = await bounds(id);
  const rendered = bbox && bbox.maxX > bbox.minX && bbox.maxY > bbox.minY;
  // A zero box or missing anchor does not establish hidden state.
  texts.push({primitiveId:id, parentId:parent, key, value, text:String(value ?? key ?? ''),
    x:state(a,'X'), y:state(a,'Y'), rotation:state(a,'Rotation'), bbox,
    keyVisible, valueVisible, visible, visibility, visibilityKnown:visibility !== 'unknown', rendered:!!rendered});
}
for (const parent of [...components, ...wires]) {
  const id = parent.getState_PrimitiveId();
  try { for (const a of await eda.sch_PrimitiveAttribute.getAll(id)) await attribute(a, id); }
  catch (e) { errors.push({id,kind:'attributes_unavailable',message:String(e)}); }
}
try { for (const a of await eda.sch_PrimitiveAttribute.getAll()) await attribute(a, state(a,'ParentPrimitiveId')); }
catch (e) { errors.push({kind:'global_attributes_unavailable',message:String(e)}); }
// Free annotation text is an independent primitive type.
if (eda.sch_PrimitiveText && typeof eda.sch_PrimitiveText.getAll === 'function') {
  for (const t of await eda.sch_PrimitiveText.getAll()) {
    const id=t.getState_PrimitiveId();
    texts.push({primitiveId:id,parentId:null,key:'free_text',text:String(state(t,'Content') ?? state(t,'Text') ?? ''),
      x:state(t,'X'),y:state(t,'Y'),rotation:state(t,'Rotation'),bbox:await bounds(id),visibility:'visible',visibilityKnown:true});
  }
} else errors.push({kind:'free_text_api_unavailable'});
return {context,texts,errors,coverage:'visible parent attributes and free text; no estimated font boxes'};

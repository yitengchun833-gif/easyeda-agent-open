// Read-only debug.exec_js probe for issue #200. No open/import/save/activation.
const evidence = { hostVersion: eda.sys_Environment.getEditorCurrentVersion(), samples: [] };
async function probe(name, read, summarize = value => value) {
  let timer;
  const start = Date.now();
  try {
    const value = await Promise.race([
      Promise.resolve().then(read),
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('probe timeout (2000ms)')), 2000); }),
    ]);
    const result = summarize(value);
    evidence.samples.push({ name, elapsedMs: Date.now() - start, ok: true, result });
    return result;
  } catch (error) {
    evidence.samples.push({ name, elapsedMs: Date.now() - start, ok: false, error: String(error) });
    return undefined;
  } finally { clearTimeout(timer); }
}
function summarizeArray(value) {
  if (!Array.isArray(value)) return { isArray: false, valueType: typeof value };
  return { isArray: true, count: value.length };
}
const before = await probe('document.before', () => eda.dmt_SelectControl.getCurrentDocumentInfo());
await probe('project', () => eda.dmt_Project.getCurrentProjectInfo(), p => p && ({ uuid: p.uuid }));
await probe('pcb.current', () => eda.dmt_Pcb.getCurrentPcbInfo(), p => p && ({ uuid: p.uuid, name: p.name, parentProjectUuid: p.parentProjectUuid }));
await probe('pcb.documents', () => eda.dmt_Pcb.getAllPcbsInfo(), a => a?.map(p => ({ uuid: p.uuid, name: p.name, parentProjectUuid: p.parentProjectUuid })));
await probe('components.noArgs', () => eda.pcb_PrimitiveComponent.getAll(), summarizeArray);
await probe('components.undefined', () => eda.pcb_PrimitiveComponent.getAll(undefined), summarizeArray);
await probe('components.top', () => eda.pcb_PrimitiveComponent.getAll(1), summarizeArray);
await probe('components.bottom', () => eda.pcb_PrimitiveComponent.getAll(2), summarizeArray);
await probe('components.ids', () => eda.pcb_PrimitiveComponent.getAllPrimitiveId(), summarizeArray);
await probe('lines.noArgs', () => eda.pcb_PrimitiveLine.getAll(), summarizeArray);
await probe('lines.ids', () => eda.pcb_PrimitiveLine.getAllPrimitiveId(), summarizeArray);
await probe('components.noArgs.again', () => eda.pcb_PrimitiveComponent.getAll(), summarizeArray);
const after = await probe('document.after', () => eda.dmt_SelectControl.getCurrentDocumentInfo());
evidence.sameDocument = !!before?.uuid && !!before?.parentProjectUuid &&
  before.uuid === after?.uuid && before.parentProjectUuid === after?.parentProjectUuid &&
  before.tabId === after?.tabId && before.documentType === after?.documentType;
evidence.note = 'Empty arrays are observations, not proof of an empty or verified PCB. A timeout does not cancel the host promise.';
return evidence;

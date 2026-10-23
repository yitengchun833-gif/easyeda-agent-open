/// <reference types="@jlceda/pro-api-types" />

export interface NetlistProvenance {
  source: 'host-compiled-connectivity';
  manufacturingExportAvailable: false;
  purpose: 'analysis';
  scope: 'single-flat-page';
  warning: string;
}
export type NetlistFile = File & { netlistProvenance?: NetlistProvenance };

interface NativeSymbol {
  attrs: Record<string, string>;
  originSymbol: { cmdKey: string; symbolId: string; partCount: number };
  maskRegion: boolean;
  purepins: Array<{ num: string; attrs: Record<string, string>; nlNet: string[]; originPin: { wire?: unknown } }>;
}
interface NativeSchematic {
  uuid: string;
  sheets: Record<string, { symbols: Record<string, NativeSymbol> }>;
  nlNetWires: Map<string, { designatorPinName: string | null }>;
}
interface NativeProject {
  isBuild: boolean; lastModify: number; transId: number; defaultNetName: boolean;
  pureSchematics: Record<string, NativeSchematic>;
}

/** Read the compiler's data, never run the production export's annotation/save path. */
export async function readAnalysisNetlist(context: { uuid: string; tabId: string }, fileName = 'netlist'): Promise<NetlistFile | undefined> {
  const host = globalThis as unknown as { window?: { SCH?: { docMemoryManager?: { getActiveDoc(): {
    uuid: string; tabId: string; projectMgr?: { uuid: string; astProject?: NativeProject };
  } } } } };
  const doc = host.window?.SCH?.docMemoryManager?.getActiveDoc();
  const manager = doc?.projectMgr, project = manager?.astProject;
  if (!project) return undefined;
  const fail = (reason: string): never => { throw new Error(`Native export returned no file; analysis netlist unavailable: ${reason}`); };
  if (doc?.uuid !== context.uuid || doc.tabId !== context.tabId || context.tabId.split('@')[1] !== manager?.uuid) fail('project/page identity mismatch');
  if (project.isBuild !== true || !Number.isFinite(project.lastModify) || typeof project.defaultNetName !== 'boolean') fail('compiled state not ready');
  const stamp = [project.lastModify, project.transId];
  const matches = Object.values(project.pureSchematics ?? {}).filter(s => s.sheets?.[context.uuid]);
  if (matches.length !== 1) fail('schematic ownership is ambiguous');
  const schematic = matches[0];
  // shortcut: only flat single-page compiler data is verified; extend after native hierarchy fixtures exist.
  if (Object.keys(schematic.sheets).length !== 1 || !(schematic.nlNetWires instanceof Map)) fail('unsupported schematic scope or network index');
  const symbols = Object.values(schematic.sheets[context.uuid].symbols ?? {});
  if (symbols.some(s => s.originSymbol?.cmdKey === 'block_symbol')) fail('hierarchical symbols are unsupported');
  const parts = symbols.filter(s => s.originSymbol?.cmdKey === 'part');
  if (parts.some(s => s.maskRegion !== false || s.originSymbol.partCount !== 1 || !Array.isArray(s.purepins))) fail('masked/multipart components or missing pins');
  const inventory = await eda.sch_PrimitiveComponent.getAll(undefined, false);
  if (!Array.isArray(inventory)) fail('component inventory unavailable');
  const publicParts = inventory.filter(c => c.getState_ComponentType() === 'part');
  if (!parts.length || publicParts.length !== parts.length) fail('component inventory incomplete');
  const byId = new Map(publicParts.map(c => [c.getState_PrimitiveId(), c]));
  const designators = new Set<string>();
  const components: Record<string, { props: Record<string, string>; pinInfoMap: Record<string, { number: string; name: string; net: string }>; source: { pageId: string; componentId: string } }> = Object.create(null);
  for (const s of parts) {
    const id = s.originSymbol.symbolId, c = byId.get(id), uid = s.attrs?.['Unique ID'], designator = s.attrs?.Designator;
    if (!c || !uid || !designator || designators.has(designator) || components[uid]
      || c.getState_UniqueId() !== uid || c.getState_Designator() !== designator) fail('component identity is missing or duplicated');
    designators.add(designator);
    const pins: Record<string, { number: string; name: string; net: string }> = Object.create(null);
    for (const pin of s.purepins) {
      if (typeof pin.num !== 'string' || !pin.num || !Array.isArray(pin.nlNet) || pin.nlNet.length > 1
        || pin.nlNet.some(n => typeof n !== 'string' || !n) || !pin.originPin || !('wire' in pin.originPin)) fail(`invalid pin state on ${designator}`);
      const rawNet = pin.nlNet[0];
      let net = '';
      if (rawNet && pin.originPin.wire) {
        if (typeof rawNet !== 'string' || !schematic.nlNetWires.has(rawNet)) fail(`network absent from compiled index on ${designator}`);
        net = project.defaultNetName ? rawNet : schematic.nlNetWires.get(rawNet)?.designatorPinName || rawNet;
        if (typeof net !== 'string') fail(`invalid native network name on ${designator}`);
      }
      for (const number of pin.num.split(',')) {
        if (!number || pins[number]) fail(`duplicate/empty pin number on ${designator}`);
        pins[number] = { number, name: pin.attrs?.['Pin Name'] ?? '', net };
      }
    }
    components[uid] = { props: { ...s.attrs }, pinInfoMap: pins, source: { pageId: context.uuid, componentId: id } };
  }
  if (project.lastModify !== stamp[0] || project.transId !== stamp[1]
    || manager?.astProject !== project || host.window?.SCH?.docMemoryManager?.getActiveDoc() !== doc) fail('compiled state changed during read');
  const provenance: NetlistProvenance = { source: 'host-compiled-connectivity', manufacturingExportAvailable: false,
    purpose: 'analysis', scope: 'single-flat-page', warning: 'Native manufacturing export returned no file. This is electrical analysis data, not a manufacturing netlist or DRC approval.' };
  const data = { schema: 'easyeda-analysis-netlist/1', projectId: manager!.uuid, schematicUuid: schematic.uuid, components, provenance };
  const file = new File([JSON.stringify(data)], `${fileName.replace(/\.(enet|json|net)$/i, '')}.analysis-netlist.json`, { type: 'application/json' });
  return Object.assign(file, { netlistProvenance: provenance });
}

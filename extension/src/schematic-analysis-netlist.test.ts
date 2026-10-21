/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { getSchematicNetlistFile } from './schematic-netlist-file';

function fixture(t: { after(fn: () => void): void }) {
  const g = globalThis as any, old = { eda: g.eda, window: g.window };
  t.after(() => Object.assign(g, old));
  const pin = (num: string, net: string) => ({ num, attrs: { 'Pin Name': num }, nlNet: net ? [net] : [], originPin: { wire: !!net } });
  const symbol = { attrs: { Designator: 'TP1', 'Unique ID': 'uid1', Footprint: '' },
    originSymbol: { cmdKey: 'part', symbolId: 'part1', partCount: 1 }, maskRegion: false,
    purepins: [pin('1', 'POWER/A'), pin('2', '')] };
  const schematic = { uuid: 'schematic', sheets: { page: { symbols: { part1: symbol } } },
    nlNetWires: new Map([['POWER/A', { designatorPinName: null }]]) };
  const project = { isBuild: true, lastModify: 1, transId: 1, defaultNetName: true, pureSchematics: { schematic } };
  const doc = { uuid: 'page', tabId: 'page@project', profileSetting: { readonlyMode: false }, projectMgr: { uuid: 'project', astProject: project } };
  const part = { getState_ComponentType: () => 'part', getState_PrimitiveId: () => 'part1',
    getState_Designator: () => 'TP1', getState_UniqueId: () => 'uid1' };
  const calls: string[] = [];
  g.window = { SCH: { docMemoryManager: { getActiveDoc: () => doc }, app: { actionRunner: { running: false, currentAction: null } } } };
  g.eda = { dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: doc.uuid, tabId: doc.tabId, parentProjectUuid: 'project' }) },
    sch_ManufactureData: { getNetlistFile: async () => { calls.push('native-export'); return undefined; } },
    sch_PrimitiveComponent: { getAll: async () => [part] } };
  return { g, doc, project, schematic, symbol, part, calls };
}

test('JSON export recovers native connectivity despite missing footprints without modifying the design', async t => {
  const f = fixture(t), before = JSON.stringify(f.project);
  const file = await getSchematicNetlistFile('board');
  assert.ok(file, 'native production export returned no file but compiled connections exist');
  const json = JSON.parse(await file.text());
  assert.equal(json.schema, 'easyeda-analysis-netlist/1');
  assert.equal(json.components.uid1.props.Footprint, '');
  assert.equal(json.components.uid1.pinInfoMap['1'].net, 'POWER/A');
  assert.equal(json.components.uid1.pinInfoMap['2'].net, '');
  assert.equal(json.provenance.manufacturingExportAvailable, false);
  assert.equal(json.provenance.source, 'host-compiled-connectivity');
  assert.match(file.name, /analysis-netlist\.json$/);
  assert.equal(JSON.stringify(f.project), before);
  assert.deepEqual(f.calls, ['native-export']);
});

test('fallback preserves native automatic net names and combined pin numbers', async t => {
  const f = fixture(t);
  f.project.defaultNetName = false;
  f.schematic.nlNetWires.set('POWER/A', { designatorPinName: 'NETTP1_1' } as any);
  f.symbol.purepins[0].num = '1,3';
  const json = JSON.parse(await (await getSchematicNetlistFile())!.text());
  assert.equal(json.components.uid1.pinInfoMap['1'].net, 'NETTP1_1');
  assert.equal(json.components.uid1.pinInfoMap['3'].net, 'NETTP1_1');
});

for (const mode of ['identity', 'inventory', 'unbuilt', 'hierarchy', 'multipage', 'duplicate-pin', 'missing-net', 'missing-pin-state', 'changed', 'wrong-project']) {
  test(`fallback refuses incomplete or ambiguous native evidence: ${mode}`, async t => {
    const f = fixture(t);
    if (mode === 'identity') f.part.getState_UniqueId = () => 'other';
    if (mode === 'inventory') f.g.eda.sch_PrimitiveComponent.getAll = async () => [];
    if (mode === 'unbuilt') f.project.isBuild = false;
    if (mode === 'hierarchy') (f.schematic.sheets.page.symbols as any).block = { originSymbol: { cmdKey: 'block_symbol' } };
    if (mode === 'multipage') (f.schematic.sheets as any).page2 = { symbols: {} };
    if (mode === 'duplicate-pin') f.symbol.purepins.push({ ...f.symbol.purepins[1], num: '1' });
    if (mode === 'missing-net') f.schematic.nlNetWires.clear();
    if (mode === 'missing-pin-state') delete (f.symbol.purepins[0] as any).nlNet;
    if (mode === 'changed') f.g.eda.sch_PrimitiveComponent.getAll = async () => { f.project.lastModify++; return [f.part]; };
    if (mode === 'wrong-project') f.doc.projectMgr.uuid = 'other';
    await assert.rejects(getSchematicNetlistFile(), /analysis netlist/i);
  });
}

test('non-JSON formats do not silently return a different format', async t => {
  fixture(t);
  assert.equal(await getSchematicNetlistFile('board', 'Protel2' as ESYS_NetlistType), undefined);
});

test('successful native export remains the original file', async t => {
  const f = fixture(t), file = new File(['native'], 'board.enet');
  f.g.eda.sch_ManufactureData.getNetlistFile = async () => file;
  f.g.eda.sch_PrimitiveComponent.getAll = async () => { throw new Error('unnecessary fallback'); };
  assert.equal(await getSchematicNetlistFile(), file);
});

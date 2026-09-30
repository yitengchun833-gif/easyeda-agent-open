/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction, schematicAttributeModify, schematicComponentModify, schematicComponentPlace, schematicWireLabelsApply } from './actions';
import { runTargetedControl } from './control-target';

const primitive = (state: Record<string, any>): any => new Proxy({}, { get: (_t, name) => typeof name === 'string' && name.startsWith('getState_')
	? () => state[name[9].toLowerCase() + name.slice(10)] : undefined });
function host(t: TestContext, eda: any) {
	const globals = globalThis as any, old = globals.eda;
	globals.eda = eda; t.after(() => { globals.eda = old; });
}
function componentHost(t: TestContext, options: { wrongRotation?: boolean; loseProperty?: boolean } = {}) {
	const part: any = { primitiveId: 'part', componentType: 'part', designator: 'R1', name: '={Value}', uniqueId: 'uid',
		x: 10, y: 20, rotation: 0, mirror: false, net: '', subPartName: 'R.1', addIntoBom: true, addIntoPcb: true,
		manufacturer: 'Maker', manufacturerId: 'M1', supplier: 'LCSC', supplierId: 'C1',
		component: { uuid: 'dev', libraryUuid: 'lib' }, symbol: { uuid: 'sym' }, footprint: { uuid: 'fp' },
		otherProperty: { Value: '10k', Description: 'retain' } };
	const writes: any[] = [];
	host(t, { sch_PrimitiveComponent: {
		get: async () => primitive(part), getAll: async () => [primitive(part)],
		create: async (_d: unknown, x: number, y: number, _sub: unknown, rotation = 0) => {
			writes.push({ create: rotation }); Object.assign(part, { x, y, rotation: options.wrongRotation ? rotation : ((-rotation % 360) + 360) % 360 });
			return primitive({ ...part, rotation }); // returned handle echoes input, fresh read is authoritative
		},
		modify: async (_id: string, patch: any) => {
			writes.push(patch); part.otherProperty = patch.otherProperty ? { ...patch.otherProperty } : {};
			Object.assign(part, patch);
			if (options.loseProperty) part.otherProperty.Value = '';
			if (options.wrongRotation && patch.rotation !== undefined) part.rotation = -patch.rotation;
			return primitive({ ...part, rotation: patch.rotation ?? part.rotation });
		},
	}, sch_PrimitiveWire: { getAll: async () => [] } });
	return { part, writes };
}

test('group move shares component property preservation rather than raw geometry modify', async t => {
	const h = componentHost(t);
	const result: any = await runAction('schematic.group.move', { primitiveIds: ['part'], dx: 15, dy: -10 });
	assert.equal(result.result.count, 1);
	assert.deepEqual(h.part.otherProperty, { Value: '10k', Description: 'retain' });
	assert.deepEqual(h.writes[0].otherProperty, h.part.otherProperty);
	assert.equal(h.part.x, 25);
});
test('group move stops with recoverable readback if property preservation fails', async t => {
	componentHost(t, { loseProperty: true });
	const result: any = await runAction('schematic.group.move', { primitiveIds: ['part'], dx: 15, dy: 0 });
	assert.equal(result.result.partial, true);
	assert.equal(result.result.readback.propertiesBefore.Value, '10k');
	assert.deepEqual(result.result.readback.notApplied, ['Value']);
});
test('shared component modification retains active-page publication fallback after placement', async t => {
	const h = componentHost(t), g = globalThis as any;
	g.eda.sch_PrimitiveComponent.get = async () => undefined;
	const result: any = await runAction('schematic.group.move', { primitiveIds: ['part'], dx: 1, dy: 0 });
	assert.equal(result.result.count, 1); assert.equal(h.part.otherProperty.Value, '10k');
});
test('stored rotation compensates only component create and verifies fresh stored result', async t => {
	const h = componentHost(t);
	const placed: any = await schematicComponentPlace({ libraryUuid: 'lib', uuid: 'dev', x: 10, y: 20, rotation: 90, rotationMode: 'stored' });
	assert.equal(h.writes[0].create, -90); assert.equal(placed.result.component.rotation, 90); assert.equal(placed.result.rotationVerified, true);
	const modified: any = await schematicComponentModify({ primitiveId: 'part', patch: { rotation: 270 }, rotationMode: 'stored' });
	assert.equal(h.writes[1].rotation, 270); assert.equal(modified.result.rotationVerified, true);
	assert.equal(h.part.otherProperty.Value, '10k');
});
test('unexpected rotation convention returns partial without corrective replay', async t => {
	const h = componentHost(t, { wrongRotation: true });
	const result: any = await schematicComponentPlace({ libraryUuid: 'lib', uuid: 'dev', x: 10, y: 20, rotation: 90, rotationMode: 'stored' });
	assert.equal(result.result.rotationVerified, false); assert.equal(result.result.partial, true); assert.equal(h.writes.length, 1);
});
test('native rotation input stays unchanged and unknown mode rejects before writes', async t => {
	const h = componentHost(t);
	await schematicComponentPlace({ libraryUuid: 'lib', uuid: 'dev', x: 10, y: 20, rotation: 90 });
	assert.equal(h.writes[0].create, 90);
	await assert.rejects(schematicComponentModify({ primitiveId: 'part', patch: { rotation: 90 }, rotationMode: 'guess' }), /rotationMode/);
	assert.equal(h.writes.length, 1);
});

function attributeHost(t: TestContext, scale = 100) {
	const attr: any = { primitiveId: 'name', parentPrimitiveId: 'part', key: 'Name', value: 'R1', fontSize: 6.75, x: 5 };
	const writes: any[] = [];
	host(t, { dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'project' }) }, dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'page' }) }, sch_PrimitiveAttribute: {
		get: async () => primitive(attr),
		modify: async (_id: string, patch: any) => { writes.push(patch); Object.assign(attr, patch); if (typeof patch.fontSize === 'number') attr.fontSize = patch.fontSize * scale; return primitive(attr); },
	} });
	return { attr, writes };
}
test('display font size is converted only for attribute modify and checked in display units', async t => {
	const h = attributeHost(t);
	const result: any = await schematicAttributeModify({ primitiveId: 'name', props: { fontSize: 6.75 }, fontSizeUnit: 'display' });
	assert.equal(h.writes[0].fontSize, 0.0675); assert.equal(result.result.attribute.FontSize, 6.75); assert.equal(result.result.verified, true);
	await schematicAttributeModify({ primitiveId: 'name', props: { fontSize: 6.75 } });
	assert.equal(h.writes[1].fontSize, 6.75, 'native payload must remain untouched');
});
test('different font convention is partial and invalid font is refused before mutation', async t => {
	const h = attributeHost(t, 1);
	const result: any = await schematicAttributeModify({ primitiveId: 'name', props: { fontSize: 6.75 }, fontSizeUnit: 'display' });
	assert.equal(result.result.partial, true); assert.deepEqual(result.result.mismatch, ['fontSize']);
	await assert.rejects(schematicAttributeModify({ primitiveId: 'name', props: { fontSize: Number.NaN }, fontSizeUnit: 'display' }), /finite/);
	assert.equal(h.writes.length, 1);
});
test('open script can use shared helpers while its overall effects remain unknown', async t => {
	const h = attributeHost(t);
	const result: any = await runAction('debug.exec_js', { code: "return await helpers.call('schematic.attribute.modify',{primitiveId:'name',props:{fontSize:6.75},fontSizeUnit:'display'});", _mutation: true });
	assert.equal(result.result.value.result.verified, true); assert.equal(result.result.rules.status, 'unknown');
	assert.equal(h.writes[0].fontSize, 0.0675);
});
test('shared helpers refuse explicit target substitution and a raw page switch within the script', async t => {
	const h = attributeHost(t), g = globalThis as any;
	const target = { projectUuid: 'project', documentUuid: 'page' };
	await assert.rejects(runAction('debug.exec_js', { _target: target, code: "return helpers.call('schematic.attribute.modify',{primitiveId:'name',props:{x:20},_target:{documentUuid:'other'}});" }), /exec_js failed/);
	let page = 'page';
	g.eda.dmt_SelectControl.getCurrentDocumentInfo = async () => ({ uuid: page });
	g.eda.switchPage = async () => { page = 'other'; };
	const result: any = await runAction('debug.batch', { _target: target, steps: [{ action: 'debug.exec_js', payload: {
		code: "await eda.switchPage(); return helpers.call('schematic.attribute.modify',{primitiveId:'name',props:{x:20}});",
	} }] });
	assert.equal(result.result.ok, false); assert.equal(result.result.partial, true); assert.equal(h.writes.length, 0);
});

function labelsHost(t: TestContext, options: { missing?: boolean; late?: boolean; dropVisibility?: boolean; wrongParent?: boolean; mutateLine?: boolean } = {}) {
	const wires: any[] = ['a', 'b', 'outside'].map(id => ({ primitiveId: id, net: 'SIG', line: [0, 0, 10, 0] }));
	const attrs: any[] = wires.map(w => ({ primitiveId: `n-${w.primitiveId}`, parentPrimitiveId: w.primitiveId, key: 'Name', value: 'SIG', keyVisible: false, valueVisible: true, x: 0, y: 0, rotation: 0 }));
	const writes: string[] = [], reads: string[] = [];
	let aReads = 0;
	host(t, { sch_PrimitiveWire: { get: async (id: string) => wires.find(w => w.primitiveId === id) && primitive(wires.find(w => w.primitiveId === id)!), create: async () => { throw new Error('must not recreate wire'); } },
		sch_PrimitiveAttribute: {
			getAll: async (parent?: string) => {
				assert.ok(parent, 'must use parent-scoped attribute enumeration'); reads.push(parent);
				if (parent === 'a') { aReads++; if (options.missing) return []; if (options.late && aReads === 2) attrs.push({ ...attrs[0], primitiveId: 'late-name' }); }
				return attrs.filter(a => a.parentPrimitiveId === parent).map(primitive);
			},
			get: async (id: string) => { const a = attrs.find(a => a.primitiveId === id); return a && primitive(a); },
			modify: async (id: string, patch: any) => { const a = attrs.find(a => a.primitiveId === id); writes.push(id); if (!options.dropVisibility) Object.assign(a, patch); if (options.wrongParent) a.parentPrimitiveId = 'outside'; if (options.mutateLine) wires[0].line[0] = 50; return primitive(a); },
		} });
	return { attrs, writes, reads };
}
test('wire label display applies exact scoped selection and retains electrical Name', async t => {
	const h = labelsHost(t);
	const result: any = await schematicWireLabelsApply({ wireIds: ['a', 'b'], labels: [{ wireId: 'b', x: 20, y: 30 }] });
	assert.equal(result.result.verified, true); assert.equal(h.attrs[0].valueVisible, false); assert.equal(h.attrs[1].valueVisible, true);
	assert.equal(h.attrs[1].x, 20); assert.equal(h.attrs[2].valueVisible, true); assert.ok(h.attrs.every(a => a.value === 'SIG'));
	assert.deepEqual(h.writes, ['n-a', 'n-b']);
});
for (const [name, options] of Object.entries({ missing: { missing: true }, late: { late: true }, 'write ignored': { dropVisibility: true }, 'owner changed': { wrongParent: true }, 'in-place wire mutation': { mutateLine: true } })) {
	test(`wire labels report ${name} without replay or new wire creation`, async t => {
		const h = labelsHost(t, options);
		const result: any = await schematicWireLabelsApply({ wireIds: ['a'], labels: [] });
		assert.equal(result.result.verified, false); assert.equal(result.result.partial, true); assert.ok(result.result.remaining.length);
		assert.ok(h.writes.length <= 1, 'successful or ambiguous mutations must not replay');
	});
}
test('wire label cancellation after one native write stops all remaining writes', async t => {
	const h = labelsHost(t), g = globalThis as any, controller = new AbortController();
	const modify = g.eda.sch_PrimitiveAttribute.modify;
	g.eda.sch_PrimitiveAttribute.modify = async (...args: any[]) => { const r = await modify(...args); controller.abort(); return r; };
	const result: any = await schematicWireLabelsApply({ wireIds: ['a', 'b'], labels: [] }, { signal: controller.signal, progress: () => {} });
	assert.equal(result.result.partial, true); assert.equal(result.result.cancelled, true);
	assert.deepEqual(h.writes, ['n-a']); assert.ok(result.result.remaining.some((r: any) => r.attributeId === 'n-b'));
});
test('wire label scope and forbidden electrical fields reject before native calls', async t => {
	const h = labelsHost(t);
	await assert.rejects(schematicWireLabelsApply({ wireIds: ['a'], labels: [{ wireId: 'outside' }] }), /outside/);
	await assert.rejects(schematicWireLabelsApply({ wireIds: ['a'], labels: [{ wireId: 'a', value: 'GND' }] }), /electrical/);
	assert.deepEqual(h.reads, []); assert.deepEqual(h.writes, []);
});

test('targeted bypass control refuses changed or unreadable page before touching the queue', async t => {
	let count = 0;
	host(t, { dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'project' }) }, dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'page' }) } });
	const control = () => { count++; return { paused: true }; };
	await assert.rejects(runTargetedControl({ _target: { projectUuid: 'project', documentUuid: 'other' } }, control), /target/);
	assert.equal(count, 0);
	const result = await runTargetedControl({ _target: { projectUuid: 'project', documentUuid: 'page' } }, control);
	assert.equal(result.result?.targetChecked, true); assert.equal(result.context?.documentUuid, 'page');
	const legacy = await runTargetedControl({}, control); assert.equal(legacy.result?.targetChecked, undefined); assert.equal(count, 2);
});

test('target check reads complete fresh pin facts and batch propagates semantic failure', async t => {
	const h = componentHost(t), g = globalThis as any;
	const oldWindow = g.window;
	const document = { uuid: 'page', tabId: 'tab', profileSetting: { readonlyMode: false } };
	g.window = { SCH: { app: { actionRunner: { running: false, currentAction: null } }, docMemoryManager: { getActiveDoc: () => document } } };
	t.after(() => { g.window = oldWindow; });
	h.part.component = { uuid: 'd'.repeat(32), libraryUuid: 'lib' };
	let pinNet = 'SIG', pinReads = 0, netReads = 0, writes = 0;
	g.eda.dmt_Project = { getCurrentProjectInfo: async () => ({ uuid: 'project' }) };
	g.eda.dmt_SelectControl = { getCurrentDocumentInfo: async () => ({ uuid: 'page', tabId: 'tab' }) };
	g.eda.sch_PrimitiveComponent.getAllPinsByPrimitiveId = async () => { pinReads++; return [primitive({ primitiveId: 'pin', pinNumber: '1', pinName: 'A', noConnected: false, x: 0, y: 0, rotation: 0 })]; };
	g.eda.sch_ManufactureData = { getNetlistFile: async () => { netReads++; return { text: async () => JSON.stringify({ components: { part: { props: { Designator: 'R1' }, pinInfoMap: { pin: { number: '1', net: pinNet } } } } }) }; } };
	g.eda.write = async () => { writes++; };
	const target = { schemaVersion: 1, scope: { kind: 'page', complete: true, projectUuid: 'project', documentUuid: 'page' }, components: [{
		key: 'unique:uid', uniqueId: 'uid', ref: 'R1', sourcePrimitiveId: 'old', device: { ...h.part.component, name: h.part.name },
		symbol: h.part.symbol, footprint: h.part.footprint, properties: { otherProperty: { Value: '10k' } },
		pins: [{ number: '1', name: 'A', net: 'SIG', noConnected: false, state: 'connected' }],
	}], nets: [{ name: 'SIG', scope: 'unknown', members: [{ componentKey: 'unique:uid', pinNumber: '1' }] }] };
	const pass: any = await runAction('schematic.target.check', { semanticTarget: target }, false);
	assert.equal(pass.result.status, 'pass', JSON.stringify(pass.result));
	assert.equal(pass.result.idMap['unique:uid'].primitiveId, 'part');
	assert.equal(pinReads, 1); assert.equal(netReads, 1);
	pinNet = 'WRONG';
	const failed: any = await runAction('debug.batch', { steps: [{ action: 'schematic.target.check', payload: { semanticTarget: target } }, { action: 'debug.exec_js', payload: { code: 'await eda.write();' } }] }, false);
	assert.equal(failed.result.ok, false); assert.equal(failed.result.partial, true); assert.equal(failed.result.completed, 1);
	assert.equal(failed.result.results[0].result.status, 'fail'); assert.equal(writes, 0);
	assert.equal(pinReads, 2); assert.equal(netReads, 2, 'each target check must read current native facts');
	g.eda.sch_ManufactureData.getNetlistFile = async () => { throw new Error('unavailable'); };
	const unknown: any = await runAction('schematic.target.check', { semanticTarget: target }, false);
	assert.equal(unknown.result.verified, false); assert.equal(unknown.result.status, 'unknown');
});

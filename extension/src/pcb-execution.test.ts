/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';

function primitive(values: Record<string, unknown>): any {
	const snapshot = { ...values };
	return new Proxy({}, { get: (_target, key) => String(key).startsWith('getState_') ? () => snapshot[String(key).slice(9)] : undefined });
}

function install(t: TestContext, eda: Record<string, unknown>) {
	const globals = globalThis as any, previous = globals.eda;
	globals.eda = eda;
	t.after(() => { globals.eda = previous; });
}

function addStub(t: TestContext, options: { drop?: string; readback?: 'throws' | 'unavailable'; writeThrows?: string } = {}) {
	const component = { PrimitiveId: 'created', Designator: 'U?', UniqueId: 'auto' };
	const pads = [
		{ PrimitiveId: 'p1a', PadNumber: '1', Net: '' },
		{ PrimitiveId: 'p1b', PadNumber: '1', Net: '' },
		{ PrimitiveId: 'p2', PadNumber: '2', Net: 'OLD' },
	];
	let creates = 0, padReads = 0;
	const writes: string[] = [];
	install(t, {
		pcb_PrimitiveComponent: {
			create: async () => { creates++; return primitive(component); },
			modify: async (_id: string, patch: any) => {
				if (patch.designator) component.Designator = patch.designator;
				if (patch.uniqueId) component.UniqueId = patch.uniqueId;
				return primitive(component);
			},
			getAll: async () => [primitive(component)],
			getAllPinsByPrimitiveId: async () => {
				padReads++;
				if (padReads > 1 && options.readback === 'throws') throw new Error('pad read unavailable');
				if (padReads > 1 && options.readback === 'unavailable') return undefined;
				return pads.map(primitive);
			},
		},
		pcb_PrimitivePad: { modify: async (id: string, patch: { net: string }) => {
			writes.push(id);
			if (options.writeThrows === id) throw new Error('pad write failed');
			const pad = pads.find(p => p.PrimitiveId === id)!;
			if (options.drop !== id && options.drop !== 'all') pad.Net = patch.net;
			return primitive({ ...pad, Net: patch.net }); // Echoing the input is not evidence.
		} },
		pcb_Document: { startCalculatingRatline: async () => true },
	});
	return { pads, writes, creates: () => creates, reads: () => padReads };
}

const placement = { libraryUuid: 'library', uuid: 'device', x: 10, y: 20, designator: 'U1', uniqueId: 'schematic-u1' };

test('PCB add verifies every physical pad, including repeated logical numbers and explicit empty nets', async t => {
	const stub = addStub(t);
	const { result }: any = await runAction('pcb.add_component', { ...placement, nets: { 1: 'SIG', 2: '' } }, false);
	assert.equal(stub.creates(), 1);
	assert.equal(stub.reads(), 2);
	assert.equal(result.verified, true);
	assert.equal(result.assignedNets, 3);
	assert.equal(result.attemptedNetAssignments, 3);
	assert.equal(result.netMapping.verified, true);
	assert.deepEqual(stub.pads.map(p => p.Net), ['SIG', 'SIG', '']);
	assert.deepEqual(result.unmatchedPads, []);
});

test('PCB add reports missing logical pads and conflicting actual nets without repeating creation', async t => {
	const stub = addStub(t, { drop: 'p1b' });
	const { result }: any = await runAction('pcb.add_component', { ...placement, nets: { 1: 'SIG', 99: 'GND' } }, false);
	assert.equal(result.primitiveId, 'created');
	assert.equal(result.bindingVerified, true);
	assert.equal(result.verified, false);
	assert.equal(result.partial, true);
	assert.equal(result.assignedNets, 1);
	assert.deepEqual(result.netMapping.missingPadNumbers, ['99']);
	assert.deepEqual(result.netMapping.conflictingPadNumbers, ['1']);
	assert.deepEqual(result.netMapping.mismatched, [{ primitiveId: 'p1b', padNumber: '1', net: '', expectedNet: 'SIG' }]);
	assert.deepEqual(new Set(result.unmatchedPads), new Set(['1', '99']));
	assert.equal(stub.creates(), 1);
});

test('PCB add does not count modify echoes as verified net assignments', async t => {
	addStub(t, { drop: 'all' });
	const { result }: any = await runAction('pcb.add_component', { ...placement, nets: { 1: 'SIG' } }, false);
	assert.equal(result.assignedNets, 0);
	assert.equal(result.attemptedNetAssignments, 2);
	assert.equal(result.netMapping.mismatched.length, 2);
	assert.equal(result.partial, true);
});

for (const readback of ['throws', 'unavailable'] as const) {
	test(`PCB add preserves created ID and unknown pad mapping when readback ${readback}`, async t => {
		const stub = addStub(t, { readback });
		const { result }: any = await runAction('pcb.add_component', { ...placement, nets: { 1: 'SIG', 99: 'GND' } }, false);
		assert.equal(result.primitiveId, 'created');
		assert.equal(result.partial, true);
		assert.equal(result.netMapping.readbackAvailable, false);
		assert.deepEqual(result.netMapping.unverifiedPadNumbers, ['1', '99']);
		assert.deepEqual(result.netMapping.missingPadNumbers, [], 'unavailable is not proof of absence');
		assert.equal(stub.creates(), 1);
	});
}

test('PCB add validates pad maps before creation and retains pad write errors after creation', async t => {
	const stub = addStub(t, { writeThrows: 'p1b' });
	for (const nets of [[], null, 'SIG', { 1: 42 }, { ' 1': 'SIG' }, { 1: ' SIG' }]) {
		await assert.rejects(runAction('pcb.add_component', { ...placement, nets }, false), /nets/);
	}
	assert.equal(stub.creates(), 0);
	const { result }: any = await runAction('pcb.add_component', { ...placement, nets: { 1: 'SIG' } }, false);
	assert.equal(result.partial, true);
	assert.equal(result.assignedNets, 1);
	assert.deepEqual(result.padWriteFailures, [{ primitiveId: 'p1b', padNumber: '1', error: 'pad write failed' }]);
	assert.equal(stub.creates(), 1);
});

function moveStub(t: TestContext, options: { drop?: string; throws?: string; readbackFails?: string; lockBeforeWrite?: string; invalidBox?: string } = {}) {
	const store = new Map(['a', 'b', 'c', 'locked'].map((id, i) => [id, {
		PrimitiveId: id, UniqueId: `unique-${id}`, Designator: `U${i + 1}`, Name: id, Layer: 1,
		X: i * 100, Y: i * 20, Rotation: 90, PrimitiveLock: id === 'locked',
	}]));
	const writes: string[] = [];
	const reads = new Map<string, number>();
	install(t, {
		pcb_PrimitiveComponent: {
			get: async (id: string) => {
				const count = (reads.get(id) ?? 0) + 1;
				reads.set(id, count);
				if (options.readbackFails === id && writes.includes(id)) throw new Error('fresh read failed');
				const current = store.get(id);
				if (options.lockBeforeWrite === id && count >= 2 && current) current.PrimitiveLock = true;
				return current ? primitive(current) : undefined;
			},
			modify: async (id: string, patch: { x: number; y: number }) => {
				writes.push(id);
				if (options.throws === id) throw new Error('write failed');
				const current = store.get(id)!;
				if (options.drop !== id) { current.X = patch.x; current.Y = patch.y; }
				return primitive({ ...current, X: patch.x, Y: patch.y });
			},
			getAllPinsByPrimitiveId: async () => [],
		},
		pcb_Primitive: { getPrimitivesBBox: async ([id]: string[]) => {
			if (options.invalidBox === id) return { minX: NaN, minY: 0, maxX: 10, maxY: 10 };
			const current = store.get(id)!;
			return { minX: current.X - 12, minY: current.Y - 5, maxX: current.X + 8, maxY: current.Y + 5 };
		} },
	});
	return { store, writes };
}

test('PCB batch move protects locked objects, deduplicates IDs and reports missing targets', async t => {
	const stub = moveStub(t);
	const { result }: any = await runAction('pcb.components.move', { primitiveIds: ['a', 'a', 'locked', 'missing'], dx: 5, dy: -3 }, false);
	assert.deepEqual(stub.writes, ['a']);
	assert.equal(stub.store.get('a')!.X, 5);
	assert.equal(stub.store.get('a')!.Y, -3);
	assert.equal(stub.store.get('locked')!.X, 300);
	assert.equal(result.count, 1);
	assert.equal(result.requestedCount, 3);
	assert.deepEqual(result.skipped.map((item: any) => item.primitiveId), ['locked', 'missing']);
	assert.equal(result.partial, true);
	assert.equal(result.moved[0].verified, true);
});

test('PCB batch move reports complete only after every fresh pose matches', async t => {
	const stub = moveStub(t);
	const { result }: any = await runAction('pcb.components.move', { primitiveIds: ['a', 'b'], dx: 5, dy: 2 }, false);
	assert.deepEqual(stub.writes, ['a', 'b']);
	assert.equal(result.verified, true);
	assert.equal(result.partial, false);
	assert.equal(result.count, 2);
	assert.deepEqual(result.remaining, []);
	assert.equal(stub.store.get('b')!.Rotation, 90);
	assert.equal(stub.store.get('b')!.Layer, 1);
});

for (const mode of ['drop', 'throws', 'readbackFails'] as const) {
	test(`PCB batch move preserves completed IDs and stops after ${mode}`, async t => {
		const stub = moveStub(t, { [mode]: 'b' });
		const { result }: any = await runAction('pcb.components.move', { primitiveIds: ['a', 'b', 'c'], dx: 5, dy: 2 }, false);
		assert.deepEqual(stub.writes, ['a', 'b']);
		assert.deepEqual(result.moved.map((item: any) => item.primitiveId), ['a']);
		assert.deepEqual(result.remaining, ['c']);
		assert.equal(result.partial, true);
		assert.equal(result.verified, false);
		assert.equal(stub.store.get('c')!.X, 200);
		if (mode === 'readbackFails') {
			assert.equal(result.executionState, 'unknown');
			assert.equal(result.unverified[0].primitiveId, 'b');
		} else assert.equal(result.notApplied[0].primitiveId, 'b');
	});
}

test('PCB batch move rechecks a lock acquired after planning before dispatch', async t => {
	const stub = moveStub(t, { lockBeforeWrite: 'b' });
	const { result }: any = await runAction('pcb.components.move', { primitiveIds: ['a', 'b'], dx: 5, dy: 2 }, false);
	assert.deepEqual(stub.writes, ['a']);
	assert.deepEqual(result.skipped, [{ primitiveId: 'b', reason: 'locked' }]);
	assert.equal(stub.store.get('b')!.X, 100);
});

test('PCB arrange preserves anchor offsets and verifies fresh coordinates while skipping locked targets', async t => {
	const stub = moveStub(t);
	const { result }: any = await runAction('pcb.components.arrange', { primitiveIds: ['a', 'b', 'locked'], mode: 'grid', cols: 2, pitch: 10 }, false);
	assert.deepEqual(stub.writes, ['a', 'b']);
	assert.deepEqual(result.moved.map((item: any) => item.to), [{ x: 5, y: 15 }, { x: 35, y: 15 }]);
	assert.equal(result.count, 2);
	assert.equal(result.skipped[0].reason, 'locked');
	assert.equal(stub.store.get('locked')!.X, 300);
});

test('PCB arrange reports a silent no-op and does not dispatch later placements', async t => {
	const stub = moveStub(t, { drop: 'b' });
	const { result }: any = await runAction('pcb.components.arrange', { primitiveIds: ['a', 'b', 'c'], mode: 'cluster' }, false);
	assert.deepEqual(stub.writes, ['a', 'b']);
	assert.equal(result.count, 1);
	assert.equal(result.notApplied[0].primitiveId, 'b');
	assert.deepEqual(result.remaining, ['c']);
});

test('PCB arrange reports unavailable geometry and rejects invalid dimensions before writes', async t => {
	const stub = moveStub(t, { invalidBox: 'b' });
	for (const patch of [{ cols: 0 }, { cols: 1.5 }, { pitch: Infinity }, { gutter: -1 }, { mode: 'unknown' }]) {
		await assert.rejects(runAction('pcb.components.arrange', { primitiveIds: ['a', 'b'], ...patch }, false), /arrange requires/);
	}
	assert.deepEqual(stub.writes, []);
	const { result }: any = await runAction('pcb.components.arrange', { primitiveIds: ['a', 'b'], mode: 'grid' }, false);
	assert.deepEqual(stub.writes, ['a']);
	assert.match(result.skipped[0].reason, /bounding box/);
	assert.equal(result.partial, true);
});

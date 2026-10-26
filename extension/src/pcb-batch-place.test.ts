/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';

function fixture(t: TestContext, options: { dropRotation?: string; failRead?: string; race?: string; throwsAfterWrite?: string; wrongIdentity?: string;
	onWrite?: (id: string) => void; onRead?: (id: string, count: number) => void } = {}) {
	const g = globalThis as any, previous = g.eda;
	const state = new Map<string, any>(['a', 'b', 'c', 'locked', 'outside'].map((id, i) => [id, {
		PrimitiveId: id, Designator: `U${i}`, UniqueId: `source-${id}`, X: i * 100, Y: 50,
		Rotation: 90, Layer: 1, PrimitiveLock: id === 'locked',
	}]));
	const calls: Array<{ id: string; patch: Record<string, number> }> = [], reads = new Map<string, number>();
	g.eda = { pcb_PrimitiveComponent: {
		get: async (id: string) => {
			reads.set(id, (reads.get(id) ?? 0) + 1);
			options.onRead?.(id, reads.get(id)!);
			if (id === options.failRead && calls.some(call => call.id === id)) throw new Error('readback unavailable');
			const s = state.get(id);
			if (s && id === options.race && reads.get(id) === 2) s.Designator = 'CHANGED';
			const snapshot = s && { ...s };
			if (snapshot && options.wrongIdentity === id) snapshot.PrimitiveId = 'outside';
			return snapshot && new Proxy({}, { get: (_t, key) => String(key).startsWith('getState_') ? () => snapshot[String(key).slice(9)] : undefined });
		},
		modify: async (id: string, patch: Record<string, number>) => {
			calls.push({ id, patch });
			for (const [key, value] of Object.entries(patch)) {
				if (key === 'rotation' && id === options.dropRotation) continue;
				state.get(id)[key === 'rotation' ? 'Rotation' : key.toUpperCase()] = value;
			}
			if (id === options.throwsAfterWrite) throw new Error('response failed after mutation');
			options.onWrite?.(id);
			return {}; // Deliberately unusable: the executor must read fresh state.
		},
		getAllPinsByPrimitiveId: async () => [],
	}, pcb_Primitive: { getPrimitivesBBox: async ([id]: string[]) => {
		const part = state.get(id);
		return { minX: part.X - 10, minY: part.Y - 5, maxX: part.X + 10, maxY: part.Y + 5 };
	} } };
	t.after(() => { g.eda = previous; });
	return { state, calls };
}

test('absolute PCB batch uses one action with distinct poses, optional rotation and no unrelated writes', async t => {
	const { state, calls } = fixture(t), outside = { ...state.get('outside') };
	const { result }: any = await runAction('pcb.components.place', { placements: [
		{ primitiveId: 'a', x: -20, y: 10, rotation: 450, expected: { uniqueId: 'source-a', x: 0, layer: 1 } },
		{ primitiveId: 'b', x: 200, y: 400 },
	] }, false);
	assert.deepEqual(calls, [{ id: 'a', patch: { x: -20, y: 10, rotation: 90 } }, { id: 'b', patch: { x: 200, y: 400 } }]);
	assert.equal(result.verified, true);
	assert.equal(result.partial, false);
	assert.equal(result.count, 2);
	assert.equal(result.units, 'mil');
	assert.deepEqual(result.moved[0].from, { x: 0, y: 50, rotation: 90 });
	assert.deepEqual(state.get('outside'), outside);
	assert.equal(state.get('b').Rotation, 90);
	assert.equal(state.get('a').UniqueId, 'source-a');
});

test('absolute PCB batch validates the whole list before any write', async t => {
	const { calls } = fixture(t), first = { primitiveId: 'a', x: 1, y: 2 };
	for (const placements of [[], [first, first], [first, { primitiveId: 'b', x: Infinity, y: 0 }],
		[first, { primitiveId: 'b', x: 0, y: 0, net: 'GND' }], [first, { primitiveId: 'b', x: 0, y: 0, expected: { layer: 3 } }]]) {
		await assert.rejects(runAction('pcb.components.place', { placements }, false));
	}
	await assert.rejects(runAction('pcb.components.place', { placements: [first], dryRun: true }, false), /does not accept dryRun/);
	assert.deepEqual(calls, []);
});

test('absolute PCB batch skips locked, missing, stale and unreadable source targets and reports partial', async t => {
	const { state, calls } = fixture(t);
	state.get('c').PrimitiveLock = undefined;
	const { result }: any = await runAction('pcb.components.place', { placements: [
		{ primitiveId: 'a', x: 10, y: 20 }, { primitiveId: 'locked', x: 10, y: 20 },
		{ primitiveId: 'missing', x: 10, y: 20 }, { primitiveId: 'b', x: 10, y: 20, expected: { x: 99 } },
		{ primitiveId: 'c', x: 10, y: 20 },
	] }, false);
	assert.deepEqual(calls.map(c => c.id), ['a']);
	assert.equal(result.partial, true);
	assert.equal(result.verified, false);
	assert.equal(result.requestedCount, 5);
	assert.equal(result.skipped.length, 4);
	assert.equal(state.get('b').X, 100);
	assert.equal(state.get('locked').X, 300);
});

test('absolute PCB batch refuses a lookup that returns an object outside the explicit scope', async t => {
	const { state, calls } = fixture(t, { wrongIdentity: 'a' }), outside = { ...state.get('outside') };
	const { result }: any = await runAction('pcb.components.place', { placements: [{ primitiveId: 'a', x: 10, y: 20 }] }, false);
	assert.deepEqual(calls, []);
	assert.equal(result.partial, true);
	assert.match(result.skipped[0].reason, /different identity/);
	assert.deepEqual(state.get('outside'), outside);
});

for (const failure of ['dropRotation', 'failRead', 'throwsAfterWrite', 'race'] as const) {
	test(`absolute PCB batch stops without replay after ${failure}, preserving completed and remaining targets`, async t => {
		const { state, calls } = fixture(t, { [failure]: 'b' });
		const { result }: any = await runAction('pcb.components.place', { placements: ['a', 'b', 'c'].map(id => ({ primitiveId: id, x: 20, y: 30, rotation: 180 })) }, false);
		assert.equal(result.partial, true);
		assert.equal(result.verified, false);
		assert.deepEqual(calls.map(c => c.id), failure === 'race' ? ['a'] : ['a', 'b']);
		assert.ok(result.moved.some((m: any) => m.primitiveId === 'a'));
		assert.ok(result.remaining.includes('c'));
		assert.equal(state.get('c').X, 200);
		if (failure === 'failRead' || failure === 'throwsAfterWrite') assert.equal(result.executionState, 'unknown');
		if (failure === 'dropRotation') assert.ok(result.notApplied[0].notApplied.some((p: any) => p.field === 'rotation'));
	});
}

for (const action of ['pcb.components.place', 'pcb.components.move', 'pcb.components.arrange']) {
	test(`${action} retains the finished pose and stops following writes after cancellation`, async t => {
		const controller = new AbortController();
		const { calls } = fixture(t, { onWrite: id => { if (id === 'a') controller.abort(); } });
		const payload = action === 'pcb.components.place'
			? { placements: ['a', 'b', 'c'].map(primitiveId => ({ primitiveId, x: 25, y: 30 })) }
			: { primitiveIds: ['a', 'b', 'c'], dx: 25, dy: 30, mode: 'grid' };
		const { result }: any = await runAction(action, payload, false, { signal: controller.signal, progress: () => {} });
		assert.deepEqual(calls.map(c => c.id), ['a']);
		assert.deepEqual(result.moved.map((c: any) => c.primitiveId), ['a']);
		assert.deepEqual(result.attempted, ['a']);
		assert.deepEqual(result.remaining, ['b', 'c']);
		assert.equal(result.cancelled, true);
		assert.equal(result.partial, true);
		assert.equal(result.verified, false);
	});
}

for (const interruption of ['cancel', 'pause']) {
	test(`absolute PCB batch rechecks ${interruption} after the awaited pre-write read`, async t => {
		const controller = new AbortController(); let paused = false;
		const { calls } = fixture(t, { onRead: (id, count) => {
			if (id === 'b' && count === 2) { if (interruption === 'cancel') controller.abort(); else paused = true; }
		} });
		const { result }: any = await runAction('pcb.components.place', { placements: ['a', 'b', 'c'].map(primitiveId => ({ primitiveId, x: 25, y: 30 })) }, false,
			{ signal: controller.signal, isPaused: () => paused, progress: () => {} });
		assert.deepEqual(calls.map(c => c.id), ['a']);
		assert.deepEqual(result.moved.map((c: any) => c.primitiveId), ['a']);
		assert.deepEqual(result.remaining, ['b', 'c']);
		assert.equal(result[interruption === 'cancel' ? 'cancelled' : 'paused'], true);
		assert.equal(result.partial, true);
		assert.equal(result.verified, false);
	});
}

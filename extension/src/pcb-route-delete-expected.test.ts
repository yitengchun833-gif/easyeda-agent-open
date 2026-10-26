/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';

type Track = { PrimitiveId: string; PrimitiveLock: boolean; Net: string; Layer: number; StartX: number; StartY: number; EndX: number; EndY: number; LineWidth: number };

function primitive(values: Record<string, unknown>): any {
	return new Proxy({}, { get: (_target, key) => String(key).startsWith('getState_') ? () => values[String(key).slice(9)] : undefined });
}

function routeDeleteStub(t: TestContext) {
	const tracks: Track[] = [
		{ PrimitiveId: 'old-a', PrimitiveLock: false, Net: 'SIG', Layer: 1, StartX: 10, StartY: 20, EndX: 30, EndY: 20, LineWidth: 4 },
		{ PrimitiveId: 'old-b', PrimitiveLock: false, Net: 'SIG', Layer: 1, StartX: 30, StartY: 20, EndX: 50, EndY: 20, LineWidth: 4 },
	];
	const expectedTracks = tracks.map(track => ({ primitiveId: track.PrimitiveId, locked: false, net: track.Net, layer: track.Layer,
		startX: track.StartX, startY: track.StartY, endX: track.EndX, endY: track.EndY, lineWidth: track.LineWidth }));
	const writes: string[][] = [];
	const globals = globalThis as any, previous = globals.eda;
	globals.eda = {
		pcb_PrimitiveLine: {
			getAll: async () => tracks.map(track => primitive(track)),
			delete: async (ids: string[]) => { writes.push([...ids]); for (const id of ids) tracks.splice(tracks.findIndex(track => track.PrimitiveId === id), 1); return true; },
		},
		pcb_PrimitiveArc: { getAll: async () => [] },
		pcb_PrimitiveVia: { getAll: async () => [] },
		pcb_PrimitiveComponent: { getAll: async () => [] },
	};
	t.after(() => { globals.eda = previous; });
	return { tracks, expectedTracks, writes };
}

for (const change of ['locked', 'net', 'layer', 'geometry'] as const) {
	test(`strict PCB route delete refuses an entire changed chain (${change})`, async t => {
		const { tracks, expectedTracks, writes } = routeDeleteStub(t);
		if (change === 'locked') tracks[1].PrimitiveLock = true;
		if (change === 'net') tracks[1].Net = 'OTHER';
		if (change === 'layer') tracks[1].Layer = 2;
		if (change === 'geometry') tracks[1].EndX = 51;
		await assert.rejects(runAction('pcb.route.delete', { kind: 'track', primitiveIds: ['old-a', 'old-b'], expectedTracks }, false), /No tracks deleted/);
		assert.deepEqual(writes, []);
		assert.deepEqual(tracks.map(track => track.PrimitiveId), ['old-a', 'old-b']);
	});
}

test('strict PCB route delete rejects incomplete expectation before any native delete', async t => {
	const { tracks, expectedTracks, writes } = routeDeleteStub(t);
	await assert.rejects(runAction('pcb.route.delete', { kind: 'track', primitiveIds: ['old-a', 'old-b'], expectedTracks: expectedTracks.slice(0, 1) }, false), /No tracks deleted/);
	assert.deepEqual(writes, []);
	assert.equal(tracks.length, 2);
});

test('strict PCB route delete accepts the exact chain', async t => {
	const { tracks, expectedTracks, writes } = routeDeleteStub(t);
	const strict = await runAction('pcb.route.delete', { kind: 'track', primitiveIds: ['old-a', 'old-b'], expectedTracks }, false);
	assert.deepEqual(writes, [['old-a', 'old-b']]);
	assert.equal((strict.result as any).count, 2);
	assert.equal(tracks.length, 0);
});

test('ordinary PCB route delete retains partial compatibility', async t => {
	const legacy = routeDeleteStub(t);
	legacy.tracks[1].PrimitiveLock = true;
	const ordinary = await runAction('pcb.route.delete', { kind: 'track', primitiveIds: ['old-a', 'old-b'] }, false);
	assert.deepEqual(legacy.writes, [['old-a']]);
	assert.deepEqual(legacy.tracks.map(track => track.PrimitiveId), ['old-b']);
	assert.deepEqual((ordinary.result as any).skippedLocked, ['old-b']);
});

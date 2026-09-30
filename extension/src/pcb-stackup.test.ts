/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pcbStackupSet } from './actions';

const layers = (type15 = 'SIGNAL') => [
	{ id: 1, name: 'Top Layer', type: 'SIGNAL' },
	{ id: 2, name: 'Bottom Layer', type: 'SIGNAL' },
	{ id: 15, name: 'Inner1', type: type15 },
	{ id: 16, name: 'Inner2', type: 'SIGNAL' },
];

async function withLayerHost(options: { count?: number; modify?: boolean; apply?: boolean; initialType?: string }, run: (host: any) => Promise<void>) {
	let count = options.count ?? 2;
	let current = layers(options.initialType);
	const host = { countWrites: 0, layerWrites: 0 };
	(globalThis as any).eda = { pcb_Layer: {
		getAllLayers: async () => structuredClone(current),
		getTheNumberOfCopperLayers: async () => count,
		setTheNumberOfCopperLayers: async (next: number) => { host.countWrites++; count = next; return true; },
		modifyLayer: async (id: number, props: any) => {
			host.layerWrites++;
			if (options.apply !== false) current = current.map(v => v.id === id ? { ...v, ...props } : v);
			return options.modify !== false;
		},
	} };
	try { await run(host); } finally { delete (globalThis as any).eda; }
}

test('stackup confirms count and layer readback, and repeated request is a no-op', async () => withLayerHost({}, async host => {
	const result = (await pcbStackupSet({ count: 4, layers: [{ id: 15, type: 'plane' }] })).result as any;
	assert.equal(result.verified, true); assert.equal(result.partial, false); assert.equal(result.changed, true);
	assert.equal(result.modified[0].actual.type, 'PLANE');
	const repeated = (await pcbStackupSet({ count: 4, layers: [{ id: 15, type: 'plane' }] })).result as any;
	assert.equal(repeated.verified, true); assert.equal(repeated.changed, false);
	assert.equal(host.countWrites, 1); assert.equal(host.layerWrites, 1);
}));

test('count success plus rejected layer is an explicit partial failure', async () => withLayerHost({ modify: false, apply: false }, async () => {
	const result = (await pcbStackupSet({ count: 4, layers: [{ id: 15, type: 'plane' }] })).result as any;
	assert.equal(result.countVerified, true); assert.equal(result.layersVerified, false);
	assert.equal(result.verified, false); assert.equal(result.partial, true);
	assert.equal(result.modified[0].written, false); assert.equal(result.modified[0].actual.type, 'SIGNAL');
}));

test('rejected layer-only write is unverified without claiming partial mutation', async () => withLayerHost({ count: 4, modify: false, apply: false }, async () => {
	const result = (await pcbStackupSet({ layers: [{ id: 15, type: 'plane' }] })).result as any;
	assert.equal(result.changed, false); assert.equal(result.verified, false); assert.equal(result.partial, false);
}));

test('a false/no-op host result is accepted only when fresh state already matches', async () => withLayerHost({ count: 4, initialType: 'PLANE', modify: false, apply: false }, async host => {
	const result = (await pcbStackupSet({ layers: [{ id: 15, type: 'plane' }] })).result as any;
	assert.equal(result.verified, true); assert.equal(result.changed, false); assert.equal(host.layerWrites, 0);
}));

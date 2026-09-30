import assert from 'node:assert/strict';
import { test } from 'node:test';
import { pcbNetColorSet } from './pcb-net-color';

test('color preview is read-only; writes preserve alpha and verify normalized channels', async () => {
	let color = { r: 0, g: 0, b: 0, alpha: 0.5 }, writes = 0;
	(globalThis as any).eda = { pcb_Net: {
		getNetColor: async () => ({ ...color }),
		setNetColor: async (_: string, next: typeof color) => { writes++; color = next; return true; },
	} };
	try {
		const payload = { net: '+5V', color: '#FF8000' };
		assert.equal((await pcbNetColorSet({ ...payload, dryRun: true })).result?.dryRun, true);
		assert.equal(writes, 0);
		assert.equal((await pcbNetColorSet(payload)).result?.verified, true);
		assert.deepEqual(color, { r: 1, g: 128 / 255, b: 0, alpha: 0.5 });
		assert.equal((await pcbNetColorSet(payload)).result?.changed, false);
		assert.equal(writes, 1);
	} finally { delete (globalThis as any).eda; }
});

test('invalid input or unavailable network never writes', async () => {
	let writes = 0;
	(globalThis as any).eda = { pcb_Net: { getNetColor: async () => undefined, setNetColor: async () => { writes++; return true; } } };
	try {
		for (const payload of [{ net:'x', color:'#xyz' }, { net:'x', color:'#FF8000' }, { net:'x',color:'#FF8000',typo:true }]) {
			await assert.rejects(pcbNetColorSet(payload));
		}
		assert.equal(writes, 0);
	} finally { delete (globalThis as any).eda; }
});

test('silently dropped write and unavailable final readback are partial failures', async () => {
	for (const drop of [true, false]) {
		let wrote = false;
		(globalThis as any).eda = { pcb_Net: {
			getNetColor: async () => { if (wrote && !drop) throw Error('readback failed'); return {r:0,g:0,b:0,alpha:1}; },
			setNetColor: async () => { wrote = true; return true; },
		} };
		try {
			const result = (await pcbNetColorSet({ net:'+5V',color:'#FF8000' })).result;
			assert.equal(result?.verified,false); assert.equal(result?.partial,true);
		} finally { delete (globalThis as any).eda; }
	}
});

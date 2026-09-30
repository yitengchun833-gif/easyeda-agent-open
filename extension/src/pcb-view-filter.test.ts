/// <reference types="@jlceda/pro-api-types" />

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

function withEda(mock: Record<string, unknown>, run: () => Promise<void>): Promise<void> {
	(globalThis as any).eda = mock;
	return run().finally(() => { delete (globalThis as any).eda; });
}

test('pcb.view.filter.get returns the raw view state and never invents a writable path', async () => {
	const raw = { all: true, component: true, componentProperty: false, guide: true };
	await withEda({
		pcb_Document: { getCurrentFilterConfiguration: async () => raw },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ documentType: 2, uuid: 'pcb-1' }) },
	}, async () => {
		const response: any = await runAction('pcb.view.filter.get', undefined);
		assert.deepEqual(response.result.configuration, raw);
		assert.equal(response.result.readable, true);
		assert.equal(response.result.writable, false);
		assert.equal(response.result.componentAttributesVisible, null);
		assert.equal(response.result.componentAttributesPath, null);
		assert.equal(response.result.api.setter, null);
	});
});

test('pcb.view.filter.get fails closed when the official getter is unavailable', async () => {
	await withEda({
		pcb_Document: {},
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ documentType: 2, uuid: 'pcb-1' }) },
	}, async () => {
		await assert.rejects(
			() => runAction('pcb.view.filter.get', undefined),
			(err: any) => err.code === 'EDA_API_UNAVAILABLE' && /No view state was changed/.test(err.message),
		);
	});
});

test('pcb.view.filter.get rejects an empty readback without mutating attributes', async () => {
	let attributeWrites = 0;
	await withEda({
		pcb_Document: { getCurrentFilterConfiguration: async () => undefined },
		pcb_PrimitiveAttribute: { modify: async () => { attributeWrites++; } },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ documentType: 2, uuid: 'pcb-1' }) },
	}, async () => {
		await assert.rejects(
			() => runAction('pcb.view.filter.get', undefined),
			(err: any) => err.code === 'EDA_CALL_FAILED' && /No view state was changed/.test(err.message),
		);
		assert.equal(attributeWrites, 0);
	});
});

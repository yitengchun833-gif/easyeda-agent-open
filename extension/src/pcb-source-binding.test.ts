/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

function primitive(values: Record<string, unknown>): any {
	return new Proxy({}, { get: (_target, key) => String(key).startsWith('getState_') ? () => values[String(key).slice(9)] : undefined });
}

test('PCB placement preserves and independently verifies source Channel ID without breaking legacy callers', async t => {
	for (const mode of ['match', 'drop', 'wrong-board', 'legacy', 'missing-source'] as const) {
		await t.test(mode, async sub => {
			const globals = globalThis as any, previous = globals.eda;
			sub.after(() => { globals.eda = previous; });
			let creates = 0;
			const component = { PrimitiveId: 'placed', Designator: 'R?', UniqueId: 'auto', OtherProperty: { Value: '470R', '3D Model': 'keep' } as Record<string, string> };
			const pads = [{ PrimitiveId: 'pad1', PadNumber: '1', Net: '' }, { PrimitiveId: 'pad2', PadNumber: '2', Net: '' }];
			globals.eda = {
				dmt_Board: { getCurrentBoardInfo: async () => ({ schematic: { uuid: mode === 'wrong-board' ? 'other-source' : 'source' } }) },
				pcb_PrimitiveComponent: {
					create: async () => { creates++; return primitive({ ...component }); },
					modify: async (_id: string, patch: any) => {
						component.Designator = patch.designator;
						component.UniqueId = patch.uniqueId;
						if (patch.otherProperty && mode !== 'drop') component.OtherProperty = { ...patch.otherProperty };
						return primitive({ ...component, OtherProperty: patch.otherProperty }); // An echo is not readback.
					},
					getAll: async () => [primitive(component)],
					getAllPinsByPrimitiveId: async () => pads.map(primitive),
				},
				pcb_PrimitivePad: { modify: async (id: string, patch: { net: string }) => { pads.find(p => p.PrimitiveId === id)!.Net = patch.net; } },
				pcb_Document: { startCalculatingRatline: async () => true },
			};
			const payload = {
				libraryUuid: 'library', uuid: 'device', x: 10, y: 20,
				designator: 'R1', uniqueId: 'gge1', nets: { 1: 'GND', 2: '' },
				...(mode === 'legacy' ? {} : { channelId: '$1I2', ...(mode === 'missing-source' ? {} : { schematicUuid: 'source' }) }),
			};
			if (mode === 'wrong-board' || mode === 'missing-source') {
				await assert.rejects(runAction('pcb.add_component', payload, false), /source|schematic/i);
				assert.equal(creates, 0);
				return;
			}
			const { result, warnings }: any = await runAction('pcb.add_component', payload, false);
			assert.equal(creates, 1);
			assert.equal(result.primitiveId, 'placed');
			assert.equal(component.OtherProperty.Value, '470R');
			assert.equal(component.OtherProperty['3D Model'], 'keep');
			if (mode === 'legacy') {
				assert.equal(result.verified, true, 'legacy provided-field verification is retained');
				assert.equal(result.sourceBinding.requested, false);
				assert.equal(result.sourceBinding.verified, false);
				assert.match(warnings.join(' '), /Channel ID/);
			} else {
				assert.equal(result.sourceBinding.requested, true);
				assert.equal(result.sourceBinding.expectedChannelId, '$1I2');
				assert.equal(result.sourceBinding.verified, mode === 'match');
				assert.equal(result.verified, mode === 'match');
				if (mode === 'drop') assert.equal(result.partial, true);
				else assert.equal(result.sourceBinding.actualChannelId, '$1I2');
			}
		});
	}
});

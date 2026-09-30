/// <reference types="@jlceda/pro-api-types" />

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { runAction } from './actions';

const DEVICE_UUID = '11111111111111111111111111111111';
const DEVICE_LIB = 'device-library';
const OLD_FOOTPRINT = { uuid: 'old-footprint', libraryUuid: 'old-footprint-library' };
const NEW_FOOTPRINT = { uuid: 'new-footprint', libraryUuid: 'new-footprint-library' };

type ComponentState = Record<string, unknown>;

function component(state: ComponentState): any {
	const getter = (key: string) => () => state[key];
	return {
		getState_PrimitiveId: getter('primitiveId'),
		getState_ComponentType: getter('componentType'),
		getState_Designator: getter('designator'),
		getState_Name: getter('name'),
		getState_X: getter('x'), getState_Y: getter('y'),
		getState_Rotation: getter('rotation'), getState_Mirror: getter('mirror'),
		getState_Net: getter('net'), getState_SubPartName: getter('subPartName'),
		getState_AddIntoBom: getter('addIntoBom'), getState_AddIntoPcb: getter('addIntoPcb'),
		getState_UniqueId: getter('uniqueId'),
		getState_Manufacturer: getter('manufacturer'), getState_ManufacturerId: getter('manufacturerId'),
		getState_Supplier: getter('supplier'), getState_SupplierId: getter('supplierId'),
		getState_Component: getter('device'), getState_Symbol: getter('symbol'),
		getState_Footprint: getter('footprint'), getState_OtherProperty: getter('otherProperty'),
	};
}

function originalState(): ComponentState {
	return {
		primitiveId: 'old-primitive', componentType: 'part', designator: 'U3', name: 'LCD',
		x: 1350, y: 1020, rotation: 0, mirror: false, net: '', subPartName: '',
		addIntoBom: true, addIntoPcb: true, uniqueId: 'stable-u3-link',
		manufacturer: 'DEMO', manufacturerId: 'LCD-13P', supplier: 'LCSC', supplierId: 'C123',
		device: { uuid: DEVICE_UUID, libraryUuid: DEVICE_LIB },
		symbol: { uuid: 'symbol-instance', libraryUuid: 'symbol-library' },
		footprint: { ...OLD_FOOTPRINT }, otherProperty: { Voltage: '3.3V' },
	};
}

function rebindHarness(options: {
	failCandidateCreate?: boolean;
	mismatchUniqueId?: boolean;
	failCandidateDelete?: boolean;
	failDeviceRollback?: boolean;
	ignoreDeviceAssociationModify?: boolean;
	deviceModifyReturnsFalseAfterApply?: boolean;
	candidateUsesWrongBinding?: boolean;
} = {}): { eda: any; events: string[]; states: Map<string, ComponentState> } {
	const events: string[] = [];
	const states = new Map<string, ComponentState>([['old-primitive', originalState()]]);
	let createCount = 0;
	let association = { ...OLD_FOOTPRINT };
	const edaMock: any = {
		sys_Log: { add: () => undefined },
		sch_PrimitiveComponent: {
			get: async (id: string) => {
				events.push(`component.get:${id}`);
				const state = states.get(id);
				return state ? component(state) : undefined;
			},
			getAll: async () => [...states.values()].map(component),
			create: async (_device: unknown, x: number, y: number, subPartName: string, rotation: number, mirror: boolean, addIntoBom: boolean, addIntoPcb: boolean) => {
				createCount++;
				events.push(`component.create:${createCount}`);
				if (createCount === 1 && options.failCandidateCreate) throw new Error('candidate create failed');
				const id = createCount === 1 ? 'candidate-primitive' : 'restored-original';
				const state: ComponentState = {
					...originalState(), primitiveId: id, designator: 'U?', uniqueId: `generated-${createCount}`,
					x, y, subPartName, rotation, mirror, addIntoBom, addIntoPcb,
					manufacturer: '', manufacturerId: '', supplier: '', supplierId: '', otherProperty: {},
					footprint: createCount === 1 && options.candidateUsesWrongBinding
						? { ...OLD_FOOTPRINT }
						: { ...association },
				};
				states.set(id, state);
				return component(state);
			},
			modify: async (id: string, props: Record<string, unknown>) => {
				events.push(`component.modify:${id}`);
				const state = states.get(id);
				if (!state) return undefined;
				for (const [key, value] of Object.entries(props)) {
					if (key === 'uniqueId' && options.mismatchUniqueId && id === 'candidate-primitive') continue;
					state[key] = value;
				}
				return component(state);
			},
			delete: async (id: string) => {
				events.push(`component.delete:${id}`);
				if (id === 'candidate-primitive' && options.failCandidateDelete) throw new Error('candidate delete failed');
				states.delete(id);
				return true;
			},
		},
		lib_Device: {
			getByLcscIds: async () => [{
				uuid: DEVICE_UUID, libraryUuid: DEVICE_LIB, supplierId: 'C123', manufacturerId: 'LCD-13P',
				footprint: { ...association, name: 'LCD-FP' },
			}],
			modify: async (_uuid: string, _library: string, _name: unknown, _description: unknown, update: any) => {
				const ref = update?.footprint;
				events.push(`device.modify:${ref?.uuid ?? 'unknown'}`);
				if (ref?.uuid === OLD_FOOTPRINT.uuid && options.failDeviceRollback) throw new Error('device rollback failed');
				if (ref && !options.ignoreDeviceAssociationModify) association = { uuid: ref.uuid, libraryUuid: ref.libraryUuid };
				if (ref?.uuid === NEW_FOOTPRINT.uuid && options.deviceModifyReturnsFalseAfterApply) return false;
				return true;
			},
			get: async () => ({ association: { footprint: { ...association } } }),
		},
	};
	return { eda: edaMock, events, states };
}

async function runFootprintRebind(mock: any): Promise<any> {
	(globalThis as any).eda = mock;
	try {
		return await runAction('schematic.rebind.footprint', {
			primitiveId: 'old-primitive',
			footprintUuid: NEW_FOOTPRINT.uuid,
			footprintLibraryUuid: NEW_FOOTPRINT.libraryUuid,
		});
	}
	finally { delete (globalThis as any).eda; }
}

test('rebind candidate create failure never deletes the original instance', async () => {
	const fixture = rebindHarness({ failCandidateCreate: true });
	await assert.rejects(() => runFootprintRebind(fixture.eda), (err: any) => {
		assert.match(err.message, /candidate-create/);
		assert.match(String(err.detail), /"verified":true/);
		return true;
	});
	assert.equal(fixture.states.has('old-primitive'), true);
	assert.equal(fixture.events.includes('component.delete:old-primitive'), false);
});

test('rebind refuses a missing stable uniqueId before any mutation', async () => {
	const fixture = rebindHarness();
	fixture.states.get('old-primitive')!.uniqueId = '';
	await assert.rejects(() => runFootprintRebind(fixture.eda), (err: any) => {
		assert.equal(err.code, 'PRECONDITION_REFUSED');
		assert.match(err.message, /no stable non-empty uniqueId/);
		return true;
	});
	assert.equal(fixture.events.some(event => event.startsWith('device.modify:')), false);
	assert.equal(fixture.events.some(event => event.startsWith('component.create:')), false);
	assert.equal(fixture.events.some(event => event.startsWith('component.delete:')), false);
});

test('rebind treats a true-but-not-applied association as the original binding and never deletes before clone fallback', async () => {
	const fixture = rebindHarness({ ignoreDeviceAssociationModify: true });
	await assert.rejects(() => runFootprintRebind(fixture.eda), (err: any) => {
		assert.match(err.message, /no personal library is available for the clone fallback/);
		return true;
	});
	assert.equal(fixture.events.some(event => event.startsWith('component.create:')), false);
	assert.equal(fixture.events.some(event => event.startsWith('component.delete:')), false);
	assert.equal(fixture.states.has('old-primitive'), true);
});

test('rebind trusts fresh target association when lib_Device.modify returns false after applying', async () => {
	const fixture = rebindHarness({ deviceModifyReturnsFalseAfterApply: true });
	const result = await runFootprintRebind(fixture.eda);
	assert.equal(result.result.mode, 'in-place');
	assert.equal(result.result.component.uniqueId, 'stable-u3-link');
	assert.equal(fixture.states.has('old-primitive'), false);
	assert.equal(fixture.states.has('candidate-primitive'), true);
});

test('rebind creates and freshly reads the candidate before deleting the original', async () => {
	const fixture = rebindHarness();
	const result = await runFootprintRebind(fixture.eda);
	const create = fixture.events.indexOf('component.create:1');
	const read = fixture.events.indexOf('component.get:candidate-primitive');
	const removeOld = fixture.events.indexOf('component.delete:old-primitive');
	assert.ok(create >= 0 && read > create && removeOld > read, fixture.events.join('\n'));
	assert.equal(result.result.component.uniqueId, 'stable-u3-link');
	assert.equal(result.result.transaction.verified, true);
});

test('rebind never reports success when fresh uniqueId readback differs', async () => {
	const fixture = rebindHarness({ mismatchUniqueId: true });
	await assert.rejects(() => runFootprintRebind(fixture.eda), (err: any) => {
		assert.match(err.message, /restore-and-verify/);
		assert.match(String(err.detail), /uniqueId/);
		assert.match(String(err.detail), /"verified":true/);
		return true;
	});
	assert.equal(fixture.states.has('candidate-primitive'), false);
	assert.equal(fixture.states.get('restored-original')?.uniqueId, 'stable-u3-link');
});

test('rebind rolls back when the fresh replacement resolves to the wrong footprint binding', async () => {
	const fixture = rebindHarness({ candidateUsesWrongBinding: true });
	await assert.rejects(() => runFootprintRebind(fixture.eda), (err: any) => {
		assert.match(err.message, /restore-and-verify/);
		assert.match(String(err.detail), /device identity could not be proven|requested binding/);
		assert.match(String(err.detail), /"verified":true/);
		return true;
	});
	assert.equal(fixture.states.has('candidate-primitive'), false);
	assert.equal(fixture.states.get('restored-original')?.footprint && (fixture.states.get('restored-original')!.footprint as any).uuid, OLD_FOOTPRINT.uuid);
});

test('rebind failure exposes unverified rollback facts instead of swallowing rollback errors', async () => {
	const fixture = rebindHarness({ mismatchUniqueId: true, failCandidateDelete: true, failDeviceRollback: true });
	await assert.rejects(() => runFootprintRebind(fixture.eda), (err: any) => {
		assert.match(String(err.detail), /"verified":false/);
		assert.match(String(err.detail), /candidate delete/);
		assert.match(String(err.detail), /device association rollback/);
		assert.match(String(err.detail), /"phase":"restore-and-verify"/);
		return true;
	});
});

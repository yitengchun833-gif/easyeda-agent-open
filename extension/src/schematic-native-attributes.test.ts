/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import JSZip from 'jszip';
import { runAction } from './actions';
import { readProjectNativeSourceArchive } from './native-footprint-source';
import { projectSchematicAttributeInventory } from './util';

const page = 'page-1';
const head = (uuid = page) => `{"type":"DOCHEAD"}||{"docType":"SCH_PAGE","uuid":"${uuid}"}|\n`;
const attr = (overrides: Record<string, unknown> = {}) =>
	`{"type":"ATTR","id":"attr-1"}||${JSON.stringify({ key: 'Unique ID', value: 'gge1', parentId: 'part-1', keyVisible: null, valueVisible: null, align: null, ...overrides })}|\n`;

async function archive(source: string): Promise<Blob> {
	const zip = new JSZip();
	zip.file('project.epru', source);
	return new Blob([await zip.generateAsync({ type: 'arraybuffer', compression: 'DEFLATE' })]);
}

test('native SCH_PAGE attributes recover explicit null visibility from the exact page', async () => {
	const source = head('other-page') + attr({ keyVisible: true }) + head() + attr();
	const inventory = projectSchematicAttributeInventory(await readProjectNativeSourceArchive(await archive(source)), page);
	assert.deepEqual(inventory.get('attr-1'), {
		id: 'attr-1', key: 'Unique ID', value: 'gge1', parentId: 'part-1', keyVisible: null, valueVisible: null, align: null,
	});
});

for (const [label, source] of [
	['missing page', head('other-page') + attr()],
	['duplicate page', head() + attr() + head()],
	['duplicate attribute', head() + attr() + attr()],
	['one visibility field', head() + attr({ keyVisible: undefined })],
	['invalid visibility', head() + attr({ keyVisible: 'false' })],
	['malformed row', head() + 'invalid'],
] as const) {
	test(`native SCH_PAGE attribute inventory refuses ${label}`, () => {
		assert.throws(() => projectSchematicAttributeInventory(source, page));
	});
}

function installHost(source: Blob | undefined, mutate?: (state: Record<string, unknown>) => void, readableExtra = false, sheet = false, mutateReread?: (state: Record<string, unknown>) => void) {
	const globals = globalThis as any;
	const previousEda = globals.eda;
	const previousTypes = globals.EDMT_EditorDocumentType;
	let exports = 0;
	let deletes = 0;
	const state: Record<string, unknown> = {
		PrimitiveId: 'attr-1', X: 0, Y: 0, Rotation: 0, Color: null, FontName: null,
		FontSize: null, Bold: null, Italic: null, UnderLine: null, AlignMode: undefined,
		FillColor: null, Key: 'Unique ID', Value: 'gge1', KeyVisible: undefined,
		ValueVisible: undefined, ParentPrimitiveId: 'part-1',
	};
	mutate?.(state);
	const proxy = (record: Record<string, unknown>) => new Proxy({}, { get: (_target, prop) => {
		if (typeof prop === 'string' && prop.startsWith('getState_')) return () => record[prop.slice(9)];
		return undefined;
	} });
	const attribute = proxy(state);
	const rereadState = { ...state };
	mutateReread?.(rereadState);
	const reread = proxy(rereadState);
	const extra = proxy({ ...state, PrimitiveId: 'attr-2', Key: 'Generated', KeyVisible: false, ValueVisible: true, AlignMode: null });
	const titleBlock = proxy({ PrimitiveId: 'sheet-1', ComponentType: 'sheet', Name: '', Component: {}, OtherProperty: {} });
	const empty = () => ({ getAll: async () => [], delete: async () => { deletes++; return true; } });
	globals.EDMT_EditorDocumentType = { HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, PCB: 3 };
	globals.eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'project-1' }) },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: page, tabId: 'tab-1', documentType: 1 }) },
		sys_FileManager: source ? { getProjectFile: async () => { exports++; return source; } } : {},
		sch_PrimitiveComponent: { ...empty(), getAll: async () => sheet ? [titleBlock] : [] }, sch_PrimitiveWire: empty(), sch_PrimitiveBus: empty(),
		sch_PrimitiveArc: empty(), sch_PrimitiveCircle: empty(), sch_PrimitiveRectangle: empty(),
		sch_PrimitivePolygon: empty(), sch_PrimitiveText: empty(), sch_PrimitiveObject: empty(),
		sch_PrimitiveAttribute: {
			getAll: async () => readableExtra ? [attribute, extra] : [attribute],
			getAllPrimitiveId: async () => readableExtra ? ['attr-1', 'attr-2'] : ['attr-1'],
			get: async (id: string) => id === 'attr-1' ? reread : readableExtra && id === 'attr-2' ? extra : undefined,
			delete: async () => { deletes++; return true; },
		},
	};
	return {
		exports: () => exports,
		deletes: () => deletes,
		restore: () => {
			if (previousEda === undefined) delete globals.eda; else globals.eda = previousEda;
			if (previousTypes === undefined) delete globals.EDMT_EditorDocumentType; else globals.EDMT_EditorDocumentType = previousTypes;
		},
	};
}

test('page primitive read uses exact native nulls only for unreadable visibility and alignment getters', async () => {
	const host = installHost(await archive(head() + attr()), undefined, true);
	try {
		const response: any = await runAction('schematic.components.list', { includePagePrimitives: true });
		const actual = response.result.pagePrimitives.attributes[0];
		assert.equal(actual.primitiveId, 'attr-1');
		assert.equal(actual.KeyVisible, null);
		assert.equal(actual.ValueVisible, null);
		assert.equal(actual.AlignMode, null);
		assert.equal(actual.Value, 'gge1');
		assert.equal(response.result.pagePrimitives.attributes[1].primitiveId, 'attr-2',
			'a fully readable generated SDK attribute need not be in native source');
		assert.equal(host.exports(), 1);
		assert.equal(host.deletes(), 0);
	}
	finally { host.restore(); }
});

test('page primitive read recovers an unreadable sheet update time before normalizing its dynamic value', async () => {
	const host = installHost(
		await archive(head() + attr({ key: '@Update Time', value: '18:51:48', parentId: 'sheet-1' })),
		state => { state.Key = '@Update Time'; state.Value = '19:00:58'; state.ParentPrimitiveId = 'sheet-1'; },
		false, true,
	);
	try {
		const response: any = await runAction('schematic.components.list', { includePagePrimitives: true });
		assert.equal(response.result.pagePrimitives.attributes[0].Value, null);
	}
	finally { host.restore(); }
});

test('page primitive read retains an update-time value difference outside the actual sheet', async () => {
	const host = installHost(
		await archive(head() + attr({ key: '@Update Time', value: '18:51:48' })),
		state => { state.Key = '@Update Time'; state.Value = '19:00:58'; },
	);
	try {
		const response: any = await runAction('schematic.components.list', { includePagePrimitives: true });
		assert.equal(response.result.pagePrimitives.attributes[0].Value, '19:00:58');
	}
	finally { host.restore(); }
});

test('page primitive read retains a different typed value when native source has a different non-null value', async () => {
	const host = installHost(
		await archive(head() + attr({ key: '@Update Revision', value: 'old', parentId: 'sheet-1' })),
		state => { state.Key = '@Update Revision'; state.Value = 'new'; state.ParentPrimitiveId = 'sheet-1'; },
		false, true,
	);
	try {
		const response: any = await runAction('schematic.components.list', { includePagePrimitives: true });
		assert.equal(response.result.pagePrimitives.attributes[0].Value, 'new');
	}
	finally { host.restore(); }
});

for (const [label, nativeValue, typedValue] of [
	['native null projects a UUID', null, '77469f2a80da8241'],
	['native null projects an empty string', null, ''],
] as const) {
	test(`page primitive read ${label} without replacing the typed value`, async () => {
		const host = installHost(await archive(head() + attr({ value: nativeValue })), state => { state.Value = typedValue; });
		try {
			const response: any = await runAction('schematic.components.list', { includePagePrimitives: true });
			assert.equal(response.result.pagePrimitives.attributes[0].Value, typedValue);
		}
		finally { host.restore(); }
	});
}

for (const [label, source, mutate, readableExtra = false, sheet = false, mutateReread] of [
	['missing archive', undefined, undefined],
	['different attribute key', head() + attr({ key: 'Other' }), undefined],
	['different live parent', head() + attr(), (state: Record<string, unknown>) => { state.ParentPrimitiveId = 'another-part'; }],
	['different reread ID', head() + attr(), undefined, false, false, (state: Record<string, unknown>) => { state.PrimitiveId = 'other-id'; }],
	['missing attribute', head(), undefined],
	['missing typed value', head() + attr({ value: null }), (state: Record<string, unknown>) => { state.Value = undefined; }],
	['native visibility absent', head() + attr({ keyVisible: undefined, valueVisible: undefined }), undefined],
	['native alignment absent', head() + attr({ align: undefined }), undefined],
	['native alignment is non-null', head() + attr({ align: 1 }), undefined],
] as const) {
	test(`page primitive read fails closed with ${label}`, async () => {
		const host = installHost(source ? await archive(source) : undefined, mutate, readableExtra, sheet, mutateReread);
		try {
			await assert.rejects(() => runAction('schematic.components.list', { includePagePrimitives: true }));
			assert.equal(host.deletes(), 0);
		}
		finally { host.restore(); }
	});
}

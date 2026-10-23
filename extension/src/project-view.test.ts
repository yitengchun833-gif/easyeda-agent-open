/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test, type TestContext } from 'node:test';
import { runAction } from './actions';
import { runTargetedControl } from './control-target';
import { readResponseContext } from './eda-context';

function host(t: TestContext, doc: unknown, options: { project?: unknown; docError?: boolean; projectError?: boolean } = {}) {
	const globals = globalThis as any, old = globals.eda, oldTypes = globals.EDMT_EditorDocumentType;
	globals.EDMT_EditorDocumentType = { SCHEMATIC_PAGE: 1 };
	globals.eda = {
		dmt_Project: { getCurrentProjectInfo: async () => {
			if (options.projectError) throw new Error('project API failed');
			return 'project' in options ? options.project : { uuid: 'project', friendlyName: 'Test project' };
		} },
		dmt_SelectControl: { getCurrentDocumentInfo: async () => {
			if (options.docError) throw new Error('document API failed');
			return doc;
		} },
	};
	t.after(() => { globals.eda = old; globals.EDMT_EditorDocumentType = oldTypes; });
}

for (const doc of [undefined, null]) {
	test(`document.current reports an open project with no active page (${doc})`, async t => {
		host(t, doc);
		const result = await runAction('document.current', { _target: { projectUuid: 'project' } });
		const { _scene, ...data } = result.result!;
		assert.equal(typeof (_scene as any).instance, 'string');
		assert.deepEqual(data, { uuid: null, tabId: null, documentType: null, documentTypeCode: null,
			parentProjectUuid: 'project', projectOpened: true, documentActive: false,
			state: 'project_open_no_active_document', message: '工程已打开、无活动图页' });
		assert.equal(result.context?.projectUuid, 'project');
		assert.equal(result.context?.documentUuid, undefined);
	});
}

test('document.current keeps the active-page result unchanged', async t => {
	host(t, { uuid: 'page', tabId: 'tab', documentType: 1, parentProjectUuid: 'project' });
	const result = await runAction('document.current', {});
	assert.equal(result.result?.uuid, 'page');
	assert.equal(result.result?.documentType, 'schematic');
	assert.equal(result.result?.state, undefined);
});

for (const options of [{ docError: true }, { projectError: true }, { project: undefined }]) {
	test(`document.current does not turn unavailable evidence into an open-project state (${JSON.stringify(options)})`, async t => {
		host(t, undefined, options);
		await assert.rejects(runAction('document.current', {}), (error: any) => error.code === 'EDA_CALL_FAILED');
	});
}

test('project-only status reports normal project view without changing the queue', async t => {
	host(t, undefined);
	let reads = 0;
	for (const operation of [undefined, 'status']) {
		const result = await runTargetedControl({ operation, _target: { projectUuid: 'project' } }, () => {
			reads++; return { paused: false, pending: 0, inFlight: [] };
		});
		assert.equal(result.context?.projectUuid, 'project');
		assert.equal(result.result?.targetChecked, true);
		assert.equal(result.result?.documentActive, false);
		assert.equal(result.result?.state, 'project_open_no_active_document');
		assert.equal(result.result?.message, '工程已打开、无活动图页');
		assert.deepEqual(result.result?.inFlight, []);
	}
	assert.equal(reads, 2);
});

test('explicit wrong targets and queue-changing controls retain exact identity requirements', async t => {
	host(t, undefined);
	let changes = 0;
	for (const payload of [
		{ operation: 'status', _target: { projectUuid: 'other' } },
		{ operation: 'status', _target: { projectUuid: 'project', documentUuid: 'page' } },
		{ operation: 'status', _target: { projectUuid: 'project', documentUuid: '' } },
		{ operation: 'status', _target: {} },
		...['cancel', 'pause', 'resume', 'supersede'].map(operation => ({ operation, _target: { projectUuid: 'project' } })),
	]) {
		await assert.rejects(runTargetedControl(payload, () => { changes++; return {}; }), (error: any) => error.code === 'PRECONDITION_REFUSED');
	}
	await assert.rejects(runAction('document.current', { _target: { projectUuid: 'project', documentUuid: 'page' } }), /Target documentUuid/);
	assert.equal(changes, 0);
});

for (const options of [{ docError: true }, { projectError: true }]) {
	test(`project-only status preserves native API exceptions (${JSON.stringify(options)})`, async t => {
		host(t, undefined, options);
		let reads = 0;
		await assert.rejects(runTargetedControl({ operation: 'status', _target: { projectUuid: 'project' } }, () => { reads++; return {}; }), /API failed/);
		assert.equal(reads, 0);
		// Existing non-diagnostic response decoration remains best-effort.
		await assert.doesNotReject(readResponseContext());
	});
}

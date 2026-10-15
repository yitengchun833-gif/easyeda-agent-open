/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { getSchematicNetlistFile } from './schematic-netlist-file';

for (const mode of ['success', 'empty', 'no-message-channel', 'export-error', 'drift', 'busy-other', 'busy-before', 'sync-before', 'unsupported', 'readback-error']) {
	test(`netlist transaction boundary: ${mode}`, async t => {
		const globals = globalThis as any, old = { eda: globals.eda, window: globals.window, setTimeout: globals.setTimeout, MessageChannel: globals.MessageChannel };
		t.after(() => Object.assign(globals, old));
		let tab = { uuid: 'page', tabId: 'page@project' };
		const calls: Array<string> = [];
		const file = { name: 'netlist.json' };
		const document = { uuid: 'page', tabId: 'page@project', profileSetting: { readonlyMode: false } };
		const runner = { running: mode === 'busy-before' || mode === 'sync-before', currentAction: mode === 'busy-before' ? { name: 'copy', status: 'running' } : mode === 'sync-before' ? { name: 'RealTimeSync', status: 'running' } : null as any };
		let documentReads = 0;
		if (mode === 'no-message-channel') globals.MessageChannel = undefined;
		globals.window = mode === 'unsupported' ? {} : { SCH: { docMemoryManager: { getActiveDoc: () => document }, app: { actionRunner: runner } } };
		globals.eda = {
			dmt_SelectControl: { getCurrentDocumentInfo: async () => {
				if (mode === 'sync-before' && ++documentReads > 1) Object.assign(runner, { running: false, currentAction: null });
				if (mode === 'readback-error' && calls.length) throw new Error('host read failed');
				return { ...tab };
			} },
			dmt_EditorControl: { activateDocument: async () => { throw new Error('must not refocus'); } },
			sch_ManufactureData: { getNetlistFile: async (_name: string | undefined, type: string) => {
				assert.equal(runner.running, false, 'export must wait for the previous transaction before reading a file');
				assert.equal(type, 'JLCEDA', 'pin-net reads must request the JSON format explicitly');
				calls.push('export');
				if (mode === 'drift') tab = { uuid: 'other', tabId: 'other@project' };
				if (mode === 'busy-other') Object.assign(runner, { running: true, currentAction: { name: 'copy', status: 'running' } });
				if (mode === 'export-error') throw new Error('export failed');
				return mode === 'empty' ? undefined : file;
			} },
		};
		if (mode === 'success' || mode === 'empty' || mode === 'no-message-channel' || mode === 'sync-before') assert.equal(await getSchematicNetlistFile(), mode === 'empty' ? undefined : file);
		else await assert.rejects(getSchematicNetlistFile(), /export failed|Cannot verify schematic readiness/);
		assert.deepEqual(calls, mode === 'unsupported' || mode === 'busy-before' ? [] : ['export']);
	});
}

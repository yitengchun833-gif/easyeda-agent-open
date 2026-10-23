import assert from 'node:assert/strict';
import test from 'node:test';
import { openKnowledgeBase } from './knowledge-panel';

test('reopening restores a minimized panel when host existence detection always returns false', async () => {
	let exists = false, visible = false, opened = 0;
	let emptyContainer = true;
	let button: ((value: 'close' | 'minimize' | 'maximize') => void) | undefined;
	(globalThis as any).eda = { sys_IFrame: {
		isIFrameAlreadyExist: async () => false, // Observed 3.2.175 SDK stub.
		showIFrame: async () => true, // Host can retain a stale record after an extension reload.
		closeIFrame: async () => { setTimeout(() => {
			exists = visible = false; emptyContainer = true; button?.('close');
		}, 100); return true; },
		openIFrame: async (_path: string, _w: number, _h: number, _id: string, props: any) => {
			if (exists || emptyContainer) return true; // Host reuses even a closed empty container.
			exists = visible = true; opened++; button = props.buttonCallbackFn;
			return true;
		},
	} };
	(globalThis as any).document = { getElementById: (id: string) => {
		assert.equal(id, 'de2aa2ac3bd74c00b6701e3adf3ee723.easyeda-agent-knowledge');
		return emptyContainer || exists ? {
			childElementCount: emptyContainer ? 0 : 1,
			remove: () => { emptyContainer = false; },
			querySelector: (selector: string) => {
				assert.equal(selector, 'iframe');
				return exists ? { getBoundingClientRect: () => ({ height: visible ? 720 : 0 }) } : null;
			},
		} : null;
	} };
	try {
		await openKnowledgeBase();
		await new Promise(r => setTimeout(r,110));
		// A fresh invocation must use the real window, not a module-local opened flag.
		delete require.cache[require.resolve('./knowledge-panel')];
		const reopen = require('./knowledge-panel').openKnowledgeBase as typeof openKnowledgeBase;
		await reopen();
		assert.equal(opened, 1, 'normal reopening preserves the existing form');
		visible = false; button?.('minimize');
		await reopen();
		assert.equal(visible, true, 'minimized body must become visible again');
		assert.equal(opened, 2);
		exists = visible = false; button?.('close');
		await reopen();
		assert.equal(visible, true, 'closing then reopening creates a visible window');
		assert.equal(opened, 3);
	} finally { delete (globalThis as any).eda; delete (globalThis as any).document; }
});

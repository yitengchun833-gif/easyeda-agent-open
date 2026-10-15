/// <reference types="@jlceda/pro-api-types" />
import { uuid } from '../extension.json';
export async function openKnowledgeBase(): Promise<void> {
	const id = 'easyeda-agent-knowledge';
	// As in clickImportConfirm, AsyncFunction reaches the host DOM outside the module sandbox.
	const AsyncFunction = Object.getPrototypeOf(async () => {}).constructor as {
		new (name: string, body: string): (id: string) => Promise<unknown>;
	};
	const fullId = `${uuid}.${id}`;
	// ponytail: 3.2.175 existence/show APIs can report stale windows; inspect only our rendered frame.
	const visible = await new AsyncFunction('id', 'return (document.getElementById(id)?.querySelector("iframe")?.getBoundingClientRect().height ?? 0) > 0;')(fullId);
	if (visible && await eda.sys_IFrame.showIFrame(id)) {
		return;
	}
	// Clear folded/stale records. The 3.2.175 existence API is a false stub,
	// and show can report an old window after extension reload without rendering it.
	await eda.sys_IFrame.closeIFrame(id);
	// ponytail: 3.2.175 completes closing after its promise; wait for the host before reusing the ID.
	await new Promise(resolve => setTimeout(resolve, 150));
	// ponytail: 3.2.175 leaves an empty DOM container after close; drop this shim when the host removes it.
	await new AsyncFunction('id', 'const empty = document.getElementById(id); if (empty?.childElementCount === 0) empty.remove();')(fullId);
	const opened = await eda.sys_IFrame.openIFrame('/iframe/knowledge/index.html', 1040, 720, id, {
		title: 'EDA Agent 知识库',
		maximizeButton: true,
		minimizeButton: true,
		minimizeStyle: 'constricted',
	});
	if (!opened) throw new Error('Knowledge panel could not be opened');
}

/** Native event hints, not a document revision or proof of complete event coverage. */
const instance = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
let sequence = 0;
let installed = false;
let supported = false;
let notify: ((stamp: ReturnType<typeof sceneObservation>) => void) | undefined;
export function observeSceneChanges(callback: typeof notify) {
	notify=callback;
	if (!supported) installed=false; // Retry unavailable Beta API once on reconnect.
	sceneObservation();
}
const changes: Array<{ sequence: number; primitiveIds: string[]; eventType: string }> = [];

export function sceneObservation() {
	if (!installed && typeof eda !== 'undefined') {
		installed = true;
		try {
			eda.sch_Event.addPrimitiveEventListener('easyeda-open-observe', 'all', (eventType, props) => {
				changes.push({ sequence: ++sequence, primitiveIds: [...(props?.primitiveIds ?? [])], eventType: String(eventType) });
				if (changes.length > 128) changes.shift();
				try { notify?.(stamp(true)); } catch { /* state hints never interrupt native edits */ }
			});
			supported = true;
		} catch { /* API is Beta; missing support never becomes a fresh-state claim. */ }
	}
	return stamp(false);
}

function stamp(delta: boolean) {
	return { instance, sequence, supported, eventCoverage: 'beta-unverified', delta,
		oldestSequence: changes[0]?.sequence ?? sequence,
		changes: (delta ? changes.slice(-1) : changes).map(c => ({ ...c, primitiveIds: [...c.primitiveIds] })) };
}

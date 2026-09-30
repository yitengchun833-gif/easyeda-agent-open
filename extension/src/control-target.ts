import { readResponseContext } from './eda-context';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';

/** Bypass controls stay outside the FIFO, but an explicit target must be native-confirmed. */
export async function runTargetedControl(
	payload: Record<string, unknown>, control: () => Record<string, unknown>,
): Promise<ActionResult> {
	if (!Object.prototype.hasOwnProperty.call(payload, '_target')) return { result: control() };
	const target = payload._target;
	if (!target || typeof target !== 'object' || Array.isArray(target)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Control target must identify projectUuid and documentUuid.');
	const context = await readResponseContext();
	for (const key of ['projectUuid', 'documentUuid'] as const) {
		const expected = (target as Record<string, unknown>)[key];
		if (typeof expected !== 'string' || !expected || context[key] !== expected) {
			throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Control target ${key} differs or is unavailable; queue was not changed.`);
		}
	}
	return { context, result: { ...control(), targetChecked: true } };
}

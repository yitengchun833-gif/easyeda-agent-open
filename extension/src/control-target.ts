import { readResponseContext } from './eda-context';
import { ActionError, ErrorCodes, type ActionResult } from './protocol';

/** Bypass controls stay outside the FIFO, but an explicit target must be native-confirmed. */
export async function runTargetedControl(
	payload: Record<string, unknown>, control: () => Record<string, unknown>,
): Promise<ActionResult> {
	if (!Object.prototype.hasOwnProperty.call(payload, '_target')) return { result: control() };
	const target = payload._target;
	if (!target || typeof target !== 'object' || Array.isArray(target)) throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, 'Control target must be an object identifying projectUuid; queue changes also require documentUuid.');
	const statusOnly = payload.operation === undefined || payload.operation === 'status';
	const context = await readResponseContext(statusOnly);
	for (const key of ['projectUuid', 'documentUuid'] as const) {
		if (statusOnly && key === 'documentUuid' && !Object.prototype.hasOwnProperty.call(target, key)) continue;
		const expected = (target as Record<string, unknown>)[key];
		if (typeof expected !== 'string' || !expected || context[key] !== expected) {
			throw new ActionError(ErrorCodes.PRECONDITION_REFUSED, `Control target ${key} differs or is unavailable; queue was not changed.`);
		}
	}
	return { context, result: { ...control(), targetChecked: true,
		...(statusOnly && !context.documentUuid ? { projectOpened: true, documentActive: false,
			state: 'project_open_no_active_document', message: '工程已打开、无活动图页' } : {}) } };
}

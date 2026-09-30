/// <reference types="@jlceda/pro-api-types" />
import { ActionError, type ActionResult, ErrorCodes } from './protocol';
import { describeThrown, requireString } from './util';

type Color = { r: number; g: number; b: number; alpha: number };
function readColor(value: unknown): Color {
	const color = value as Color | undefined;
	if (!color || ['r', 'g', 'b', 'alpha'].some(k => {
		const n = (color as any)[k];
		return typeof n !== 'number' || !Number.isFinite(n) || n < 0 || n > 1;
	})) throw new ActionError(ErrorCodes.INVALID_STATE, 'Network color is unavailable or does not use the supported normalized RGBA schema.');
	return { r: color.r, g: color.g, b: color.b, alpha: color.alpha };
}
const same = (a: Color, b: Color) => (['r', 'g', 'b', 'alpha'] as const).every(k => Math.abs(a[k] - b[k]) <= 1e-6);

export async function pcbNetColorSet(payload: Record<string, unknown>): Promise<ActionResult> {
	for (const key of Object.keys(payload)) if (!['net', 'color', 'dryRun'].includes(key)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, `Unknown net-color field: ${key}`);
	}
	const net = requireString(payload, 'net'), color = requireString(payload, 'color');
	if (!/^#[0-9a-f]{6}$/i.test(color) || !net.trim() || (payload.dryRun !== undefined && typeof payload.dryRun !== 'boolean')) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'net-color requires a non-empty net, #RRGGBB color and optional boolean dryRun.');
	}
	const before = readColor(await eda.pcb_Net.getNetColor(net));
	const requested: Color = { r: parseInt(color.slice(1, 3), 16) / 255, g: parseInt(color.slice(3, 5), 16) / 255, b: parseInt(color.slice(5, 7), 16) / 255, alpha: before.alpha };
	if (payload.dryRun === true) return { result: { dryRun: true, net, before, requested } };
	if (same(before, requested)) return { result: { net, before, requested, actual: before, changed: false, verified: true, partial: false } };
	let written: boolean | null = null, writeError: string | undefined;
	try { written = await eda.pcb_Net.setNetColor(net, requested); }
	catch (err) { writeError = describeThrown(err); }
	let actual: Color | null = null, readbackError: string | undefined;
	try { actual = readColor(await eda.pcb_Net.getNetColor(net)); }
	catch (err) { readbackError = describeThrown(err); }
	const verified = written === true && !!actual && same(actual, requested);
	return { result: { net, before, requested, actual, written, writeError, readbackError, verified, partial: !verified },
		...(!verified ? { warnings: ['Network color was not confirmed; inspect before/actual and do not blindly retry.'] } : {}) };
}

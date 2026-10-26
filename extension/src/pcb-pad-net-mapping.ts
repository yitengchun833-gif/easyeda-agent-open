import { ActionError, ErrorCodes } from './protocol';

export function pcbPadNetAssignments(value: unknown): Map<string, string> {
	if (value === undefined) return new Map();
	if (!value || typeof value !== 'object' || Array.isArray(value)) {
		throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'nets must be an object mapping pad numbers to net names.');
	}
	const assignments = new Map<string, string>();
	for (const [number, net] of Object.entries(value)) {
		if (!number.trim() || number !== number.trim() || typeof net !== 'string' || net !== net.trim()) {
			throw new ActionError(ErrorCodes.MISSING_PAYLOAD_FIELD, 'nets requires non-empty pad numbers and string net names without surrounding whitespace; an empty net explicitly leaves a pad unassigned.');
		}
		assignments.set(number, net);
	}
	return assignments;
}

export type PcbPadNetReadback = { primitiveId: string; padNumber: string; net: string | null };

// Several physical pads may share a logical number; every one must match.
export function verifyPcbPadNets(expected: Map<string, string>, pads: PcbPadNetReadback[] | null) {
	const matched: PcbPadNetReadback[] = [];
	const mismatched: Array<PcbPadNetReadback & { expectedNet: string }> = [];
	const missingPadNumbers: string[] = [];
	const unverifiedPadNumbers: string[] = [];
	const conflictingPadNumbers: string[] = [];
	for (const [number, net] of expected) {
		if (!pads) { unverifiedPadNumbers.push(number); continue; }
		const observed = pads.filter(p => p.padNumber === number);
		if (!observed.length) { missingPadNumbers.push(number); continue; }
		if (new Set(observed.map(p => p.net)).size > 1) conflictingPadNumbers.push(number);
		for (const pad of observed) {
			if (pad.net === null) {
				if (!unverifiedPadNumbers.includes(number)) unverifiedPadNumbers.push(number);
			} else if (pad.net === net) matched.push(pad);
			else mismatched.push({ ...pad, expectedNet: net });
		}
	}
	return {
		verified: !missingPadNumbers.length && !mismatched.length && !unverifiedPadNumbers.length,
		readbackAvailable: pads !== null,
		matched, mismatched, missingPadNumbers, conflictingPadNumbers, unverifiedPadNumbers,
	};
}

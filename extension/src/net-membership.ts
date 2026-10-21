/** Compare one intended local net with the already exported native document netlist. */
export function checkNetMembers(index: unknown, members: unknown): { status: 'pass'|'fail'|'unknown'; net?: string; reason: string } {
  if (!Array.isArray(members) || members.length < 2 || members.some(p => typeof p !== 'string' || !p)
    || new Set(members).size !== members.length) return { status: 'fail', reason: 'invalid_members' };
  if (!index || typeof index !== 'object' || Array.isArray(index)) return { status: 'unknown', reason: 'netlist_unavailable' };
  const pins = new Map<string, string>();
  for (const [ref, values] of Object.entries(index)) {
    if (!values || typeof values !== 'object' || Array.isArray(values)) return { status: 'unknown', reason: 'invalid_netlist' };
    for (const [number, net] of Object.entries(values)) {
      const key = `${ref}.${number}`;
      if (typeof net !== 'string' || pins.has(key)) return { status: 'unknown', reason: 'ambiguous_netlist' };
      pins.set(key, net);
    }
  }
  if (members.some(p => !pins.has(p))) return { status: 'unknown', reason: 'member_unavailable' };
  const net = pins.get(members[0])!;
  if (!net || members.some(p => pins.get(p) !== net)) return { status: 'fail', reason: 'disconnected_or_split' };
  if ([...pins].some(([pin, value]) => value === net && !members.includes(pin))) return { status: 'fail', net, reason: 'unexpected_member' };
  return { status: 'pass', net, reason: 'exact_members' };
}

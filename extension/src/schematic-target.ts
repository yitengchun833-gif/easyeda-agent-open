type RecordValue = Record<string, any>;
type Finding = { code: string; status: 'fail' | 'unknown'; component?: string; pin?: string; field?: string; expected?: unknown; actual?: unknown; note?: string };
const record = (value: unknown): value is RecordValue => value !== null && typeof value === 'object' && !Array.isArray(value);
const string = (value: unknown): string => typeof value === 'string' ? value : '';
const stableKey = (part: RecordValue): string => part.uniqueId ? `unique:${part.uniqueId}` : `ref:${part.designator ?? part.ref ?? ''}`;
const knownNet = (pin: RecordValue): string => typeof pin.net === 'string' ? pin.net : '';

function connectionState(pin: RecordValue): string {
  if (pin.noConnected === true && knownNet(pin)) return 'unknown';
  if (pin.noConnected === true) return 'nc';
  if (typeof pin.net !== 'string') return 'unknown';
  if (pin.net) return 'connected';
  return pin.noConnected === false ? 'unconnected' : 'unknown';
}

/** Compare observed facts with an independent semantic target. Never writes or qualifies parts. */
export function checkSchematicTarget(input: unknown, target: unknown) {
  const findings: Finding[] = [];
  const idMap: Record<string, { sourcePrimitiveId: string | null; primitiveId: string | null; ref: string }> = {};
  const counts = { expectedComponents: 0, actualComponents: 0, expectedPins: 0, actualPins: 0, matchedComponents: 0 };
  const add = (code: string, status: 'fail' | 'unknown', fields: Omit<Finding, 'code' | 'status'> = {}) => findings.push({ code, status, ...fields });
  const finish = () => {
    const status = findings.some(f => f.status === 'fail') ? 'fail' : findings.length ? 'unknown' : 'pass';
    return { ok: status === 'pass', verified: status === 'pass', partial: findings.some(f => f.status === 'unknown'), status, findings, counts, idMap,
      limits: ['Checks recorded schematic identities and pin membership within the declared page only', 'Preserving source references and notes does not approve physical mappings, BOM selection, visual appearance or saved persistence'] };
  };
  if (!record(target) || target.schemaVersion !== 1 || !Array.isArray(target.components) || !Array.isArray(target.nets)) {
    add('invalid_target', 'fail'); return finish();
  }
  let envelope: RecordValue = record(input) ? input : {};
  if (envelope.structuredContent) envelope = envelope.structuredContent;
  const context: RecordValue = envelope.context ?? {};
  const snapshot: RecordValue = record(envelope.snapshot) ? envelope.snapshot : record(envelope.result) ? envelope.result : envelope;
  if (envelope.ok === false || !Array.isArray(snapshot.components)) { add('snapshot_unavailable', 'unknown'); return finish(); }
  const scope = snapshot.readScope ?? {};
  const inventoryComplete = scope.complete === true && scope.concurrentChange !== true && scope.allPages !== true && !scope.primitiveIds?.length && !scope.missingIds?.length && snapshot.partial !== true;
  if (target.scope?.complete !== true || target.scope?.kind !== 'page') add('target_scope_unsupported', 'unknown');
  if (!inventoryComplete) add('snapshot_scope_incomplete', 'unknown');
  for (const field of ['projectUuid', 'documentUuid']) {
    if (!target.scope?.[field] || !context[field]) add('target_identity_unavailable', 'unknown', { field });
    else if (target.scope[field] !== context[field]) add('target_identity_mismatch', 'fail', { field, expected: target.scope[field], actual: context[field] });
  }
  if (context.documentType && context.documentType !== 'schematic') add('wrong_document_type', 'fail');
  if (snapshot.components.some((c: unknown) => !record(c))) add('malformed_component_inventory', 'unknown');
  const actualParts: RecordValue[] = snapshot.components.filter((c: unknown): c is RecordValue => record(c) && c.componentType === 'part');
  counts.expectedComponents = target.components.length; counts.actualComponents = actualParts.length;
  const actual = new Map<string, RecordValue>(), actualRefs = new Map<string, RecordValue>();
  for (const part of actualParts) {
    const key = stableKey(part), ref = string(part.designator);
    if (key === 'ref:' || !ref) add('observed_component_identity_missing', 'unknown', { component: key });
    if (actual.has(key)) add('duplicate_observed_identity', 'fail', { component: key });
    if (actualRefs.has(ref)) add('duplicate_observed_designator', 'fail', { component: key, actual: ref });
    actual.set(key, part); actualRefs.set(ref, part);
    counts.actualPins += Array.isArray(part.pins) ? part.pins.length : 0;
  }
  const expectedKeys = new Set<string>(), expectedRefs = new Set<string>(), expectedPins = new Map<string, Map<string, RecordValue>>(), used = new Set<RecordValue>();
  const compareProperties = (wanted: unknown, observed: unknown, component: string, field: string): void => {
    if (wanted === null || wanted === undefined) return;
    if (observed === null || observed === undefined) { add('property_unavailable', 'unknown', { component, field, expected: wanted }); return; }
    if (record(wanted)) {
      if (!record(observed)) { add('property_mismatch', 'fail', { component, field, expected: wanted, actual: observed }); return; }
      for (const [name, value] of Object.entries(wanted)) compareProperties(value, observed[name], component, `${field}.${name}`);
    } else if (Array.isArray(wanted) ? JSON.stringify(wanted) !== JSON.stringify(observed) : wanted !== observed) add('property_mismatch', 'fail', { component, field, expected: wanted, actual: observed });
  };
  const compareLibrary = (wantedPart: RecordValue, actualPart: RecordValue, field: string) => {
    const component = wantedPart.key;
    const wanted = wantedPart[field], observed = actualPart[field];
    if (!record(wanted) || !record(observed)) { add('library_reference_unavailable', 'unknown', { component, field }); return; }
    for (const name of ['name', 'libraryUuid']) if (string(wanted[name])) compareProperties(wanted[name], observed[name], component, `${field}.${name}`);
    const sourceField = `${field}Source`;
    const expectedSource = wantedPart.deviceResolution?.[sourceField];
    const observedSource = actualPart.deviceResolution?.[sourceField];
    const left = record(expectedSource) && string(expectedSource.uuid) ? expectedSource : wanted;
    const right = record(observedSource) && string(observedSource.uuid) ? observedSource : observed;
    const a = string(left.uuid), b = string(right.uuid);
    if (!a || !b) add('library_identity_unavailable', 'unknown', { component, field });
    else if (a !== b) {
      // Project instance identifiers may change on recreation; only source UUIDs share a comparison domain.
      if (/^[a-f0-9]{32}$/i.test(a) && /^[a-f0-9]{32}$/i.test(b)) add('library_identity_mismatch', 'fail', { component, field, expected: a, actual: b });
      else add('library_identity_domain_unknown', 'unknown', { component, field, expected: a, actual: b, note: 'Instance/source identity equivalence needs original source evidence' });
    }
  };
  for (const wanted of target.components) {
    if (!record(wanted) || !string(wanted.key) || !string(wanted.ref) || !Array.isArray(wanted.pins)) { add('invalid_target_component', 'fail'); continue; }
    const key = wanted.key;
    if (key !== (wanted.uniqueId ? `unique:${wanted.uniqueId}` : `ref:${wanted.ref}`)) add('target_key_mismatch', 'fail', { component: key });
    if (expectedKeys.has(key)) add('duplicate_target_identity', 'fail', { component: key });
    if (expectedRefs.has(wanted.ref)) add('duplicate_target_designator', 'fail', { component: key });
    expectedKeys.add(key); expectedRefs.add(wanted.ref);
    counts.expectedPins += wanted.pins.length;
    const wantedPins = new Map<string, RecordValue>(); expectedPins.set(key, wantedPins);
    for (const pin of wanted.pins) {
      if (!record(pin) || !string(pin.number) || wantedPins.has(pin.number)) { add('invalid_target_pin', 'fail', { component: key }); continue; }
      wantedPins.set(pin.number, pin);
    }
    // uniqueId is preferred; ref fallback is only for targets explicitly lacking a stable native uniqueId.
    const part = wanted.uniqueId ? actual.get(key) : actualRefs.get(wanted.ref);
    if (!part) { add('missing_component', inventoryComplete ? 'fail' : 'unknown', { component: key }); continue; }
    used.add(part); counts.matchedComponents++;
    idMap[key] = { sourcePrimitiveId: wanted.sourcePrimitiveId ?? null, primitiveId: part.primitiveId ?? null, ref: wanted.ref };
    compareProperties(wanted.ref, part.designator, key, 'designator');
    for (const field of ['device', 'symbol', 'footprint']) compareLibrary(wanted, part, field);
    if (record(wanted.properties)) for (const [field, value] of Object.entries(wanted.properties)) compareProperties(value, part[field], key, field);
    if (!Array.isArray(part.pins) || !part.pins.length || part.pinsAvailable === false) { add('pin_inventory_unavailable', 'unknown', { component: key }); continue; }
    const observedPins = new Map<string, RecordValue>();
    for (const pin of part.pins) {
      if (!record(pin)) { add('malformed_pin_inventory', 'unknown', { component: key }); continue; }
      const number = string(pin.pinNumber ?? pin.number);
      if (!number) { add('observed_pin_number_missing', 'unknown', { component: key }); continue; }
      if (observedPins.has(number)) add('duplicate_observed_pin', 'fail', { component: key, pin: number });
      observedPins.set(number, pin);
      if (!wantedPins.has(number)) add('extra_pin', 'fail', { component: key, pin: number });
    }
    for (const [number, wantedPin] of wantedPins) {
      const pin = observedPins.get(number);
      if (!pin) { add('missing_pin', 'fail', { component: key, pin: number }); continue; }
      if (string(wantedPin.name)) compareProperties(wantedPin.name, pin.pinName ?? pin.name, key, `pins.${number}.name`);
      const state = connectionState(pin), expectedState = wantedPin.state;
      if (!['connected', 'nc', 'unconnected', 'unknown'].includes(expectedState)) { add('invalid_target_pin_state', 'fail', { component: key, pin: number }); continue; }
      if (expectedState === 'unknown' || state === 'unknown' || part.netlistAvailable === false || snapshot.pinNetsAvailable === false || scope.netRead === 'not-requested') { add('pin_connection_unknown', 'unknown', { component: key, pin: number }); continue; }
      if (expectedState === 'connected' && (!string(wantedPin.net) || wantedPin.noConnected === true) || expectedState === 'nc' && (wantedPin.noConnected !== true || knownNet(wantedPin)) || expectedState === 'unconnected' && (wantedPin.net !== '' || wantedPin.noConnected !== false)) { add('invalid_target_connection', 'fail', { component: key, pin: number }); continue; }
      if (expectedState !== state || expectedState === 'connected' && wantedPin.net !== pin.net) add('pin_connection_mismatch', 'fail', { component: key, pin: number, expected: { state: expectedState, net: wantedPin.net }, actual: { state, net: pin.net } });
    }
  }
  for (const part of actualParts) if (!used.has(part)) add('extra_component', 'fail', { component: stableKey(part) });
  const netNames = new Set<string>(), members = new Set<string>();
  for (const net of target.nets) {
    if (!record(net) || !string(net.name) || netNames.has(net.name) || !Array.isArray(net.members)) { add('invalid_target_net', 'fail'); continue; }
    netNames.add(net.name);
    if (net.scope && !['unknown', 'page', 'local'].includes(net.scope)) add('network_scope_unverified', 'unknown', { field: net.name, expected: net.scope, note: 'Page pin membership cannot prove cross-page network scope' });
    for (const member of net.members) {
      if (!record(member) || !string(member.componentKey) || !string(member.pinNumber)) { add('invalid_target_net_member', 'fail'); continue; }
      const pin = expectedPins.get(member.componentKey)?.get(member.pinNumber), pair = JSON.stringify([member.componentKey, member.pinNumber]);
      if (!pin || pin.state !== 'connected' || pin.net !== net.name || members.has(pair)) add('target_net_membership_mismatch', 'fail', { component: member.componentKey, pin: member.pinNumber, expected: net.name });
      members.add(pair);
    }
  }
  for (const [key, pins] of expectedPins) for (const [number, pin] of pins) if (pin.state === 'connected' && !members.has(JSON.stringify([key, number]))) add('target_net_member_missing', 'fail', { component: key, pin: number });
  return finish();
}

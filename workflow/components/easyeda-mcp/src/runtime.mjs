import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';

// Capture once when this process starts. Reading changed files on health would
// falsely claim the running module hot-loaded them.
export async function loadedBuild() {
  const files = ['core.mjs', 'server.mjs', 'save.mjs', 'runtime.mjs', 'execution.mjs'];
  const hash = createHash('sha256');
  for (const file of files) hash.update(file).update(await readFile(new URL(file, import.meta.url)));
  for (const file of ['workflow.py','execution_contract.py']) hash.update(file).update(await readFile(new URL('../../../skills/evidence-to-delivery/scripts/' + file, import.meta.url)));
  for (const file of ['schematic_layout.py','schematic_preflight.py']) hash.update(file).update(await readFile(new URL('../../../skills/easyeda-agent/scripts/' + file, import.meta.url)));
  const packageInfo = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'));
  return { version: packageInfo.version, build: hash.digest('hex'), capabilities: [
    'runtime.identity.v1', 'mutation.outcome.v1', 'execution.bound.v1', 'snapshot.local.v1', 'save.guarded.v1',
  ] };
}

export function runtimeGate(health, expected, route, mcp) {
  const daemon = health?.found?.raw;
  const windows = daemon?.windows?.filter(w => w.context?.projectUuid === route.project &&
    w.context?.documentUuid === route.doc && (!route.window || route.window === w.windowId)) ?? [];
  const issues = [];
  if (windows.length !== 1) issues.push('target_window_ambiguous_or_missing');
  if (!expected?.daemonVersion || expected.daemonVersion !== daemon?.version) issues.push('daemon_version_mismatch');
  if (!expected?.connectorVersion || expected.connectorVersion !== windows[0]?.connectorVersion) issues.push('connector_version_mismatch');
  if (!windows[0]?.capabilities?.includes('schematic.review.v2')) issues.push('connector_review_v2_missing');
  if (!expected?.mcpBuild || expected.mcpBuild !== mcp?.build) issues.push('mcp_build_mismatch');
  if (!Array.isArray(expected?.capabilities) || !expected.capabilities.length ||
      expected.capabilities.some(c => !mcp?.capabilities?.includes(c) && !windows[0]?.capabilities?.includes(c))) issues.push('capability_missing');
  return { passed: issues.length === 0, issues, mcp, daemonVersion: daemon?.version,
    window: windows.length === 1 ? windows[0] : null };
}

export function snapshotStamp(packet) {
  const hash = createHash('sha256').update(JSON.stringify(packet)).digest('hex');
  const complete = packet?.ok === true && typeof packet.runtimeInstance === 'string' &&
    Number.isSafeInteger(packet.mutationSeq) && Number.isSafeInteger(packet.seq) &&
    Number.isSafeInteger(packet.seqAbandoned) && packet.unordered !== true &&
    !!packet.context?.projectUuid && !!packet.context?.documentUuid;
  const text = packet.result?.textMeasurements?.coverage;
  const expected = Array.isArray(text?.expectedIds) ? text.expectedIds : null;
  const measured = Array.isArray(text?.measuredIds) ? new Set(text.measuredIds) : null;
  const missing = expected && measured ? expected.filter(id => !measured.has(id)) : null;
  return { snapshot_id: hash, project_id: packet.context?.projectUuid, document_id: packet.context?.documentUuid,
    page_id: packet.context?.documentUuid, runtime_instance: packet.runtimeInstance ?? null,
    mutation_seq: packet.mutationSeq ?? null, seq: packet.seq ?? null, seq_abandoned: packet.seqAbandoned ?? null,
    captured_at: packet.createdAt ?? null, source: 'easyeda-daemon/connector', hash,
    coverage: { primitives: !!packet.result?.pagePrimitives, pins: packet.result?.pinNetsAvailable === true,
      wires: packet.result?.wiresAvailable === true, text: expected && measured ? {
        expected_count: expected.length, measured_count: measured.size, missing_count: missing.length,
        missing_sample: missing.slice(0, 5),
      } : null },
    freshness_available: complete };
}

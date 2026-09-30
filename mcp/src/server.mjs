#!/usr/bin/env node

import { readFile } from 'node:fs/promises';
import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from '@modelcontextprotocol/sdk/types.js';
import {
  buildBlocksArgs,
  buildProjectTransferArgs,
  buildActionCallArgs,
  DOMAIN_NAMES,
  filterActions,
  runEasyeda,
  toMcpResult,
} from './core.mjs';
import { apiReference, callAction, control, daemonURL, imageResult, runtimeRequest } from './open.mjs';
import { captureSnapshot, planTool } from './plan-tools.mjs';

const catalogExecution = await runEasyeda(['actions'], 30_000);
if (!catalogExecution.ok || !Array.isArray(catalogExecution.result)) {
  process.stderr.write(`easyeda-agent-mcp: cannot load action catalog: ${JSON.stringify(catalogExecution)}\n`);
  process.exit(1);
}
const actions = catalogExecution.result.filter((action) => DOMAIN_NAMES.includes(action.domain));
const byName = new Map(actions.map((action) => [action.name, action]));

const server = new Server(
  { name: 'easyeda-agent-open', version: JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8')).version },
  {
    capabilities: { tools: {} },
    instructions: 'Control EasyEDA Pro using typed actions or arbitrary eda.* JavaScript. No phase approvals or typed-only policy. Use window from health; optional target UUIDs are checked immediately before execution. Batch independent operations to reduce round trips. Errors and unknown results are not success; inspect before retrying writes.',
  },
);

const commonRouteProperties = {
  project: {
    type: 'string',
    description: 'Existing EasyEDA project name or UUID. Optional legacy CLI navigation; omit when using window and target.',
  },
  doc: {
    type: 'string',
    description: 'Existing schematic page or PCB name/UUID. Optional legacy CLI navigation; omit when using window and target.',
  },
  window: {
    type: 'string',
    description: 'Connector windowId from easyeda_health. Use to bind the intended editor window.',
  },
  target: { type: 'object', properties: { projectUuid: { type: 'string' }, documentUuid: { type: 'string' } }, additionalProperties: false, description: 'Optional exact identity assertions, checked inside the Connector before execution.' },
  timeoutMs: { type: 'integer', minimum: 1000, maximum: 600000 },
  edit:{type:'object',properties:{mode:{type:'string',enum:['layout','design']},primitiveIds:{type:'array',items:{type:'string'},minItems:1,maxItems:500},expectedPins:{type:'object',additionalProperties:{type:['string','null']}},requiredChecks:{type:'array',items:{type:'string',enum:['drc']},uniqueItems:true},complete:{type:'boolean'}},additionalProperties:false,description:'Complete modification unit: affected component IDs and approved pin targets. Fixed checks run once after the unit. requiredChecks:drc requires a current DRC receipt at finish. complete:false defers checks and is unverified.'},
  payload: {
    type: 'object',
    description: 'Typed action payload. Use easyeda_actions to inspect the action inputs.',
    additionalProperties: true,
  },
};

function domainTool(domain) {
  const domainActions = actions.filter((action) => action.domain === domain);
  return {
    name: `easyeda_${domain}`,
    title: `EasyEDA ${domain}`,
    description: `Run one typed ${domain} action through easyeda-agent. Use easyeda_actions for input guidance.`,
    inputSchema: {
      type: 'object',
      properties: {
        action: {
          type: 'string',
          description: 'Exact typed action name.',
        },
        ...commonRouteProperties,
      },
      required: ['action'],
      additionalProperties: false,
    },
    annotations: {
      readOnlyHint: domainActions.every((action) => !action.mutates),
      destructiveHint: domainActions.some((action) => action.mutates),
      idempotentHint: false,
      openWorldHint: false,
    },
  };
}

const tools = [
  {
    name: 'easyeda_plan',
    description: 'Prepare a semantic drawing plan and readable Markdown from a full local snapshot, inspect its exact hash/source bindings, check the expected part/pin set, or apply explicit typed steps. Staged plans require stage: geometry reads only affected components; final independently checks the full target. Stage completion is not whole-page delivery. Uses the existing queue; no automatic layout, source approval, replay, or save.',
    inputSchema: { type: 'object', properties: {
      operation: { type: 'string', enum: ['prepare', 'render', 'inspect', 'check', 'apply'] },
      snapshotFile: { type: 'string', description: 'prepare: absolute path to a full identified schematic snapshot.' },
      outputFile: { type: 'string', description: 'prepare: absolute new plan.json path; derives plan.md beside it.' },
      planFile: { type: 'string', description: 'render/inspect/check/apply: absolute plan.json path. render refreshes derived Markdown from current JSON without altering the plan.' },
      expectedHash: { type: 'string', description: 'SHA256 of reviewed plan bytes; required for apply.' },
      stage: { type: 'string', minLength: 1, description: 'apply only: exact plan.stages id. Required when the plan has stages; executes only this stage. Compile later static steps from actual component IDs/readback.' },
      sources: { type: 'array', items: { type: 'object', properties: { id: { type: 'string' }, path: { type: 'string' }, sha256: { type: 'string' }, anchor: { type: 'string' } }, required: ['id', 'path'], additionalProperties: false } },
      goal: { type: 'string' }, layoutMode: { type: 'string', enum: ['preserve', 'relayout'] },
      overwrite: { type: 'boolean' }, requireConfirmed: { type: 'boolean', description: 'Optional caller-authored confirmation metadata check; not independent proof of human approval.' },
      window: commonRouteProperties.window, target: commonRouteProperties.target, timeoutMs: commonRouteProperties.timeoutMs,
      edit: commonRouteProperties.edit, dryRun: { type: 'boolean' },
    }, required: ['operation'], additionalProperties: false },
  },
  {name:'easyeda_runtime',description:'Read existing daemon observations without EDA calls; control the shared queue, resume confirmed paused batches, record a current-image visual review, finish a verified task and save, or persist caller-authored task intent with task_update, or replace it with task_replace after cancelling old queued work across clients (not execution evidence; remains paused). Unknown writes are never automatically replayed.',inputSchema:{type:'object',properties:{operation:{type:'string',enum:['state','pause','resume_queue','cancel','resume','retry','visual_review','finish','task_update','task_replace','restore']},window:commonRouteProperties.window,target:{type:'object',properties:{projectUuid:{type:'string'},documentUuid:{type:'string'}},required:['projectUuid','documentUuid'],additionalProperties:false},sourceWindow:{type:'string',description:'restore: historical source window chosen from state; target identity and current generation are required.'},expectedRevision:{type:'integer',minimum:0,description:'task_update/task_replace: current task revision, 0 if absent; rejects stale updates.'},task:{type:'object',properties:{goal:{type:'string',minLength:1,maxLength:2000},primitiveIds:{type:'array',items:{type:'string',minLength:1,maxLength:256},maxItems:500},remaining:{type:'array',items:{type:'string',minLength:1,maxLength:1000},maxItems:32},note:{type:'string',maxLength:4000}},required:['goal','primitiveIds','remaining'],additionalProperties:false},includeObjects:{type:'boolean',description:'State only: full cached objects (may be large). Default is a summary; primitiveIds returns a local projection.'},primitiveIds:{type:'array',items:{type:'string'},maxItems:500},requestId:{type:'string'},generation:{type:'integer',minimum:0},screenshotRequestId:{type:'string'},source:{type:'string',enum:['ai','engineer']},timeoutMs:commonRouteProperties.timeoutMs},additionalProperties:false}},
  { name: 'easyeda_control', description: 'Query runtime progress/receipts, cancel requests or supersede this MCP session’s old instruction in a window. Call supersede when the user changes instructions, before new writes. Native calls cannot be forcibly stopped; inFlight lists unresolved calls. No rollback.',
    inputSchema: { type: 'object', properties: { window: commonRouteProperties.window, target: commonRouteProperties.target, operation: { type: 'string', enum: ['status', 'cancel', 'supersede','pause','resume'] }, requestId: { type: 'string' }, allClients: { type: 'boolean', description: 'For status/cancel/pause/resume: inspect or control work from other/restarted MCP sessions too.' } }, required: ['window'], additionalProperties: false } },
  {
    name: 'easyeda_project_transfer',
    title: 'Open or export a native EasyEDA project',
    description: 'Project-level operations through fixed official-API adapters. Open can discard unsaved data: save all documents and acknowledge explicitly. Export requires the expected project already active; writes a new epro2 archive with ZIP integrity and hash, never overwrites. No document routing required.',
    inputSchema: {
      type: 'object',
      properties: {
        operation: { type: 'string', enum: ['open', 'export'] },
        window: { type: 'string', minLength: 1 },
        projectUuid: { type: 'string', minLength: 1 },
        pageUuid: { type: 'string', minLength: 1, description: 'For open only: wait for and open this schematic page, then verify both identities.' },
        allowDiscardUnsaved: { type: 'boolean', description: 'For open only: explicit acknowledgement after saving all documents.' },
        out: { type: 'string', description: 'For export only: new local .epro2 path, never overwritten.' },
      },
      required: ['operation', 'window', 'projectUuid'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: false },
  },
  {
    name: 'easyeda_health',
    title: 'EasyEDA connection health',
    description: 'Check the local daemon and connected EasyEDA Pro windows.',
    inputSchema: { type: 'object', properties: {}, additionalProperties: false },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'easyeda_actions',
    title: 'Discover EasyEDA actions',
    description: `Search the ${actions.length} typed EasyEDA actions and inspect inputs, mutation flags, with pagination.`,
    inputSchema: {
      type: 'object',
      properties: {
        domain: { type: 'string', enum: DOMAIN_NAMES },
        search: { type: 'string' },
        exact: { type: 'string' },
        limit: { type: 'integer', minimum: 1, maximum: 200 },
        offset: { type: 'integer', minimum: 0 },
        mutates: { type: 'boolean' },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  ...DOMAIN_NAMES.map(domainTool),
  {
    name: 'easyeda_blocks',
    title: 'EasyEDA circuit blocks',
    description: 'List, search, or show an embedded proven circuit block. Does not require a running daemon.',
    inputSchema: {
      type: 'object',
      properties: {
        operation: { type: 'string', enum: ['list', 'search', 'show'] },
        query: { type: 'string', description: 'Required for search.' },
        id: { type: 'string', description: 'Required for show.' },
      },
      required: ['operation'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'easyeda_execute',
    description: 'Run arbitrary async JavaScript with the official eda object. Return JSON or a Blob. No method allowlist or workflow gate. Runs once; timeout is not cancellation.',
    inputSchema: { type: 'object', properties: { window: commonRouteProperties.window, target: commonRouteProperties.target, timeoutMs: commonRouteProperties.timeoutMs, edit:commonRouteProperties.edit, code: { type: 'string', minLength: 1 } }, required: ['code'], additionalProperties: false },
  },
  {
    name: 'easyeda_batch',
    description: 'Execute ordered typed actions in one Connector request. Not atomic. edit declares a complete local modification unit for automatic scoped checks, with no intermediate DRC. Legacy verifyPreservedSchematic does a whole-page comparison; use one comparison path to avoid duplicate reads.',
    inputSchema: { type: 'object', properties: { window: commonRouteProperties.window, target: commonRouteProperties.target, timeoutMs: commonRouteProperties.timeoutMs, edit:commonRouteProperties.edit, steps: { type: 'array', minItems: 1, maxItems: 500, items: { type: 'object', properties: { action: { type: 'string' }, payload: { type: 'object', additionalProperties: true } }, required: ['action'], additionalProperties: false } }, dryRun: { type: 'boolean' }, stopOnError: { type: 'boolean' }, verifyPreservedSchematic: { type: 'boolean', description: 'Use only when this batch contains the complete layout edit and original part identities/pin nets must be preserved. Reads the active page before and after; unavailable evidence is unknown, not pass.' } }, required: ['steps'], additionalProperties: false },
  },
  {
    name: 'easyeda_screenshot',
    description: 'Return the current schematic/PCB canvas as an inline image. fit optionally frames all primitives. Data readback is needed to detect stale renders.',
    inputSchema: { type: 'object', properties: { window: commonRouteProperties.window, target: commonRouteProperties.target, timeoutMs: commonRouteProperties.timeoutMs, fit: { type: 'boolean' } }, additionalProperties: false },
  },
  {
    name: 'easyeda_snapshot',
    description: 'Collect schematic semantic data or PCB components/layers/nets in one request; optionally include PCB routing primitives. Does not infer correctness or run mandatory checks.',
    inputSchema: { type: 'object', properties: { window: commonRouteProperties.window, target: commonRouteProperties.target, timeoutMs: commonRouteProperties.timeoutMs, domain: { type: 'string', enum: ['schematic', 'pcb'] }, outputFile:{type:'string',description:'Absolute snapshot.json path: save full native observations plus Markdown locally and return a compact receipt. Default does not overwrite.'},overwrite:{type:'boolean'},baselineFile:{type:'string',description:'Optional fixed baseline path for a local patch reference; omitted objects are not deletions.'},cacheOnly:{type:'boolean'}, primitiveIds:{type:'array',items:{type:'string'},maxItems:500}, includeTexts:{type:'boolean'}, profile:{type:'string',enum:['full','geometry','electrical'],description:'Schematic only: geometry omits netlist and device hydration; electrical omits drawing geometry. Default full. Geometry cannot prove electrical correctness.'}, routing: { type: 'boolean' }, detail: { type: 'boolean', description: 'Schematic: include attributes, pins, bounding boxes, wires and active-page primitives using the rich reader.' }, allPages: { type: 'boolean' } }, required: ['domain'], additionalProperties: false },
    annotations: { readOnlyHint: true },
  },
  {
    name: 'easyeda_pcb_netlist_report',
    title: 'PCB pad-to-net report',
    description: 'Read-only report of PCB components, exact pad numbers and their assigned nets, plus the reverse net-to-pad index. This reports EDA net membership only; it does not prove copper continuity or run DRC.',
    inputSchema: {
      type: 'object',
      properties: {
        window: commonRouteProperties.window,
        target: commonRouteProperties.target,
        timeoutMs: commonRouteProperties.timeoutMs,
        designators: { type: 'array', items: { type: 'string', minLength: 1 }, maxItems: 500, description: 'Optional exact component designators to include.' },
        nets: { type: 'array', items: { type: 'string', minLength: 1 }, maxItems: 500, description: 'Optional exact net names to include.' },
      },
      additionalProperties: false,
    },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'easyeda_api',
    description: 'Search or browse the bundled official eda API index offline. Use query for search, namespace for method signatures, or neither for namespace listing.',
    inputSchema: { type: 'object', properties: { className: { type: 'string', description: 'Official API class such as DMT_EditorControl; returns bundled reference documentation.' }, method: { type: 'string', description: 'Optional exact method name within className.' }, query: { type: 'string' }, namespace: { type: 'string' }, limit: { type: 'integer', minimum: 1, maximum: 200 } }, additionalProperties: false },
    annotations: { readOnlyHint: true },
  },
];

server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools }));

server.setRequestHandler(CallToolRequestSchema, async (request, extra) => {
  const { name, arguments: args = {} } = request.params;
  const input = { ...args, signal: extra.signal };
  try {
    if (name === 'easyeda_plan') return await planTool(input, byName);
    if (name === 'easyeda_runtime') return runtimeRequest(input);
    if (name === 'easyeda_snapshot' && input.cacheOnly) {
      if (input.outputFile) throw new Error('Cached observations cannot be exported as a fresh snapshot');
      return runtimeRequest({...input,operation:'state'});
    }
    if (name === 'easyeda_control') {
      if (input.allClients && input.operation === 'supersede') throw new Error('Supersede is scoped to this MCP session; use cancel to stop other clients.');
      return control(input);
    }
    if (name === 'easyeda_project_transfer') {
      return toMcpResult(await runEasyeda(buildProjectTransferArgs(input), 90_000));
    }
    if (name === 'easyeda_health') {
      const response = await fetch(`${daemonURL}/health`, { signal: AbortSignal.timeout(5000) });
      const result = await response.json();
      return toMcpResult(response.ok ? { ok: true, result } : { ok: false, error: result });
    }
    if (name === 'easyeda_actions') {
      const filtered = input.exact ? actions.filter(a => a.name === input.exact) : filterActions(actions, input);
      const offset = Math.max(0, input.offset || 0), limit = Math.min(200, Math.max(1, input.limit || 30));
      return toMcpResult({ ok: true, result: { count: filtered.length, offset, actions: filtered.slice(offset, offset + limit) } });
    }
    if (name === 'easyeda_blocks') {
      return toMcpResult(await runEasyeda(buildBlocksArgs(input), 30_000));
    }
    if (name === 'easyeda_api') {
      if (input.className) return toMcpResult({ ok: true, result: await apiReference(input.className, input.method) });
      const args = input.namespace ? ['api', 'show', input.namespace, '--json'] : input.query
        ? ['api', 'search', input.query, '--json', '--limit', String(input.limit || 30)] : ['api', 'ls', '--json'];
      return toMcpResult(await runEasyeda(args, 30000));
    }
    if (name === 'easyeda_execute') {
      return callAction('debug.exec_js', { ...input, payload: { code: input.code } });
    }
    if (name === 'easyeda_snapshot') return await captureSnapshot(input);
    if (name === 'easyeda_batch') {
      const steps = input.steps;
      for (const step of steps || []) if (!byName.has(step.action)) throw new Error(`Unknown action: ${step.action}`);
      return callAction('debug.batch', { ...input, payload: { steps, dryRun: input.dryRun, stopOnError: input.stopOnError, verifyPreservedSchematic: input.verifyPreservedSchematic } });
    }
    if (name === 'easyeda_screenshot') {
      return imageResult(await callAction('view.capture', { ...input, payload: { fit: input.fit } }));
    }
    if (name === 'easyeda_pcb_netlist_report') {
      const action = byName.get('pcb.components.list');
      if (!action || action.domain !== 'pcb') throw new Error('The installed action catalog does not provide pcb.components.list.');
      for (const [key, values] of [['designators', input.designators], ['nets', input.nets]]) {
        if (values !== undefined && (!Array.isArray(values) || values.length > 500 || values.some(value => typeof value !== 'string' || !value.trim()))) {
          throw new Error(`${key} must contain at most 500 non-empty strings`);
        }
      }

      const source = await callAction('pcb.components.list', { ...input, payload: { includePads: true } });
      if (source.isError) return source;
      const receipt = source.structuredContent;
      const data = receipt?.result;
      if (receipt?.ok !== true || !Array.isArray(data?.components)) {
        return toMcpResult({ ok: false, error: { message: 'PCB component/pad read returned no usable result; no netlist report was produced.', response: receipt } });
      }
      if (receipt.context?.documentType && receipt.context.documentType !== 'pcb') {
        return toMcpResult({ ok: false, error: { message: `The active document is ${receipt.context.documentType}, not a PCB.`, context: receipt.context } });
      }

      const designators = input.designators ? new Set(input.designators.map(value => value.trim())) : null;
      const netFilter = input.nets ? new Set(input.nets.map(value => value.trim())) : null;
      const components = [];
      const missingPadData = [];
      const missingPadNumbers = [];
      const netMembers = new Map();
      let padCount = 0;
      let unassignedPadCount = 0;

      for (const component of data.components) {
        const designator = typeof component.designator === 'string' && component.designator.trim() ? component.designator.trim() : null;
        if (designators && !designators.has(designator)) continue;
        if (!Array.isArray(component.pads)) {
          missingPadData.push(designator ?? component.primitiveId ?? '(unknown component)');
          components.push({ designator, componentId: component.primitiveId ?? null, name: component.name ?? null, padDataAvailable: false, pads: null });
          continue;
        }

        const pads = [];
        for (const pad of component.pads) {
          const net = typeof pad.net === 'string' && pad.net.trim() ? pad.net.trim() : null;
          if (netFilter && !netFilter.has(net)) continue;
          const padNumber = typeof pad.padNumber === 'string' || typeof pad.padNumber === 'number'
            ? String(pad.padNumber).trim() || null : null;
          if (!padNumber) missingPadNumbers.push({ designator, padPrimitiveId: pad.primitiveId ?? null });
          const member = {
            designator,
            padNumber,
            padPrimitiveId: pad.primitiveId ?? null,
            net,
            x: Number.isFinite(pad.x) ? pad.x : null,
            y: Number.isFinite(pad.y) ? pad.y : null,
            layer: pad.layer ?? null,
          };
          pads.push(member);
          padCount += 1;
          if (net) {
            if (!netMembers.has(net)) netMembers.set(net, []);
            netMembers.get(net).push(member);
          } else {
            unassignedPadCount += 1;
          }
        }
        pads.sort((a, b) => String(a.padNumber ?? '').localeCompare(String(b.padNumber ?? ''), undefined, { numeric: true }));
        components.push({ designator, componentId: component.primitiveId ?? null, name: component.name ?? null, padDataAvailable: true, pads });
      }
      components.sort((a, b) => String(a.designator ?? '').localeCompare(String(b.designator ?? ''), undefined, { numeric: true }));

      const partial = missingPadData.length > 0 || missingPadNumbers.length > 0 || (Number.isInteger(data.count) && data.count !== data.components.length);
      return toMcpResult({ ok: true, result: {
        status: partial ? 'partial' : 'complete',
        partial,
        source: 'pcb.components.list(includePads:true)',
        context: receipt.context ?? null,
        filters: { designators: designators ? [...designators] : null, nets: netFilter ? [...netFilter] : null },
        componentCount: components.length,
        sourceComponentCount: Number.isInteger(data.count) ? data.count : data.components.length,
        padCount,
        netCount: netMembers.size,
        unassignedPadCount,
        missingPadData,
        missingPadNumbers,
        components,
        nets: [...netMembers.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([net, pads]) => ({ name: net, padCount: pads.length, pads })),
        interpretation: 'Net names are copied from each PCB pad record. A shared net name does not prove that tracks, vias or copper pours form a continuous physical connection; no DRC or routing verdict is included.',
      } });
    }
    if (name.startsWith('easyeda_')) {
      const domain = name.slice('easyeda_'.length);
      if (!DOMAIN_NAMES.includes(domain)) throw new Error(`unknown EasyEDA domain tool: ${name}`);
      const action = byName.get(input.action);
      if (!action || action.domain !== domain) {
        throw new Error(`action ${input.action || '(missing)'} does not belong to domain ${domain}`);
      }
      if (input.project || input.doc) {
        if (input.target || input.timeoutMs) throw new Error('Use window without project/doc navigation for target assertions or custom timeout.');
        return toMcpResult(await runEasyeda(buildActionCallArgs(action, input)));
      }
      return callAction(action.name, input);
    }
    throw new Error(`unknown tool: ${name}`);
  }
  catch (error) {
    return toMcpResult({ ok: false, error: { message: error.message } });
  }
});

await server.connect(new StdioServerTransport());
process.stdin.once('end', () => { void server.close().catch(error => { console.error(error); process.exitCode = 1; }); });

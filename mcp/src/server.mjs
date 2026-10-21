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
import { pcbConstraintDefinition, pcbConstraintTool } from './pcb-constraint-tools.mjs';
import { requirementsSchema, expressionSchema } from './project-files.mjs';
import { compileRoutes } from './route-tools.mjs';
import { compileLabels } from './label-tools.mjs';
import { knowledgeTool, knowledgeTools } from './knowledge-tools.mjs';

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
  edit:{type:'object',properties:{mode:{type:'string',enum:['layout','design']},primitiveIds:{type:'array',items:{type:'string'},minItems:1,maxItems:500},expectedIdentities:{type:'object',minProperties:1,additionalProperties:{type:'object',properties:{designator:{type:'string',minLength:1},uniqueId:{type:'string',minLength:1}},required:['designator'],additionalProperties:false}},expectedPins:{type:'object',additionalProperties:{anyOf:[{type:['string','null']},{type:'object',required:['members'],properties:{members:{type:'array',items:{type:'string',minLength:1},minItems:2,uniqueItems:true}},additionalProperties:false}]}},requiredChecks:{type:'array',items:{type:'string',enum:['drc']},uniqueItems:true},complete:{type:'boolean'}},additionalProperties:false,description:'Complete modification unit: affected component IDs and approved pin targets. expectedIdentities checks the existing scope before writing. Fixed checks run once after the unit. requiredChecks:drc requires a current DRC receipt at finish. complete:false defers checks and is unverified.'},
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
  pcbConstraintDefinition,
  ...knowledgeTools,
  {
    name: 'easyeda_plan',
    description: 'Prepare/render/inspect a bound schematic plan. prepare with optional planFile compiles existing caller-confirmed module declarations and a complete page snapshot into the fixed continuous stages and hashed job, without EDA calls or inferred design/approval. compile_routes and compile_labels use actual geometry/wire IDs. run executes a reviewed contiguous range through the same apply path, stopping on failure or unknown. apply binds supported requirements to native evidence, derives scoped checks and independent identity/pin targets, and runs declared DRC only at final delivery. Managed receipts enforce predecessor completion and prevent replay. reconcile reads receipts and the current target without modifying evidence; resume only skips a verified completed prefix and reuses the same remaining-stage executor. Pending/unknown/corrupt writes or any existing delivery receipt block resume. final checks the independent target; run never saves. verify_delivery only reads a confirmed saved delivery, native close/open receipts and fresh page/DRC; compares it with pre-save native evidence through existing Go design-diff and reports persistence/scope separately. It never closes, opens or saves the page. After actual image inspection, finish binds a hashed run to managed execution/image, calls existing visual_review/finish gates, writes raw delivery.json, then derives its summary separately. summary rebuilds only a local derived view with original fail/unknown and UTC timing; no EDA/knowledge calls or edits to originals. No automatic retry; saved:true is not reopen proof, and a summary is not new execution evidence.',
    inputSchema: { type: 'object', properties: {
      operation: { type: 'string', enum: ['prepare', 'render', 'inspect', 'check', 'apply', 'run', 'reconcile', 'resume', 'finish', 'verify_delivery', 'summary', 'bind', 'compile_routes', 'compile_labels'] },
      continuous: { type: 'boolean', description: 'run only, candidate continuous v0: confirmed five-stage place/measurement/routeFrom/labelsFrom/final module; derives stages and edits, binds actual placement IDs and the new plan hash, compiles routes/labels and returns current image. No automatic retry, design or visual acceptance. Do not also pass runStages.' },
      recheckFinal: { type:'boolean', const:true, description:'apply/run only: one explicit last empty final stage after its unchanged settled fail/unknown target check. Preserves original stage.json and writes one stage.recheck.json bound to its hash; never retries mutation stages or repeats an existing recheck.' },
      runResultFile: { type: 'string', description: 'finish only: absolute successful run result file; binds the managed plan, edit and image.' },
      closeRequestId:{type:'string',description:'verify_delivery only: actual current daemon document.close receipt after this save; does not execute close.'},
      openRequestId:{type:'string',description:'verify_delivery only: actual successful document.open/schematic.page.open receipt after close; current fresh readback must follow it.'},
      deliveryFile: { type: 'string', description: 'summary/verify_delivery: absolute settled delivery.json. Derives a local view from its hashed run/plan/stage/image evidence; never calls EDA, knowledge or changes originals.' },
      timingFile: { type: 'string', description: 'summary only: optional historical acceptance record with timing.dailySampleStart and matching delivery SHA/target. Used as a caller-recorded UTC anchor, not as native or user-message timing.' },
      expectedRunResultHash: { type: 'string', pattern: '^[a-f0-9]{64}$', description: 'finish only: SHA256 of the result bytes reviewed by the caller.' },
      visualAccepted: { type: 'boolean', const: true, description: 'finish only: explicitly true after actually inspecting the unchanged run image; then calls the existing visual/save gates and writes delivery.json.' },
      bindStages: { type: 'array', minItems: 1, items: { type: 'string' }, description: 'bind: explicitly named unexecuted empty geometry stages to receive native placed IDs.' },
      routeStages: { type: 'object', additionalProperties: { type: 'string' }, description: 'bind: route-stage name to its earlier measurement-stage name. Both must be in bindStages.' },
      runStages: { type: 'array', minItems: 1, maxItems: 32, items: { type: 'object', properties: { stage: { type: 'string', minLength: 1 }, edit: commonRouteProperties.edit }, required: ['stage'], additionalProperties: false }, description: 'run only: explicit contiguous forward stages with prepared typed steps or confirmed routeFrom declarations. Compiles declared direct routes from managed measurement receipts using the same apply guards; refuses existing receipts, stops on partial/unknown, never retries, saves or approves visual delivery.' },
      snapshotFile: { type: 'string', description: 'prepare: absolute path to a full identified schematic snapshot.' },
      outputFile: { type: 'string', description: 'prepare: absolute new plan.json plus Markdown; with planFile also produces .job.json and a new .execution receipt directory declaration. summary: absolute new derived JSON/Markdown. compile_routes/compile_labels: absolute new artifact JSON path.' },
      planFile: { type: 'string', description: 'prepare optional: existing easyeda.plan/1 design declarations only, with explicit semanticTarget components/nets, layoutHints.parts (key/x/y/rotation/rotationMode/mirror/subPartName), expression, sources and caller confirmation; requires full snapshotFile and window; a nonempty page also needs a confirmed empty layoutHints.region and complete preserved geometry/connectivity, builds fixed five stages/job without execution. Other operations: exact plan.json; render refreshes Markdown only.' },
      expectedHash: { type: 'string', description: 'SHA256 of reviewed plan bytes; required for apply/run.' },
      stage: { type: 'string', minLength: 1, description: 'apply or compile: exact plan.stages id. Compilers produce steps for this unexecuted stage; apply executes only the selected stage.' },
      geometryReceiptFile: { type: 'string', description: 'compile_routes: actual settled successful geometry execution receipt.' },
      wireReceiptFile: { type: 'string', description: 'compile_labels: actual settled successful wire execution receipt.' },
      wireReadbackFile: { type: 'string', description: 'compile_labels: alternative to wireReceiptFile; original fresh scoped components.list response with existing wires. Requires explicit wireIds.' },
      wireIds: { type: 'array', items: { type: 'string', minLength: 1 }, minItems: 1, maxItems: 500, uniqueItems: true, description: 'compile_labels with wireReadbackFile: only these observed existing wires may have Name visibility changed.' },
      netPolicies: { type: 'object', additionalProperties: { type: 'string', enum: ['direct', 'module_port', 'local_power', 'local_ground'] }, description: 'compile_routes: optional supplement to confirmed layoutHints.netPolicies; conflicting policies are rejected. Every connected net in the stage scope needs a policy; only direct is routed, and all its endpoints must be measured in scope.' },
      labels: { type: 'array', items: { type: 'object', properties: { wireId: { type: 'string' }, attributeId: { type: 'string' }, x: { type: 'number' }, y: { type: 'number' }, rotation: { type: 'number' } }, required: ['wireId'], additionalProperties: false }, description: 'compile_labels: explicit visible Name labels. May be omitted only when confirmed layoutHints.visibleNetNames is declared; one Name per observed same-net wire island is selected. Explicit [] hides names while preserving electrical values.' },
      sources: { type: 'array', items: { type: 'object', properties: { id: { type: 'string' }, path: { type: 'string' }, sha256: { type: 'string' }, anchor: { type: 'string' }, knowledge: { type: 'object', properties: { adoptedFor: { type: 'string', minLength: 1 }, conditions: { type: 'string', minLength: 1 }, componentKeys: { type: 'array', items: { type: 'string' } }, netNames: { type: 'array', items: { type: 'string' } }, checkMethod: { type: 'string', enum: ['native','checker','ai_review','manual_unknown'] } }, required: ['adoptedFor','conditions','componentKeys','netNames','checkMethod'], additionalProperties: false } }, required: ['id', 'path'], additionalProperties: false } },
      taskKind:{type:'string',enum:['local_edit'],description:'Snapshot prepare: scoped existing-page edit; derives mandatory source/local/final checks and maps native source IDs. Module declarations infer their task kind automatically.'},
      goal: { type: 'string' }, layoutMode: { type: 'string', enum: ['preserve', 'relayout'] },
      requirements: requirementsSchema, layoutHints: expressionSchema,
      overwrite: { type: 'boolean' }, requireConfirmed: { type: 'boolean', description: 'Optional caller-authored confirmation metadata check; not independent proof of human approval.' },
      window: commonRouteProperties.window, target: commonRouteProperties.target, timeoutMs: commonRouteProperties.timeoutMs,
      edit: commonRouteProperties.edit, dryRun: { type: 'boolean' },
      receiptFile: { type: 'string', description: 'apply staged non-dry-run only: absolute new JSON path for an execution receipt, created before EDA calls.' },
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
    if (name === 'easyeda_pcb_constraints') return await pcbConstraintTool(input);
    if (name.startsWith('knowledge_')) return await knowledgeTool(name, input);
    if (name === 'easyeda_plan') {
      if (input.operation === 'compile_routes') return toMcpResult({ ok: true, result: await compileRoutes(input) });
      if (input.operation === 'compile_labels') return toMcpResult({ ok: true, result: await compileLabels(input) });
      return await planTool(input, byName);
    }
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
      const { ensureDaemon, recoveryRevision } = await import('./daemon-lifecycle.mjs');
      await ensureDaemon();
      const response = await fetch(`${daemonURL}/health`, { signal: AbortSignal.timeout(5000) });
      const result = await response.json();
      result.connectionRecovery = recoveryRevision;
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

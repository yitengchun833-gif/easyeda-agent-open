#!/usr/bin/env node

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
  buildRawArgs,
  buildWorkflowArgs,
  DOMAIN_NAMES,
  discoverActions,
  runEasyeda,
  toMcpResult,
  localSchematicSnapshot,
} from './core.mjs';
import { createSerialQueue, runTask, taskTools } from './tasks.mjs';
import { guardedSchematicSave } from './save.mjs';
import { guardedSchematicAction } from './execution.mjs';
import { loadedBuild, runtimeGate } from './runtime.mjs';

import { isAbsolute, dirname } from 'node:path';
import { existsSync, statSync } from 'node:fs';

import { workflowChoiceTool, chooseWorkflow } from './jev.mjs';

const controlQueue = createSerialQueue();
const mcpBuild = await loadedBuild();

const catalogExecution = await runEasyeda(['actions'], 30_000);
if (!catalogExecution.ok || !Array.isArray(catalogExecution.result)) {
  process.stderr.write(`easyeda-agent-mcp: cannot load action catalog: ${JSON.stringify(catalogExecution)}\n`);
  process.exit(1);
}
const actions = catalogExecution.result.filter((action) => DOMAIN_NAMES.includes(action.domain));
const byName = new Map(actions.map((action) => [action.name, action]));

const server = new Server(
  { name: 'easyeda-agent-mcp', version: mcpBuild.version },
  {
    capabilities: { tools: {} },
    instructions: [
      'Control EasyEDA Pro through easyeda-agent.',
      'For project.create, provide an explicit window and payload.friendlyName, without project/doc routing. For other mutations, provide project and doc.',
      'Inspect before editing and run schematic/PCB checks plus native DRC after editing.',
      'Do not bypass workflow gates or use force-unsafe in real projects.',
    ].join(' '),
  },
);

const commonRouteProperties = {
  project: {
    type: 'string',
    description: 'Existing EasyEDA project name or UUID. Required for mutations except project.create; omit for project.create.',
  },
  doc: {
    type: 'string',
    description: 'Existing schematic page or PCB name/UUID. Required for mutations except project.create; omit for project.create.',
  },
  window: {
    type: 'string',
    description: 'Connector windowId from easyeda_health. Required for project.create; otherwise use to resolve multi-window ambiguity.',
  },
  payload: {
    type: 'object',
    description: 'Typed action payload. schematic.save requires verificationReport: absolute path to a before_save rule report. Other actions: use easyeda_actions for inputs.',
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
          enum: domainActions.map((action) => action.name),
          description: 'Exact typed action name.',
        },
        ...commonRouteProperties,
        ...(domain === 'schematic' ? {
          execution: { type: 'object', description: 'Required for schematic writes except save: absolute root/requirements/plan/baseline, stable action_id, or action_ids for a geometry-only layout group; recover=true reads and reconciles without replay.' },
          outputFile: { type: 'string', description: 'Absolute new local JSON path for schematic.components.list. Required with includePagePrimitives; model receives summary only.' },
          baselineFile: { type: 'string', description: 'Optional previous full snapshot path; returns bounded ID/field differences. Requires outputFile.' },
        } : {}),
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
  workflowChoiceTool,
  ...taskTools,
  {
    name: 'easyeda_raw',
    title: 'Run official eda.* JavaScript',
    description: 'Legacy raw interface; this workflow rejects arbitrary writes because they cannot satisfy its typed execution contract. Use a supported typed action.',
    inputSchema: {
      type: 'object',
      properties: {
        code: { type: 'string', minLength: 1, maxLength: 16000 },
        project: { type: 'string', minLength: 1 },
        doc: { type: 'string', minLength: 1 },
        window: { type: 'string' },
        allowArbitraryCode: { type: 'boolean', description: 'Explicitly acknowledge that this script can modify the document.' },
      },
      required: ['code', 'project', 'doc', 'allowArbitraryCode'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: false },
  },
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
    inputSchema: { type: 'object', properties: { expected: { type: 'object' }, project: {type:'string'}, doc: {type:'string'}, window: {type:'string'} }, additionalProperties: false },
    annotations: { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
  },
  {
    name: 'easyeda_actions',
    title: 'Discover EasyEDA actions',
    description: `Find existing capability before implementing it: search ${actions.length} installed typed actions (10 per page). A nonempty search with no typed match also searches the CLI's native eda.* API index. Catalog/API presence is not live verification; a missing match is not proof the platform lacks the feature.`,
    inputSchema: {
      type: 'object',
      properties: {
        domain: { type: 'string', enum: DOMAIN_NAMES },
        search: { type: 'string' },
        mutates: { type: 'boolean' },
        limit: { type: 'integer', minimum: 1, maximum: 50, default: 10 },
        offset: { type: 'integer', minimum: 0, default: 0 },
        includeApi: { type: 'boolean', default: false, description: 'Also search native API references even when typed actions match. API results do not inherit the action domain/mutation filter.' },
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
    name: 'easyeda_workflow',
    title: 'EasyEDA legacy checklist',
    description: 'Legacy CLI checklist only; does not authorize writes. Task state and execution contracts use workflow.py.',
    inputSchema: {
      type: 'object',
      properties: {
        operation: { type: 'string', enum: ['init', 'status', 'advance', 'confirm', 'reset'] },
        project: { type: 'string' },
        doc: { type: 'string' },
        reconcile: { type: 'boolean', description: 'For status: reconcile persisted state with the live document.' },
        minScore: { type: 'integer', minimum: 0, maximum: 100, description: 'For advance: minimum routability score.' },
        maxCrossings: { type: 'integer', minimum: -1, description: 'For advance: maximum ratline crossings; -1 is unlimited.' },
        confirmation: { type: 'string', enum: ['layout', 'outline'], description: 'Required for confirm.' },
        note: { type: 'string', description: 'Human review note recorded by confirm.' },
        resetAll: { type: 'boolean', description: 'For reset: clear every confirmation.' },
        resetFrom: { type: 'string', description: 'For reset: first stage to clear, inclusive.' },
      },
      required: ['operation', 'project'],
      additionalProperties: false,
    },
    annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: false },
  },
];

server.setRequestHandler(ListToolsRequestSchema, async () => ({ tools }));

server.setRequestHandler(CallToolRequestSchema, async (request, extra) => controlQueue(async () => {
  const { name, arguments: input = {} } = request.params;
  try {
    if (name === 'workflow_choose') {
      const report = await chooseWorkflow(input, { signal: extra.signal });
      return { content: [{ type: 'text', text: JSON.stringify(report) }], structuredContent: report };
    }
    if (taskTools.some(tool => tool.name === name)) {
      const report = await runTask(name, input, { signal: extra.signal });
      return {
        content: [{ type: 'text', text: JSON.stringify(report) }],
        structuredContent: report,
        isError: ['blocked', 'incomplete', 'timeout', 'cancelled'].includes(report.status),
      };
    }
    if (name === 'easyeda_raw') {
      throw new Error('Unverifiable raw writes cannot satisfy the execution contract. Use a typed action.');
    }
    if (name === 'easyeda_project_transfer') {
      return toMcpResult(await runEasyeda(buildProjectTransferArgs(input), 90_000, extra.signal), true);
    }
    if (name === 'easyeda_health') {
      const health = await runEasyeda(['daemon', 'health'], 30_000, extra.signal);
      if (health.ok) {
        health.result.mcp = mcpBuild;
        if (health.stderr) { health.result.warnings = health.stderr; delete health.stderr; }
        if (input.expected) health.result.runtimeGate = runtimeGate(health.result, input.expected, input, mcpBuild);
      }
      const result = toMcpResult(health);
      if (health.result?.runtimeGate?.passed === false) result.isError = true;
      return result;
    }
    if (name === 'easyeda_actions') {
      return toMcpResult({ ok: true, result: await discoverActions(actions, input) });
    }
    if (name === 'easyeda_blocks') {
      return toMcpResult(await runEasyeda(buildBlocksArgs(input), 30_000));
    }
    if (name === 'easyeda_workflow') {
      return toMcpResult(await runEasyeda(buildWorkflowArgs(input), undefined, extra.signal),
        input.operation !== 'status' || input.reconcile === true);
    }
    if (name.startsWith('easyeda_')) {
      const domain = name.slice('easyeda_'.length);
      if (!DOMAIN_NAMES.includes(domain)) throw new Error(`unknown EasyEDA domain tool: ${name}`);
      const action = byName.get(input.action);
      if (!action || action.domain !== domain) {
        throw new Error(`action ${input.action || '(missing)'} does not belong to domain ${domain}`);
      }
      if (input.outputFile !== undefined || input.baselineFile !== undefined ||
          (action.name === 'schematic.components.list' && input.payload?.includePagePrimitives === true)) {
        if (action.name !== 'schematic.components.list' || typeof input.outputFile !== 'string' || !input.outputFile.trim()) {
          throw new Error('Full schematic snapshots require outputFile; local output is only available for schematic.components.list');
        }
        if (!isAbsolute(input.outputFile) || !input.outputFile.toLowerCase().endsWith('.json') || existsSync(input.outputFile) ||
            !existsSync(dirname(input.outputFile)) || !statSync(dirname(input.outputFile)).isDirectory() ||
            (input.baselineFile !== undefined && (typeof input.baselineFile !== 'string' || !isAbsolute(input.baselineFile)))) {
          throw new Error('Snapshot paths invalid/existing; use a new absolute JSON outputFile in an existing directory and absolute baselineFile');
        }
        return await localSchematicSnapshot(await runEasyeda(buildActionCallArgs(action, input), undefined, extra.signal), input);
      }
      if (action.name === 'schematic.save') return toMcpResult(await guardedSchematicSave(input, { mcp: mcpBuild, signal: extra.signal }), true);
      if (domain === 'schematic' && action.mutates) return await guardedSchematicAction(action, input, { mcp: mcpBuild, signal: extra.signal });
      return toMcpResult(await runEasyeda(buildActionCallArgs(action, input), undefined, extra.signal), action.mutates);
    }
    throw new Error(`unknown tool: ${name}`);
  }
  catch (error) {
    return toMcpResult({ ok: false, error: { message: error.message } });
  }
}, extra.signal).catch(error => toMcpResult({ ok: false, error: { message: error.message } })));

await server.connect(new StdioServerTransport());

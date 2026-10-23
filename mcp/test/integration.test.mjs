import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';
import { knowledgeTools } from '../src/knowledge-tools.mjs';

const packageDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const serverPath = process.env.EASYEDA_MCP_SERVER || path.join(packageDir, 'src', 'server.mjs');

test('stdio MCP initializes, lists tools, and invokes offline discovery', async () => {
  assert.ok(process.env.EASYEDA_BIN, 'EASYEDA_BIN must point to the local CLI for integration tests');
  const calls = [];
  const mock = createServer(async (req, res) => {
    let text = ''; for await (const chunk of req) text += chunk;
    calls.push(JSON.parse(text));
    res.setHeader('content-type', 'application/json');
    if (calls.at(-1).operation === 'recovery_open_document' && calls.at(-1).acceptance.reason === 'mock unknown') {
      res.statusCode = 409;
      res.end(JSON.stringify({ ok: false, executionState: 'unknown', retryable: false }));
      return;
    }
    res.end(JSON.stringify({ ok: true, result: { value: 'mock-result' } }));
  }).listen(0, '127.0.0.1');
  await once(mock, 'listening');
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [serverPath],
    cwd: packageDir,
    env: { ...process.env, EASYEDA_BIN: process.env.EASYEDA_BIN, EASYEDA_DAEMON_URL: `http://127.0.0.1:${mock.address().port}` },
  });
  const client = new Client({ name: 'easyeda-agent-mcp-test', version: '1.0.0' });

  try {
    await client.connect(transport);
    const listed = await client.listTools();
    assert.equal(new Set(listed.tools.map(tool => tool.name)).size, listed.tools.length);
    assert.equal(listed.tools.filter(tool => tool.name.startsWith('knowledge_')).length, knowledgeTools.length);
    assert.ok(listed.tools.some(tool => tool.name === 'easyeda_plan'));
    const planSchema = listed.tools.find(tool => tool.name === 'easyeda_plan').inputSchema;
    for (const operation of ['compile_routes', 'compile_labels']) {
      assert.ok(planSchema.properties.operation.enum.includes(operation));
      const rejected = await client.callTool({ name: 'easyeda_plan', arguments: { operation } });
      assert.equal(rejected.isError, true);
    }
    assert.equal(calls.length, 0, 'offline compilers reject incomplete inputs without contacting EDA');
    assert.ok(listed.tools.find(tool => tool.name === 'easyeda_runtime').inputSchema.properties.operation.enum.includes('task_replace'));
    const runtimeSchema = listed.tools.find(tool => tool.name === 'easyeda_runtime').inputSchema;
    assert.ok(runtimeSchema.properties.operation.enum.includes('task_accept_current_baseline'));
    assert.ok(runtimeSchema.properties.operation.enum.includes('recovery_open_document'));
    assert.equal(runtimeSchema.properties.acceptance.properties.expectedBaselineSha256.type, 'string');
    assert.equal(runtimeSchema.properties.acceptance.additionalProperties, false);
    assert.match(listed.tools.find(tool => tool.name === 'easyeda_runtime').description, /view-changing native navigation/);
    assert.match(runtimeSchema.properties.generation.description, /source-page/);
    assert.match(runtimeSchema.properties.expectedRevision.description, /source-page/);
    assert.deepEqual(listed.tools.find(tool=>tool.name==='easyeda_snapshot').inputSchema.properties.profile.enum, ['full','geometry','electrical']);
    assert.ok(listed.tools.some(tool=>tool.name==='easyeda_runtime'));
    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_pcb'));
    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_debug'));
    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_library'));
    assert.ok(!listed.tools.some((tool) => tool.name === 'easyeda_workflow'));

    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_project_transfer'));
    const rejectedOpen = await client.callTool({ name: 'easyeda_project_transfer', arguments: { operation: 'open', window: 'w', projectUuid: 'p' } });
    assert.equal(rejectedOpen.isError, true);
    assert.match(rejectedOpen.content[0].text, /acknowledge/);

    const allActions = await client.callTool({
      name: 'easyeda_actions',
      arguments: {},
    });
    assert.equal(allActions.isError, false);
    assert.ok(allActions.structuredContent.actions.some((action) => action.domain === 'debug'));
    assert.equal(allActions.structuredContent.actions.length, 30);
    for (const name of ['project.open', 'project.export']) assert.ok(allActions.structuredContent.actions.some(action => action.name === name));

    const discovered = await client.callTool({
      name: 'easyeda_actions',
      arguments: { domain: 'schematic', search: 'check', mutates: false },
    });
    assert.equal(discovered.isError, false);
    assert.ok(Array.isArray(discovered.structuredContent.actions));
    assert.ok(discovered.structuredContent.actions.some((action) => action.name === 'schematic.check'));

    const pinRoute = await client.callTool({ name: 'easyeda_actions', arguments: { exact: 'schematic.wire.from_pin' } });
    assert.equal(pinRoute.isError, false);
    assert.ok(pinRoute.structuredContent.actions.some(action => action.name === 'schematic.wire.from_pin'));

    for (const [name, args] of [
      ['easyeda_schematic', { action: 'schematic.page.create', window: 'w', payload: { name: 'Mock' } }],
      ['easyeda_execute', { window: 'w', code: 'return await eda.anyMethod();' }],
      ['easyeda_batch', { window: 'w', steps: [{ action: 'pcb.components.list' }], verifyPreservedSchematic: true }],
    ]) {
      const outcome = await client.callTool({ name, arguments: args });
      assert.equal(outcome.isError, false);
    }
    assert.equal(calls.length, 3, 'exactly one daemon request for each tool call');
    assert.equal(calls[2].payload.verifyPreservedSchematic, true);
    const api = await client.callTool({ name: 'easyeda_api', arguments: { className: 'DMT_EditorControl', method: 'getCurrentRenderedAreaImage' } });
    assert.equal(api.isError, false);

    const blocks = await client.callTool({
      name: 'easyeda_blocks',
      arguments: { operation: 'search', query: 'led' },
    });
    assert.equal(blocks.isError, false);

    const navigation = {
      operation: 'recovery_open_document', window: 'current-window',
      target: { projectUuid: 'p', documentUuid: 'destination-D' }, generation: 7, expectedRevision: 3,
      acceptance: {
        acceptCurrentState: true, requestIds: ['old-request'], connectionEpoch: 'current-epoch',
        expectedBaselineSha256: 'a'.repeat(64), reason: 'user accepted source A and ended old instance',
        attempts: [{ requestId: 'old-request', revision: 2, sha256: 'b'.repeat(64), scopeKey: 'p/source-A', windowId: 'old-window' }],
        unsavedScopes: [{ key: 'p/source-A', revision: 2, connectionEpoch: 'old-epoch' }],
        nativeEndEvidence: { type: 'manual_session_termination_attestation', attestedBy: 'user', oldWindow: 'old-window', statement: 'old native instance ended',
          terminal: [{ requestId: 'old-request', statusRequestId: 'terminal-status', sha256: 'c'.repeat(64), initialStatusRequestId: 'initial-status', initialSha256: 'd'.repeat(64) }] },
      },
    };
    const beforeNavigation = calls.length;
    const navigated = await client.callTool({ name: 'easyeda_runtime', arguments: navigation });
    assert.equal(navigated.isError, false);
    assert.equal(calls.length, beforeNavigation + 1, 'one runtime dispatch, with no client-side task acceptance or replay');
    const { signal, ...dispatchedNavigation } = calls.at(-1);
    assert.deepEqual(dispatchedNavigation, navigation, 'destination and source evidence pass unchanged through the existing runtime transport');
    const unknown = await client.callTool({ name: 'easyeda_runtime', arguments: { ...navigation, acceptance: { ...navigation.acceptance, reason: 'mock unknown' } } });
    assert.equal(unknown.isError, true);
    assert.equal(calls.length, beforeNavigation + 2, 'unknown navigation is never retried');
    assert.match(unknown.content[0].text, /unknown/);
  }
  finally {
    await client.close();
    mock.closeAllConnections(); mock.close();
  }
});

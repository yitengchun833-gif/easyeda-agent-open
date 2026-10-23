import test from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js';

const packageDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const serverPath = process.env.EASYEDA_MCP_SERVER || path.join(packageDir, 'src', 'server.mjs');

test('stdio MCP initializes, lists tools, and invokes offline discovery', async () => {
  assert.ok(process.env.EASYEDA_BIN, 'EASYEDA_BIN must point to the local CLI for integration tests');
  const transport = new StdioClientTransport({
    command: process.execPath,
    args: [serverPath],
    cwd: packageDir,
    env: { ...process.env, EASYEDA_BIN: process.env.EASYEDA_BIN },
  });
  const client = new Client({ name: 'easyeda-agent-mcp-test', version: '1.0.0' });

  try {
    await client.connect(transport);
    const listed = await client.listTools();
    assert.equal(listed.tools.length, 16);
    for (const name of ['easyeda_inspect', 'easyeda_resolve', 'workflow_choose']) {
      assert.equal(listed.tools.find(tool => tool.name === name)?.annotations.readOnlyHint, true);
    }
    const invalidChoice = await client.callTool({ name: 'workflow_choose', arguments: {} });
    assert.equal(invalidChoice.structuredContent.fallback, 'invalid_input');
    const invalidTask = await client.callTool({ name: 'easyeda_resolve', arguments: { goal: 'U1', filters: { arbitraryCode: 'do not run' } } });
    assert.equal(invalidTask.isError, true);
    assert.equal(invalidTask.structuredContent.error.code, 'INVALID_INPUT');
    assert.ok(listed.tools.find(t=>t.name==='easyeda_schematic').inputSchema.properties.outputFile);
    const unlocalized=await client.callTool({name:'easyeda_schematic',arguments:{action:'schematic.components.list',payload:{includePagePrimitives:true}}});
    assert.equal(unlocalized.isError,true);assert.match(unlocalized.content[0].text,/outputFile/);
    const missingDirectory=await client.callTool({name:'easyeda_schematic',arguments:{action:'schematic.components.list',
      outputFile:path.join(packageDir,'absent-output-directory','snapshot.json'),payload:{includePagePrimitives:true}}});
    assert.equal(missingDirectory.isError,true);assert.match(missingDirectory.content[0].text,/existing directory/);
    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_pcb'));
    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_raw'));
    assert.ok(!listed.tools.some((tool) => tool.name === 'easyeda_debug'));
    const unreviewedSave = await client.callTool({ name:'easyeda_schematic', arguments:{ action:'schematic.save',project:'fixture',doc:'fixture',payload:{} } });
    assert.equal(unreviewedSave.isError, true);
    assert.match(unreviewedSave.content[0].text, /verificationReport/);

    assert.ok(listed.tools.some((tool) => tool.name === 'easyeda_project_transfer'));
    const rejectedOpen = await client.callTool({ name: 'easyeda_project_transfer', arguments: { operation: 'open', window: 'w', projectUuid: 'p' } });
    assert.equal(rejectedOpen.isError, true);
    assert.match(rejectedOpen.content[0].text, /acknowledge/);

    const allActions = await client.callTool({
      name: 'easyeda_actions',
      arguments: {},
    });
    assert.equal(allActions.isError, false);
    assert.ok(!allActions.structuredContent.actions.some((action) => action.domain === 'debug'));
    for (const name of ['project.open', 'project.export']) assert.ok(allActions.structuredContent.actions.some(action => action.name === name));

    const discovered = await client.callTool({
      name: 'easyeda_actions',
      arguments: { domain: 'schematic', search: 'check', mutates: false },
    });
    assert.equal(discovered.isError, false);
    assert.ok(Array.isArray(discovered.structuredContent.actions));
    assert.ok(discovered.structuredContent.actions.some((action) => action.name === 'schematic.check'));

    // Real installed CLI indexes, fresh MCP process; no editor or daemon required.
    const reuse = await client.callTool({name:'easyeda_actions',arguments:{search:'schematic.components.list'}});
    assert.equal(reuse.structuredContent.status,'typed_actions_found');
    assert.equal(reuse.structuredContent.apiStatus,'not_requested');
    assert.equal(reuse.structuredContent.liveVerified,false);
    const copy = await client.callTool({name:'easyeda_actions',arguments:{search:'copySchematicPage'}});
    assert.equal(copy.structuredContent.status,'native_api_reference_found');
    assert.ok(copy.structuredContent.apiReferences.some(x=>x.method==='copySchematicPage'));
    assert.equal(copy.structuredContent.designMutations,0);

    const rejectedMutation = await client.callTool({
      name: 'easyeda_schematic',
      arguments: { action: 'schematic.page.create', payload: { name: 'Unsafe' } },
    });
    assert.equal(rejectedMutation.isError, true);
    assert.match(rejectedMutation.content[0].text, /requires both project and doc/);

    for (const routing of [{}, { window: 'home-window', doc: 'tab_page1' }, { window: 'home-window', project: 'Future project' }]) {
      const rejectedCreate = await client.callTool({
        name: 'easyeda_project',
        arguments: { action: 'project.create', payload: { friendlyName: 'Must not be created' }, ...routing },
      });
      assert.equal(rejectedCreate.isError, true);
      assert.match(rejectedCreate.content[0].text, /requires an explicit window|does not accept project or doc/);
    }

    const blocks = await client.callTool({
      name: 'easyeda_blocks',
      arguments: { operation: 'search', query: 'led' },
    });
    assert.equal(blocks.isError, false);
  }
  finally {
    await client.close();
  }
});

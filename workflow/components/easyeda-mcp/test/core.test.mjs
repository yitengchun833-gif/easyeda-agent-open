import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildBlocksArgs,
  buildProjectTransferArgs,
  buildCallArgs,
  buildActionCallArgs,
  buildRawArgs,
  buildWorkflowArgs,
  filterActions,
  discoverActions,
  parseOutput,
  runEasyeda,
  toMcpResult,
} from '../src/core.mjs';

test('buildCallArgs pins project/doc and keeps payload structured', () => {
  assert.deepEqual(
    buildCallArgs('pcb.drc.run', {
      project: 'Motor',
      doc: 'PCB1',
      window: 'win-1',
      payload: { rebuild: true },
    }),
    ['--project', 'Motor', '--doc', 'PCB1', 'call', 'pcb.drc.run', '--payload', '{"rebuild":true}', '--window', 'win-1'],
  );
});

test('drawing reads default to native identity and no text metrology; explicit requests survive', () => {
  const payload = input => { const args = buildCallArgs('schematic.components.list', input);
    return JSON.parse(args[args.indexOf('--payload') + 1]); };
  assert.deepEqual(payload({payload:{includePagePrimitives:true}}),
    {includeDeviceIdentity:false,measureTextIds:[],includePagePrimitives:true});
  assert.deepEqual(payload({payload:{includeDeviceIdentity:true,includePagePrimitives:true,measureTextIds:['label']}}),
    {includeDeviceIdentity:true,measureTextIds:['label'],includePagePrimitives:true});
});

test('filterActions applies domain, mutation, and text filters', () => {
  const actions = [
    { name: 'pcb.drc.run', domain: 'pcb', mutates: false, description: 'native check', inputs: [] },
    { name: 'pcb.track.create', domain: 'pcb', mutates: true, description: 'route copper', inputs: ['net'] },
    { name: 'schematic.check', domain: 'schematic', mutates: false, description: 'lint', inputs: [] },
  ];
  assert.deepEqual(filterActions(actions, { domain: 'pcb', mutates: true, search: 'copper' }), [actions[1]]);
});

test('workflow and blocks arguments are shell-free arrays', () => {
  assert.deepEqual(buildWorkflowArgs({ project: 'P', doc: 'PCB1', operation: 'status', reconcile: true }),
    ['--project', 'P', '--doc', 'PCB1', 'workflow', 'status', '--json', '--reconcile']);
  assert.deepEqual(buildWorkflowArgs({ project: 'P', operation: 'confirm', confirmation: 'layout', note: 'reviewed' }),
    ['--project', 'P', 'workflow', 'confirm', 'layout', '--note', 'reviewed']);
  assert.throws(() => buildWorkflowArgs({ project: 'P', operation: 'reset' }), /reset requires/);
  assert.deepEqual(buildBlocksArgs({ operation: 'search', query: 'usb serial' }), ['blocks', 'search', 'usb serial']);
});

test('parseOutput and MCP result preserve structured JSON', () => {
  assert.deepEqual(parseOutput('{"ok":true}'), { ok: true });
  assert.equal(parseOutput('plain'), 'plain');
  const result = toMcpResult({ ok: true, result: { passed: true } });
  assert.deepEqual(result.structuredContent, { passed: true });
  assert.equal(result.isError, false);

  const warned = toMcpResult({ ok: true, result: { passed: true }, stderr: 'staleRisk' });
  assert.deepEqual(warned.structuredContent, { result: { passed: true }, warnings: 'staleRisk' });
});

test('failed writes retain partial output and expose the readback policy', () => {
  const error = { code: 'ETIMEDOUT', message: 'fixture timeout', stdout: { created: ['A'] } };
  const failure = toMcpResult({ ok: false, error }, true);
  assert.equal(failure.isError, true);
  assert.deepEqual(failure.structuredContent.stdout, { created: ['A'] });
  assert.equal(failure.structuredContent.recovery.readbackRequired, true);
  assert.equal(failure.structuredContent.recovery.retryPolicy, 'readback_before_deciding');
  assert.deepEqual(error, { code: 'ETIMEDOUT', message: 'fixture timeout', stdout: { created: ['A'] } });
  const readFailure = toMcpResult({ ok: false, error });
  assert.equal(JSON.parse(readFailure.content[0].text).recovery, undefined);
});

test('cancelled local CLI stops and returns an uncertain-write recovery policy', async () => {
  const original = process.env.EASYEDA_BIN;
  process.env.EASYEDA_BIN = process.execPath;
  try {
    const outcome = await runEasyeda(['-e', 'setInterval(() => {}, 1000)'], 2000, AbortSignal.timeout(25));
    assert.equal(outcome.ok, false);
    assert.equal(outcome.error.code, 'ABORT_ERR');
    assert.equal(toMcpResult(outcome, true).structuredContent.recovery.readbackRequired, true);
  } finally {
    if (original === undefined) delete process.env.EASYEDA_BIN;
    else process.env.EASYEDA_BIN = original;
  }
});

test('partial-success mutations return recovery evidence while full success stays compatible', () => {
  const partial = toMcpResult({ ok: true, result: { partial: true, created: 'A', opened: false }, stderr: 'fixture warning' }, true);
  assert.equal(partial.structuredContent.result.created, 'A');
  assert.equal(partial.structuredContent.warnings, 'fixture warning');
  assert.equal(partial.structuredContent.recovery.readbackRequired, true);
  assert.equal(partial.isError, true);
  assert.equal(toMcpResult({ ok: true, result: { created: 'A' } }, true).structuredContent.outcome.effective_state, 'UNKNOWN');
});

test('transport success cannot hide action failure, nested partial, or an empty mutation receipt', () => {
  for (const value of [{verified:false},{writeState:'unknown'},{notApplied:['a']},{deleted:false}]) {
    const result = toMcpResult({ok:true,result:{ok:true,result:value}}, true);
    assert.equal(result.isError,true);
    assert.equal(result.structuredContent.recovery.readbackRequired,true);
  }
  const recovered = toMcpResult({ok:false,error:{message:'timeout'},reconciliation:{verified:true,action_id:'a',snapshot_id:'s'}}, true);
  assert.equal(recovered.isError,false);
  assert.deepEqual(recovered.structuredContent.outcome,{transport_status:'error',apply_state:'applied',verification_state:'verified',effective_state:'APPLIED_VERIFIED'});
  const failed = toMcpResult({ ok: true, result: { ok: false, error: { code: 'EDA_CALL_FAILED' } } }, true);
  assert.equal(failed.isError, true);
  assert.equal(failed.structuredContent.recovery.outcome, 'unknown');
  const nested = toMcpResult({ ok: true, result: { ok: true, result: { partial: true, created: ['A'] } } }, true);
  assert.equal(nested.isError, true);
  assert.equal(nested.structuredContent.recovery.outcome, 'partial');
  assert.deepEqual(nested.structuredContent.result.created, ['A']);
  const empty = toMcpResult({ ok: true, result: null }, true);
  assert.equal(empty.isError, true);
  assert.equal(empty.structuredContent.recovery.outcome, 'unknown');
  assert.equal(toMcpResult({ ok: true, result: null }).isError, false);
  assert.equal(toMcpResult({ ok: true, result: { ok: false } }).isError, true);
  // A completed check which finds design issues is not an execution failure.
  assert.equal(toMcpResult({ ok: true, result: { passed: false, issues: ['overlap'] } }).isError, false);
});

test('project creation routes to a window without an existing project or document', () => {
  const payload = { friendlyName: 'New project', open: true, teamUuid: 'team-1' };
  assert.deepEqual(
    buildActionCallArgs({ name: 'project.create', mutates: true }, { window: 'win-home', payload }),
    ['call', 'project.create', '--payload', JSON.stringify(payload), '--window', 'win-home'],
  );
});

test('project creation rejects ambiguous or contradictory routing', () => {
  const action = { name: 'project.create', mutates: true };
  for (const window of [undefined, '', '   ']) {
    assert.throws(() => buildActionCallArgs(action, { window }), /requires an explicit window/);
  }
  for (const routing of [{ project: 'Not created yet' }, { doc: 'tab_page1' }]) {
    assert.throws(() => buildActionCallArgs(action, { window: 'win-home', ...routing }), /does not accept project or doc/);
  }
});

test('other mutations still require both existing targets; reads preserve routing', () => {
  for (const name of ['schematic.page.create', 'board.create', 'schematic.component.place', 'pcb.track.create']) {
    const action = { name, mutates: true };
    for (const route of [{}, { project: 'P' }, { doc: 'D' }, { window: 'W' }]) {
      assert.throws(() => buildActionCallArgs(action, route), /requires both project and doc/);
    }
    assert.deepEqual(buildActionCallArgs(action, { project: 'P', doc: 'D' }),
      ['--project', 'P', '--doc', 'D', 'call', name]);
  }
  assert.deepEqual(buildActionCallArgs({ name: 'project.current', mutates: false }, { window: 'W' }),
    ['call', 'project.current', '--window', 'W']);
});

test('discovery reuses typed reads without API lookup; paginates existing inventory', async () => {
  const actions = Array.from({length: 21}, (_,i) => ({name:`schematic.read${i}`,domain:'schematic',mutates:false,description:'read pins and netlist'}));
  const run = () => { throw new Error('unnecessary native lookup'); };
  const first = await discoverActions(actions, {search:'netlist',mutates:false}, run);
  assert.equal(first.status, 'typed_actions_found');
  assert.equal(first.count, 21); assert.equal(first.returned,10); assert.equal(first.nextOffset,10);
  assert.equal(first.liveVerified,false); assert.equal(first.designMutations,0);
  const last = await discoverActions(actions, {offset:20}, run);
  assert.equal(last.returned,1); assert.equal(last.nextOffset,null);
  const exact = await discoverActions([
    {name:'schematic.components.list',domain:'schematic',mutates:false,description:'read'},
    {name:'schematic.component.place',domain:'schematic',mutates:true,description:'Use schematic.components.list to read back'},
  ], {search:'schematic.components.list'}, run);
  assert.equal(exact.count,1); assert.equal(exact.actions[0].mutates,false);
});

test('copy API discovery does not mistake missing action for missing platform capability', async () => {
  const calls=[];
  const native={ns:'eda.dmt_Schematic',method:'copySchematicPage',stability:'beta'};
  const report=await discoverActions([], {search:'copySchematicPage'}, async args => {
    calls.push(args); return {ok:true,result:[native]};
  });
  assert.deepEqual(calls,[['api','search','copySchematicPage','--json','--limit','10']]);
  assert.equal(report.status,'native_api_reference_found');
  assert.equal(report.liveVerified,false); assert.deepEqual(report.apiReferences,[native]);
  assert.match(report.apiScope,/cross-project/);
});

test('failed native discovery is unknown and never retries or writes', async () => {
  let calls=0;
  const report=await discoverActions([], {search:'missing'}, async args=> {
    calls++; assert.equal(args[0],'api'); return {ok:false,error:{message:'timeout'}};
  });
  assert.equal(calls,1); assert.equal(report.apiStatus,'unavailable');
  assert.equal(report.liveVerified,false); assert.equal(report.designMutations,0);
  for(const input of [{limit:0},{offset:-1},{includeApi:'yes'},{search:{}},{run:'write'},{domain:'invalid'}]) {
    await assert.rejects(discoverActions([],input),/Invalid/);
  }
});

test('raw API calls require deliberate use and a pinned document', () => {
  const input = { code: 'return 1 + 1', project: 'P', doc: 'D', allowArbitraryCode: true };
  assert.deepEqual(buildRawArgs(input), ['--project', 'P', '--doc', 'D', 'call', 'debug.exec_js', '--payload', '{"code":"return 1 + 1"}']);
  for (const bad of [{ ...input, allowArbitraryCode: false }, { ...input, doc: ' ' }, { ...input, code: '' }]) {
    assert.throws(() => buildRawArgs(bad));
  }
});

test('project transfer preserves project identity and requires explicit discard acknowledgement', () => {
  const input = { operation: 'open', window: 'w', projectUuid: 'p' };
  assert.throws(() => buildProjectTransferArgs(input), /acknowledge/);
  assert.deepEqual(buildProjectTransferArgs({ ...input, allowDiscardUnsaved: true }), ['project','open','--window','w','--project-uuid','p','--allow-discard-unsaved']);
  assert.deepEqual(buildProjectTransferArgs({ ...input, operation:'export', out:'test project.epro2' }), ['project','export','--window','w','--project-uuid','p','--out','test project.epro2']);
  assert.throws(() => buildProjectTransferArgs({ ...input, operation:'export', out:'wrong.zip' }), /epro2/);
  assert.throws(() => buildProjectTransferArgs({ ...input, project:'wrong', allowDiscardUnsaved:true }), /routing/);
  assert.throws(() => buildProjectTransferArgs({ ...input, window:' ', allowDiscardUnsaved:true }), /window/);
});

test('catalogued project transfers enforce window, identity and acknowledgement', () => {
  for (const name of ['project.open', 'project.export']) {
    const action = { name, mutates: name === 'project.open' };
    const input = { window: 'w', payload: { projectUuid: 'p', allowDiscardUnsaved: true } };
    assert.ok(buildActionCallArgs(action, input).includes(name));
    for (const bad of [{}, { ...input, window: '' }, { ...input, project: 'old' }, { ...input, doc: 'old' }, { window: 'w', payload: {} }]) assert.throws(() => buildActionCallArgs(action, bad));
  }
  assert.throws(() => buildActionCallArgs({ name: 'project.open', mutates: true }, { window: 'w', payload: { projectUuid: 'p', allowDiscardUnsaved: 'true' } }));
});


test('controlled writes need reconciliation and accepted saves still need reopen', () => {
  const write = {ok:true,requiresReconciliation:true,result:{ok:true,result:{verified:true}}};
  assert.equal(toMcpResult(write,true).structuredContent.outcome.effective_state,'UNKNOWN');
  write.reconciliation={action_id:'a',snapshot_id:'s',effective_state:'UNKNOWN'};
  assert.equal(toMcpResult(write,true).isError,true);
  const save=toMcpResult({ok:true,result:{ok:true,result:{saved:true,workflowSave:{passed:true}}}},true);
  assert.equal(save.isError,false);
  assert.equal(save.structuredContent.outcome.verification_state,'pending_reopen');
  assert.equal(save.structuredContent.recovery.readbackRequired,true);
});

test('local snapshots retain complete evidence and return bounded differences without raw attributes', async () => {
  const { localSchematicSnapshot } = await import('../src/core.mjs');
  const { mkdtemp, readFile, writeFile, rm } = await import('node:fs/promises');
  const { tmpdir } = await import('node:os');
  const { join } = await import('node:path');
  const dir = await mkdtemp(join(tmpdir(), 'runeda-snapshot-'));
  try {
    const packet = {ok:true,context:{projectUuid:'p',documentUuid:'d'},result:{
      components:[],wires:[],pagePrimitives:{attributes:Array.from({length:2000},(_,i)=>({primitiveId:String(i),Value:'RAW_SENTINEL'.repeat(15),X:i}))}}};
    packet.result.textMeasurements = {texts:[],errors:[],coverage:{
      expectedIds: packet.result.pagePrimitives.attributes.map(a=>a.primitiveId), measuredIds:[]}};
    const first = join(dir,'before.json'), next = join(dir,'after.json');
    const r = await localSchematicSnapshot({ok:true,result:packet},{outputFile:first});
    assert.deepEqual(JSON.parse(await readFile(first,'utf8')),packet);
    assert.equal(r.structuredContent.counts.attributes,2000);
    assert.equal(r.structuredContent.counts.unknownVisibility,2000);
    assert.equal(r.structuredContent.snapshot.coverage.text.missing_count,2000);
    assert.equal(r.structuredContent.snapshot.coverage.text.missing_sample.length,5);
    assert.ok(!JSON.stringify(r).includes('RAW_SENTINEL'));
    assert.ok(JSON.stringify(r).length<3000);
    const visible = structuredClone(packet);
    visible.result.pagePrimitives.attributes[0].ValueVisible = true;
    const observed = await localSchematicSnapshot({ok:true,result:visible},{outputFile:join(dir,'one-visible.json')});
    assert.equal(observed.structuredContent.counts.unknownVisibility,1999);
    await assert.rejects(localSchematicSnapshot({ok:true,result:packet},{outputFile:first}),/EEXIST/);
    packet.result.pagePrimitives.attributes[0].X=42;
    const diff = await localSchematicSnapshot({ok:true,result:packet},{outputFile:next,baselineFile:first});
    assert.deepEqual(diff.structuredContent.diff.items,[{kind:'attributes',id:'0',change:'modified',fields:['X']}]);
    packet.context.documentUuid='wrong';
    const wrong = await localSchematicSnapshot({ok:true,result:packet},{outputFile:join(dir,'wrong.json'),baselineFile:first});
    assert.equal(wrong.isError,true);
    assert.equal(wrong.structuredContent.status,'snapshot_recorded');
    assert.equal(wrong.structuredContent.diff.status,'unavailable');
    assert.match(wrong.structuredContent.diff.message,/target mismatch/);
    assert.deepEqual(JSON.parse(await readFile(wrong.structuredContent.evidence.path,'utf8')),packet);
    const broken = join(dir,'broken.json');
    await writeFile(broken,'{broken');
    for (const [name, baselineFile] of [['broken',broken],['missing',join(dir,'absent.json')]]) {
      const retained = await localSchematicSnapshot({ok:true,result:packet},{outputFile:join(dir,name+'-read.json'),baselineFile});
      assert.equal(retained.isError,true);
      assert.equal(retained.structuredContent.diff.status,'unavailable');
      assert.equal(retained.structuredContent.diff.retryPolicy,'reuse_recorded_snapshot');
      assert.deepEqual(JSON.parse(await readFile(retained.structuredContent.evidence.path,'utf8')),packet);
      assert.ok(!JSON.stringify(retained).includes('RAW_SENTINEL'));
    }
    const failed=await localSchematicSnapshot({ok:false,error:{message:'read timeout',stdout:{huge:'RAW_SENTINEL'}}},{outputFile:join(dir,'failed.json')});
    const incomplete = await localSchematicSnapshot({ok:true,result:{ok:true,result:null}},{outputFile:join(dir,'empty.json')});
    assert.equal(incomplete.isError,true);
    packet.context.documentUuid='d';packet.result.partial=true;
    const partial=await localSchematicSnapshot({ok:true,result:packet},{outputFile:join(dir,'partial.json'),baselineFile:first});
    assert.equal(partial.isError,true);
    assert.equal(partial.structuredContent.diff.status,'unavailable');
    assert.equal(failed.isError,true);
    assert.ok(!JSON.stringify(failed).includes('RAW_SENTINEL'));
  } finally { await rm(dir,{recursive:true,force:true}); }
});

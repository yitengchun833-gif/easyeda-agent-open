/// <reference types="@jlceda/pro-api-types" />
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { comparePreservedSchematic, planOutwardPinWire, runAction } from './actions';

test('completed schematic comparison distinguishes intact, changed and unknown nets', () => {
  const part = { primitiveId: 'u1', componentType: 'part', designator: 'U1', uniqueId: 'gge1', name: 'IC',
    footprint: 'FP', supplierId: 'C1', pinsAvailable: true, pins: [{ number: '1', net: 'VCC' }] };
  const before = { components: [part], partial: false };
  assert.equal(comparePreservedSchematic(before, { components: [{ ...part, x: 100 }], partial: false }).status, 'pass');
  assert.equal(comparePreservedSchematic(before, { components: [{ ...part, pins: [{ number: '1', net: 'GND' }] }], partial: false }).status, 'fail');
  assert.equal(comparePreservedSchematic(before, { components: [{ ...part, pins: [{ number: '1', net: null }] }], partial: true }).status, 'unknown');
  assert.equal(comparePreservedSchematic({ components: [part, { ...part, primitiveId: 'u2', uniqueId: 'gge2' }], partial: false }, before).status, 'unknown');
});

test('measured pin routes start outward, turn orthogonally and reject backward targets', () => {
  assert.deepEqual(planOutwardPinWire(0, 0, 0, 30, 20), [0, 0, 30, 0, 30, 20]);
  assert.deepEqual(planOutwardPinWire(0, 0, 90, 20, 0), [0, 0, 0, 10, 20, 10, 20, 0]);
  assert.deepEqual(planOutwardPinWire(0, 0, 180, -20, 0), [0, 0, -20, 0]);
  assert.throws(() => planOutwardPinWire(0, 0, 0, -10, 20), /behind/);
  assert.throws(() => planOutwardPinWire(0, 0, 45, 10, 10), /cardinal/);
});

test('wire.from_pin reads the actual pin and honors dryRun before creating', async t => {
  const globals = globalThis as any, previous = globals.eda;
  t.after(() => { globals.eda = previous; });
  let created = 0;
  globals.eda = {
    sch_PrimitiveComponent: {
      get: async () => ({ getState_PrimitiveId: () => 'c1' }),
      getAll: async () => [{ getState_PrimitiveId: () => 'c1' }],
      getAllPinsByPrimitiveId: async () => [{
        getState_PinNumber: () => '2', getState_NoConnected: () => false,
        getState_X: () => 10, getState_Y: () => 20, getState_Rotation: () => 180,
      }],
    },
    sch_PrimitiveWire: { create: async (points: number[]) => {
      created++;
      return { getState_PrimitiveId: () => 'w1', getState_Net: () => 'SIG', getState_Line: () => points };
    } },
  };
  const payload = { primitiveId: 'c1', pinNumber: '2', targetX: -20, targetY: 20 };
  const preview: any = await runAction('schematic.wire.from_pin', { ...payload, dryRun: true }, false);
  assert.deepEqual(preview.result.points, [10, 20, -20, 20]);
  assert.equal(created, 0);
  const result: any = await runAction('schematic.wire.from_pin', payload, false);
  assert.equal(created, 1);
  assert.equal(result.result.primitiveId, 'w1');
});

test('open execution, batch ordering, target assertions and error evidence', async () => {
  let writes = 0, contextReads = 0;
  (globalThis as any).eda = {
    mutate: async () => ++writes,
    dmt_Project: { getCurrentProjectInfo: async () => { contextReads++; return { uuid: 'p' }; } },
    dmt_SelectControl: { getCurrentDocumentInfo: async () => ({ uuid: 'd' }) },
  };
  const step = { action: 'debug.exec_js', payload: { code: 'return await eda.mutate();' } };
  const result: any = await runAction('debug.batch', { steps: [step, step, step], _target: { documentUuid: 'd' } });
  assert.equal(writes, 3);
  assert.equal(result.result.ok, true);
  assert.deepEqual(result.result.results.map((r: any) => r.result.value), [1, 2, 3]);
  assert.equal(contextReads, 2, 'one target assertion and one outer readback, not per-step metadata');
  await assert.rejects(runAction('debug.exec_js', { ...step.payload, _target: { documentUuid: 'wrong' } }), /target|identity|documentUuid/i);
  assert.equal(writes, 3);
  await assert.rejects(runAction('debug.batch', { steps: [step, { action: 'missing' }] }));
  assert.equal(writes, 3, 'invalid action rejected before any effect');
  await runAction('debug.batch', { steps: [step], dryRun: true });
  assert.equal(writes, 3);
  const failed: any = await runAction('debug.batch', { steps: [step, { action: 'debug.exec_js', payload: { code: 'throw new Error("fail")' } }, step] });
  assert.equal(writes, 4);
  assert.equal(failed.result.partial, true);
  assert.equal(failed.result.completed, 2);
  assert.equal(failed.result.results[1].error.code, 'EDA_CALL_FAILED');
  assert.match(failed.result.results[1].error.detail, /fail/);
  const continued: any = await runAction('debug.batch', { stopOnError: false, steps: [{ action: 'debug.exec_js', payload: { code: 'throw "failure"' } }, step] });
  assert.equal(writes, 5);
  assert.equal(continued.result.ok, false);
  const image: any = await runAction('debug.exec_js', { code: 'return new Blob(["pixels"], {type:"image/png"})' });
  assert.equal(image.result.value.base64, Buffer.from('pixels').toString('base64'));
});

test('batch cooperatively stops remaining steps and exposes actual progress', async t => {
 const globals = globalThis as any, old = globals.eda;
 t.after(() => { globals.eda = old; });
 const abort = new AbortController(); let writes = 0; let progress: any;
 globals.eda = { mutate: async () => { writes++; abort.abort(); return writes; } };
 const result: any = await runAction('debug.batch', { steps: [1, 2, 3].map(() => ({ action: 'debug.exec_js', payload: { code: 'return await eda.mutate();' } })) }, false,
  { signal: abort.signal, progress: value => { progress = value; } });
 assert.equal(writes, 1); assert.equal(result.result.cancelled, true); assert.equal(result.result.completed, 1);
 assert.equal(progress.completed, 1); assert.equal(progress.outcomes[0].ok, true);
 await assert.rejects(runAction('debug.exec_js', { code: 'return await eda.mutate();' }, false, { signal: abort.signal, progress: () => {} }));
 assert.equal(writes, 1);
});

test('view.capture waits across a repaint with and without fit before reading the image',async t=>{
 const globals=globalThis as any,oldEda=globals.eda,oldRaf=globals.requestAnimationFrame;
 t.after(()=>{globals.eda=oldEda;globals.requestAnimationFrame=oldRaf;});
 for(const fit of [false,true]) {
  const frames:Array<(time:number)=>void>=[];let captures=0,zooms=0;
  globals.requestAnimationFrame=(callback:(time:number)=>void)=>{frames.push(callback);return frames.length};
  globals.eda={dmt_EditorControl:{zoomToAllPrimitives:async()=>{zooms++},getCurrentRenderedAreaImage:async()=>{captures++;return new Blob(['current pixels'],{type:'image/png'})}}};
  const pending=runAction('view.capture',{fit},false);
  await Promise.resolve();await Promise.resolve();
  assert.equal(captures,0);assert.equal(frames.length,1);assert.equal(zooms,Number(fit));
  frames.shift()!(0);
  assert.equal(captures,0,'first frame must schedule the second paint boundary');
  frames.shift()!(16);
  const result:any=await pending;
  assert.equal(captures,1);assert.equal(result.result.value.base64,Buffer.from('current pixels').toString('base64'));
 }
});

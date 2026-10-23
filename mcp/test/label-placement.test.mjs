import test from 'node:test';
import assert from 'node:assert/strict';
import { placeWireLabels } from '../src/label-placement.mjs';

const wire = (primitiveId, a, b) => ({ primitiveId, points: [[a, b]] });
const group = (net, wires) => ({ net, wires });
const segments = groups => groups.flatMap(group => group.wires.flatMap(wire => wire.points));

test('automatic Name placement uses the long horizontal run instead of the first short stub', () => {
  const groups = [group('VIN', [wire('stub', [0, 0], [0, 10]), wire('long', [0, 0], [200, 0])])];
  const [label] = placeWireLabels(groups, segments(groups));
  assert.equal(label.wireId, 'long');
  assert.equal(label.rotation, 0);
  assert.ok(label.x > 0 && label.x < 200 && label.y !== 0);
});

test('automatic Names choose the clear side of a measured body and stay within the incremental region', () => {
  const groups = [group('VIN', [wire('w', [0, 0], [200, 0])])];
  const body = { componentType: 'part', bbox: { minX: 0, minY: 1, maxX: 200, maxY: 50 } };
  const region = { minX: -30, minY: -30, maxX: 230, maxY: 60 };
  const [label] = placeWireLabels(groups, segments(groups), [body], region);
  assert.ok(label.y < 0, JSON.stringify(label));
  const [bounded] = placeWireLabels(groups, segments(groups), [], { ...region, minY: -1 });
  assert.ok(bounded.y > 0);
  assert.deepEqual(placeWireLabels(groups, segments(groups), [
    { componentType: 'part', bbox: { minX: -100, minY: -100, maxX: 300, maxY: 100 } },
  ]), [{ wireId: 'w' }]);
});

test('nearby visible islands reserve separate Name positions in stable net order', () => {
  const groups = [group('B', [wire('b', [0, 0], [200, 0])]), group('A', [wire('a', [0, 5], [200, 5])])];
  const labels = placeWireLabels(groups, segments(groups));
  assert.equal(labels.length, 2);
  assert.ok(Math.abs(labels[0].y - labels[1].y) >= 14);
  assert.deepEqual(labels, placeWireLabels(groups.toReversed(), segments(groups)));
});

test('vertical-only wires get a vertical Name; no in-region candidate preserves native placement', () => {
  const groups = [group('VIN', [wire('w', [0, 0], [0, 200])])];
  const [label] = placeWireLabels(groups, segments(groups));
  assert.equal(label.rotation, 90);
  assert.ok(label.x !== 0 && label.y > 0 && label.y < 200);
  assert.deepEqual(placeWireLabels(groups, segments(groups), [], { minX: -1, minY: 0, maxX: 1, maxY: 200 }), [{ wireId: 'w' }]);
});

test('diagonal crossings and boundary contacts block the only in-region Name candidate', () => {
  const groups = [group('VIN', [wire('w', [0, 0], [200, 0])])];
  const region = { minX: 78, minY: 2, maxX: 122, maxY: 18 };
  for (const obstruction of [
    [[90, 0], [110, 20]],
    [[110, 20], [90, 0]],
    [[79, 17], [60, 36]],
    [[70, 8], [88, 26]],
    [[79, 3], [121, 3]],
  ]) {
    assert.deepEqual(placeWireLabels(groups, [...segments(groups), obstruction], [], region),
      [{ wireId: 'w' }], JSON.stringify(obstruction));
  }
});

test('an overlapping diagonal bounding box alone does not block a clear Name candidate', () => {
  const groups = [group('VIN', [wire('w', [0, 0], [200, 0])])];
  const region = { minX: 78, minY: 2, maxX: 122, maxY: 18 };
  assert.deepEqual(placeWireLabels(groups, [...segments(groups), [[70, 10], [90, 30]]], [], region),
    [{ wireId: 'w', x: 100, y: 10, rotation: 0 }]);
});

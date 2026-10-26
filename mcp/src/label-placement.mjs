const finiteBox = box => box && [box.minX, box.minY, box.maxX, box.maxY].every(Number.isFinite)
  && box.minX < box.maxX && box.minY < box.maxY;
const overlaps = (a, b) => a.minX < b.maxX && a.maxX > b.minX && a.minY < b.maxY && a.maxY > b.minY;
const inside = (box, region) => box.minX > region.minX && box.maxX < region.maxX
  && box.minY > region.minY && box.maxY < region.maxY;
function meets(box, [a, b]) {
  let enter = 0, exit = 1;
  for (const [axis, low, high] of [[0, box.minX, box.maxX], [1, box.minY, box.maxY]]) {
    const delta = b[axis] - a[axis];
    if (delta === 0) {
      if (a[axis] < low || a[axis] > high) return false;
      continue;
    }
    const first = (low - a[axis]) / delta, last = (high - a[axis]) / delta;
    enter = Math.max(enter, Math.min(first, last));
    exit = Math.min(exit, Math.max(first, last));
    if (enter > exit) return false;
  }
  return true;
}

// Native Name font/alignment metrics are not in wire receipts. These conservative
// display reservations rank candidates, never certify text collision freedom.
export function placeWireLabels(groups, segments, components = [], region) {
  const bodies = components.filter(part => part.componentType !== 'sheet' && finiteBox(part.bbox)).map(part => part.bbox);
  const occupied = [], labels = [];
  const groupKey = group => group.wires.map(wire => wire.primitiveId).sort()[0];
  for (const group of [...groups].sort((a, b) => a.net.localeCompare(b.net)
    || groupKey(a).localeCompare(groupKey(b)))) {
    const halfWidth = Math.max(15, [...group.net].length * 7), halfHeight = 7;
    const candidates = [];
    for (const wire of [...group.wires].sort((a, b) => a.primitiveId.localeCompare(b.primitiveId))) {
      for (const [a, b] of wire.points) {
        const horizontal = a[1] === b[1], vertical = a[0] === b[0];
        if (!horizontal && !vertical) continue;
        const axis = horizontal ? 0 : 1, lo = Math.min(a[axis], b[axis]), hi = Math.max(a[axis], b[axis]);
        const length = hi - lo;
        if (!length) continue;
        const margin = Math.min(halfWidth + 5, length / 2);
        for (const along of new Set([(lo + hi) / 2, lo + margin, hi - margin])) {
          // Coordinates use EasyEDA's y-up space. Try above/right before below/left.
          for (const offset of [10, -10]) {
            const x = horizontal ? along : a[0] + offset, y = horizontal ? a[1] + offset : along;
            const box = { minX: x - (horizontal ? halfWidth : halfHeight), maxX: x + (horizontal ? halfWidth : halfHeight),
              minY: y - (horizontal ? halfHeight : halfWidth), maxY: y + (horizontal ? halfHeight : halfWidth) };
            if (region && !inside(box, region)) continue;
            const collisions = bodies.filter(body => overlaps(box, body)).length
              + occupied.filter(other => overlaps(box, other)).length
              + segments.filter(segment => meets(box, segment)).length;
            candidates.push({ label: { wireId: wire.primitiveId, x, y, rotation: horizontal ? 0 : 90 }, box,
              collisions, vertical: horizontal ? 0 : 1, length });
          }
        }
      }
    }
    candidates.sort((a, b) => a.collisions - b.collisions || a.vertical - b.vertical || b.length - a.length);
    const best = candidates.find(candidate => candidate.collisions === 0);
    // Preserve native placement instead of moving into a known obstruction or
    // outside the authorized region when no clear candidate was found.
    labels.push(best?.label ?? { wireId: group.wires[0].primitiveId });
    if (best) occupied.push(best.box);
  }
  return labels;
}

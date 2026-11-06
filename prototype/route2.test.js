const test = require('node:test');
const assert = require('node:assert');
const { nearestOrder, sortStops, mockTravelMin, haversineKm } = require('./route.js');

const at = (lng, lat = 0) => ({ lat, lng });
const pathKm = (start, pts) => pts.reduce((a, p, i) => a + haversineKm(i ? pts[i - 1] : start, p), 0);

test('nearestOrder: greedy from the start', () => {
  assert.deepStrictEqual(nearestOrder(at(0), [at(3), at(1), at(2)]), [1, 2, 0]);
  assert.deepStrictEqual(nearestOrder(at(0), []), []);
});

test('sortStops: shorter than both the current order and plain nearest-neighbour', () => {
  // greedy goes 1 → 1.5? no: classic trap — nearest-first walks away from the far point and must come back
  const pts = [at(0.1), at(-1), at(0.2), at(2), at(-1.1)];
  const current = pts.map((p, i) => i);
  const best = sortStops(at(0), pts);
  const nn = nearestOrder(at(0), pts);
  assert.deepStrictEqual([...best].sort(), current);
  assert.ok(pathKm(at(0), best.map((i) => pts[i])) <= pathKm(at(0), pts) + 1e-9);
  assert.ok(pathKm(at(0), best.map((i) => pts[i])) <= pathKm(at(0), nn.map((i) => pts[i])) + 1e-9);
});

test('sortStops: calendar items keep their slots; never longer than now', () => {
  const pts = [at(3), at(1), at(5, 1), at(2)];
  const fixed = [false, false, true, false];
  const order = sortStops(at(0), pts, fixed);
  assert.strictEqual(order[2], 2);
  assert.ok(pathKm(at(0), order.map((i) => pts[i])) <= pathKm(at(0), pts) + 1e-9);
  assert.deepStrictEqual(sortStops(at(0), [at(1)], [true]), [0]);
  assert.deepStrictEqual(sortStops(at(0), []), []);
});

test('mockTravelMin by mode: walking > transit > driving; unknown = driving; floor 3', () => {
  const a = { lat: 26.2, lng: 127.68 }, b = { lat: 26.29, lng: 127.68 }; // ~10 km
  const [d, t, w] = ['driving', 'transit', 'walking'].map((m) => mockTravelMin(a, b, m));
  assert.ok(w > t && t > d, `${d} ${t} ${w}`);
  assert.strictEqual(mockTravelMin(a, b, 'boat'), d);
  assert.strictEqual(mockTravelMin(a, b), d);
  assert.strictEqual(mockTravelMin(a, a, 'walking'), 3);
});

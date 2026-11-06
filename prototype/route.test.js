const test = require('node:test');
const assert = require('node:assert');
const { planRoute } = require('./route.js');

const fixed = (min) => () => min; // travel time stub
const all = (...w) => Object.fromEntries([0, 1, 2, 3, 4, 5, 6].map((d) => [d, w]));
const run = (stop, startMin, weekday = 6) =>
  planRoute({ weekday, startMin, start: {}, stops: [{ id: 's', stayMin: 60, ...stop }], travelFn: fixed(30) }).legs[0];

test('arrive before open → waits until open', () => {
  const l = run({ hours: all([540, 1080]) }, 480); // arrive 8:30, open 9:00
  assert.deepStrictEqual([l.arrive, l.wait, l.availMin, l.depart, l.warn], [510, 30, 540, 600, null]);
});

test('arrive near close → SHORT, stay clipped', () => {
  const l = run({ hours: all([540, 1080]) }, 1020); // arrive 17:30, close 18:00
  assert.deepStrictEqual([l.availMin, l.stayMin, l.warn], [30, 30, 'SHORT']);
});

test('closed weekday → CLOSED, no stay', () => {
  const l = run({ hours: { ...all([540, 1080]), 6: [] } }, 600);
  assert.deepStrictEqual([l.stayMin, l.warn, l.depart], [0, 'CLOSED', 630]);
});

test('arrive after close → CLOSED', () => {
  assert.strictEqual(run({ hours: all([540, 1080]) }, 1080).warn, 'CLOSED');
});

test('arrive during break time → waits for next window', () => {
  const l = run({ hours: all([660, 900], [1020, 1380]) }, 900); // arrive 15:30
  assert.deepStrictEqual([l.open, l.wait, l.availMin], [1020, 90, 360]);
});

test('unknown hours → stay as requested, NO_HOURS', () => {
  const l = run({ hours: null }, 600);
  assert.deepStrictEqual([l.stayMin, l.warn, l.depart], [60, 'NO_HOURS', 690]);
});

test('multi-stop chains departure times and totals travel', () => {
  const r = planRoute({
    weekday: 1, startMin: 540, start: {}, travelFn: fixed(20),
    stops: [{ id: 'a', stayMin: 60, hours: null }, { id: 'b', stayMin: 30, hours: null }],
  });
  assert.deepStrictEqual([r.legs[1].arrive, r.endMin, r.travelTotal], [640, 670, 40]);
});

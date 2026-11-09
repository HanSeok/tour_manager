const test = require('node:test');
const assert = require('node:assert');
const { planRoute } = require('./route.js');
const Slots = require('./slots.js');

const MON = 1, SAT = 6;
const all = (...w) => Object.fromEntries([0, 1, 2, 3, 4, 5, 6].map((d) => [d, w]));
const place = (id, hours, lat = 26.2, lng = 127.7) => ({ id, name: id, lat, lng, hours });
const soba = place('soba', { ...all([630, 960]), [MON]: [] });          // 10:30-16:00, closed Mondays
const steak = place('steak', all([660, 900], [1020, 1380]));           // 11-15, 17-23 (break time)
const cafe = place('cafe', null);                                       // hours unknown
const bar = place('bar', all([1080, 1440]));                            // opens 18:00
const start = place('hotel', null);
const flat = (n) => () => n;
const run = (stops, o = {}) => planRoute({ weekday: MON, startMin: 700, start, stops, travelFn: flat(20), candTravelFn: flat(20), ...o });
const slot = (cands, extra = {}) => ({ cands, pickId: null, excluded: [], stayMin: 60, label: '점심', ...extra });

test('unconfirmed slot picks the candidate that is open at arrival (Monday: soba closed)', () => {
  const [leg] = run([slot([soba, steak, cafe])]).legs; // arrive 12:00
  assert.strictEqual(leg.id, 'steak');
  assert.strictEqual(leg.slot.tentative, true);
  assert.deepStrictEqual(leg.slot.candidates.map((c) => [c.id, c.tier, c.warn]), [['soba', 3, 'CLOSED'], ['steak', 0, null], ['cafe', 1, 'NO_HOURS']]);
  assert.deepStrictEqual([leg.arrive, leg.stayMin, leg.warn], [720, 60, null]);
});

test('open beats unknown hours beats too-short beats closed; ties keep list order', () => {
  assert.strictEqual(run([slot([cafe, steak])]).legs[0].id, 'steak');                // open > unknown
  const closesSoon = place('soon', all([600, 740]));                                  // arrive 12:00, 20 min left, wants 60
  assert.strictEqual(run([slot([soba, closesSoon])]).legs[0].id, 'soon');            // short > closed
  assert.strictEqual(run([slot([closesSoon, cafe])]).legs[0].id, 'cafe');            // unknown > short
  const a = place('a', all([0, 1440])), b = place('b', all([0, 1440]));
  assert.strictEqual(run([slot([a, b])]).legs[0].id, 'a');                            // tie → first
  assert.strictEqual(run([slot([b, a])]).legs[0].id, 'b');
});

test('a confirmed pick wins even when it looks closed (a person decided); the warning still shows', () => {
  const [leg] = run([slot([steak, soba], { pickId: 'soba' })]).legs;
  assert.strictEqual(leg.id, 'soba');
  assert.strictEqual(leg.slot.tentative, false);
  assert.strictEqual(leg.warn, 'CLOSED');
});

test('excluded candidates are skipped; a pick that was excluded no longer counts', () => {
  const r1 = run([slot([steak, cafe], { excluded: ['steak'] })]).legs[0];
  assert.strictEqual(r1.id, 'cafe');
  assert.deepStrictEqual(r1.slot.excluded, ['steak']);
  const r2 = run([slot([steak, cafe], { pickId: 'steak', excluded: ['steak'] })]).legs[0];
  assert.deepStrictEqual([r2.id, r2.slot.tentative], ['cafe', true]);
});

test('a slot with no candidates is a rest block: takes time, no travel, position unchanged', () => {
  const seen = [];
  const travelFn = (a, b) => { seen.push([a.id, b.id]); return 20; };
  const r = run([slot([], { label: '카페·휴식', stayMin: 45 }), steak], { travelFn, startMin: 600 });
  const [rest, next] = r.legs;
  assert.deepStrictEqual([rest.id, rest.travelMin, rest.arrive, rest.depart, rest.warn], [null, 0, 600, 645, null]);
  assert.deepStrictEqual(seen, [['hotel', 'steak']]); // next leg travels from where we were, not from the rest block
  assert.strictEqual(next.arrive, 665);
  assert.strictEqual(r.travelTotal, 20);
});

test('every candidate ruled out → NO_CANDIDATE (UI asks for a new one), time still reserved', () => {
  const [leg] = run([slot([soba], { excluded: ['soba'] })]).legs;
  assert.deepStrictEqual([leg.id, leg.warn, leg.travelMin, leg.stayMin], [null, 'NO_CANDIDATE', 0, 60]);
});

test('the slot shifts everything after it', () => {
  const r = run([slot([steak]), bar]);
  const stay = r.legs[0];
  assert.strictEqual(r.legs[1].arrive, stay.depart + 20);
});

test('only the chosen place costs a real route lookup; candidates use the cheap estimate', () => {
  const real = [], cheap = [];
  const r = run([slot([soba, steak, cafe])], {
    travelFn: (a, b) => { real.push(b.id); return 20; },
    candTravelFn: (a, b) => { cheap.push(b.id); return 20; },
  });
  assert.deepStrictEqual(real, ['steak']);
  assert.deepStrictEqual(cheap, ['soba', 'steak', 'cafe']);
  assert.strictEqual(r.legs[0].id, 'steak');
});

test('candidates are judged at their own arrival (different travel times)', () => {
  const near = place('near', all([780, 900])), far = place('far', all([600, 1000]));   // near opens 13:00
  const cand = (a, b) => (b.id === 'far' ? 60 : 10);
  const [leg] = run([slot([near, far])], { candTravelFn: cand, travelFn: cand }).legs;   // near: arrive 11:50 (wait), far: 12:40
  const [n, f] = leg.slot.candidates;
  assert.deepStrictEqual([n.arrive, n.wait, f.arrive, f.wait], [710, 70, 760, 0]);
});

test('regression: single-candidate slot via toStop computes exactly like the old plain stop', () => {
  const byId = { steak, bar };
  const plain = run([{ ...steak, stayMin: 60 }, { ...bar, stayMin: 90 }]);
  const viaSlots = run([Slots.toStop({ id: 'steak', stayMin: 60 }, byId), Slots.toStop({ sid: 's2', cands: ['bar'], pick: null, stayMin: 90 }, byId)]);
  assert.deepStrictEqual(viaSlots, plain);
});

test('Saturday: soba is open, so it is the first (list order) viable candidate', () => {
  const [leg] = run([slot([soba, steak])], { weekday: SAT }).legs;
  assert.strictEqual(leg.id, 'soba');
});

test('a long wait for opening (break time) ranks below unknown hours; a short wait still counts as open', () => {
  // arrive 15:01: the steak house is in its break (reopens 17:00 → waits ~2h), the cafe has no hours
  const late = run([slot([steak, cafe])], { startMin: 15 * 60 - 19 });
  assert.strictEqual(late.legs[0].id, 'cafe');
  assert.strictEqual(late.legs[0].slot.candidates[0].tier, 2);
  // arrive 12:00 at a place that opens 12:20 (wait 20 ≤ 30) is still "open"
  const soon = place('soon', all([740, 900]));
  const r = run([slot([cafe, soon])]);
  assert.deepStrictEqual([r.legs[0].id, r.legs[0].slot.candidates[1].tier], ['soon', 0]);
  // ...but 45 minutes is not
  const later = place('later', all([765, 900]));
  assert.strictEqual(run([slot([cafe, later])]).legs[0].id, 'cafe');
});

const test = require('node:test');
const assert = require('node:assert');
const S = require('./schedule.js');
S.configure({ destKeywords: ['오키나와', '나하', 'OKA'] });
const { planRoute } = require('./route.js');

const EV = [
  { id: 'f1', date: '12-12', start: '09:00', end: '11:20', title: 'ICN → OKA' },
  { id: 'h1', date: '12-12', start: '15:00', end: null, title: '체크인 A' },
  { id: 'd1', date: '12-13', start: '18:00', end: '19:30', title: '저녁 예약' },
  { id: 'o1', date: '12-14', start: '11:00', end: null, title: '체크아웃 A' },
  { id: 'h2', date: '12-14', start: '15:00', end: null, title: '체크인 B' },
  { id: 'f2', date: '12-16', start: '13:10', end: '15:40', title: 'OKA → ICN' },
  { id: 'r1', date: '12-13', start: '10:00', end: '11:00', title: '📍 X', kind: 'route' },
];
const PLACE = { h1: 'hotelA', o1: 'hotelA', h2: 'hotelB', d1: 'steak', f2: 'air' };
const placeOf = (e) => PLACE[e.id] || null;

test('real calendar format: stay expands, flight arrow sides classify', () => {
  const raw = [
    { id: 'f', title: '샘플항공 101 인천 → 오키나와(나하)', startDate: '12-12', start: '11:25', endDate: '12-12', end: '14:00' },
    { id: 's', title: '샘플 호텔 숙박', startDate: '12-12', start: '14:00', endDate: '12-16', end: '11:00', location: 'Sample', lat: 1, lng: 2 },
    { id: 'r', title: '샘플항공 102 오키나와(나하) → 인천', startDate: '12-16', start: '15:00', endDate: '12-16', end: '17:30' },
  ];
  const ev = S.expandEvents(raw);
  assert.deepStrictEqual(ev.map((e) => [e.id, e.date, e.start, S.kindOf(e)]), [
    ['f', '12-12', '11:25', 'flightIn'], ['s:in', '12-12', '14:00', 'hotelIn'], ['s:out', '12-16', '11:00', 'hotelOut'], ['r', '12-16', '15:00', 'flightOut'],
  ]);
  const placeOf = (e) => (e.lat ? 'lequ' : null);
  assert.deepStrictEqual(S.dayStart('12-12', ev, placeOf, 'air').min, 900); // 14:00 landing + 60
  assert.deepStrictEqual(S.dayStart('12-13', ev, placeOf, 'air').placeId, 'lequ'); // night 1 at the stay
  const d16 = S.dayStart('12-16', ev, placeOf, 'air');
  assert.deepStrictEqual([d16.placeId, d16.min], ['lequ', 660]);
  assert.deepStrictEqual(S.fixedStops('12-12', ev, placeOf, 'f').map((f) => f.kind), ['hotelIn']);
});

test('kindOf classifies titles', () => {
  assert.deepStrictEqual(EV.map(S.kindOf), ['flightIn', 'hotelIn', 'fixed', 'hotelOut', 'hotelIn', 'flightOut', 'route']);
});

test('day start: arrival flight → airport, landing + 60', () => {
  assert.deepStrictEqual([S.dayStart('12-12', EV, placeOf, 'air').placeId, S.dayStart('12-12', EV, placeOf, 'air').min], ['air', 680 + 60]);
});

test('day start: checkout day → that hotel at checkout time', () => {
  const d = S.dayStart('12-14', EV, placeOf, 'air');
  assert.deepStrictEqual([d.placeId, d.min, d.eventId], ['hotelA', 660, 'o1']);
});

test('day start: plain day → previous night hotel at 09:00', () => {
  assert.deepStrictEqual([S.dayStart('12-15', EV, placeOf, 'air').placeId, S.dayStart('12-15', EV, placeOf, 'air').min], ['hotelB', 540]);
  assert.strictEqual(S.dayStart('12-15', EV, () => null, 'air').placeId, null);
  const onlyOldLinked = (e) => (e.id === 'h1' ? 'hotelA' : null); // newest check-in h2 unlinked
  assert.strictEqual(S.dayStart('12-15', EV, onlyOldLinked, 'air').placeId, null);
});

test('fixed stops exclude start event, flightIn, route kind and unlinked', () => {
  assert.deepStrictEqual(S.fixedStops('12-14', EV, placeOf, 'o1').map((f) => f.ev), ['h2']);
  assert.deepStrictEqual(S.fixedStops('12-13', EV, placeOf, null).map((f) => f.ev), ['d1']);
  assert.deepStrictEqual(S.fixedStops('12-16', EV, placeOf, null)[0].fixed, { start: 700, end: null, due: 700, isEnd: true });
});

test('mergeOrder keeps user order, appends new fixed, end items last, drops stale', () => {
  const fixed = [{ ev: 'd1', fixed: { isEnd: false } }, { ev: 'h2', fixed: { isEnd: true } }];
  const stored = [{ ev: 'h2' }, { id: 'a' }, { ev: 'gone' }, { id: 'b' }];
  assert.deepStrictEqual(S.mergeOrder(stored, fixed), [{ id: 'a' }, { id: 'b' }, { ev: 'd1' }, { ev: 'h2' }]);
});

test('planRoute fixed: early waits, late warns, stay = remaining event time', () => {
  const fx = { start: 1080, end: 1170, due: 1080 };
  const run = (startMin) => planRoute({ weekday: 0, startMin, start: {}, stops: [{ id: 'x', fixed: fx }], travelFn: () => 30 }).legs[0];
  const early = run(1000);
  assert.deepStrictEqual([early.wait, early.slackMin, early.stayMin, early.warn], [50, 50, 90, null]);
  const late = run(1070);
  assert.deepStrictEqual([late.lateMin, late.stayMin, late.warn], [20, 70, 'LATE']);
});

test('planRoute end item with due=Infinity never LATE', () => {
  const l = planRoute({ weekday: 0, startMin: 1300, start: {}, stops: [{ id: 'h', fixed: { start: 900, end: null, due: Infinity } }], travelFn: () => 30 }).legs[0];
  assert.deepStrictEqual([l.warn, l.wait, l.stayMin], [null, 0, 0]);
});

test('configure: other destination, buffers and default start', () => {
  S.configure({ destKeywords: ['방콕', 'BKK', 'a.b'], arrivalBufferMin: 30, flightLeadMin: 120, defaultStart: '08:30' });
  const ev = [
    { id: 'f', date: 'd1', start: '10:00', end: '13:00', title: 'TG659 인천 → 방콕(BKK)' },
    { id: 'r', date: 'd3', start: '22:00', end: null, title: 'BKK → 인천' },
    { id: 'x', date: 'd1', start: '09:00', end: null, title: 'axb → 서울' }, // keywords are literal, not regex
  ];
  assert.deepStrictEqual(ev.map(S.kindOf), ['flightIn', 'flightOut', 'fixed']);
  assert.strictEqual(S.dayStart('d1', ev, () => null, 'air').min, 13 * 60 + 30);
  assert.strictEqual(S.dayStart('d2', ev, () => null, 'air').min, 8 * 60 + 30);
  assert.strictEqual(S.fixedStops('d3', ev, () => 'air', null)[0].fixed.due, 22 * 60 - 120);
  S.configure({ destKeywords: [] });
  assert.strictEqual(S.kindOf(ev[0]), 'fixed'); // no keywords → never a flight
  S.configure({ destKeywords: ['오키나와', '나하', 'OKA'] });
});

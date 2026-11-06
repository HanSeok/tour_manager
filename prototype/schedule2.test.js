const test = require('node:test');
const assert = require('node:assert');
const S = require('./schedule.js');

test('Gmail-style flight titles', () => {
  S.configure({ destKeywords: ['나하', 'Naha'] });
  const kind = (title) => S.kindOf({ title });
  assert.strictEqual(kind('Flight to Naha (NH 123)'), 'flightIn');
  assert.strictEqual(kind('flight to Seoul (KE 742)'), 'flightOut');
  assert.strictEqual(kind('항공편: 나하'), 'flightIn');
  assert.strictEqual(kind('항공편 예약 확인'), 'fixed'); // no colon → not a flight
  assert.strictEqual(kind('KE741 인천 → 나하'), 'flightIn');
});

test('overnight flight stays one event; multi-day stay splits into check-in/out', () => {
  S.configure({ destKeywords: ['Naha'] });
  const out = S.expandEvents([
    { id: 'f', title: 'Flight to Naha', startDate: '2027-04-10', start: '23:00', endDate: '2027-04-11', end: '02:00' },
    { id: 'h', title: 'Stay at Hotel', startDate: '2027-04-10', start: '15:00', endDate: '2027-04-12', end: '11:00' },
  ]);
  assert.deepStrictEqual(out.map((e) => e.id), ['f', 'h:in', 'h:out']);
  assert.ok(out[1].title.startsWith('체크인') && out[2].title.startsWith('체크아웃'));
  assert.strictEqual(S.kindOf(out[1]), 'hotelIn');
});

const test = require('node:test');
const assert = require('node:assert');
const { phaseOf, buildTimeline, toMin } = require('./timeline.js');

test('phaseOf: boundaries are inclusive for the live phase', () => {
  assert.strictEqual(phaseOf('2027-04-09', '2027-04-10', '2027-04-14'), 'planning');
  assert.strictEqual(phaseOf('2027-04-10', '2027-04-10', '2027-04-14'), 'live');
  assert.strictEqual(phaseOf('2027-04-14', '2027-04-10', '2027-04-14'), 'live');
  assert.strictEqual(phaseOf('2027-04-15', '2027-04-10', '2027-04-14'), 'done');
});

test('toMin parses HH:mm and rejects anything else', () => {
  assert.strictEqual(toMin('09:05'), 545);
  assert.strictEqual(toMin('9:05'), 545);
  assert.strictEqual(toMin(''), null);
  assert.strictEqual(toMin(undefined), null);
  assert.strictEqual(toMin('25:xx'), null);
});

test('buildTimeline: groups per date, sorts by minute, unknown time last', () => {
  const items = [
    { date: '2027-04-11', min: null, kind: 'expense', amount: 500 },
    { date: '2027-04-11', min: 720, kind: 'expense', amount: 1200 },
    { date: '2027-04-11', min: 600, kind: 'place', name: '슈리성' },
    { date: '2027-04-10', min: 900, kind: 'photo', id: 'p1' },
  ];
  const t = buildTimeline(['2027-04-10', '2027-04-11', '2027-04-12'], items);
  assert.deepStrictEqual(t.map((d) => d.date), ['2027-04-10', '2027-04-11', '2027-04-12']);
  assert.deepStrictEqual(t[1].entries.map((e) => e.kind + ':' + (e.name || e.amount)), ['place:슈리성', 'expense:1200', 'expense:500']);
  assert.strictEqual(t[1].expenseTotal, 1700);
  assert.strictEqual(t[0].expenseTotal, 0);
  assert.deepStrictEqual(t[2].entries, []);
});

test('buildTimeline: same minute orders place → expense → photo, ties keep input order', () => {
  const items = [
    { date: 'd', min: 600, kind: 'photo', id: 'a' },
    { date: 'd', min: 600, kind: 'expense', amount: 1 },
    { date: 'd', min: 600, kind: 'place', name: 'x' },
    { date: 'd', min: 600, kind: 'photo', id: 'b' },
  ];
  const [day] = buildTimeline(['d'], items);
  assert.deepStrictEqual(day.entries.map((e) => e.id || e.name || e.amount), ['x', 1, 'a', 'b']);
});

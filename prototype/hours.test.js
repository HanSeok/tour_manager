const test = require('node:test');
const assert = require('node:assert');
const H = require('./hours.js');

test('fromText matches the CSV importer rules', () => {
  assert.deepStrictEqual(H.fromText('월요일 24시간 영업 / 화요일 휴무일 / 수요일 오전 9:00~오후 6:00'), { 1: [[0, 1440]], 2: [], 3: [[540, 1080]] });
  assert.deepStrictEqual(H.fromText('금요일 오전 10:00~오전 12:00'), { 5: [[600, 1440]] });
  assert.deepStrictEqual(H.fromText('토요일 오후 6:00~오전 2:00')[6], [[1080, 1560]]);
  assert.deepStrictEqual(H.fromText('일요일 오후 2:00~9:00')[0], [[840, 1260]]);
  assert.strictEqual(H.fromText(''), null);
  assert.strictEqual(H.fromText('영업시간 정보 없음'), null);
});

test('fromPeriods: lunch + dinner, overnight, closed day', () => {
  const h = H.fromPeriods([
    { open: { day: 1, hour: 11, minute: 0 }, close: { day: 1, hour: 15, minute: 0 } },
    { open: { day: 1, hour: 17, minute: 0 }, close: { day: 1, hour: 23, minute: 0 } },
    { open: { day: 5, hour: 18, minute: 0 }, close: { day: 6, hour: 2, minute: 0 } },
  ]);
  assert.deepStrictEqual(h[1], [[660, 900], [1020, 1380]]);
  assert.deepStrictEqual(h[5], [[1080, 1560]]);
  assert.deepStrictEqual(h[2], []);
});

test('fromPeriods: 24/7 and empty', () => {
  assert.deepStrictEqual(H.fromPeriods([{ open: { day: 0, hour: 0, minute: 0 } }])[3], [[0, 1440]]);
  assert.strictEqual(H.fromPeriods([]), null);
  assert.strictEqual(H.fromPeriods(undefined), null);
});

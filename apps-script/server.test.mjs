// Node tests for pure server helpers (Apps Script globals stubbed). Run: node --test apps-script/server.test.mjs
// (.mjs so clasp never uploads it — .claspignore only pushes *.js / *.html.)
import test from 'node:test';
import assert from 'node:assert';
import fs from 'node:fs';
import vm from 'node:vm';

const dir = new URL('.', import.meta.url).pathname;
const ctx = {
  console,
  Intl,
  Utilities: { formatDate: () => '+09:00' },
  SpreadsheetApp: {},
  PropertiesService: {},
  LockService: {},
  HtmlService: {},
};
vm.createContext(ctx);
for (const f of ['Config.js', 'Code.js', 'Maps.js']) vm.runInContext(fs.readFileSync(dir + f, 'utf8'), ctx, { filename: f });
const run = (expr) => vm.runInContext(expr, ctx);

test('safeCell_ neutralizes formulas, keeps normal text/numbers', () => {
  assert.strictEqual(run(`safeCell_('=IMAGE("x")')`), `'=IMAGE("x")`);
  assert.strictEqual(run(`safeCell_('+1')`), `'+1`);
  assert.strictEqual(run(`safeCell_('@a')`), `'@a`);
  assert.strictEqual(run(`safeCell_('소바')`), '소바');
  assert.strictEqual(run('safeCell_(1500)'), 1500);
});

test('isRealDate_ rejects rolled-over dates', () => {
  assert.strictEqual(run(`isRealDate_('2027-02-28')`), true);
  assert.strictEqual(run(`isRealDate_('2027-02-30')`), false);
  assert.strictEqual(run(`isRealDate_('2027-4-1')`), false);
});

test('validateConfig_: unknown timezone, empty lists, reserved sheet, bad buffers', () => {
  const raw = {
    title: 't', start: '2027-04-10', end: '2027-04-14', timezone: 'Asia/Tokio', destKeywords: ',', myMapsUrl: 'https://x/?mid=abc',
    localCurrency: '엔', localSymbol: '¥', homeSymbol: '₩', rate: '9.3', payers: ' , ', methods: '카드', expenseSheet: '설정',
    arrivalBufferMin: '-5', flightLeadMin: '90', defaultStart: '25:00',
  };
  ctx.raw = raw;
  const cfg = run('validateConfig_(raw, true)');
  const text = cfg.invalid.join('\n');
  for (const label of ['현지 시간대', '목적지 키워드', '결제자', '지출 시트 이름', '입국 소요', '기본 출발 시각']) {
    assert.ok(text.includes(label), `expected invalid: ${label}\n${text}`);
  }
  assert.ok(!text.includes('결제수단'));
  ctx.raw = { ...raw, timezone: 'Asia/Tokyo', destKeywords: '나하', payers: '나', expenseSheet: '여행_지출', arrivalBufferMin: '60', defaultStart: '9:00' };
  const good = run('validateConfig_(raw, true)');
  assert.strictEqual(good.invalid.join('|'), ''); // vm arrays: compare by value
  assert.strictEqual(good.defaultStart, '09:00'); // single-digit hour as stored by Sheets is normalized
});

test('parseTravelKey_: coordinates/departure come from the key only', () => {
  ctx.cfg = { start: '2027-04-10', end: '2027-04-14', offset: '+09:00' };
  const p = run(`parseTravelKey_('26.20600,127.64660>26.31460,127.75680@2027-04-10+900', cfg)`);
  assert.strictEqual([p.from.lat, p.to.lng, p.depart].join('|'), '26.206|127.7568|2027-04-10T06:30:00.000Z');
  assert.strictEqual(run(`parseTravelKey_('26.20600,127.64660>26.31460,127.75680', cfg)`).depart, null);
  for (const bad of ['=IMAGE("x")', 'probe>none', '26.2,127.6>26.3,127.7', '26.20600,127.64660>26.31460,127.75680@2030-01-01+900',
    '26.20600,127.64660>26.31460,127.75680@2027-04-10+905']) {
    assert.strictEqual(run(`parseTravelKey_(${JSON.stringify(bad)}, cfg)`), null, bad);
  }
});

test('STATE_SHAPES accept real shapes, reject junk', () => {
  const ok = {
    routes: { '2027-04-10': [{ id: 'ChIJx', stayMin: 60 }, { ev: 'abc@google.com:in' }] },
    starts: { '2027-04-10': 900 }, startFrom: { '2027-04-10': 's-hotel' }, eventPlace: { 'abc@google.com': 's-steak', other: '' },
  };
  for (const [k, v] of Object.entries(ok)) assert.ok(run(`STATE_SHAPES.${k}`)(v), k);
  const bad = { routes: 'x', starts: { '2027-04-10': '9' }, startFrom: { nope: 'a' }, eventPlace: [] };
  for (const [k, v] of Object.entries(bad)) assert.ok(!run(`STATE_SHAPES.${k}`)(v), k);
  assert.ok(!run('STATE_SHAPES.routes')({ '2027-04-10': [{ id: 'a', stayMin: -1 }] }));
});

test('route tag must start the description (user events mentioning it are not deleted)', () => {
  assert.ok(run(`ROUTE_TAG_RE.test('#trip-route place:x')`));
  assert.ok(run(`ROUTE_TAG_RE.test('#okinawa-route place:x')`));
  assert.ok(!run(`ROUTE_TAG_RE.test('메모: #trip-route 참고')`));
});

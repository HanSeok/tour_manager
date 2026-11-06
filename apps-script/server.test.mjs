// Node tests for pure server helpers (Apps Script globals stubbed). Run: node --test apps-script/server.test.mjs
// (.mjs so clasp never uploads it — .claspignore only pushes *.js / *.html.)
import test from 'node:test';
import assert from 'node:assert';
import fs from 'node:fs';
import vm from 'node:vm';

const dir = new URL('.', import.meta.url).pathname;
// In-memory Script Properties + no-op lock: lets saveState / requireEdit_ run for real.
const props = new Map();
const scriptProps = {
  getProperty: (k) => (props.has(k) ? props.get(k) : null),
  setProperty: (k, v) => { props.set(k, String(v)); },
  setProperties: (o) => { Object.entries(o).forEach(([k, v]) => props.set(k, String(v))); },
  deleteProperty: (k) => { props.delete(k); },
  getProperties: () => Object.fromEntries(props),
};
const ctx = {
  console,
  Intl,
  Utilities: { formatDate: () => '+09:00' },
  SpreadsheetApp: {},
  PropertiesService: { getScriptProperties: () => scriptProps },
  LockService: { getScriptLock: () => ({ waitLock() {}, releaseLock() {} }) },
  HtmlService: {},
};
vm.createContext(ctx);
for (const f of ['Config.js', 'Code.js', 'Maps.js', 'Admin.js', 'Expenses.js', 'Calendar.js']) vm.runInContext(fs.readFileSync(dir + f, 'utf8'), ctx, { filename: f });
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

test('new config fields: travel mode, decimals, calendar id, tile url', () => {
  const base = {
    title: 't', start: '2027-04-10', end: '2027-04-14', timezone: 'Asia/Tokyo', destKeywords: '나하', myMapsUrl: 'https://x/?mid=abc',
    localCurrency: '유로', localSymbol: '€', homeSymbol: '₩', rate: '1450', payers: '나', methods: '카드', expenseSheet: '지출',
    arrivalBufferMin: '', flightLeadMin: '', defaultStart: '',
  };
  ctx.raw = { ...base, travelMode: '대중교통', currencyDecimals: '2', calendarId: 'abc@group.calendar.google.com', tileUrl: 'https://{s}.tile.example/{z}/{x}/{y}.png' };
  const good = run('validateConfig_(raw, true)');
  assert.strictEqual(good.invalid.join('|'), '');
  assert.strictEqual([good.travelMode, good.currencyDecimals, good.calendarId].join('|'), 'transit|2|abc@group.calendar.google.com');
  ctx.raw = base; // optional fields empty → defaults
  const dflt = run('validateConfig_(raw, true)');
  assert.strictEqual([dflt.travelMode, dflt.currencyDecimals, dflt.tileUrl, dflt.invalid.length].join('|'), 'driving|0||0');
  ctx.raw = { ...base, travelMode: '비행기', currencyDecimals: '3', calendarId: 'a b', tileUrl: 'javascript:alert(1)/{z}/{x}/{y}' };
  const text = run('validateConfig_(raw, true)').invalid.join('\n');
  for (const label of ['이동수단', '소수 자릿수', '캘린더 ID', '지도 타일 주소']) assert.ok(text.includes(label), label);
  ctx.c = good;
  assert.strictEqual(run('JSON.stringify(Object.keys(publicConfig_(c)).filter((k) => k === "calendarId" || k === "mid"))'), '[]');
});

test('requireEdit_: open without a key, exact match required with one', () => {
  props.clear();
  assert.doesNotThrow(() => run(`requireEdit_(undefined)`));
  props.set('EDIT_KEY', 'a'.repeat(32));
  assert.throws(() => run(`requireEdit_('')`), /보기 전용/);
  assert.throws(() => run(`requireEdit_('${'b'.repeat(32)}')`), /보기 전용/);
  assert.doesNotThrow(() => run(`requireEdit_('${'a'.repeat(32)}')`));
  props.set('EDIT_KEY', 'not-hex'); // a malformed stored key is ignored, not echoed or matched
  assert.strictEqual(run('editKey_()'), '');
  props.clear();
});

const TRIP_CFG = { start: '2027-04-10', end: '2027-04-14', missing: [], invalid: [] };

test('saveState merges per-date patches (two companions, different days) and migrates legacy routes', () => {
  props.clear();
  ctx.tc = TRIP_CFG;
  run('configMemo_ = tc');
  props.set('state.routes', JSON.stringify({ '2027-04-10': [{ id: 'old', stayMin: 30 }] }));
  run(`saveState('routes', { '2027-04-11': [{ id: 'a', stayMin: 60 }] })`); // companion A
  run(`saveState('routes', { '2027-04-12': [{ id: 'b', stayMin: 90 }] })`); // companion B
  const routes = run('readState_().routes');
  assert.deepStrictEqual(Object.keys(routes).sort(), ['2027-04-10', '2027-04-11', '2027-04-12']);
  assert.ok(!props.has('state.routes'), 'legacy key removed after migration');
  run(`saveState('routes', { '2027-04-11': null })`);
  assert.ok(!props.has('state.routes.2027-04-11') && props.has('state.routes.2027-04-12'), 'null patch deletes only that day');
  run(`saveState('eventPlace', { e1: 'p1' })`);
  run(`saveState('eventPlace', { e2: '' })`);
  assert.deepStrictEqual({ ...run('readState_().eventPlace') }, { e1: 'p1', e2: '' });
  assert.throws(() => run(`saveState('routes', { 'not-a-date': [] })`), /형식/);
  assert.throws(() => run(`saveState('routes', [1])`), /형식/);
  assert.throws(() => run(`saveState('routes', { '2031-01-01': [] })`), /여행 기간/); // bounds per-date property growth
  assert.throws(() => run(`saveState('starts', { '2027-04-09': 600 })`), /여행 기간/);
  props.set('EDIT_KEY', 'c'.repeat(32));
  assert.throws(() => run(`saveState('starts', { '2027-04-10': 600 }, 'wrong')`), /보기 전용/);
  props.clear();
});

test('cleanExpense_: decimals, pre-trip dates, home-currency input', () => {
  ctx.cfg = { start: '2027-04-10', end: '2027-04-14', currencyDecimals: 2, homeDecimals: 0, rate: 1450, payers: ['나'], methods: ['카드'], localCurrency: '유로', homeSymbol: '₩' };
  const e = { date: '2027-04-11', category: '식비', payer: '나', method: '카드', item: '' };
  ctx.e = { ...e, amount: 4.5 };
  const c = run('cleanExpense_(cfg, e)');
  assert.strictEqual([c.amount, c.home, c.item, c.currency].join('|'), '4.5|6525|식비|유로');
  ctx.e = { ...e, amount: 1.1 }; // float: 1.1 * 100 !== 110
  assert.strictEqual(run('cleanExpense_(cfg, e)').amount, 1.1);
  ctx.e = { ...e, amount: ['2027-04-11'] };
  assert.throws(() => run('cleanExpense_(cfg, e)'), /금액/);
  ctx.e = { ...e, amount: 1, date: ['2027-04-11'] }; // stored date is the coerced string, never the raw array
  assert.strictEqual(typeof run('cleanExpense_(cfg, e)').date, 'string');
  ctx.e = { ...e, amount: 4.555 };
  assert.throws(() => run('cleanExpense_(cfg, e)'), /소수 2자리/);
  ctx.e = { ...e, amount: 290000, currency: 'home', date: '2026-12-01' }; // pre-paid flight in ₩
  const h = run('cleanExpense_(cfg, e)');
  assert.strictEqual([h.amount, h.home, h.currency].join('|'), '200|290000|₩');
  for (const date of ['2026-04-09', '2027-04-15', '2027-02-30']) {
    ctx.e = { ...e, amount: 1, date };
    assert.throws(() => run('cleanExpense_(cfg, e)'), /날짜/, date);
  }
  ctx.cfg = { ...ctx.cfg, currencyDecimals: 0 };
  ctx.e = { ...e, amount: 1.5 };
  assert.throws(() => run('cleanExpense_(cfg, e)'), /정수/);
  ctx.e = { ...e, amount: 290000.5, currency: 'home' }; // ₩ has no decimals (homeDecimals 0)
  assert.throws(() => run('cleanExpense_(cfg, e)'), /정수/);
  ctx.cfg = { ...ctx.cfg, homeSymbol: '$', homeDecimals: 2, rate: 0.0067 }; // ¥ trip, USD home: cents kept
  ctx.e = { ...e, amount: 1500 };
  assert.strictEqual(run('cleanExpense_(cfg, e)').home, 10.05);
  ctx.e = { ...e, amount: 1, payer: '모르는 사람' };
  assert.throws(() => run('cleanExpense_(cfg, e)'), /결제자/);
});

test('travel key mode suffix', () => {
  ctx.cfg = { start: '2027-04-10', end: '2027-04-14', offset: '+09:00' };
  const k = '26.20600,127.64660>26.31460,127.75680';
  assert.strictEqual(run(`parseTravelKey_('${k}', cfg).mode`), 'driving');
  assert.strictEqual(run(`parseTravelKey_('${k}!t', cfg).mode`), 'transit');
  assert.strictEqual(run(`parseTravelKey_('${k}@2027-04-10+900!w', cfg).mode`), 'walking');
  assert.strictEqual(run(`parseTravelKey_('${k}!x', cfg)`), null);
});

test('VERSION matches the repo VERSION file; stay titles', () => {
  assert.strictEqual(run('VERSION'), fs.readFileSync(`${dir}../VERSION`, 'utf8').trim());
  for (const t of ['Stay at Hotel Moon', '샘플 호텔 숙박', 'Hilton Hotel']) assert.ok(run('STAY_TITLE_RE').test(t), t);
  assert.ok(!run('STAY_TITLE_RE').test('오키나와 여행'));
});

test('unescapeCell_ undoes safeCell_ for display only', () => {
  for (const v of ['=IMAGE("x")', '-', '+5 coffee', '소바']) assert.strictEqual(run(`unescapeCell_(safeCell_(${JSON.stringify(v)}))`), v);
});

test('inBox_: travel lookups only near the trip', () => {
  ctx.b = { minLat: 25.7, maxLat: 27.2, minLng: 127.1, maxLng: 128.5 };
  assert.ok(run('inBox_(b, { lat: 26.2, lng: 127.7 })'));
  assert.ok(!run('inBox_(b, { lat: 37.5, lng: 127.0 })'));
  assert.ok(!run('inBox_(null, { lat: 26.2, lng: 127.7 })'));
});

test('upgradeTabs_: appends only missing setting rows, keeps values, refreshes a stale guide', () => {
  const grid = (rows) => {
    let data = rows.map((r) => [...r]);
    const formats = [];
    return {
      data: () => data, formats,
      getLastRow: () => data.length,
      clear: () => { data = []; },
      setColumnWidth: () => {},
      getRange: (row, col, nRows = 1, nCols = 1) => ({
        getValues: () => Array.from({ length: nRows }, (_, i) => Array.from({ length: nCols }, (_, j) => ((data[row - 1 + i] || [])[col - 1 + j] ?? ''))),
        setValues: (v) => { v.forEach((r, i) => { data[row - 1 + i] = [...(data[row - 1 + i] || []), '', '', ''].slice(0, Math.max(3, col - 1)); r.forEach((x, j) => { data[row - 1 + i][col - 1 + j] = x; }); }); return { setFontWeight() {} }; },
        setNumberFormat: (f) => { formats.push([row, f]); return { setBackground() {} }; },
        setFontWeight: () => {},
      }),
    };
  };
  const v1Labels = run('CONFIG_FIELDS').slice(0, 17).map((f) => [f[1], `값-${f[0]}`, '']); // a v1 tab (17 rows) with values
  const config = grid([['항목', '값', '설명'], ...v1Labels]);
  const guide = grid([['여행 대시보드 사용법 (옛 버전)']]);
  ctx.ss = { getSheetByName: (n) => (n === '설정' ? config : n === '안내' ? guide : null) };
  const added = run('upgradeTabs_(ss)');
  const total = run('CONFIG_FIELDS.length');
  assert.strictEqual(added, total - 17);
  assert.strictEqual(config.data().length, total + 1);
  assert.strictEqual(config.data()[1][1], '값-title', 'existing value untouched');
  assert.strictEqual(config.data().slice(18).map((r) => r[0]).join('|'), run('CONFIG_FIELDS').slice(17).map((f) => f[1]).join('|'));
  assert.ok(config.formats.every(([, f]) => f === '@'), 'new value cells are text (no formulas)');
  assert.strictEqual(guide.data()[0][0], run('guideLines_()')[0], 'stale guide rewritten');
  assert.strictEqual(run('upgradeTabs_(ss)'), 0, 'second run adds nothing');
  const mine = grid([['내 메모'], ['여행 준비물']]);
  ctx.ss = { getSheetByName: (n) => (n === '안내' ? mine : null) };
  run('upgradeTabs_(ss)');
  assert.strictEqual(mine.data()[0][0], '내 메모', "a user's own 안내 tab is left alone");
});

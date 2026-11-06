// Expenses: one row per entry in the "지출 시트 이름" tab. Column order is fixed (reads are positional).
const EXPENSE_COLS = 10;
const ID_COL = 9;
const PRE_TRIP_DAYS = 365; // pre-paid flights/hotels may be booked up to a year ahead
const CLIENT_ID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const CURRENCY_COL_LABEL = '입력 통화';
const expenseHeader_ = (cfg) => ['날짜', '시간', '분류', '내용', `금액(${cfg.localCurrency})`, `환산(${cfg.homeSymbol})`, '결제자', '결제수단', 'ID', CURRENCY_COL_LABEL];
const amountFormat_ = (decimals) => (decimals > 0 ? `#,##0.${'0'.repeat(decimals)}` : '#,##0');

function ensureTripSheet_() {
  return withLock_(() => {
    const ss = SpreadsheetApp.getActive();
    const cfg = config_();
    const header = expenseHeader_(cfg);
    let sheet = ss.getSheetByName(cfg.expenseSheet);
    const created = !sheet;
    if (!sheet) sheet = ss.insertSheet(cfg.expenseSheet, ss.getSheets().length);
    const head = sheet.getRange(1, 1, 1, header.length).getDisplayValues()[0];
    if (sheet.getLastRow() <= 1 && head.join('|') !== header.join('|')) {
      sheet.getRange(1, 1, 1, sheet.getMaxColumns()).breakApart().clearContent();
      sheet.getRange(1, 1, 1, header.length).setValues([header]).setFontWeight('bold');
      sheet.setFrozenRows(1);
      sheet.getRange('A:D').setNumberFormat('@'); // date/time/category/item stay text (also blocks formula injection)
      sheet.getRange('G:J').setNumberFormat('@');
      sheet.getRange('E:F').setNumberFormat(amountFormat_(cfg.currencyDecimals));
    } else if (!head[EXPENSE_COLS - 1]) { // v1 sheet (9 columns) → add the currency column header
      sheet.getRange(1, EXPENSE_COLS).setValue(CURRENCY_COL_LABEL).setFontWeight('bold');
      sheet.getRange('J:J').setNumberFormat('@');
    }
    return { name: sheet.getName(), created, header };
  });
}

function expenseSheet_() {
  const name = config_().expenseSheet;
  return SpreadsheetApp.getActive().getSheetByName(name) || fail_(`시트 '${name}'가 없습니다`);
}

/** Undo safeCell_ when the sheet kept the apostrophe literally ('=1 → =1). The value is display text, never re-evaluated. */
const unescapeCell_ = (v) => { const t = String(v); return /^'[=+\-@\t\r]/.test(t) ? t.slice(1) : t; };

function readExpenses_() {
  const sheet = expenseSheet_();
  const n = sheet.getLastRow() - 1;
  if (n <= 0) return [];
  const homeMark = config_().homeSymbol;
  return sheet.getRange(2, 1, n, EXPENSE_COLS).getValues()
    .filter((r) => r[ID_COL - 1])
    .map((r) => ({
      date: cellText_(r[0]), time: cellText_(r[1]), category: String(r[2]), item: unescapeCell_(r[3]),
      amount: Number(r[4]) || 0, home: Number(r[5]) || 0, payer: String(r[6]), method: String(r[7]), id: String(r[8]),
      currency: String(r[9]) === homeMark ? 'home' : 'local',
    }));
}

const addDays_ = (iso, n) => new Date(Date.parse(`${iso}T00:00:00Z`) + n * 86400000).toISOString().slice(0, 10);
const roundTo_ = (v, d) => Math.round(v * 10 ** d) / 10 ** d;
const hasDecimals_ = (v, d) => Math.abs(Math.round(v * 10 ** d) - v * 10 ** d) < 1e-6;

/** Validates a web-submitted expense → normalized {date, category, item, amount, home, payer, method, currency}. */
function cleanExpense_(cfg, e) {
  const x = e || {};
  const isHome = x.currency === 'home';
  const value = Number(x.amount);
  const date = String(x.date);
  const d = isHome ? cfg.homeDecimals : cfg.currencyDecimals;
  if (!isRealDate_(date) || date < addDays_(cfg.start, -PRE_TRIP_DAYS) || date > cfg.end) fail_('날짜는 여행 1년 전부터 종료일까지만 됩니다');
  if (!Number.isFinite(value) || value <= 0 || value > MAX_AMOUNT || !hasDecimals_(value, d)) {
    fail_(`금액은 0보다 큰 숫자${d ? `(소수 ${d}자리까지)` : '(정수)'}여야 합니다`);
  }
  if (!CATEGORIES.includes(x.category) || !cfg.payers.includes(x.payer) || !cfg.methods.includes(x.method)) fail_('분류/결제자/결제수단 값이 올바르지 않습니다');
  const amount = roundTo_(isHome ? value / cfg.rate : value, cfg.currencyDecimals);
  const home = roundTo_(isHome ? value : value * cfg.rate, cfg.homeDecimals);
  return { date, category: x.category, item: String(x.item || '').trim().slice(0, 60) || x.category, amount, home, payer: x.payer, method: x.method, currency: isHome ? cfg.homeSymbol : cfg.localCurrency };
}

const expenseRow_ = (c, time, id) => [c.date, time, c.category, c.item, c.amount, c.home, c.payer, c.method, id, c.currency].map(safeCell_);

function expenseIds_(sheet) {
  const n = sheet.getLastRow() - 1;
  return n > 0 ? sheet.getRange(2, ID_COL, n, 1).getValues().map((r) => String(r[0])) : [];
}

/** e.id (client UUID) makes retries idempotent: an id already in the sheet is not added again. */
function addExpense(e, key) {
  requireEdit_(key);
  const cfg = requireConfig_();
  const c = cleanExpense_(cfg, e);
  const clientId = e && CLIENT_ID_RE.test(String(e.id)) ? String(e.id) : '';
  return withLock_(() => {
    const sheet = expenseSheet_();
    if (clientId && expenseIds_(sheet).includes(clientId)) return readExpenses_();
    sheet.appendRow(expenseRow_(c, Utilities.formatDate(new Date(), cfg.timezone, 'HH:mm'), clientId || Utilities.getUuid()));
    return readExpenses_();
  });
}

function updateExpense(id, e, key) {
  requireEdit_(key);
  const cfg = requireConfig_();
  const c = cleanExpense_(cfg, e);
  return withLock_(() => {
    const sheet = expenseSheet_();
    const i = expenseIds_(sheet).indexOf(String(id));
    if (i < 0) fail_('이미 삭제된 지출입니다');
    const time = cellText_(sheet.getRange(i + 2, 2).getValue()); // keep the original entry time
    sheet.getRange(i + 2, 1, 1, EXPENSE_COLS).setValues([expenseRow_(c, time, String(id))]);
    return readExpenses_();
  });
}

function deleteExpense(id, key) {
  requireEdit_(key);
  requireConfig_();
  return withLock_(() => {
    const sheet = expenseSheet_();
    const i = expenseIds_(sheet).indexOf(String(id));
    if (i < 0) fail_('이미 삭제된 지출입니다');
    sheet.deleteRow(i + 2);
    return readExpenses_();
  });
}

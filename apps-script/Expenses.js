// Expenses: one row per entry in the "지출 시트 이름" tab. Column order is fixed (reads are positional).
const EXPENSE_COLS = 9;
const expenseHeader_ = (cfg) => ['날짜', '시간', '분류', '내용', `금액(${cfg.localCurrency})`, `환산(${cfg.homeSymbol})`, '결제자', '결제수단', 'ID'];

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
      sheet.getRange('G:I').setNumberFormat('@');
      sheet.getRange('E:F').setNumberFormat('#,##0');
    }
    return { name: sheet.getName(), created, header };
  });
}

function expenseSheet_() {
  const name = config_().expenseSheet;
  return SpreadsheetApp.getActive().getSheetByName(name) || fail_(`시트 '${name}'가 없습니다`);
}

function readExpenses_() {
  const sheet = expenseSheet_();
  const n = sheet.getLastRow() - 1;
  if (n <= 0) return [];
  return sheet.getRange(2, 1, n, EXPENSE_COLS).getValues()
    .filter((r) => r[8])
    .map((r) => ({
      date: String(r[0]), time: String(r[1]), category: String(r[2]), item: String(r[3]),
      amount: Number(r[4]) || 0, home: Number(r[5]) || 0, payer: String(r[6]), method: String(r[7]), id: String(r[8]),
    }));
}

function addExpense(e) {
  const cfg = requireConfig_();
  const x = e || {};
  const amount = Number(x.amount);
  if (!DATE_RE.test(String(x.date)) || x.date < cfg.start || x.date > cfg.end) fail_('날짜가 여행 기간이 아닙니다');
  if (!Number.isInteger(amount) || amount <= 0 || amount > MAX_AMOUNT) fail_(`금액은 1${cfg.localCurrency} 이상의 정수여야 합니다`);
  if (!CATEGORIES.includes(x.category) || !cfg.payers.includes(x.payer) || !cfg.methods.includes(x.method)) fail_('분류/결제자/결제수단 값이 올바르지 않습니다');
  const item = String(x.item || '').trim().slice(0, 60) || x.category;
  return withLock_(() => {
    const time = Utilities.formatDate(new Date(), cfg.timezone, 'HH:mm');
    expenseSheet_().appendRow([x.date, time, x.category, item, amount, Math.round(amount * cfg.rate), x.payer, x.method, Utilities.getUuid()].map(safeCell_));
    return readExpenses_();
  });
}

function deleteExpense(id) {
  requireConfig_();
  return withLock_(() => {
    const sheet = expenseSheet_();
    const n = sheet.getLastRow() - 1;
    const ids = n > 0 ? sheet.getRange(2, 9, n, 1).getValues().map((r) => String(r[0])) : [];
    const i = ids.indexOf(String(id));
    if (i < 0) fail_('이미 삭제된 지출입니다');
    sheet.deleteRow(i + 2);
    return readExpenses_();
  });
}

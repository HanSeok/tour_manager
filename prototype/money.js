// Expense tab (prototype: localStorage; real: rows in the expense Google Sheet).
const PRE_TRIP = 'pre'; // date <select> value for pre-paid items (flights, hotels)
const homeOf = (e) => (Number.isFinite(e.home) && e.home > 0 ? e.home : Math.round(e.amount * CFG.rate * 10 ** (CFG.homeDecimals || 0)) / 10 ** (CFG.homeDecimals || 0));
const homeDecimals = () => CFG.homeDecimals || 0;
const draftDecimals = (d) => (d.currency === 'home' ? homeDecimals() : CFG.currencyDecimals || 0);
const isPreTrip = (date) => date < CFG.start;
const newId = () => (typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID()
  : 'xxxxxxxx-xxxx-4xxx-8xxx-xxxxxxxxxxxx'.replace(/x/g, () => Math.floor(Math.random() * 16).toString(16)));

/** "12a.345" → digits + at most one dot + `decimals` fraction digits */
function cleanAmount(text, decimals) {
  const [int, ...rest] = String(text).replace(/[^\d.]/g, '').split('.');
  const intPart = int.slice(0, MAX_AMOUNT_DIGITS);
  return decimals > 0 && rest.length ? `${intPart}.${rest.join('').slice(0, decimals)}` : intPart;
}

/** Pre-trip entries get a real date: today if before the trip, else the day before it. */
function preTripDate() {
  const t = todayTrip();
  if (t < CFG.start) return t;
  const d = new Date(`${CFG.start}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() - 1);
  return d.toISOString().slice(0, 10);
}

const allExpenses = () => [...state.expenses, ...state.outbox.map((e) => ({ ...e, pending: true }))];

function bars(rows) {
  const max = Math.max(1, ...rows.map(([, v]) => v));
  return `<div class="bars">${rows.map(([k, v]) => `<div class="bar"><span>${k}</span><i style="width:${(v / max) * 100}%"></i><span>${local(v)}</span></div>`).join('')}</div>`;
}

function ledgerRows(ex) {
  const groups = [...DATES].reverse().map((dt) => [`${md(dt)} (${WD[weekday(dt)]})`, ex.filter((e) => e.date === dt)]);
  const pre = ex.filter((e) => isPreTrip(e.date));
  return [...groups, ['여행 전 (사전 결제)', pre]].filter(([, rows]) => rows.length).map(([label, rows]) =>
    `<li class="day"><span>${label}</span><span>${local(sum(rows))}</span></li>` + rows.slice().reverse().map((e) =>
      `<li class="${e.id === state.editing ? 'editing' : ''}"><span class="tag">${esc(e.category)}</span>
        <button type="button" class="exp-item" data-exp-edit="${esc(e.id)}" ${e.pending ? 'disabled' : ''}>${esc(e.item)}<small>${esc(e.payer)} · ${esc(e.method)}${e.pending ? ' · <b class="wait">⏳ 전송 대기</b>' : ''}</small></button>
        <span class="amt">${local(e.amount)}${e.currency === 'home' ? `<small>${homeFmt(homeOf(e))} 입력</small>` : ''}</span>
        <span class="ctrl edit-only"><button data-exp-del="${esc(e.id)}" aria-label="삭제" ${e.pending ? 'disabled' : ''}>✕</button></span></li>`).join('')).join('');
}

function settlement(ex) {
  if (CFG.payers.length < 2 || !ex.length) return '';
  const d = CFG.currencyDecimals || 0;
  const bals = Settle.balances(ex, CFG.payers, d);
  const moves = Settle.transfers(bals, d);
  const rows = bals.map((b) => `<li><span>${esc(b.payer)}</span><span>${local(b.paid)} 냄</span><span class="${b.balance >= 0 ? 'plus' : 'minus'}">${b.balance >= 0 ? '+' : '−'}${local(Math.abs(Math.round(b.balance * 10 ** d) / 10 ** d))}</span></li>`).join('');
  const todo = moves.length
    ? moves.map((m) => `<li class="move"><span><b>${esc(m.from)}</b> → <b>${esc(m.to)}</b></span><span>${local(m.amount)} <small>≈ ${home(m.amount)}</small></span></li>`).join('')
    : '<li class="move">정산할 금액이 없습니다</li>';
  return `<h2>정산 · 1/${CFG.payers.length}씩 (1인 ${local(bals[0].share)})</h2><ul class="settle">${rows}${todo}</ul>`;
}

function renderMoney() {
  const d = state.draft, ex = allExpenses();
  const seg = (key, opts, labels = opts) => `<div class="seg">${opts.map((o, i) => `<button type="button" data-${key}="${esc(o)}" aria-pressed="${o === d[key]}">${esc(labels[i])}</button>`).join('')}</div>`;
  const ledger = ledgerRows(ex);
  const isHome = d.currency === 'home';
  const preview = !d.amount ? '' : isHome ? `≈ ${local(+d.amount / CFG.rate)}` : `≈ ${home(+d.amount)}`;
  const dateOpts = [[PRE_TRIP, '여행 전 (사전 결제)'], ...DATES.map((dt) => [dt, `${md(dt)} (${WD[weekday(dt)]})`])];
  const dateVal = isPreTrip(d.date) ? PRE_TRIP : d.date;
  const preRow = ex.some((e) => isPreTrip(e.date)) ? [['여행 전', sum(ex.filter((e) => isPreTrip(e.date)))]] : [];
  $('#tab-money').innerHTML = `
    <form class="quick edit-only" id="expForm" autocomplete="off">
      ${state.editing ? '<div class="editbar">✎ 지출 수정 중 <button type="button" class="mini" data-edit-cancel="1">취소</button></div>' : ''}
      <div class="amount"><span>${esc(isHome ? CFG.homeSymbol : CFG.localSymbol)}</span><input id="amt" inputmode="${draftDecimals(d) ? 'decimal' : 'numeric'}" placeholder="0" value="${esc(d.amount)}" aria-label="금액"><em id="amtKrw">${preview}</em></div>
      <div class="row" style="margin-top:8px">${seg('currency', ['local', 'home'], [`${CFG.localSymbol} ${CFG.localCurrency}`, `${CFG.homeSymbol} 환산 통화로 입력`])}</div>
      <div class="chips" style="margin-top:12px">${CATEGORIES.map((c) => `<button type="button" class="chip" data-category="${c}" aria-pressed="${c === d.category}">${c}</button>`).join('')}</div>
      <div class="grid">
        <input type="text" id="item" placeholder="내용 (예: 소바)" value="${esc(d.item)}" maxlength="60" aria-label="내용">
        <select id="expDate" aria-label="날짜">${dateOpts.map(([v, label]) => `<option value="${v}" ${v === dateVal ? 'selected' : ''}>${label}</option>`).join('')}</select>
        ${seg('payer', CFG.payers)}${seg('method', CFG.methods)}
      </div>
      <button class="submit" ${state.saving ? 'disabled' : ''}>${state.saving ? '저장 중…' : state.editing ? '수정 저장' : '기록하기'}</button>
      <div class="err" role="alert">${esc(state.error)}</div>
    </form>
    ${settlement(ex)}
    <h2>카테고리별</h2>${bars(CATEGORIES.map((c) => [c, sum(ex.filter((e) => e.category === c))]))}
    <h2>날짜별</h2>${bars([...preRow, ...DATES.map((dt) => [md(dt), sum(ex.filter((e) => e.date === dt))])])}
    <h2>내역${state.outbox.length ? ` · ⏳ ${state.outbox.length}건 전송 대기` : ''}</h2>${ledger ? `<ul class="ledger">${ledger}</ul>` : '<div class="empty">아직 지출이 없습니다</div>'}`;
}

function draftEntry() {
  const d = state.draft;
  const amount = Number(d.amount);
  const decimals = draftDecimals(d);
  if (!(amount > 0) || Math.abs(Math.round(amount * 10 ** decimals) - amount * 10 ** decimals) > 1e-6) return null; // 1.1*100 ≠ 110 in floats
  return { date: d.date, category: d.category, item: d.item.trim() || d.category, amount, payer: d.payer, method: d.method, currency: d.currency };
}

const clearedDraft = () => ({ ...state.draft, amount: '', item: '' });

/** Prototype: local only. Apps Script: send; on a network failure queue it in the outbox (sent again when online). */
function addExpense(e) {
  e.preventDefault();
  if (state.saving || !isEditable()) return;
  const entry = draftEntry();
  if (!entry) return setState({ error: `금액을 0보다 큰 숫자${draftDecimals(state.draft) ? `(소수 ${draftDecimals(state.draft)}자리까지)` : ''}로 입력하세요` });
  if (state.editing) return updateExpense(state.editing, entry);
  const row = { ...entry, id: newId(), home: entry.currency === 'home' ? entry.amount : homeOf({ amount: entry.amount }) };
  if (!IS_GAS) {
    const stored = entry.currency === 'home' ? { ...row, amount: entry.amount / CFG.rate } : row;
    return setState({ expenses: [...state.expenses, stored], draft: clearedDraft(), error: '' });
  }
  setState({ saving: true, error: '' });
  callWrite('addExpense', row)
    .then((list) => setState({ expenses: list, draft: clearedDraft(), saving: false }))
    .catch((err) => (isNetworkError(err)
      ? setState({ outbox: [...state.outbox, queuedRow(row)], draft: clearedDraft(), saving: false, error: '오프라인: 연결되면 자동으로 보냅니다' })
      : setState({ error: `저장 실패: ${err.message}`, saving: false })));
}

// Queued rows show the local amount even when typed in the home currency.
const queuedRow = (row) => (row.currency === 'home' ? { ...row, amount: row.amount / CFG.rate, sendAmount: row.amount } : row);

let isFlushing = false;
/** Sends queued expenses one by one. The client UUID makes a retry after a lost response harmless. */
function flushOutbox() {
  if (!IS_GAS || isFlushing || !state.outbox.length || !isEditable()) return;
  isFlushing = true;
  const [first] = state.outbox;
  const { sendAmount, ...row } = first;
  callWrite('addExpense', sendAmount ? { ...row, amount: sendAmount } : row)
    .then((list) => {
      setState({ expenses: list, outbox: state.outbox.filter((x) => x.id !== first.id), error: '' });
      isFlushing = false;
      flushOutbox();
    })
    .catch((err) => {
      isFlushing = false;
      if (!isNetworkError(err)) { // the server refused it (e.g. settings changed) → hand it back to the user
        setState({ outbox: state.outbox.filter((x) => x.id !== first.id) });
        showError(`대기 중이던 지출을 저장하지 못했습니다 (${first.item} ${local(first.amount)}): ${err.message}`);
      }
    });
}
window.addEventListener('online', () => flushOutbox());

function startEdit(id) {
  const e = state.expenses.find((x) => x.id === id);
  if (!e || !isEditable()) return;
  const isHome = e.currency === 'home';
  setState({
    editing: id, error: '',
    draft: { ...state.draft, amount: String(isHome ? homeOf(e) : e.amount), item: e.item, category: e.category, payer: e.payer, method: e.method, date: e.date, currency: isHome ? 'home' : 'local' },
  });
}

function updateExpense(id, entry) {
  const done = { editing: '', draft: clearedDraft(), saving: false, error: '' };
  if (!IS_GAS) {
    const amount = entry.currency === 'home' ? entry.amount / CFG.rate : entry.amount;
    const home = entry.currency === 'home' ? entry.amount : homeOf({ amount: entry.amount });
    return setState({ ...done, expenses: state.expenses.map((x) => (x.id === id ? { ...x, ...entry, amount, home } : x)) });
  }
  setState({ saving: true, error: '' });
  callWrite('updateExpense', id, entry)
    .then((list) => setState({ ...done, expenses: list }))
    .catch((err) => setState({ error: `수정 실패: ${err.message}`, saving: false }));
}

function deleteExpense(id) {
  if (!isEditable() || !confirm('이 지출을 삭제할까요?')) return;
  if (!IS_GAS) return setState({ expenses: state.expenses.filter((x) => String(x.id) !== id) });
  callWrite('deleteExpense', id)
    .then((list) => setState({ expenses: list }))
    .catch((err) => setState({ error: `삭제 실패: ${err.message}` }));
}

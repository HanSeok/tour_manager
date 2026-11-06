// Expense tab (prototype: localStorage; real: append to the expense Google Sheet).
function bars(rows) {
  const max = Math.max(1, ...rows.map(([, v]) => v));
  return `<div class="bars">${rows.map(([k, v]) => `<div class="bar"><span>${k}</span><i style="width:${(v / max) * 100}%"></i><span>${local(v)}</span></div>`).join('')}</div>`;
}

function renderMoney() {
  const d = state.draft, ex = state.expenses;
  const seg = (key, opts) => `<div class="seg">${opts.map((o) => `<button type="button" data-${key}="${o}" aria-pressed="${o === d[key]}">${o}</button>`).join('')}</div>`;
  const ledger = [...DATES].reverse().map((dt) => {
    const rows = ex.filter((e) => e.date === dt);
    if (!rows.length) return '';
    return `<li class="day"><span>${md(dt)} (${WD[weekday(dt)]})</span><span>${local(sum(rows))}</span></li>` + rows.slice().reverse().map((e) =>
      `<li><span class="tag">${esc(e.category)}</span><span>${esc(e.item)}<small>${esc(e.payer)} · ${esc(e.method)}</small></span><span class="amt">${local(e.amount)}</span>
        <span class="ctrl"><button data-exp-del="${esc(e.id)}" aria-label="삭제">✕</button></span></li>`).join('');
  }).join('');
  $('#tab-money').innerHTML = `
    <form class="quick" id="expForm" autocomplete="off">
      <div class="amount"><span>${esc(CFG.localSymbol)}</span><input id="amt" inputmode="numeric" placeholder="0" value="${esc(d.amount)}" aria-label="금액"><em id="amtKrw">${d.amount ? `≈ ${home(+d.amount)}` : ''}</em></div>
      <div class="chips" style="margin-top:12px">${CATEGORIES.map((c) => `<button type="button" class="chip" data-category="${c}" aria-pressed="${c === d.category}">${c}</button>`).join('')}</div>
      <div class="grid">
        <input type="text" id="item" placeholder="내용 (예: 소바)" value="${esc(d.item)}" maxlength="60">
        <select id="expDate">${DATES.map((dt) => `<option value="${dt}" ${dt === d.date ? 'selected' : ''}>${md(dt)} (${WD[weekday(dt)]})</option>`).join('')}</select>
        ${seg('payer', CFG.payers)}${seg('method', CFG.methods)}
      </div>
      <button class="submit" ${state.saving ? 'disabled' : ''}>${state.saving ? '저장 중…' : '기록하기'}</button>
      <div class="err" role="alert">${esc(state.error)}</div>
    </form>
    <h2>카테고리별</h2>${bars(CATEGORIES.map((c) => [c, sum(ex.filter((e) => e.category === c))]))}
    <h2>날짜별</h2>${bars(DATES.map((dt) => [md(dt), sum(ex.filter((e) => e.date === dt))]))}
    <h2>내역</h2>${ledger ? `<ul class="ledger">${ledger}</ul>` : '<div class="empty">아직 지출이 없습니다</div>'}`;
}

function addExpense(e) {
  e.preventDefault();
  if (state.saving) return;
  const amount = Number(state.draft.amount);
  if (!Number.isInteger(amount) || amount <= 0) return setState({ error: `금액을 1${CFG.localCurrency} 이상의 숫자로 입력하세요` });
  const item = state.draft.item.trim() || state.draft.category;
  const cleared = { ...state.draft, amount: '', item: '' };
  if (!IS_GAS) {
    return setState({ expenses: [...state.expenses, { id: String(Date.now()), ...state.draft, item, amount }], draft: cleared, error: '' });
  }
  setState({ saving: true, error: '' });
  callServer('addExpense', { ...state.draft, item, amount })
    .then((list) => setState({ expenses: list, draft: cleared, saving: false }))
    .catch((err) => setState({ error: `저장 실패: ${err.message}`, saving: false }));
}

function deleteExpense(id) {
  if (!confirm('이 지출을 삭제할까요?')) return;
  if (!IS_GAS) return setState({ expenses: state.expenses.filter((x) => String(x.id) !== id) });
  callServer('deleteExpense', id)
    .then((list) => setState({ expenses: list }))
    .catch((err) => setState({ error: `삭제 실패: ${err.message}` }));
}

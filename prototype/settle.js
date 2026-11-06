// Group settlement: who paid what, each person's equal share, and the transfers that square it up.
(function (root) {
  const EPS = 0.005;
  const roundTo = (v, d) => Math.round(v * 10 ** d) / 10 ** d;

  /** expenses [{payer, amount}] (local currency), payers [name] → [{payer, paid, share, balance}] (balance > 0 = is owed). */
  function balances(expenses, payers, decimals = 0) {
    const counted = expenses.filter((e) => payers.includes(e.payer));
    const total = counted.reduce((a, e) => a + e.amount, 0);
    const share = payers.length ? total / payers.length : 0;
    return payers.map((payer) => {
      const paid = counted.filter((e) => e.payer === payer).reduce((a, e) => a + e.amount, 0);
      return { payer, paid: roundTo(paid, decimals), share: roundTo(share, decimals), balance: paid - share };
    });
  }

  /** Greedy: largest debtor pays largest creditor → at most n-1 transfers. [{from, to, amount}] */
  function transfers(bals, decimals = 0) {
    const debt = bals.filter((b) => b.balance < -EPS).map((b) => ({ name: b.payer, v: -b.balance })).sort((a, b) => b.v - a.v);
    const cred = bals.filter((b) => b.balance > EPS).map((b) => ({ name: b.payer, v: b.balance })).sort((a, b) => b.v - a.v);
    const out = [];
    let i = 0, j = 0;
    while (i < debt.length && j < cred.length) {
      const x = Math.min(debt[i].v, cred[j].v);
      if (roundTo(x, decimals) > 0) out.push({ from: debt[i].name, to: cred[j].name, amount: roundTo(x, decimals) });
      debt[i] = { ...debt[i], v: debt[i].v - x };
      cred[j] = { ...cred[j], v: cred[j].v - x };
      if (debt[i].v <= EPS) i += 1;
      if (cred[j].v <= EPS) j += 1;
    }
    return out;
  }

  const api = { balances, transfers };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Settle = api;
})(typeof window !== 'undefined' ? window : globalThis);

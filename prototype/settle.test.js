const test = require('node:test');
const assert = require('node:assert');
const { balances, transfers } = require('./settle.js');

const spend = (...pairs) => pairs.map(([payer, amount]) => ({ payer, amount }));
// apply transfers to balances: debtor pays → balance up, creditor receives → balance down
const settleUp = (bals, moves) => moves.reduce((acc, m) => acc.map((b) => (
  b.payer === m.from ? { ...b, balance: b.balance + m.amount } : b.payer === m.to ? { ...b, balance: b.balance - m.amount } : b)), bals);

test('balances: one payer covered everything', () => {
  const b = balances(spend(['A', 300], ['B', 0]), ['A', 'B', 'C']);
  assert.deepStrictEqual(b.map((x) => [x.payer, x.paid, x.share, x.balance]), [['A', 300, 100, 200], ['B', 0, 100, -100], ['C', 0, 100, -100]]);
  assert.deepStrictEqual(transfers(b), [{ from: 'B', to: 'A', amount: 100 }, { from: 'C', to: 'A', amount: 100 }]);
});

test('balances ignore payers outside the list; empty list → []', () => {
  const b = balances(spend(['A', 100], ['손님', 900]), ['A', 'B']);
  assert.deepStrictEqual(b.map((x) => x.paid), [100, 0]);
  assert.deepStrictEqual(balances(spend(['A', 1]), []), []);
});

test('uneven split with cents: totals are conserved and everyone ends near 0', () => {
  const b = balances(spend(['A', 100]), ['A', 'B', 'C'], 2);
  assert.ok(Math.abs(b.reduce((a, x) => a + x.balance, 0)) < 1e-9);
  const after = settleUp(b, transfers(b, 2));
  assert.ok(after.every((x) => Math.abs(x.balance) < 0.01), JSON.stringify(after));
});

test('4 payers, mixed: at most n-1 transfers, all settled (whole units → residual < 1)', () => {
  const b = balances(spend(['A', 1200], ['B', 300], ['C', 50], ['D', 0], ['A', 450]), ['A', 'B', 'C', 'D']);
  const moves = transfers(b);
  assert.ok(moves.length <= 3, JSON.stringify(moves));
  assert.ok(settleUp(b, moves).every((x) => Math.abs(x.balance) < 1));
});

test('nothing to settle → no transfers', () => {
  assert.deepStrictEqual(transfers(balances(spend(['A', 50], ['B', 50]), ['A', 'B'])), []);
  assert.deepStrictEqual(transfers(balances([], ['A', 'B'])), []);
});

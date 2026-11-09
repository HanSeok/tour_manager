const test = require('node:test');
const assert = require('node:assert');
const S = require('./slots.js');

const v = (sid, placeId, result, at) => ({ sid, placeId, result, at });
const byId = { a: { id: 'a', name: 'A' }, b: { id: 'b', name: 'B' }, c: { id: 'c', name: 'C' } };

test('legacy {id, stayMin} reads as a one-candidate slot, already confirmed', () => {
  assert.deepStrictEqual(S.normalizeItem({ id: 'a', stayMin: 30 }), { sid: 'p:a', label: '', cands: ['a'], pick: 'a', stayMin: 30 });
  assert.strictEqual(S.normalizeItem({ ev: 'x@y:in' }), null);
  assert.strictEqual(S.normalizeItem(null), null);
});

test('normalize drops duplicate candidates and a pick that is not a candidate', () => {
  const s = S.normalizeItem({ sid: 's', cands: ['a', 'a', 'b'], pick: 'z' });
  assert.deepStrictEqual([s.cands, s.pick, s.stayMin], [['a', 'b'], null, 60]);
});

test('add / remove / confirm are pure and validated', () => {
  const s0 = S.newSlot({ sid: 's', label: '점심', cands: ['a'] });
  const s1 = S.addCandidate(s0, 'b');
  assert.deepStrictEqual([s0.cands, s1.cands], [['a'], ['a', 'b']]);
  assert.strictEqual(S.addCandidate(s1, 'b').cands.length, 2);          // duplicate ignored
  const s2 = S.confirm(s1, 'b');
  assert.strictEqual(s2.pick, 'b');
  assert.throws(() => S.confirm(s1, 'z'), /후보에 없는/);
  assert.strictEqual(S.removeCandidate(s2, 'b').pick, null);             // removing the pick un-confirms
  assert.strictEqual(S.removeCandidate(s2, 'a').pick, 'b');
  assert.strictEqual(S.clearPick(s2).pick, null);
  assert.strictEqual(s2.pick, 'b');                                      // input untouched
});

test('a slot holds at most MAX_CANDS candidates', () => {
  let s = S.newSlot({ sid: 's' });
  for (let i = 0; i < S.MAX_CANDS; i += 1) s = S.addCandidate(s, `p${i}`);
  assert.throws(() => S.addCandidate(s, 'one-more'), /후보는/);
  assert.strictEqual(S.newSlot({ sid: 's', cands: Array.from({ length: 20 }, (_, i) => `p${i}`) }).cands.length, S.MAX_CANDS);
});

test('closed / skipped rule a candidate out; a later visit to it lifts that', () => {
  const log = [v('s', 'a', 'closed', '12:00'), v('s', 'b', 'skipped', '12:10'), v('other', 'c', 'closed', '12:00')];
  assert.deepStrictEqual(S.excludedIds(log, 's').sort(), ['a', 'b']);
  assert.deepStrictEqual(S.excludedIds([...log, v('s', 'a', 'visited', '13:00')], 's'), ['b']);
});

test('same timestamp: the record written later wins', () => {
  assert.deepStrictEqual(S.excludedIds([v('s', 'a', 'visited', '12:00'), v('s', 'a', 'closed', '12:00')], 's'), ['a']);
});

test('statusOf: open → decided → visited / skipped', () => {
  const s = S.newSlot({ sid: 's', cands: ['a', 'b'] });
  assert.strictEqual(S.statusOf(s, []), 'open');
  const picked = S.confirm(s, 'a');
  assert.strictEqual(S.statusOf(picked, []), 'decided');
  assert.strictEqual(S.statusOf(picked, [v('s', 'a', 'closed', '1')]), 'open');            // closed → pick no longer valid
  assert.strictEqual(S.statusOf(picked, [v('s', 'a', 'closed', '1'), v('s', 'b', 'visited', '2')]), 'visited');
  assert.strictEqual(S.statusOf(s, [v('s', null, 'skipped', '1')]), 'skipped');
  assert.strictEqual(S.statusOf({ id: 'a', stayMin: 30 }, []), 'decided');                  // legacy item
});

test('toStop: single live candidate without a label is a plain stop; otherwise a slot stop', () => {
  assert.deepStrictEqual(S.toStop({ id: 'a', stayMin: 45 }, byId), { id: 'a', name: 'A', stayMin: 45 });
  const stop = S.toStop({ sid: 's', label: '점심', cands: ['a', 'b', 'zzz'], pick: 'b', stayMin: 50 }, byId, [v('s', 'a', 'closed', '1')]);
  assert.deepStrictEqual([stop.cands.map((c) => c.id), stop.pickId, stop.excluded, stop.stayMin, stop.label], [['a', 'b'], 'b', ['a'], 50, '점심']);
  const labelled = S.toStop({ sid: 's', label: '카페', cands: ['a'], pick: 'a' }, byId);
  assert.ok(labelled.cands, 'a labelled slot stays a slot');
  const ruledOut = S.toStop({ sid: 's', cands: ['a'] }, byId, [v('s', 'a', 'closed', '1')]);
  assert.deepStrictEqual([ruledOut.cands.length, ruledOut.excluded], [1, ['a']]);        // stays a slot so planRoute reports NO_CANDIDATE
  assert.strictEqual(S.toStop({ ev: 'x' }, byId), null);
});

// Slots: one block of the day with several candidate places, of which one is confirmed on the trip.
// Item shape (state.routes[date][i]):  {sid, label, cands:[placeId], pick:placeId|null, stayMin}
// Old items {id, stayMin} (one place) are still valid: they read as a slot with a single, already-confirmed candidate.
// Pure functions only: nothing here mutates its input.
(function (root) {
  const MAX_CANDS = 8; // a lunch has 3-4; more than this is a list, not a choice
  const DEFAULT_STAY = 60;
  const OUT_OF_PLAY = ['closed', 'skipped']; // results that rule a candidate out

  const isSlotItem = (item) => Boolean(item) && Array.isArray(item.cands);

  /** Canonical slot for either item shape. Calendar items ({ev}) are not slots → null. */
  function normalizeItem(item) {
    if (!item || item.ev) return null;
    if (isSlotItem(item)) {
      const cands = [...new Set(item.cands)];
      return { sid: item.sid, label: item.label || '', cands, pick: cands.includes(item.pick) ? item.pick : null, stayMin: item.stayMin ?? DEFAULT_STAY };
    }
    if (!item.id) return null;
    return { sid: `p:${item.id}`, label: '', cands: [item.id], pick: item.id, stayMin: item.stayMin ?? DEFAULT_STAY };
  }

  function newSlot({ sid, label = '', cands = [], stayMin = DEFAULT_STAY }) {
    if (!sid) throw new Error('sid required');
    return normalizeItem({ sid, label, cands: cands.slice(0, MAX_CANDS), pick: null, stayMin });
  }

  function addCandidate(item, id) {
    const s = normalizeItem(item);
    if (!s || !id || s.cands.includes(id)) return s;
    if (s.cands.length >= MAX_CANDS) throw new Error(`후보는 ${MAX_CANDS}곳까지 둘 수 있습니다`);
    return { ...s, cands: [...s.cands, id] };
  }

  /** Removing the confirmed pick un-confirms the slot. */
  function removeCandidate(item, id) {
    const s = normalizeItem(item);
    return { ...s, cands: s.cands.filter((c) => c !== id), pick: s.pick === id ? null : s.pick };
  }

  function confirm(item, id) {
    const s = normalizeItem(item);
    if (!s.cands.includes(id)) throw new Error('후보에 없는 장소입니다');
    return { ...s, pick: id };
  }

  const clearPick = (item) => ({ ...normalizeItem(item), pick: null });

  /** visits: [{sid, placeId|null, result: 'visited'|'closed'|'skipped', at}] (oldest first when `at` is equal).
   *  → Map placeId → latest result for that slot. A record without placeId (whole slot skipped) is under key ''. */
  function candidateResults(visits, sid) {
    const latest = new Map();
    (visits || []).forEach((v, i) => {
      if (v.sid !== sid) return;
      const key = v.placeId || '';
      const prev = latest.get(key);
      if (!prev || String(v.at || '') >= String(prev.at || '') ) latest.set(key, { result: v.result, at: v.at || '', i });
    });
    return latest;
  }

  /** Candidates ruled out by a "closed"/"skipped" record (a later "visited" for the same place lifts it). */
  function excludedIds(visits, sid) {
    return [...candidateResults(visits, sid)].filter(([k, r]) => k && OUT_OF_PLAY.includes(r.result)).map(([k]) => k);
  }

  /** 'visited' | 'skipped' | 'decided' | 'open' */
  function statusOf(item, visits) {
    const s = normalizeItem(item);
    const res = candidateResults(visits, s.sid);
    if ([...res.values()].some((r) => r.result === 'visited')) return 'visited';
    if (res.get('')?.result === 'skipped') return 'skipped';
    return s.pick && !excludedIds(visits, s.sid).includes(s.pick) ? 'decided' : 'open';
  }

  /** Slot → the stop planRoute() understands. Candidates that no longer exist (byId miss) are dropped.
   *  A slot with exactly one live candidate and no label becomes a plain stop, so old single-place days compute exactly as before. */
  function toStop(item, byId, visits) {
    const s = normalizeItem(item);
    if (!s) return null;
    const excluded = excludedIds(visits, s.sid);
    const cands = s.cands.filter((id) => byId[id]).map((id) => byId[id]);
    const live = cands.filter((c) => !excluded.includes(c.id));
    if (cands.length === 1 && live.length === 1 && !s.label) return { ...cands[0], stayMin: s.stayMin };
    return { cands, pickId: s.pick, excluded, stayMin: s.stayMin, label: s.label };
  }

  const api = { MAX_CANDS, isSlotItem, normalizeItem, newSlot, addCandidate, removeCandidate, confirm, clearPick, candidateResults, excludedIds, statusOf, toStop };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Slots = api;
})(typeof window !== 'undefined' ? window : globalThis);

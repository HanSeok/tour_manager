// Route feasibility calc (DESIGN.md LLD §3 planRoute). Times = minutes since 00:00.
// hours: {weekday(0=Sun): [[open, close], ...]} | null(unknown). [] = closed that day.
// ponytail: closing past midnight (e.g. 02:00) not modeled; add when a late-night bar is planned.
(function (root) {
  // Fallback estimate while the real time (Directions) is loading or unavailable: straight line × detour factor ÷ speed.
  const ESTIMATE = { driving: { factor: 1.4, kmh: 35 }, transit: { factor: 1.3, kmh: 18 }, walking: { factor: 1.3, kmh: 4.5 } };

  function haversineKm(a, b) {
    const R = 6371, rad = (d) => (d * Math.PI) / 180;
    const dLat = rad(b.lat - a.lat), dLng = rad(b.lng - a.lng);
    const x = Math.sin(dLat / 2) ** 2 + Math.cos(rad(a.lat)) * Math.cos(rad(b.lat)) * Math.sin(dLng / 2) ** 2;
    return 2 * R * Math.asin(Math.sqrt(x));
  }

  const MAX_SWAP_ROUNDS = 20; // ≤ 15 stops: converges in a few rounds

  function mockTravelMin(a, b, mode = 'driving') {
    const { factor, kmh } = ESTIMATE[mode] || ESTIMATE.driving;
    return Math.max(3, Math.round(((haversineKm(a, b) * factor) / kmh) * 60));
  }

  /** Nearest-neighbour visiting order from `start` → indices into `stops`. ponytail: greedy, not optimal; fine for ≤ 15 stops. */
  function nearestOrder(start, stops) {
    const left = stops.map((s, i) => i);
    const order = [];
    let at = start;
    while (left.length) {
      const k = left.reduce((best, i, n) => (haversineKm(at, stops[i]) < haversineKm(at, stops[left[best]]) ? n : best), 0);
      const [i] = left.splice(k, 1);
      order.push(i);
      at = stops[i];
    }
    return order;
  }

  // Calendar-fixed stop: fixed = {start, end|null, due}. Early → wait until start; after due → LATE.
  function planFixed(fixed, base) {
    const { start, end, due } = fixed;
    const lateMin = Math.max(0, base.arrive - due);
    return {
      ...base, lateMin,
      wait: Math.max(0, start - base.arrive),
      stayMin: end == null ? 0 : Math.max(0, end - Math.max(base.arrive, start)),
      slackMin: Math.max(0, due - base.arrive),
      warn: lateMin > 0 ? 'LATE' : null,
    };
  }

  function planStop(stop, arrive, weekday) {
    const base = { arrive, wait: 0, open: null, close: null, availMin: null, stayMin: stop.stayMin, warn: null };
    if (stop.fixed) return planFixed(stop.fixed, base);
    if (!stop.hours) return { ...base, warn: 'NO_HOURS' };
    const win = (stop.hours[weekday] || []).find(([, close]) => close > arrive);
    if (!win) return { ...base, stayMin: 0, warn: 'CLOSED' };
    const [open, close] = win;
    const availMin = close - Math.max(arrive, open);
    const isShort = availMin < stop.stayMin;
    return {
      ...base, open, close, availMin,
      wait: Math.max(0, open - arrive),
      stayMin: isShort ? availMin : stop.stayMin,
      warn: isShort ? 'SHORT' : null,
    };
  }

  // How good a candidate looks at its arrival time (lower is better): open > hours unknown > too short or a long wait > closed.
  // "Open" means open on arrival: a place that only opens after a long wait (break time, late opening) is no better than a short stay.
  const MAX_EASY_WAIT_MIN = 30;
  const tierOf = (warn, wait) => (warn === null ? (wait > MAX_EASY_WAIT_MIN ? 2 : 0) : warn === 'NO_HOURS' ? 1 : warn === 'SHORT' ? 2 : 3);

  /** A slot is one time block with several candidate places: {cands:[place], pickId, excluded:[id], stayMin, label}.
   *  The confirmed pick wins (even if it looks closed: a person decided). Otherwise the best candidate for the arrival
   *  time, ties by list order. Candidates are judged with `candTravelFn` (cheap), so a 4-way lunch choice does not
   *  cost 4 route lookups; only the chosen place goes through the real `travelFn`. */
  function resolveSlot(slot, prev, t, weekday, candTravelFn) {
    const skip = new Set(slot.excluded || []);
    const live = (slot.cands || []).filter((c) => !skip.has(c.id));
    const candidates = live.map((c, order) => {
      const p = planStop({ ...c, stayMin: slot.stayMin }, t + candTravelFn(prev, c, t), weekday);
      return { id: c.id, name: c.name, order, tier: tierOf(p.warn, p.wait), warn: p.warn, arrive: p.arrive, wait: p.wait, availMin: p.availMin, stayMin: p.stayMin };
    });
    const picked = slot.pickId == null ? null : live.find((c) => c.id === slot.pickId) || null;
    const bestIdx = candidates.reduce((b, e) => (e.tier < candidates[b].tier ? e.order : b), 0);
    return { place: picked || (live.length ? live[bestIdx] : null), tentative: !picked, candidates, excluded: [...skip] };
  }

  /** Stops may be plain places, calendar-fixed stops, or slots (`cands`). A slot with nothing to go to (a rest block,
   *  or every candidate ruled out) only takes time: no travel, position unchanged (`warn: 'NO_CANDIDATE'` when
   *  candidates existed but all were ruled out, so the UI can ask for a new one). */
  function planRoute({ weekday, startMin, start, stops, travelFn = mockTravelMin, candTravelFn = mockTravelMin }) {
    let t = startMin, prev = start, travelTotal = 0;
    const legs = stops.map((raw) => {
      let stop = raw, slot = null;
      if (raw.cands) {
        const r = resolveSlot(raw, prev, t, weekday, candTravelFn);
        slot = { label: raw.label || '', tentative: r.tentative, candidates: r.candidates, excluded: r.excluded };
        if (!r.place) {
          const depart = t + raw.stayMin;
          const leg = { id: null, travelMin: 0, arrive: t, wait: 0, open: null, close: null, availMin: null, stayMin: raw.stayMin, warn: r.excluded.length ? 'NO_CANDIDATE' : null, depart, slot };
          t = depart;
          return leg;
        }
        stop = { ...r.place, stayMin: raw.stayMin };
      }
      const travelMin = travelFn(prev, stop, t); // t = departure minute of this leg
      const leg = planStop(stop, t + travelMin, weekday);
      const depart = leg.arrive + leg.wait + leg.stayMin;
      travelTotal += travelMin;
      t = depart;
      prev = stop;
      return { id: stop.id, travelMin, ...leg, depart, ...(slot ? { slot } : {}) };
    });
    return { legs, endMin: t, travelTotal };
  }

  const pathKm = (start, pts) => pts.reduce((acc, p, i) => acc + haversineKm(i ? pts[i - 1] : start, p), 0);

  /** Shorter visiting order for `pts` (fixed[i] = calendar item that keeps its slot) → index order.
   *  Never longer than the current order: best of (current, nearest-neighbour), then pairwise swaps while they help. */
  function sortStops(start, pts, fixed = []) {
    const slots = pts.map((p, i) => i).filter((i) => !fixed[i]);
    const fill = (perm) => { let k = 0; return pts.map((p, i) => (fixed[i] ? i : perm[k++])); };
    const km = (order) => pathKm(start, order.map((i) => pts[i]));
    const nn = fill(nearestOrder(start, slots.map((i) => pts[i])).map((k) => slots[k]));
    let best = km(nn) < km(pts.map((p, i) => i)) ? nn : pts.map((p, i) => i);
    let improved = true;
    for (let round = 0; improved && round < MAX_SWAP_ROUNDS; round += 1) {
      improved = false;
      for (let a = 0; a < slots.length; a += 1) {
        for (let b = a + 1; b < slots.length; b += 1) {
          const next = best.map((v, i) => (i === slots[a] ? best[slots[b]] : i === slots[b] ? best[slots[a]] : v));
          if (km(next) < km(best) - 1e-9) { best = next; improved = true; }
        }
      }
    }
    return best;
  }

  const api = { haversineKm, mockTravelMin, nearestOrder, sortStops, planRoute };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Route = api;
})(typeof window !== 'undefined' ? window : globalThis);

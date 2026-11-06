// Route feasibility calc (DESIGN.md LLD §3 planRoute). Times = minutes since 00:00.
// hours: {weekday(0=Sun): [[open, close], ...]} | null(unknown). [] = closed that day.
// ponytail: closing past midnight (e.g. 02:00) not modeled; add when a late-night bar is planned.
(function (root) {
  const ROAD_FACTOR = 1.4; // straight line → road distance
  const AVG_KMH = 38;      // Okinawa local roads, mock only (real = Directions API)

  function haversineKm(a, b) {
    const R = 6371, rad = (d) => (d * Math.PI) / 180;
    const dLat = rad(b.lat - a.lat), dLng = rad(b.lng - a.lng);
    const x = Math.sin(dLat / 2) ** 2 + Math.cos(rad(a.lat)) * Math.cos(rad(b.lat)) * Math.sin(dLng / 2) ** 2;
    return 2 * R * Math.asin(Math.sqrt(x));
  }

  function mockTravelMin(a, b) {
    return Math.max(3, Math.round(((haversineKm(a, b) * ROAD_FACTOR) / AVG_KMH) * 60));
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

  function planRoute({ weekday, startMin, start, stops, travelFn = mockTravelMin }) {
    let t = startMin, prev = start, travelTotal = 0;
    const legs = stops.map((stop) => {
      const travelMin = travelFn(prev, stop, t); // t = departure minute of this leg
      const leg = planStop(stop, t + travelMin, weekday);
      const depart = leg.arrive + leg.wait + leg.stayMin;
      travelTotal += travelMin;
      t = depart;
      prev = stop;
      return { id: stop.id, travelMin, ...leg, depart };
    });
    return { legs, endMin: t, travelTotal };
  }

  const api = { haversineKm, mockTravelMin, planRoute };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Route = api;
})(typeof window !== 'undefined' ? window : globalThis);

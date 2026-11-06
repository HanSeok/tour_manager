// Calendar events → route constraints (① day start, ② deadlines, ③ fixed stops).
// Event: {id, date, start:'HH:MM', end:'HH:MM'|null, title, kind?:'route'}; placeOf(ev) → placeId|null.
// ponytail: kind is guessed from the title; switch to a per-event override if real titles don't fit.
(function (root) {
  const toMin = (s) => { const [a, b] = s.split(':').map(Number); return a * 60 + b; };
  const byTime = (a, b) => (a.date + a.start).localeCompare(b.date + b.start);

  const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Trip settings (설정 tab). Defaults keep the tests/prototype self-contained.
  let settings = { arrivalBufferMin: 60, flightLeadMin: 90, defaultStartMin: 9 * 60, dest: /(?!)/ };

  function configure({ destKeywords = [], arrivalBufferMin = 60, flightLeadMin = 90, defaultStart = '09:00' } = {}) {
    const words = destKeywords.map((w) => String(w).trim()).filter(Boolean).map(escapeRe);
    settings = { arrivalBufferMin, flightLeadMin, defaultStartMin: toMin(defaultStart), dest: words.length ? new RegExp(words.join('|'), 'i') : /(?!)/ };
  }

  function kindOf(ev) {
    if (ev.kind === 'route') return 'route';
    const sides = ev.title.split(/→|->/);
    if (sides.length === 2 && settings.dest.test(sides[1])) return 'flightIn';   // "인천 → 오키나와(나하)"
    if (sides.length === 2 && settings.dest.test(sides[0])) return 'flightOut';
    if (ev.title.includes('체크인')) return 'hotelIn';
    if (ev.title.includes('체크아웃')) return 'hotelOut';
    return 'fixed';
  }

  function dayStart(date, events, placeOf, airportId) {
    const today = events.filter((e) => e.date === date);
    const arrival = today.find((e) => kindOf(e) === 'flightIn');
    if (arrival) {
      return { placeId: airportId, min: toMin(arrival.end || arrival.start) + settings.arrivalBufferMin, eventId: arrival.id,
        reason: `${arrival.title} 도착 ${arrival.end || arrival.start} + 입국 ${settings.arrivalBufferMin}분` };
    }
    // latest check-in before today, even if unlinked (an older linked hotel would be the wrong one)
    const lastNight = events.filter((e) => kindOf(e) === 'hotelIn' && e.date < date).sort(byTime).pop();
    const checkout = today.find((e) => kindOf(e) === 'hotelOut');
    const hotelId = (checkout && placeOf(checkout)) || (lastNight && placeOf(lastNight)) || null;
    if (checkout) return { placeId: hotelId, min: toMin(checkout.start), eventId: checkout.id, reason: `${checkout.title} ${checkout.start}` };
    return { placeId: hotelId, min: settings.defaultStartMin, eventId: null, reason: hotelId ? '전날 숙소에서 출발' : '숙소 일정에 장소가 연결되지 않음' };
  }

  function fixedFor(kind, ev) {
    const s = toMin(ev.start);
    if (kind === 'hotelIn') return { start: s, end: null, due: Infinity, isEnd: true };
    if (kind === 'flightOut') return { start: s - settings.flightLeadMin, end: null, due: s - settings.flightLeadMin, isEnd: true };
    return { start: s, end: ev.end ? toMin(ev.end) : null, due: s, isEnd: false };
  }

  function fixedStops(date, events, placeOf, startEventId) {
    return events
      .filter((e) => e.date === date && e.id !== startEventId && placeOf(e) && !['flightIn', 'route'].includes(kindOf(e)))
      .map((e) => ({ ev: e.id, placeId: placeOf(e), title: e.title, kind: kindOf(e), fixed: fixedFor(kindOf(e), e) }))
      .sort((a, b) => a.fixed.start - b.fixed.start);
  }

  // stored: user order [{id, stayMin} | {ev}]. Drops stale events, appends new ones, keeps end items last.
  function mergeOrder(stored, fixed) {
    const byEv = Object.fromEntries(fixed.map((f) => [f.ev, f]));
    const kept = stored.filter((s) => !s.ev || byEv[s.ev]);
    const have = new Set(kept.filter((s) => s.ev).map((s) => s.ev));
    const all = [...kept, ...fixed.filter((f) => !have.has(f.ev)).map((f) => ({ ev: f.ev }))];
    const isEnd = (s) => Boolean(s.ev && byEv[s.ev].fixed.isEnd);
    return [...all.filter((s) => !isEnd(s)), ...all.filter(isEnd)];
  }

  // Raw calendar event {id,title,startDate,start,endDate,end,location,lat,lng} → per-day events.
  // A stay spanning dates (e.g. "…숙박" 12/12 14:00 → 12/16 11:00) becomes 체크인 + 체크아웃.
  function expandEvents(raw) {
    return raw.flatMap((e) => {
      const base = { location: e.location || '', lat: e.lat, lng: e.lng, placeId: e.placeId, ...(e.kind ? { kind: e.kind } : {}) };
      const isFlight = /→|->/.test(e.title);
      if (e.endDate && e.endDate !== e.startDate && !isFlight) {
        return [
          { ...base, id: `${e.id}:in`, date: e.startDate, start: e.start, end: null, title: `체크인 · ${e.title}` },
          { ...base, id: `${e.id}:out`, date: e.endDate, start: e.end, end: null, title: `체크아웃 · ${e.title}` },
        ];
      }
      return [{ ...base, id: e.id, date: e.startDate, start: e.start, end: e.end || null, title: e.title }];
    });
  }

  const api = { configure, settings: () => settings, expandEvents, kindOf, dayStart, fixedStops, mergeOrder, toMin };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Schedule = api;
})(typeof window !== 'undefined' ? window : globalThis);

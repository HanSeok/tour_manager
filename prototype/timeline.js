// Trip phase (계획/진행/완료) + per-day timeline that merges places, expenses and photos.
(function (root) {
  const toMin = (t) => {
    const m = /^(\d{1,2}):(\d{2})$/.exec(String(t || ''));
    return m ? +m[1] * 60 + +m[2] : null;
  };

  /** ISO dates compare as strings. before start → planning, start..end → live, after end → done. */
  function phaseOf(today, start, end) {
    if (today < start) return 'planning';
    if (today > end) return 'done';
    return 'live';
  }

  const KIND_ORDER = { place: 0, expense: 1, photo: 2 }; // same minute: where → what you paid → picture

  /** dates [ISO], items [{date, min|null, kind, ...}] → [{date, entries (time order, unknown time last), expenseTotal}] */
  function buildTimeline(dates, items) {
    return dates.map((date) => {
      const entries = items
        .filter((x) => x.date === date)
        .map((x, i) => ({ x, i }))
        .sort((a, b) => {
          const ma = a.x.min == null ? Infinity : a.x.min;
          const mb = b.x.min == null ? Infinity : b.x.min;
          if (ma !== mb) return ma < mb ? -1 : 1;
          const ka = KIND_ORDER[a.x.kind] ?? 9, kb = KIND_ORDER[b.x.kind] ?? 9;
          return ka !== kb ? ka - kb : a.i - b.i;
        })
        .map(({ x }) => x);
      const expenseTotal = entries.filter((e) => e.kind === 'expense').reduce((a, e) => a + (e.amount || 0), 0);
      return { date, entries, expenseTotal };
    });
  }

  const api = { phaseOf, buildTimeline, toMin };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Timeline = api;
})(typeof window !== 'undefined' ? window : globalThis);

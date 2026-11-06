// Opening-hours parsers → {weekday(0=Sun): [[openMin, closeMin]]} (close past midnight → +1440), null = unknown.
// Sources: Korean Google Maps hours text (영업시간 column of an imported My Maps CSV) + Places API (New) regularOpeningHours.periods.
(function (root) {
  const DAYS = { 일: 0, 월: 1, 화: 2, 수: 3, 목: 4, 금: 5, 토: 6 };
  const TIME = /(?:(오전|오후)\s*)?(\d{1,2}):(\d{2})/g;
  const DAY_MIN = 1440;

  const toMin = (meridiem, hh, mm) => ((Number(hh) % 12) + (meridiem === '오후' ? 12 : 0)) * 60 + Number(mm);

  function parseRange(text) {
    if (text.includes('24시간')) return [0, DAY_MIN];
    const t = [...text.matchAll(TIME)];
    if (t.length !== 2) return null;
    const o = toMin(t[0][1], t[0][2], t[0][3]);
    const c = toMin(t[1][1] || t[0][1], t[1][2], t[1][3]);
    return [o, c > o ? c : c + DAY_MIN];
  }

  /** "금요일 오전 11:00~오후 11:00 / 화요일 휴무일 / 월요일 24시간 영업" */
  function fromText(text) {
    if (!text || !String(text).trim()) return null;
    const hours = {};
    String(text).split(' / ').forEach((seg) => {
      const [day, ...rest] = seg.trim().split(' ');
      const wd = DAYS[day[0]];
      if (wd === undefined || !day.endsWith('요일')) return;
      const body = rest.join(' ');
      if (body.includes('휴무')) { hours[wd] = []; return; }
      const r = parseRange(body);
      if (r) hours[wd] = [r];
    });
    return Object.keys(hours).length ? hours : null;
  }

  /** Places API (New) periods: [{open:{day,hour,minute}, close?:{day,hour,minute}}]; day 0 = Sunday. */
  function fromPeriods(periods) {
    if (!Array.isArray(periods) || !periods.length) return null;
    if (periods.length === 1 && !periods[0].close) {
      return Object.fromEntries([0, 1, 2, 3, 4, 5, 6].map((d) => [d, [[0, DAY_MIN]]])); // open 24/7
    }
    const hours = Object.fromEntries([0, 1, 2, 3, 4, 5, 6].map((d) => [d, []]));
    periods.forEach(({ open, close }) => {
      if (!open || !close) return;
      const o = open.hour * 60 + (open.minute || 0);
      const dayDiff = (close.day - open.day + 7) % 7;
      const c = dayDiff * DAY_MIN + close.hour * 60 + (close.minute || 0);
      hours[open.day] = [...hours[open.day], [o, c > o ? c : c + DAY_MIN]].sort((a, b) => a[0] - b[0]);
    });
    return hours;
  }

  const api = { fromText, fromPeriods };
  if (typeof module !== 'undefined') module.exports = api;
  else root.Hours = api;
})(typeof window !== 'undefined' ? window : globalThis);

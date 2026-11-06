"""In-page fake of google.script.run + the Apps Script bundle as HtmlService serves it.
Shared by e2e_gas.py (tests) and docs/capture.py (README captures). Mirror every public server function here.
"""
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent

FAKE = r"""<script>
(() => {
  const pad = (n) => String(n).padStart(2, '0');
  const hm = (m) => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
  const seed = JSON.parse(sessionStorage.getItem('fakeSeed') || '{}');
  const editKey = sessionStorage.getItem('fakeEditKey') || '';
  const guard = (key) => { if (editKey && key !== editKey) throw new Error('보기 전용 링크입니다'); };
  const fake = window.fake = {
    calls: [],
    writes: [], // [fn, key] of every write call (read-only E2E: must stay empty)
    offline: false,
    savePatches: [],
    // shared "server" state lives in localStorage so two pages (two companions) hit the same store
    get state() { return JSON.parse(localStorage.getItem('__fakeState') || 'null') || seed.state || {}; },
    set state(v) { localStorage.setItem('__fakeState', JSON.stringify(v)); },
    get expenses() { return JSON.parse(localStorage.getItem('__fakeExpenses') || '[]'); },
    set expenses(v) { localStorage.setItem('__fakeExpenses', JSON.stringify(v)); },
    events: seed.events || [
      { id: 'f', title: '샘플항공 101 인천 → 오키나와(나하)', startDate: '2027-04-10', start: '11:25', endDate: '2027-04-10', end: '14:00', location: '', lat: null, lng: null },
      { id: 's', title: '샘플 호텔 숙박', startDate: '2027-04-10', start: '14:00', endDate: '2027-04-14', end: '11:00', location: 'Sample', lat: 26.3146, lng: 127.7568 },
    ],
    // default places = data.js samples in My Maps placemark shape (hours sent as Places-style periods)
    places: null,
    toPlacemark(p) {
      const periods = p.hours ? Object.entries(p.hours).flatMap(([d, ws]) => ws.map(([o, c]) => ({
        open: { day: +d, hour: Math.floor(o / 60), minute: o % 60 },
        close: { day: (+d + Math.floor(c / 1440)) % 7, hour: Math.floor((c % 1440) / 60), minute: c % 60 } }))) : [];
      return { name: p.name, lat: p.lat, lng: p.lng, folder: '', description: '', placeId: p.id, periods,
        primaryType: p.category.includes('공항') ? 'airport' : '', data: { '분류': p.layer, '카테고리': p.category, place_id: p.id } };
    },
    bootstrap() {
      if (seed.bootFail) throw new Error('NetworkError: Connection failure due to HTTP 0');
      const config = seed.config || { ...SAMPLE_CONFIG, title: '테스트 여행', ...(seed.configPatch || {}) };
      const version = { current: '2.0.0', latest: seed.latest || '2.0.0' };
      if (config.missing.length || config.invalid.length) return { config, version, checks: this.diagnose() };
      const places = seed.places || { places: SAMPLE_PLACES.map((p) => this.toPlacemark(p)), notes: [] };
      return { config, version, sheet: { name: '여행_지출', created: false, header: [] }, events: this.events, expenses: this.expenses, state: this.state, places };
    },
    refresh() { return { state: this.state, expenses: this.expenses, events: this.events }; },
    diagnose() { return [{ label: '설정 탭', ok: false, detail: '빈 항목 2개' }, { label: 'My Maps 공유', ok: true, detail: '읽기 가능' }, { label: '편집 키', ok: null, detail: '없음 (선택)' }]; },
    write(fn, key) { this.writes.push([fn, key]); guard(key); if (this.offline) throw new Error('NetworkError: Connection failure due to HTTP 0'); },
    row(e, id) {
      const home = e.currency === 'home';
      return { ...e, id, time: '10:00', amount: home ? e.amount / 9.3 : e.amount, home: home ? e.amount : Math.round(e.amount * 9.3), currency: home ? 'home' : 'local' };
    },
    addExpense(e, key) {
      this.write('addExpense', key);
      if (e.id && this.expenses.some((x) => x.id === e.id)) return this.expenses; // idempotent retry
      this.expenses = [...this.expenses, this.row(e, e.id || `id${this.expenses.length}`)];
      return this.expenses;
    },
    updateExpense(id, e, key) {
      this.write('updateExpense', key);
      if (!this.expenses.some((x) => x.id === id)) throw new Error('이미 삭제된 지출입니다');
      this.expenses = this.expenses.map((x) => (x.id === id ? this.row(e, id) : x));
      return this.expenses;
    },
    deleteExpense(id, key) { this.write('deleteExpense', key); this.expenses = this.expenses.filter((x) => x.id !== id); return this.expenses; },
    saveState(k, patch, key) {
      this.write('saveState', key);
      this.savePatches.push([k, patch]);
      const cur = this.state[k] || {};
      const merged = Object.entries(patch).reduce((a, [sk, v]) => {
        const { [sk]: _, ...rest } = a;
        return v === null ? rest : { ...rest, [sk]: v };
      }, cur);
      this.state = { ...this.state, [k]: merged };
      return merged;
    },
    travelPairs: [],
    getTravelTimes(pairs) {
      this.travelPairs = [...this.travelPairs, ...pairs];
      // realisticTravel (captures): plausible minutes from distance instead of a constant
      if (seed.omitTravel) return {}; // a server that answers nothing (e.g. keys outside the trip box in v2.0.0-rc)
      return Object.fromEntries(pairs.map((p) => [p.key, seed.realisticTravel ? Math.round(Route.mockTravelMin(p.from, p.to) * 1.15) : 42]));
    },
    registerRoute(date, items, key) {
      this.write('registerRoute', key);
      this.events = [...this.events.filter((e) => !(e.kind === 'route' && e.startDate === date)),
        ...items.map((it, i) => ({ id: `r${date}${i}`, title: it.title, kind: 'route', startDate: date, start: hm(it.startMin), endDate: date, end: hm(it.endMin), placeId: it.placeId }))];
      return this.events;
    },
    unregisterRoute(date, key) { this.write('unregisterRoute', key); this.events = this.events.filter((e) => !(e.kind === 'route' && e.startDate === date)); return this.events; },
  };
  const runner = () => {
    const h = {};
    const r = new Proxy({}, { get(_, m) {
      if (m === 'withSuccessHandler') return (f) => { h.s = f; return r; };
      if (m === 'withFailureHandler') return (f) => { h.f = f; return r; };
      return (...args) => setTimeout(() => {
        fake.calls.push(m);
        try { h.s(JSON.parse(JSON.stringify(fake[m](...args)))); } catch (e) { h.f(e); }
      }, m === 'bootstrap' && seed.bootDelay ? seed.bootDelay : 30);
    } });
    return r;
  };
  window.google = { script: { run: new Proxy({}, { get: (_, k) => runner()[k] }) } };
})();
</script>"""


def bundle():
    index = (ROOT / 'index.html').read_text(encoding='utf-8')
    html = re.sub(r"<\?!= include\('(\w+)'\) \?>", lambda m: (ROOT / f'{m.group(1)}.html').read_text(encoding='utf-8'), index)
    access = "JSON.parse(sessionStorage.getItem('fakeAccess') || '{\"required\":false,\"key\":\"\"}')"
    html = html.replace('<?!= editAccess ?>', access)
    return html.replace('<head>', '<head>' + FAKE, 1)

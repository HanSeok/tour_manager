"""Apps Script-mode E2E: bundle (as HtmlService serves it) + in-memory fake server for google.script.run.
Run after build.py:  python3 apps-script/e2e_gas.py [shot_dir]
"""
import functools
import http.server
import pathlib
import re
import sys
import tempfile
import threading
from playwright.sync_api import sync_playwright

ROOT = pathlib.Path(__file__).resolve().parent
SHOTS = sys.argv[1] if len(sys.argv) > 1 else '.'

FAKE = r"""<script>
(() => {
  const pad = (n) => String(n).padStart(2, '0');
  const hm = (m) => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
  const seed = JSON.parse(sessionStorage.getItem('fakeSeed') || '{}');
  const fake = window.fake = {
    calls: [],
    expenses: [],
    state: seed.state || {},
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
      const config = seed.config || { ...SAMPLE_CONFIG, title: '테스트 여행' };
      if (config.missing.length || config.invalid.length) return { config };
      const places = seed.places || { places: SAMPLE_PLACES.map((p) => this.toPlacemark(p)), notes: [] };
      return { config, sheet: { name: '여행_지출', created: false, header: [] }, events: this.events, expenses: this.expenses, state: this.state, places };
    },
    addExpense(e) { this.expenses = [...this.expenses, { ...e, id: `id${this.expenses.length}`, time: '10:00', krw: Math.round(e.amount * 9.3) }]; return this.expenses; },
    deleteExpense(id) { this.expenses = this.expenses.filter((x) => x.id !== id); return this.expenses; },
    saveState(k, v) { this.state = { ...this.state, [k]: v }; return true; },
    travelPairs: [],
    getTravelTimes(pairs) { this.travelPairs = [...this.travelPairs, ...pairs]; return Object.fromEntries(pairs.map((p) => [p.key, 42])); },
    registerRoute(date, items) {
      this.events = [...this.events.filter((e) => !(e.kind === 'route' && e.startDate === date)),
        ...items.map((it, i) => ({ id: `r${date}${i}`, title: it.title, kind: 'route', startDate: date, start: hm(it.startMin), endDate: date, end: hm(it.endMin), placeId: it.placeId }))];
      return this.events;
    },
    unregisterRoute(date) { this.events = this.events.filter((e) => !(e.kind === 'route' && e.startDate === date)); return this.events; },
  };
  const runner = () => {
    const h = {};
    const r = new Proxy({}, { get(_, m) {
      if (m === 'withSuccessHandler') return (f) => { h.s = f; return r; };
      if (m === 'withFailureHandler') return (f) => { h.f = f; return r; };
      return (...args) => setTimeout(() => {
        fake.calls.push(m);
        try { h.s(JSON.parse(JSON.stringify(fake[m](...args)))); } catch (e) { h.f(e); }
      }, 30);
    } });
    return r;
  };
  window.google = { script: { run: new Proxy({}, { get: (_, k) => runner()[k] }) } };
})();
</script>"""


def bundle():
    index = (ROOT / 'index.html').read_text(encoding='utf-8')
    html = re.sub(r"<\?!= include\('(\w+)'\) \?>", lambda m: (ROOT / f'{m.group(1)}.html').read_text(encoding='utf-8'), index)
    return html.replace('<head>', '<head>' + FAKE, 1)


def check(cond, msg):
    print(('PASS ' if cond else 'FAIL ') + msg)
    check.failed = check.failed or not cond


check.failed = False

with tempfile.TemporaryDirectory() as tmp:
    pathlib.Path(tmp, 'index.html').write_text(bundle(), encoding='utf-8')
    srv = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(http.server.SimpleHTTPRequestHandler, directory=tmp))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    url = f'http://127.0.0.1:{srv.server_address[1]}/index.html'
    with sync_playwright() as pw:
        browser = pw.chromium.launch(executable_path='/usr/bin/google-chrome', args=['--no-sandbox'])
        page = browser.new_page(viewport={'width': 1400, 'height': 1000})
        errors = []
        page.on('pageerror', lambda e: errors.append(str(e)))
        page.on('dialog', lambda d: d.accept())
        page.goto(url)
        page.evaluate('localStorage.clear()')
        page.reload()
        page.wait_for_function("document.querySelector('.mock').innerText.includes('공유 저장')")
        check(True, 'bootstrap loaded')

        pid = page.evaluate("PLACES.find(p => p.name.includes('츄라우미 수족관')).id")
        page.click(f'#tab-route .chips.pick [data-add="{pid}"]')
        page.wait_for_function("[...document.querySelectorAll('.drive')].every(d => !d.innerText.includes('추정'))")
        drives = page.locator('.drive').all_inner_texts()
        check(all('42분' in d for d in drives) and all('지도앱' in d for d in drives), f'real travel times shown: {drives}')
        first = page.evaluate("fake.travelPairs.find(p => p.key.includes('@2027-04-10+900'))")
        check(first is not None and first['depart'] == '2027-04-10T06:30:00.000Z', f'first leg asks traffic for 12/12 15:30 JST: {first and first["depart"]}')
        page.wait_for_timeout(600)
        check('routes' in page.evaluate('fake.state') and page.evaluate('fake.state.routes["2027-04-10"].length') == 2, 'route saved to server state (stop + check-in)')

        page.click('[data-register]')
        page.wait_for_selector('[data-unregister]')
        check(page.evaluate("fake.events.filter(e => e.kind === 'route').length") == 1, 'register creates 1 calendar event')
        page.fill('[data-stay="0"]', '30')
        page.dispatch_event('[data-stay="0"]', 'change')
        check('다시 등록' in page.inner_text('[data-register]'), 'edit → re-register prompt')
        page.click('[data-register]')
        page.wait_for_selector('[data-unregister]')
        check(page.evaluate("fake.events.filter(e => e.kind === 'route').length") == 1, 're-register replaces (still 1 event)')
        page.click('[data-unregister]')
        page.wait_for_selector('[data-register]')
        check(page.evaluate("fake.events.filter(e => e.kind === 'route').length") == 0, 'unregister removes calendar event')

        page.click('[data-tab="money"]')
        page.fill('#amt', '2500')
        page.click('#expForm .submit')
        page.wait_for_function("fake.expenses.length === 1 && document.querySelectorAll('.ledger [data-exp-del]').length === 1")
        check(page.evaluate('fake.expenses[0].amount') == 2500, 'expense saved to server')
        page.click('.ledger [data-exp-del]')
        page.wait_for_function('fake.expenses.length === 0')
        check(page.locator('.ledger').count() == 0, 'expense deleted on server and UI')
        page.screenshot(path=f'{SHOTS}/gas.png')

        # server state wins on a fresh device
        other = browser.new_page()
        other.goto(url)
        other.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify({state: {routes: {'2027-04-11': [{id: '%s', stayMin: 45}]}}})); localStorage.clear()" % pid)
        other.reload()
        other.wait_for_function("document.querySelector('.mock').innerText.includes('공유 저장')")
        other.click('[data-date="2027-04-11"]')
        check('츄라우미' in other.inner_text('#tab-route .timeline'), 'another device sees the shared route')
        # My Maps unreadable → warning + empty-state message (no bundled fallback list)
        err = browser.new_page()
        err.goto(url)
        err.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify({places: {places: [], error: 'My Maps를 읽지 못했습니다 (HTTP 403)', notes: []}})); localStorage.clear()")
        err.reload()
        err.wait_for_function("document.querySelector('.mock').innerText.includes('My Maps를 읽지 못했습니다')")
        check('아직 장소가 없습니다' in err.inner_text('#tab-route'), 'My Maps error → warning + empty state')

        # My Maps places replace the list; imported row keeps CSV hours, new pin gets Places API periods
        mm = {'places': [
            {'name': '스시로 자탄점', 'lat': 26.3235, 'lng': 127.7562, 'folder': '오키나와_MyMaps_가져오기.csv', 'description': '',
             'data': {'분류': '식당', '카테고리': '회전초밥집', '영업시간': '토요일 오전 10:30~오후 11:00', 'place_id': 'ChIJsushi'}},
            {'name': '새로 찍은 카페', 'lat': 26.33, 'lng': 127.76, 'folder': '레이어 1', 'description': '', 'data': {},
             'placeId': 'ChIJnew', 'primaryType': 'cafe', 'periods': [{'open': {'day': 6, 'hour': 9, 'minute': 0}, 'close': {'day': 6, 'hour': 17, 'minute': 0}}]},
        ], 'notes': []}
        third = browser.new_page()
        third.on('pageerror', lambda e: errors.append(str(e)))
        third.goto(url)
        third.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify(%s)); localStorage.clear()" % __import__('json').dumps({'places': mm}))
        third.reload()
        third.wait_for_function("document.querySelector('.mock').innerText.includes('My Maps 장소 2곳')")
        chips = third.locator('#tab-route .chips.pick .chip').all_inner_texts()
        check(len(chips) == 2 and '새로 찍은 카페' in chips[1], f'place list comes from My Maps: {chips}')
        check(third.evaluate("byId['ChIJnew'].hours[6][0].join('-')") == '540-1020', 'new pin hours from Places API periods')
        check(third.evaluate("byId['ChIJsushi'].hours[6][0].join('-')") == '630-1380', 'imported pin hours from CSV column')
        check(third.evaluate("byId['ChIJnew'].layer") == '식당', 'new pin layer from Places primaryType (cafe → 식당)')
        # non-Japan trip: Bangkok config (UTC+7, baht, different keywords) — proves nothing is Okinawa-bound
        bkk = {
            'config': {'title': '방콕 여행', 'subtitle': '', 'start': '2027-03-01', 'end': '2027-03-04', 'days': 4,
                       'timezone': 'Asia/Bangkok', 'offset': '+07:00', 'destKeywords': ['방콕', 'BKK'],
                       'localCurrency': '바트', 'localSymbol': '฿', 'homeSymbol': '₩', 'rate': 40,
                       'payers': ['A', 'B'], 'methods': ['카드'], 'expenseSheet': '방콕_지출',
                       'arrivalBufferMin': 60, 'flightLeadMin': 120, 'defaultStart': '08:00', 'missing': [], 'invalid': [], 'hasMyMaps': True},
            'places': {'places': [
                {'name': '수완나품 공항', 'lat': 13.69, 'lng': 100.75, 'folder': '', 'description': '', 'data': {}, 'placeId': 'bkk-air', 'primaryType': 'airport', 'periods': []},
                {'name': '왓 아룬', 'lat': 13.7437, 'lng': 100.4888, 'folder': '', 'description': '', 'data': {}, 'placeId': 'bkk-arun', 'primaryType': 'tourist_attraction',
                 'periods': [{'open': {'day': d, 'hour': 8, 'minute': 0}, 'close': {'day': d, 'hour': 18, 'minute': 0}} for d in range(7)]},
            ], 'notes': []},
            'events': [{'id': 'tg', 'title': 'TG659 인천 → 방콕(BKK)', 'startDate': '2027-03-01', 'start': '10:00', 'endDate': '2027-03-01', 'end': '14:00', 'location': '', 'lat': None, 'lng': None}],
        }
        b = browser.new_page()
        b.on('pageerror', lambda e: errors.append(str(e)))
        b.goto(url)
        b.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify(%s)); localStorage.clear()" % __import__('json').dumps(bkk, ensure_ascii=False))
        b.reload()
        b.wait_for_function("document.querySelector('.mock').innerText.includes('My Maps 장소 2곳')")
        check('방콕 여행' in b.inner_text('#tripTitle') and '3박 4일' in b.inner_text('#tripTitle'), 'title + nights from config')
        check(b.locator('.dates button').count() == 4, '4 trip days')
        check(b.input_value('#startTime') == '15:00' and '입국' in b.inner_text('#tab-route .timeline li:first-child .meta'), 'BKK arrival (keyword 방콕) → start 15:00 at airport')
        b.click('#tab-route .chips.pick [data-add="bkk-arun"]')
        b.wait_for_function("fake.travelPairs.length > 0")
        dep = b.evaluate("fake.travelPairs[0].depart")
        check(dep == '2027-03-01T08:30:00.000Z', f'traffic departure uses UTC+7: {dep}')
        b.click('[data-tab="money"]')
        check('฿' in b.inner_text('#expForm') and [t.strip() for t in b.locator('[data-payer]').all_inner_texts()] == ['A', 'B'], 'currency symbol + payers from config')

        # unconfigured sheet → setup checklist instead of the dashboard
        u = browser.new_page()
        u.goto(url)
        u.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify({config: {missing: ['여행 제목', 'My Maps 링크'], invalid: ['시작일: YYYY-MM-DD 형식이 아닙니다']}}))")
        u.reload()
        u.wait_for_selector('.setup')
        txt = u.inner_text('.setup')
        check('여행 제목' in txt and 'My Maps 링크' in txt and '시작일' in txt, 'unconfigured → setup checklist')
        check(not errors, f'no page errors {errors}')
        browser.close()
    srv.shutdown()

sys.exit(1 if check.failed else 0)

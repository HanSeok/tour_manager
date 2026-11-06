"""Apps Script-mode E2E: bundle (as HtmlService serves it) + in-memory fake server for google.script.run.
Run after build.py:  python3 apps-script/e2e_gas.py [shot_dir]
"""
import functools
import json
import http.server
import pathlib
import re
import sys
import tempfile
import threading
from playwright.sync_api import sync_playwright

from fake_gas import bundle

ROOT = pathlib.Path(__file__).resolve().parent
SHOTS = sys.argv[1] if len(sys.argv) > 1 else '.'

def server_functions():
    """Public Apps Script functions the page can call (trailing _ = private; menu/entry points excluded)."""
    names = set()
    for f in ROOT.glob('*.js'):
        names |= set(re.findall(r'^function ([A-Za-z]\w*[^_\W])\(', f.read_text(encoding='utf-8'), re.M))
    return names - {'doGet', 'include', 'onOpen', 'setup', 'showEditLinks', 'rotateEditKey', 'createTripCalendar'}


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
        check('자가 점검' in txt and 'My Maps 공유' in txt, 'unconfigured → self-check results shown')

        def fresh(seed=None, access=None, edit_key='', context=None):
            pg = context.new_page() if context else browser.new_page(viewport={'width': 1400, 'height': 1000})
            pg.on('pageerror', lambda e: errors.append(str(e)))
            pg.on('dialog', lambda d: d.accept())
            pg.goto(url)
            pg.evaluate("([s, a, k, clear]) => { if (clear) localStorage.clear(); sessionStorage.clear(); sessionStorage.setItem('fakeSeed', s);"
                        " if (a) sessionStorage.setItem('fakeAccess', a); if (k) sessionStorage.setItem('fakeEditKey', k); }",
                        [json.dumps(seed or {}, ensure_ascii=False), json.dumps(access) if access else '', edit_key, fresh.clear])
            pg.reload()
            pg.wait_for_function("document.querySelector('.mock').innerText.match(/공유 저장|보기 전용/)")
            return pg

        fresh.clear = True
        # drift guard: every public server function exists on the fake
        probe = fresh()
        missing = server_functions() - set(probe.evaluate('Object.keys(fake)'))
        check(not missing, f'fake mirrors every server function (missing: {sorted(missing)})')

        # A1: two companions edit different days → both survive on the server. One context = one shared fake server (localStorage).
        shared_ctx = browser.new_context(viewport={'width': 1400, 'height': 1000})
        a = fresh(context=shared_ctx)
        fresh.clear = False
        b = fresh(context=shared_ctx)
        ids = a.evaluate("PLACES.filter(p => p.layer === '관광').map(p => p.id)")
        a.click('[data-date="2027-04-11"]')
        a.click(f'#tab-route .chips.pick [data-add="{ids[0]}"]')
        a.wait_for_function("fake.state.routes && fake.state.routes['2027-04-11']")
        # the fake server is shared through localStorage (async across tabs): wait until B's server copy has A's day.
        # B's *page* still has the old, empty routes — v1 sent the whole object and erased A's day here.
        b.wait_for_function("fake.state.routes && fake.state.routes['2027-04-11']")
        check(b.evaluate("!state.routes['2027-04-11']"), "B's page has not seen A's change yet")
        b.click('[data-date="2027-04-12"]')
        b.click(f'#tab-route .chips.pick [data-add="{ids[1]}"]')
        a.wait_for_timeout(800)
        b.wait_for_function('fake.savePatches.length > 0')
        days = sorted(b.evaluate('Object.keys(fake.state.routes)'))
        check(days == ['2027-04-11', '2027-04-12'], f"stale companion's save keeps the other day (v1 overwrote it): {days}")
        check(all(len(k) == 1 for k in a.evaluate('fake.savePatches.map(p => Object.keys(p[1]))')), 'only the changed day is sent')
        b.evaluate('refreshShared()')
        b.wait_for_timeout(200)
        b.click('[data-date="2027-04-11"]')
        check(a.evaluate(f"PLACES.find(p => p.id === '{ids[0]}').name") in b.inner_text('#tab-route .timeline'), "refresh shows the companion's day")
        # B8: nearest-first sort keeps the same stops and never makes the day longer
        for pid_ in ids[1:4]:
            b.click(f'#tab-route .chips.pick [data-add="{pid_}"]')
        # path length in km (travelTotal mixes the fake's fixed 42-min answers with estimates for new legs)
        path_km = "(() => { const p = planFor(state.date); return [p.stops.map(s => s.id), p.stops.map((s, i) => s.fixed ? i : -1).filter(i => i >= 0), [p.start, ...p.stops].reduce((a, s, i, all) => i ? a + Route.haversineKm(all[i - 1], s) : 0, 0)]; })()"
        before = b.evaluate(path_km)
        b.click('[data-sort]')
        after = b.evaluate(path_km)
        check(sorted(before[0]) == sorted(after[0]) and before[1] == after[1] and after[2] <= before[2] + 1e-9,
              f"sort: same stops, calendar items keep their slots {after[1]}, path {before[2]:.1f} → {after[2]:.1f} km")
        b.close()

        # A4: view-only link → no edit controls, no write calls
        fresh.clear = True
        key = 'a' * 32
        r = fresh(access={'required': True, 'key': ''}, edit_key=key)
        check(r.is_visible('#roNote') and '보기 전용' in r.inner_text('#roNote'), 'view-only notice shown')
        r.click('[data-tab="money"]')
        check(not r.is_visible('#expForm') and r.locator('.chips.pick').count() == 0 or not r.is_visible('.chips.pick'), 'view-only: no expense form / place picker')
        r.evaluate("handleButton({add: PLACES[2].id}); handleButton({register: '1'})")
        r.wait_for_timeout(600)
        check(r.evaluate('fake.writes.length') == 0, 'view-only: zero server writes')
        r.close()
        w = fresh(access={'required': True, 'key': key}, edit_key=key)
        w.click('[data-tab="money"]')
        w.fill('#amt', '800')
        w.click('#expForm .submit')
        w.wait_for_function('fake.expenses.length === 1')
        check(all(k == key for _, k in w.evaluate('fake.writes')), 'edit link: every write carries the key')
        w.close()

        # C2: a refused write leaves a persistent, closable error
        t = fresh(access={'required': False, 'key': ''}, edit_key=key)
        t.click('[data-tab="money"]')
        t.fill('#amt', '100')
        t.click('#expForm .submit')
        t.wait_for_selector('.err:has-text("보기 전용")')
        t.click('[data-tab="route"]')
        t.click(f'#tab-route .chips.pick [data-add="{ids[0]}"]')
        t.wait_for_selector('#toast .toast-item')
        check('동선 저장 실패' in t.inner_text('#toast'), 'failed save → toast')
        t.click('[data-tab="plan"]')
        check(t.locator('#toast .toast-item').count() == 1, 'toast survives later status updates')
        t.click('#toast [data-toast-close]')
        check(t.locator('#toast .toast-item').count() == 0, 'toast closes')
        t.close()

        # B5: offline → queued → sent once when back online
        o = fresh()
        o.click('[data-tab="money"]')
        o.evaluate('fake.offline = true')
        o.fill('#amt', '1500')
        o.click('#expForm .submit')
        o.wait_for_selector('text=전송 대기')
        check(o.evaluate('fake.expenses.length') == 0 and o.evaluate('state.outbox.length') == 1, 'offline expense queued')
        check(o.evaluate("JSON.parse(localStorage.getItem('trip.outbox')).length") == 1, 'outbox survives a reload (localStorage)')
        o.evaluate("fake.offline = false; window.dispatchEvent(new Event('online'))")
        o.wait_for_function('fake.expenses.length === 1 && state.outbox.length === 0')
        o.evaluate("fake.addExpense({ ...fake.expenses[0] }, '')")
        check(o.evaluate('fake.expenses.length') == 1, 'retry with the same id is not duplicated')
        o.close()

        # A2 B2 B3 B6: euro cents, settlement, pre-trip in home currency, edit
        eur = {'localCurrency': '유로', 'localSymbol': '€', 'homeSymbol': '₩', 'rate': 9.3, 'currencyDecimals': 2, 'payers': ['A', 'B']}
        m = fresh({'configPatch': eur, 'latest': '2.1.0'})
        check('새 버전 2.1.0' in m.inner_text('.mock'), 'A5: newer release → update notice')
        m.click('[data-tab="money"]')
        m.fill('#amt', '4.505')
        check(m.input_value('#amt') == '4.50', f"decimals capped at 2: {m.input_value('#amt')}")
        m.click('#expForm .submit')
        m.wait_for_function('fake.expenses.length === 1')
        m.click('[data-payer="B"]')
        m.fill('#amt', '10')
        m.click('#expForm .submit')
        m.wait_for_function('fake.expenses.length === 2')
        settle_txt = m.inner_text('.settle')
        check('€4.50' in m.inner_text('.ledger') and 'A → B' in settle_txt.replace('\n', ' ').replace('  ', ' ') and '€2.75' in settle_txt, f'settlement A pays B €2.75: {settle_txt!r}')
        m.click('.ledger .exp-item >> nth=0')
        check('수정 중' in m.inner_text('#expForm'), 'tap ledger item → edit mode')
        m.fill('#amt', '12')
        m.click('#expForm .submit')
        m.wait_for_function('fake.expenses.some(e => e.amount === 12)')
        check(m.evaluate('fake.expenses.length') == 2, 'edit replaces, does not add')
        m.select_option('#expDate', 'pre')
        m.click('[data-currency="home"]')
        m.fill('#amt', '290000')
        m.click('#expForm .submit')
        m.wait_for_function('fake.expenses.length === 3')
        pre = m.evaluate('fake.expenses[2]')
        check(pre['date'] < '2027-04-10' and pre['currency'] == 'home' and '여행 전' in m.inner_text('.ledger'), f"pre-trip home-currency expense: {pre['date']}")
        m.close()

        # C1: cached copy shows instantly (read-only) while the server is slow
        c = fresh()
        c.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify({bootDelay: 1500}))")
        c.reload()
        c.wait_for_selector('#tab-route .timeline')
        check('지난번 데이터' in c.inner_text('.mock') and c.evaluate("document.body.classList.contains('readonly')"), 'stale cache shown read-only')
        c.wait_for_function("document.querySelector('.mock').innerText.includes('캘린더 일정')")
        check(not c.evaluate("document.body.classList.contains('readonly')"), 'fresh data → editable again')

        # B1: transit mode → key suffix, icon, Google Maps link
        tr = fresh({'configPatch': {'travelMode': 'transit'}})
        tr.click(f'#tab-route .chips.pick [data-add="{ids[0]}"]')
        tr.wait_for_function('fake.travelPairs.length > 0')
        check(tr.evaluate("fake.travelPairs.every(p => p.key.endsWith('!t'))") and '🚆' in tr.inner_text('.drive') and 'travelmode=transit' in tr.inner_html('#tab-route'), 'transit mode end to end')

        # H1 regression: a stale page that saves TWICE must not erase the companion's day (evaluator repro)
        ctx2 = browser.new_context(viewport={'width': 1400, 'height': 1000})
        fresh.clear = True
        a2 = fresh(context=ctx2)
        fresh.clear = False
        b2 = fresh(context=ctx2)
        b2.click('[data-date="2027-04-12"]')
        b2.click(f'#tab-route .chips.pick [data-add="{ids[1]}"]')
        b2.wait_for_function("fake.state.routes && fake.state.routes['2027-04-12']")
        a2.wait_for_function("fake.state.routes && fake.state.routes['2027-04-12']")
        a2.click('[data-date="2027-04-11"]')
        a2.click(f'#tab-route .chips.pick [data-add="{ids[0]}"]')
        a2.wait_for_function('fake.savePatches.length === 1')
        a2.click(f'#tab-route .chips.pick [data-add="{ids[2]}"]')
        a2.wait_for_function('fake.savePatches.length === 2')
        nulls = a2.evaluate("fake.savePatches.flatMap(p => Object.entries(p[1]).filter(([, v]) => v === null).map(([k]) => k))")
        days2 = sorted(a2.evaluate('Object.keys(fake.state.routes)'))
        check(days2 == ['2027-04-11', '2027-04-12'] and not nulls, f'two saves from a stale page keep the companion day: {days2}, nulls={nulls}')
        check(a2.evaluate("Boolean(state.routes['2027-04-12'])"), "after its own save the page shows the companion's day")
        ctx2.close()

        # M4: an edit whose save never reached the server is re-sent after a reload
        u4 = fresh()
        u4.evaluate("fake.offline = true")
        u4.click(f'#tab-route .chips.pick [data-add="{ids[0]}"]')
        u4.wait_for_selector('#toast .toast-item')
        # meanwhile a companion saves another day (written straight into the shared fake server)
        u4.evaluate("fake.state = { ...fake.state, routes: { ...(fake.state.routes || {}), '2027-04-12': [{ id: '%s', stayMin: 60 }] } }" % ids[1])
        u4.reload()  # fresh fake (offline=false), same localStorage
        u4.wait_for_function("fake.savePatches.length > 0")
        has_mine = u4.evaluate("Object.values(fake.state.routes || {}).flat().some(s => s.id === '%s')" % ids[0])
        days4 = sorted(u4.evaluate('Object.keys(fake.state.routes)'))
        nulls4 = u4.evaluate("fake.savePatches.flatMap(p => Object.entries(p[1]).filter(([, v]) => v === null).map(([k]) => k))")
        check(has_mine and '2027-04-12' in days4 and not nulls4, f"unsent edit re-sent after reload without erasing the companion's day: {days4}, nulls={nulls4}")
        check(u4.evaluate("Boolean(state.routes['2027-04-12'])"), "reloaded page shows the companion's day too")

        # R2: a server that omits keys must not cause a request loop
        lp = fresh({'omitTravel': True})
        lp.click(f'#tab-route .chips.pick [data-add="{ids[0]}"]')
        lp.wait_for_timeout(3000)
        n_calls = lp.evaluate("fake.calls.filter(c => c === 'getTravelTimes').length")
        check(n_calls <= 3 and '직선거리 추정' in lp.inner_text('.drive') and '조회 중' not in lp.inner_text('.drive'), f'unanswered travel keys settle as estimates ({n_calls} calls in 3 s)')

        # M3: server unreachable with a cached copy → honest read-only notice (not "loading" forever)
        f3 = fresh()
        f3.evaluate("sessionStorage.setItem('fakeSeed', JSON.stringify({bootFail: true}))")
        f3.reload()
        f3.wait_for_function("document.querySelector('#roNote').innerText.includes('연결하지 못해')", timeout=15000)
        check(f3.evaluate("document.body.classList.contains('readonly')") and f3.locator('#toast .toast-item').count() == 1, 'boot failure → cached view, read-only, error toast')
        check(not errors, f'no page errors {errors}')
        browser.close()
    srv.shutdown()

sys.exit(1 if check.failed else 0)

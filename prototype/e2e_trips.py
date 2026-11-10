"""Switching between trips (Go server). The dev server has fixtures, not Drive, so the trip list is injected into the real
bootstrap answer with page.route and switchTrip is faked; everything on the page is the real dashboard.
Run against a dev server like e2e_api.py:  python3 prototype/e2e_trips.py http://localhost:8769/
"""
import json
import os
import sys

from playwright.sync_api import sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else 'http://localhost:8769/').rstrip('/') + '/'
CHROME = os.environ.get('CHROME', '/usr/bin/google-chrome')
fails, errs = [], []
TRIPS = [{'title': '오사카', 'sheetId': 'a', 'folderId': 'fa'}, {'title': '제주', 'sheetId': 'b', 'folderId': 'fb'}]


def check(ok, msg):
    print(('PASS ' if ok else 'FAIL ') + msg)
    if not ok:
        fails.append(msg)


def open_page(browser, email, st):
    ctx = browser.new_context(viewport={'width': 1000, 'height': 900})
    page = ctx.new_page()
    page.on('pageerror', lambda e: errs.append(str(e)))
    page.on('dialog', lambda d: d.accept())

    def bootstrap(route):
        resp = route.fetch()
        body = resp.json()
        d = body['data']
        d['currentTrip'] = st['current']
        if d['me']['role'] == 'owner':
            d['trips'] = TRIPS
        d['config']['title'] = {'a': '오사카', 'b': '제주'}[st['current']]
        route.fulfill(response=resp, body=json.dumps(body))

    def switch(route):
        st['sent'] = json.loads(route.request.post_data)['args'][0]
        if st.get('fail'):
            return route.fulfill(status=404, content_type='application/json', body=json.dumps({'error': '알 수 없는 여행입니다'}))
        st['current'] = st['sent']
        route.fulfill(status=200, content_type='application/json', body=json.dumps({'ok': True, 'data': {'current': st['sent']}}))

    page.route('**/api/bootstrap', bootstrap)
    page.route('**/api/switchTrip', switch)
    page.goto(f'{BASE}auth/dev?email={email}')
    return ctx, page


def ls(page, key):
    return page.evaluate("(k) => localStorage.getItem('trip.' + k)", key)


with sync_playwright() as p:
    b = p.chromium.launch(executable_path=CHROME, args=['--no-sandbox'])

    st = {'current': 'a'}
    ctx, page = open_page(b, 'dev-owner@example.com', st)
    page.wait_for_selector('#tripSwitch:not([hidden])', timeout=15000)
    check('오사카' in page.inner_text('#tripTitle'), 'the owner sees the current trip')
    check(page.is_hidden('#tripMenu'), 'the menu starts closed')
    page.click('#tripSwitch')
    page.wait_for_selector('#tripMenu:not([hidden])')
    items = page.evaluate("[...document.querySelectorAll('#tripMenu [data-switch-trip]')].map((b) => [b.dataset.switchTrip, b.innerText, b.hasAttribute('aria-current')])")
    check(items == [['a', '✓ 오사카', True], ['b', '제주', False]], f'both trips are listed and the current one is marked: {items}')
    check(page.locator('#tripMenu [data-new-trip-screen]').count() == 1 and '동행 모두' in page.inner_text('#tripMenu'), 'a new-trip entry and the "everyone follows" note')
    page.click('body', position={'x': 5, 'y': 600})
    check(page.is_hidden('#tripMenu'), 'a click elsewhere closes the menu')

    # routes made for the first trip are parked when the trip changes and come back when it is chosen again
    page.evaluate("localStorage.setItem('trip.routes', JSON.stringify({'2027-04-10': [{id: 'x'}]})); localStorage.setItem('trip.tripId', JSON.stringify('a'))")
    page.click('#tripSwitch')
    page.click('[data-switch-trip="a"]')  # the current trip: just closes
    check(st.get('sent') is None and page.is_hidden('#tripMenu'), 'choosing the current trip does nothing')

    st['fail'] = True
    page.click('#tripSwitch')
    page.click('[data-switch-trip="b"]')
    page.wait_for_function("document.body.innerText.includes('여행을 바꾸지 못했습니다')", timeout=8000)
    check(st['sent'] == 'b' and '오사카' in page.inner_text('#tripTitle') and ls(page, 'routes') is not None, 'a refused switch shows the reason and changes nothing')

    st['fail'] = False
    page.click('#tripSwitch')
    page.click('[data-switch-trip="b"]')
    page.wait_for_function("document.querySelector('#tripTitle').innerText.includes('제주')", timeout=15000)
    check(st['current'] == 'b', 'after switching, the page reloads into the other trip')
    check(json.loads(ls(page, 'routes') or '{}') == {} and json.loads(ls(page, 'tripId')) == 'b', 'the first trip\'s local routes are not shown on the other trip')

    page.click('#tripSwitch')
    page.click('[data-switch-trip="a"]')
    page.wait_for_function("document.querySelector('#tripTitle').innerText.includes('오사카')", timeout=15000)
    check(json.loads(ls(page, 'routes') or 'null') == {'2027-04-10': [{'id': 'x'}]}, 'switching back brings the first trip\'s routes back')

    # the new-trip screen from the dashboard, and the way back
    page.click('#tripSwitch')
    page.click('[data-new-trip-screen]')
    page.wait_for_selector('[data-create-trip]')
    check(page.locator('#nt-title').count() == 1 and page.locator('[data-back-to-trip]').count() == 1, 'the new-trip form opens with a way back')
    page.wait_for_timeout(500)
    page.click('[data-back-to-trip]')
    page.wait_for_selector('#tripSwitch:not([hidden])', timeout=15000)
    check(page.locator('[data-create-trip]').count() == 0, 'going back shows the dashboard again')
    ctx.close()

    # a browser that has not seen this trip yet picks up the right data (another member's browser after the owner switched)
    st3 = {'current': 'b'}
    ctx, page = open_page(b, 'dev-owner@example.com', st3)
    page.wait_for_selector('#tripSwitch:not([hidden])', timeout=15000)
    page.evaluate("localStorage.setItem('trip.routes', JSON.stringify({r: 1})); localStorage.setItem('trip.tripId', JSON.stringify('a'))")
    page.reload()
    page.wait_for_function("localStorage.getItem('trip.tripId') === '\"b\"' && !document.body.innerText.includes('불러오는 중')", timeout=15000)
    check(json.loads(ls(page, 'routes') or '{}') == {} and json.loads(ls(page, 'stash.a'))['routes'] == {'r': 1}, 'a browser last on another trip parks that trip\'s data on arrival')
    ctx.close()

    # an editor never gets the switcher or the list
    st2 = {'current': 'a'}
    ctx, page = open_page(b, 'dev-editor@example.com', st2)
    page.wait_for_function("document.querySelector('#tripTitle').innerText.includes('오사카')", timeout=15000)
    check(page.is_hidden('#tripSwitch'), 'an editor has no trip switcher')
    ctx.close()

    check(not errs, f'no page errors {errs}')
    b.close()

print('FAILS:', fails)
sys.exit(1 if fails else 0)

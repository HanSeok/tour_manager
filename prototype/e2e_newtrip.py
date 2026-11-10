"""'새 여행 만들기' screen (Go server, no trip yet). The server side is faked with page.route so no Google is needed:
the first bootstrap answers "newTrip", createTrip records what the form sent, then the real dev bootstrap answers.
Run against a dev server like e2e_api.py:  python3 prototype/e2e_newtrip.py http://localhost:8768/ [shots_dir]
"""
import json
import os
import sys

from playwright.sync_api import sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else 'http://localhost:8768/').rstrip('/') + '/'
SHOTS = sys.argv[2] if len(sys.argv) > 2 else None
CHROME = os.environ.get('CHROME', '/usr/bin/google-chrome')
fails, errs = [], []


def check(ok, msg):
    print(('PASS ' if ok else 'FAIL ') + msg)
    if not ok:
        fails.append(msg)


def setup_response(role):
    return {'ok': True, 'data': {
        'config': {'missing': ['설정 탭'], 'invalid': [], 'title': '', 'days': 0},
        'me': {'email': 'dev-owner@example.com', 'name': '소유자', 'role': role}, 'members': [], 'newTrip': True,
        'tripPresets': [{'name': '일본', 'timezone': 'Asia/Tokyo', 'currency': '엔', 'symbol': '¥', 'rate': '9.3'},
                        {'name': '태국', 'timezone': 'Asia/Bangkok', 'currency': '바트', 'symbol': '฿', 'rate': '40'}],
        'tripDefaults': {'timezone': 'Asia/Tokyo', 'localCurrency': '엔', 'localSymbol': '¥', 'rate': '9.3', 'payers': '나, 동행'},
        'checks': [{'label': 'Google 연결 (소유자)', 'ok': True, 'detail': '연결됨'}, {'label': '여행', 'ok': False, 'detail': '아직 여행이 없습니다'}]}}


def page_for(browser, role, state):
    ctx = browser.new_context(viewport={'width': 1000, 'height': 900})
    page = ctx.new_page()
    page.on('pageerror', lambda e: errs.append(str(e) + ' @ ' + str(getattr(e, 'stack', ''))[:300]))
    page.on('dialog', lambda d: d.accept())

    def bootstrap(route):
        if state['created']:
            return route.continue_()
        route.fulfill(status=200, content_type='application/json', body=json.dumps(setup_response(role)))

    def create(route):
        state['sent'] = json.loads(route.request.post_data)['args'][0]
        if state.get('fail'):
            return route.fulfill(status=422, content_type='application/json', body=json.dumps({'error': '올바르지 않음: 시작일'}))
        state['created'] = True
        route.fulfill(status=200, content_type='application/json', body=json.dumps({'ok': True, 'data': {'trip': {'sheetId': 's1', 'title': 'x'}}}))

    page.route('**/api/bootstrap', bootstrap)
    page.route('**/api/createTrip', create)
    page.goto(f'{BASE}auth/dev?email=dev-owner@example.com')
    return ctx, page


with sync_playwright() as p:
    b = p.chromium.launch(executable_path=CHROME, args=['--no-sandbox'])

    # owner: the form, a rejected attempt, then a successful one
    st = {'created': False, 'fail': True}
    ctx, page = page_for(b, 'owner', st)
    page.wait_for_selector('[data-create-trip]', timeout=15000)
    check('새 여행 만들기' in page.inner_text('.setup') and '여행 대시보드' in page.inner_text('.setup'), 'owner sees the new-trip form')
    check(page.locator('[data-setup-settings]').count() == 0, 'the old "설정 탭 자동으로 만들기" button is not offered here')
    check('아직 여행이 없습니다' in page.inner_text('.checks'), 'the self-check is shown under the form')
    if SHOTS:
        page.screenshot(path=os.path.join(SHOTS, 'newtrip.png'))

    ph = page.evaluate("Object.fromEntries([...document.querySelectorAll('.newtrip input')].map((i) => [i.id, i.placeholder]))")
    check(ph['nt-timezone'] == 'Asia/Tokyo' and ph['nt-localCurrency'] == '엔' and ph['nt-localSymbol'] == '¥' and ph['nt-rate'] == '9.3',
          f'the greyed-out hints are the server defaults: {ph}')
    check('비워 두면' in page.inner_text('.setup'), 'the form says blank fields get the greyed-out default')
    check(page.locator('.newtrip label:has-text("*")').count() == 3, 'only title and the two dates are marked required')

    check(page.locator('#nt-preset option').count() == 3, 'the destination list is offered (plus "직접 입력")')
    opts = page.evaluate("[...document.querySelectorAll('#nt-dl-timezone option')].map((o) => o.value)")
    check(opts == ['Asia/Tokyo', 'Asia/Bangkok'], f'time zone suggestions come from the same list: {opts}')
    check(page.get_attribute('#nt-localSymbol', 'list') == 'nt-dl-localSymbol', 'currency symbol has suggestions too')
    page.select_option('#nt-preset', label='태국')
    got = page.evaluate("['timezone','localCurrency','localSymbol','rate'].map((k) => document.getElementById('nt-' + k).value)")
    check(got == ['Asia/Bangkok', '바트', '฿', '40'], f'choosing a destination fills the four fields: {got}')
    page.fill('#nt-title', '제주 가을')
    page.fill('#nt-start', '2027-10-01')
    page.fill('#nt-end', '2027-10-03')
    page.fill('#nt-timezone', 'Asia/Seoul')  # a field can still be changed after choosing a destination
    page.fill('#nt-rate', '')
    page.click('[data-create-trip]')
    page.wait_for_function("document.body.innerText.includes('올바르지 않음')", timeout=8000)
    check(st['sent']['title'] == '제주 가을' and st['sent']['start'] == '2027-10-01' and st['sent']['end'] == '2027-10-03'
          and st['sent']['timezone'] == 'Asia/Seoul' and st['sent']['rate'] == '' and st['sent']['localSymbol'] == '฿' and st['sent']['localCurrency'] == '바트', f'the form sends what was typed and leaves blanks blank for the server to fill (preset values stay): {st["sent"]}')
    check('올바르지 않음: 시작일' in page.inner_text('body') and page.is_enabled('[data-create-trip]'), 'a server error is shown and the button works again')

    st['fail'] = False
    page.click('[data-create-trip]')
    page.wait_for_function("typeof state !== 'undefined' && !document.querySelector('[data-create-trip]')", timeout=15000)
    check(st['created'], 'after creating, the page reloads into the dashboard (no form)')
    ctx.close()

    # a non-owner never gets the form, even if the server sent newTrip
    st2 = {'created': False}
    ctx, page = page_for(b, 'editor', st2)
    page.wait_for_selector('.setup', timeout=15000)
    check(page.locator('[data-create-trip]').count() == 0, 'editor does not see the form')
    ctx.close()

    check(not errs, f'no page errors {errs}')
    b.close()

print('FAILS:', fails)
sys.exit(1 if fails else 0)

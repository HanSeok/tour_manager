"""Prototype E2E check: python3 -I e2e.py [base_url] [shot_dir]  (needs python playwright + /usr/bin/google-chrome)"""
import sys
from playwright.sync_api import sync_playwright

BASE = sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:8600/'
SHOTS = sys.argv[2] if len(sys.argv) > 2 else '.'
pid = lambda page, name: page.evaluate(f"PLACES.find(p => p.name.includes({name!r})).id")


def check(cond, msg):
    print(('PASS ' if cond else 'FAIL ') + msg)
    if not cond:
        check.failed = True


check.failed = False

with sync_playwright() as pw:
    browser = pw.chromium.launch(executable_path='/usr/bin/google-chrome', args=['--no-sandbox'])
    page = browser.new_page(viewport={'width': 1400, 'height': 1000})
    errors = []
    page.on('pageerror', lambda e: errors.append(str(e)))
    page.on('dialog', lambda d: d.accept())
    page.goto(BASE)
    page.evaluate('localStorage.clear()')
    page.reload()

    # ① day 1 starts at airport after landing
    start_note = page.inner_text('#tab-route .timeline li:first-child .meta')
    check('입국' in start_note and page.input_value('#startTime') == '15:00', f'day1 start from flight: {start_note!r}')
    # add two places → they go before the check-in (end item)
    for name in ('국제거리', '츄라우미 수족관'):
        page.click(f'#tab-route .chips.pick [data-add="{pid(page, name)}"]')
    titles = page.locator('#tab-route .timeline .card .row b').all_inner_texts()
    check(titles[-1].startswith('체크인 · 샘플 호텔') and '국제거리' in titles, f'check-in stays last: {titles}')

    # ④ register → synced; plan tab shows route events from "calendar"
    page.click('[data-register]')
    check(page.locator('.synced').count() == 1, 'register → synced')
    page.fill('[data-stay="0"]', '100')
    page.dispatch_event('[data-stay="0"]', 'change')
    check('다시 등록' in page.inner_text('[data-register]'), 'edit after register → re-register prompt')
    page.click('[data-register]')
    page.click('[data-tab="plan"]')
    check(page.locator('.daycard').first.locator('li.mine').count() == 2, 'plan tab shows 2 registered route events')
    page.screenshot(path=f'{SHOTS}/plan.png', full_page=False)

    # ③② day 2: dinner reservation is fixed; long stay before it → LATE
    page.click('[data-tab="route"]')
    page.click('[data-date="2027-04-11"]')
    check('전날 숙소' in page.inner_text('#tab-route .timeline li:first-child .meta'), 'day2 starts from previous-night hotel')
    page.click(f'#tab-route .chips.pick [data-add="{pid(page, "츄라우미 수족관")}"]')
    page.fill('[data-stay="0"]', '600')
    page.dispatch_event('[data-stay="0"]', 'change')
    check(page.locator('.card.warn-LATE').count() == 1, 'dinner reservation flagged LATE')
    page.screenshot(path=f'{SHOTS}/day2.png', full_page=True)

    # multi-day stay: mid-trip day starts at the stay; last day starts at checkout and ends with the return flight
    page.click('[data-date="2027-04-13"]')
    check('샘플 호텔' in page.locator('#startPlace option:checked').inner_text(), 'day4 starts from the stay (calendar location)')
    page.click('[data-date="2027-04-14"]')
    check('체크아웃' in page.inner_text('#tab-route .timeline li:first-child .meta') and page.input_value('#startTime') == '11:00', 'last day starts at checkout 11:00')
    check(page.locator('.card.fixed').last.inner_text().startswith('귀국편'), 'return flight is the last stop')
    # re-link an event's place in plan tab → route uses it
    page.click('[data-tab="plan"]')
    page.select_option('[data-ev-place="m3"]', pid(page, '츄라우미 수족관'))
    page.click('[data-tab="route"]')
    page.click('[data-date="2027-04-11"]')
    check('츄라우미' in page.locator('.card.fixed').first.inner_text(), 'linking event place changes the fixed stop')

    # expense input is escaped
    page.click('[data-tab="money"]')
    page.fill('#amt', '1500')
    page.fill('#item', '<img src=x onerror=alert(1)>')
    page.click('#expForm .submit')
    check(page.locator('.ledger img').count() == 0 and '<img' in page.inner_text('.ledger'), 'expense text escaped')

    # mobile render of day 1
    m = browser.new_page(viewport={'width': 390, 'height': 844}, is_mobile=True)
    m.on('pageerror', lambda e: errors.append(str(e)))
    m.goto(BASE)
    m.screenshot(path=f'{SHOTS}/mobile.png', full_page=True)
    check(m.evaluate('document.documentElement.scrollWidth') <= 390, 'mobile: no horizontal scroll')

    check(not errors, f'no page errors {errors}')
    browser.close()

sys.exit(1 if check.failed else 0)

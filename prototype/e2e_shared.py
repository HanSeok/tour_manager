"""Go-server E2E for #5: the day routes, start times and visit records are shared between members through the server.
Run (fresh dev server, in-memory sheet):
  DEV_LOGIN=1 OWNER_EMAIL=dev-owner@example.com SESSION_SECRET=$(openssl rand -hex 32) FIXTURE_DIR=dev/fixtures \\
  STATIC_DIR=prototype PORT=8770 BASE_URL=http://localhost:8770 DATA_DIR=$(mktemp -d) go run ./cmd/server &
  python3 prototype/e2e_shared.py http://localhost:8770/
"""
import json
import os
import sys
import time

from playwright.sync_api import sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else 'http://localhost:8770/').rstrip('/') + '/'
CHROME = os.environ.get('CHROME', '/usr/bin/google-chrome')
fails, errs = [], []


def check(ok, msg):
    print(('PASS ' if ok else 'FAIL ') + msg)
    if not ok:
        fails.append(msg)


def open_page(browser, email, init=None):
    ctx = browser.new_context(viewport={'width': 1000, 'height': 900})
    if init:
        ctx.add_init_script(init)
    page = ctx.new_page()
    page.on('pageerror', lambda e: errs.append(f'{email}: {e}'))
    page.on('dialog', lambda d: d.accept())
    page.goto(f'{BASE}auth/dev?email={email}')
    page.wait_for_function("typeof state !== 'undefined' && ME !== null && document.querySelector('.mock').textContent.includes('(')", timeout=15000)
    return ctx, page


def server_state(page):
    return page.evaluate("fetch('/api/refresh', {method:'POST', headers:{'Content-Type':'application/json'}, body:'{\"args\":[]}'}).then(r => r.json()).then(j => j.data.state)")


def settle(page):
    """Wait until nothing is waiting to be saved."""
    page.wait_for_function("dirtyKeys().length === 0 && syncInFlight.size === 0", timeout=8000)


def refresh(page):
    page.evaluate("refreshShared()")
    time.sleep(0.6)


with sync_playwright() as p:
    b = p.chromium.launch(executable_path=CHROME, args=['--no-sandbox'])
    ctxA, A = open_page(b, 'dev-editor@example.com')
    ctxB, B = open_page(b, 'dev-owner@example.com')
    ctxC, C = open_page(b, 'dev-viewer@example.com')
    sent_by_viewer = []
    C.on('request', lambda r: sent_by_viewer.append(r.url) if r.url.endswith('/api/saveState') else None)
    D1, D2 = A.evaluate('DATES[0]'), A.evaluate('DATES[1]')

    check('동행과 공유' in A.inner_text('.mock'), f'banner says the plan is shared: {A.inner_text(".mock")!r}')

    # a start time and a route day set by one companion reach the other
    A.evaluate("([d]) => setState({ starts: { ...state.starts, [d]: 600 } })", [D1])
    settle(A)
    check(server_state(A)['starts'].get(D1) == 600, 'the start time is saved on the server')
    refresh(B)
    check(B.evaluate("([d]) => state.starts[d]", [D1]) == 600, "the other companion sees the start time after a refresh")

    # two companions planning different days do not overwrite each other
    A.evaluate("([d]) => setState({ routes: { ...state.routes, [d]: [{ sid: 'sa', label: '점심', stayMin: 60, cands: [], excluded: [] }] } })", [D1])
    B.evaluate("([d]) => setState({ routes: { ...state.routes, [d]: [{ sid: 'sb', label: '저녁', stayMin: 60, cands: [], excluded: [] }] } })", [D2])
    settle(A)
    settle(B)
    srv = server_state(A)['routes']
    check(set(srv) == {D1, D2}, f'both days are on the server: {sorted(srv)}')
    refresh(A)
    refresh(B)
    for name, pg in (('A', A), ('B', B)):
        days = pg.evaluate("Object.keys(state.routes).sort()")
        check(days == sorted([D1, D2]), f'{name} ends up with both days: {days}')

    # visit records are shared per day
    A.evaluate("([d]) => setState({ visits: [...state.visits, { id: 'v1', sid: 'sa', date: d, placeId: null, result: 'skipped', label: '점심', time: '12:10', at: '2027-04-10T03:10:00Z' }] })", [D1])
    settle(A)
    refresh(B)
    check(B.evaluate("state.visits.map((v) => v.id)") == ['v1'], 'a visit record made by one companion shows on the other')
    B.evaluate("([d]) => setState({ visits: [...state.visits, { id: 'v2', sid: 'sb', date: d, placeId: null, result: 'skipped', label: '저녁', time: '19:00', at: '2027-04-11T10:00:00Z' }] })", [D2])
    settle(B)
    refresh(A)
    check(sorted(A.evaluate("state.visits.map((v) => v.id)")) == ['v1', 'v2'], 'visits made on different days are both kept')
    A.evaluate("setState({ visits: state.visits.filter((v) => v.id !== 'v1') })")
    settle(A)
    check([v['id'] for d in server_state(A)['visits'].values() for v in d] == ['v2'], 'removing the only visit of a day removes it on the server')

    # a viewer follows the plan but never writes it
    refresh(C)
    check(C.evaluate("state.starts") == {D1: 600}, 'a viewer sees the shared plan')
    C.evaluate("([d]) => setState({ starts: { ...state.starts, [d]: 1000 } })", [D2])
    time.sleep(1.2)
    check(D2 not in server_state(A)['starts'] and not sent_by_viewer, f"a viewer's edit is never even sent to the server: {sent_by_viewer}")

    ctxA.close(); ctxB.close(); ctxC.close()

    # a browser that planned before the server kept the plan sends its days up instead of losing them
    seed = ("(() => { if (localStorage.getItem('trip.synced') !== null || localStorage.getItem('trip.seeded')) return;"
            " localStorage.setItem('trip.seeded', '1');"
            " localStorage.setItem('trip.routes', JSON.stringify({ '%s': [{ sid: 'old', label: '아침', stayMin: 30, cands: [], excluded: [] }] }));"
            " localStorage.setItem('trip.starts', JSON.stringify({ '%s': 480, '2020-01-01': 300 })); })()") % ('DAY3', 'DAY3')
    ctxA0, A0 = open_page(b, 'dev-editor@example.com')
    day3 = A0.evaluate('DATES[2]')
    ctxA0.close()
    ctxE, E = open_page(b, 'dev-editor@example.com', seed.replace('DAY3', day3))
    settle(E)
    srv = server_state(E)
    check(srv['routes'].get(day3) and srv['starts'].get(day3) == 480, "days a browser had planned before are kept: sent to the server")
    check('2020-01-01' not in srv['starts'], 'a day outside the trip is not sent (it would be refused and stick)')
    check(srv['routes'].get(D1), "and the companions' days are still there")
    ctxE.close()
    b.close()

print('ERRORS:', errs)
print('FAILS:', fails)
sys.exit(1 if fails or errs else 0)

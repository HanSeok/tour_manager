"""Go-server E2E: the dashboard on /api with three signed-in members (editor, owner, viewer).
Run (fresh dev server, in-memory sheet):
  DEV_LOGIN=1 OWNER_EMAIL=dev-owner@example.com SESSION_SECRET=$(openssl rand -hex 32) FIXTURE_DIR=dev/fixtures \\
  STATIC_DIR=prototype PORT=8767 BASE_URL=http://localhost:8767 go run ./cmd/server &
  python3 prototype/e2e_api.py http://localhost:8767/
Needs python playwright and a Chrome (CHROME env, default /usr/bin/google-chrome).
"""
import os
import sys

from playwright.sync_api import sync_playwright

BASE = (sys.argv[1] if len(sys.argv) > 1 else 'http://localhost:8767/').rstrip('/') + '/'
CHROME = os.environ.get('CHROME', '/usr/bin/google-chrome')
fails, errs = [], []


def check(ok, msg):
    print(('PASS ' if ok else 'FAIL ') + msg)
    if not ok:
        fails.append(msg)


def member(browser, email):
    ctx = browser.new_context(viewport={'width': 1100, 'height': 900})
    page = ctx.new_page()
    page.on('pageerror', lambda e: errs.append(f'{email}: {e}'))
    page.on('dialog', lambda d: d.accept())
    page.goto(f'{BASE}auth/dev?email={email}')
    page.wait_for_function("typeof state !== 'undefined' && ME !== null && document.querySelector('.mock').textContent.includes('(')", timeout=15000)
    page.click('[data-tab=money]')
    return ctx, page


def rows(page):
    return page.locator('.ledger li:not(.day)').all_inner_texts()


def server_list(page):
    return page.evaluate("fetch('/api/refresh', {method:'POST', headers:{'Content-Type':'application/json'}, body:'{\"args\":[]}'}).then(r => r.json()).then(j => j.data.expenses)")


def add(page, item, amount):
    page.fill('#amt', str(amount))
    page.fill('#item', item)
    page.click('.submit')


with sync_playwright() as p:
    b = p.chromium.launch(executable_path=CHROME, args=['--no-sandbox'])
    ctxA, A = member(b, 'dev-editor@example.com')
    ctxB, B = member(b, 'dev-owner@example.com')
    ctxC, C = member(b, 'dev-viewer@example.com')

    check(A.evaluate('window.TM_API') is True and A.evaluate('IS_API') is True, 'page runs in server (API) mode')
    banner = A.inner_text('.mock')
    check('동행 (편집자)' in banner and '샘플(개발용)' in banner and '서버에 저장' in banner, f'banner shows who is signed in and what is where: {banner!r}')
    check(rows(A) == [] and '렌터카' not in A.inner_text('#tab-money'), 'server mode starts with the server\'s (empty) expenses, not the sample ones')
    check(not A.evaluate("document.body.classList.contains('readonly')"), 'editor can edit')
    check(C.evaluate("document.body.classList.contains('readonly')") and not C.is_visible('#expForm'), 'viewer is read-only, no entry form')
    check('보기 권한' in C.inner_text('#roNote'), 'viewer is told how to get edit rights')

    # 1. editor adds → saved on the server with the author
    add(A, '소바', 1200)
    A.wait_for_function('state.expenses.length === 1')
    check('✍ 동행' in rows(A)[0] and '소바' in rows(A)[0], f'ledger shows the entry with its author: {rows(A)}')
    srv = server_list(A)
    check(len(srv) == 1 and srv[0]['createdBy'] == 'dev-editor@example.com' and srv[0]['updatedAt'], 'stored on the server with creator and version')

    # 2. other members see it after a refresh
    for page in (B, C):
        page.evaluate('refreshShared()')
        page.wait_for_function('state.expenses.length === 1')
    check('✍ 동행' in rows(C)[0], 'viewer sees the same expense and its author')

    # 3. owner edits the editor's entry
    B.click('[data-exp-edit]')
    B.fill('#item', '소바 곱빼기')
    B.click('.submit')
    B.wait_for_function("state.expenses[0].item === '소바 곱빼기'")
    check('수정 소유자' in rows(B)[0], f'edit by another member is attributed: {rows(B)}')

    # 4. the editor still holds the old version → conflict, nothing overwritten, edit kept
    check('곱빼기' not in rows(A)[0], 'editor page is stale (as in real life, up to 3 minutes)')
    A.click('[data-exp-edit]')
    A.fill('#item', '소바 대')
    A.click('.submit')
    A.wait_for_function("document.querySelector('.err').textContent.includes('먼저 수정')")
    check(server_list(A)[0]['item'] == '소바 곱빼기', 'conflict: the other member\'s edit is not overwritten')
    A.wait_for_function("state.expenses[0].item === '소바 곱빼기'")
    check(A.input_value('#item') == '소바 대' and A.evaluate('Boolean(state.editing)'), 'conflict: the list is refreshed and the user\'s own edit is kept')
    A.click('.submit')  # knowingly save over it
    A.wait_for_function("state.expenses[0].item === '소바 대'")
    check(server_list(A)[0]['updatedBy'] == 'dev-editor@example.com', 'after seeing the latest version the save goes through')

    # 5. stale delete is refused too
    add(B, '커피', 500)
    B.wait_for_function('state.expenses.length === 2')
    A.evaluate('refreshShared()')
    A.wait_for_function('state.expenses.length === 2')
    B.click('[data-exp-edit="' + server_list(B)[1]['id'] + '"]')
    B.fill('#item', '커피 2잔')
    B.click('.submit')
    B.wait_for_function("state.expenses[1].item === '커피 2잔'")
    A.click('.ledger li:not(.day) [data-exp-del]')  # the newest row is listed first
    A.wait_for_function("document.querySelector('.err').textContent.includes('먼저 수정')")
    check(len(server_list(A)) == 2, 'stale delete is refused, both entries remain')

    # 6. viewers cannot write even by calling the API
    code = C.evaluate("fetch('/api/addExpense', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({args:[{date:'2027-04-10',category:'식비',amount:1,payer:'나',method:'카드'}]})}).then(r => r.status)")
    check(code == 403, f'viewer write is rejected by the server ({code})')

    # 7. a clean delete
    A.evaluate('refreshShared()')
    A.wait_for_function('state.expenses.length === 2')
    A.click('.ledger li:not(.day) [data-exp-del]')
    A.wait_for_function('state.expenses.length === 1')
    check(len(server_list(A)) == 1, 'delete removes it on the server')

    # 8. offline: queued, then sent once when the connection returns (no duplicate)
    ctxA.set_offline(True)
    add(A, '오프라인 간식', 300)
    A.wait_for_function('state.outbox.length === 1')
    check('전송 대기' in A.inner_text('#tab-money'), 'offline entry waits in the outbox')
    ctxA.set_offline(False)
    A.evaluate("window.dispatchEvent(new Event('online'))")
    A.wait_for_function('state.outbox.length === 0 && state.expenses.length === 2')
    A.evaluate("window.dispatchEvent(new Event('online'))")
    check(len(server_list(A)) == 2, 'queued entry arrived exactly once')

    # 9. timeline shows the author
    A.click('[data-tab=timeline]')
    day = A.inner_text('.tl-day')
    check('✍' in A.inner_text('#tab-timeline'), 'timeline shows who recorded each payment')

    # 10. setup check: owner only
    B.click('[data-tab=plan]')
    B.click('[data-diagnose]')
    B.wait_for_function("document.querySelectorAll('.checks li').length >= 4")
    check('개발용 샘플 시트' in B.inner_text('.checks') and 'Google 연결' in B.inner_text('.checks'), 'owner: the setup check lists the connection steps')
    A.click('[data-tab=plan]')
    check(A.locator('[data-diagnose]').count() == 0 and C.locator('[data-diagnose]').count() == 0, 'editor / viewer have no setup-check button')

    check(not errs, f'no page errors {errs}')
    b.close()

print('FAILS:', fails)
sys.exit(1 if fails else 0)

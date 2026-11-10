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

    # 11. places: tap the map, name it, the others see it; viewers cannot add; authors are shown; delete
    def trip_places(page):
        return page.evaluate("fetch('/api/refresh', {method:'POST', headers:{'Content-Type':'application/json'}, body:'{\"args\":[]}'}).then(r => r.json()).then(j => j.data.tripPlaces)")

    A.click('[data-tab=route]')
    before = A.locator('.pin').count()
    box = A.locator('#map').bounding_box()
    A.mouse.click(box['x'] + 40, box['y'] + 60)  # an empty corner of the map
    A.wait_for_selector('.newplace [name=name]')
    check('새 장소' in A.inner_text('.newplace') and A.locator('.newplace option').count() == 5, 'editor: tapping the map opens the new-place form with the five categories')
    A.fill('.newplace [name=name]', '내가 찍은 카페')
    A.select_option('.newplace [name=layer]', '식당')
    A.fill('.newplace [name=memo]', '오전에')
    A.click('.newplace .submit')
    A.wait_for_function("!document.querySelector('.newplace')")
    saved = trip_places(A)
    check(len(saved) == 1 and saved[0]['name'] == '내가 찍은 카페' and saved[0]['layer'] == '식당' and saved[0]['createdBy'] == 'dev-editor@example.com'
          and abs(saved[0]['lat']) <= 90 and abs(saved[0]['lng']) <= 180, f'the place is saved on the server with its author: {saved}')
    check(A.locator('.pin').count() == before + 1, 'a new pin appears on the map')
    check('내가 찍은 카페' in A.inner_text('#tab-route'), 'the place shows in the add-to-route list')
    B.evaluate('refreshShared()')
    B.wait_for_function("PLACES.some((p) => p.name === '내가 찍은 카페')")
    check(True, 'the owner sees it after a refresh')

    C.evaluate('refreshShared()')
    C.wait_for_function("PLACES.some((p) => p.name === '내가 찍은 카페')")
    cbox = C.locator('#map').bounding_box()
    C.mouse.click(cbox['x'] + 80, cbox['y'] + 120)
    C.wait_for_timeout(400)
    check(C.locator('.newplace').count() == 0, 'viewer: tapping the map does not open the form')

    # the author edits name / category / memo from the pin popup; position and author stay; others see it after a refresh
    A.evaluate("renderMap(planFor(state.date)); pins.eachLayer((m) => { const pp = m.getPopup && m.getPopup(); if (pp && /내가 찍은 카페/.test(pp.getContent())) m.openPopup(); })")
    A.wait_for_selector('.leaflet-popup [data-edit-place]')
    A.click('.leaflet-popup [data-edit-place]')
    A.wait_for_selector('.newplace[data-edit-id] [name=name]')
    check(A.input_value('.newplace [name=name]') == '내가 찍은 카페' and A.input_value('.newplace [name=memo]') == '오전에' and '장소 수정' in A.inner_text('.newplace'), 'edit form is prefilled')
    pos0 = (saved[0]['lat'], saved[0]['lng'])
    A.fill('.newplace [name=name]', '고친 카페')
    A.select_option('.newplace [name=layer]', '쇼핑')
    A.fill('.newplace [name=memo]', '오후로 변경')
    A.click('.newplace .submit')
    A.wait_for_function("PLACES.some((p) => p.name === '고친 카페')")
    ed = trip_places(A)
    check(len(ed) == 1 and ed[0]['name'] == '고친 카페' and ed[0]['layer'] == '쇼핑' and ed[0]['memo'] == '오후로 변경'
          and (ed[0]['lat'], ed[0]['lng']) == pos0 and ed[0]['createdBy'] == 'dev-editor@example.com' and ed[0]['id'] == saved[0]['id'], f'edited in place: {ed}')
    A.wait_for_function("!document.querySelector('.newplace')")
    B.evaluate('refreshShared()')
    B.wait_for_function("PLACES.some((p) => p.name === '고친 카페')")
    check(True, 'the owner sees the edit after a refresh')
    # the old name is gone from the pin popups; restore it for the steps below
    A.evaluate("callServer('updatePlace', PLACES.find((p) => p.own).id, {name:'내가 찍은 카페', layer:'식당', lat:%s, lng:%s, memo:'오전에'}).then(() => refreshExpenses())" % pos0)
    A.wait_for_function("PLACES.some((p) => p.name === '내가 찍은 카페')")
    C.evaluate('refreshShared()')
    C.wait_for_function("PLACES.some((p) => p.name === '내가 찍은 카페')")
    C.evaluate("renderMap(planFor(state.date)); pins.eachLayer((m) => { const pp = m.getPopup && m.getPopup(); if (pp && /내가 찍은 카페/.test(pp.getContent())) m.openPopup(); })")
    C.wait_for_selector('.leaflet-popup')
    check(C.locator('.leaflet-popup [data-edit-place]').count() == 0, 'viewer: no edit button')
    C.keyboard.press('Escape')

    # the author can delete it from its pin popup; the viewer's popup has no delete button
    A.evaluate("renderMap(planFor(state.date)); pins.eachLayer((m) => { const pp = m.getPopup && m.getPopup(); if (pp && /내가 찍은 카페/.test(pp.getContent())) m.openPopup(); })")
    A.wait_for_selector('.leaflet-popup [data-del-place]')
    check('✍ 동행' in A.inner_text('.leaflet-popup') or '✍ dev-editor' in A.inner_text('.leaflet-popup'), f'the popup shows who added it: {A.inner_text(".leaflet-popup")!r}')
    C.evaluate("pins.eachLayer((m) => { const pp = m.getPopup && m.getPopup(); if (pp && /내가 찍은 카페/.test(pp.getContent())) m.openPopup(); })")
    C.wait_for_selector('.leaflet-popup')
    check(C.locator('.leaflet-popup [data-del-place]').count() == 0, 'viewer: no delete button')
    A.click('.leaflet-popup [data-del-place]')
    A.wait_for_function("!PLACES.some((p) => p.own)")
    check(trip_places(A) == [], 'deleted on the server')
    B.evaluate('refreshShared()')
    B.wait_for_function("!PLACES.some((p) => p.own)")
    check(True, 'and gone for the others after a refresh')

    # 12. a pasted Google Maps link fills in the place (no network: a long URL is read directly)
    LINK = ('https://www.google.com/maps/place/%EC%98%A4%EC%82%AC%EC%B9%B4+%EA%B3%B5%ED%95%AD+(%EC%9D%B4%ED%83%80%EB%AF%B8+%EA%B3%B5%ED%95%AD)/@34.7907912,135.4356809,16z/data=!3m1!4b1!4m6!3m5!1s0x6000f06df58777b9:0x7411a874d9df4cd!8m2!3d34.7861612!4d135.4380483!16zL20vMDFteWxq?entry=tts')
    check(C.locator('.linkbox').count() == 0, 'viewer has no link box')
    A.click('.linkbtn')
    A.fill('.linkform [name=link]', '여기 어때 ' + LINK + ' 가보자')
    A.click('.linkform button')
    A.wait_for_selector('.newplace [name=name]')
    check(A.input_value('.newplace [name=name]') == '오사카 공항 (이타미 공항)', 'the name is filled in from the link')
    check('34.78616' in A.inner_text('.newplace') and '135.43805' in A.inner_text('.newplace'), f'the position is shown: {A.inner_text(".newplace")!r}')
    A.select_option('.newplace [name=layer]', '교통')
    A.click('.newplace .submit')
    A.wait_for_function("PLACES.some((p) => p.own && p.name === '오사카 공항 (이타미 공항)')")
    got = [p for p in trip_places(A) if p['name'].startswith('오사카')]
    check(len(got) == 1 and got[0]['layer'] == '교통' and abs(got[0]['lat'] - 34.786161) < 1e-5 and abs(got[0]['lng'] - 135.438048) < 1e-5, f'saved with the pin position: {got}')
    check(got[0]['link'] == LINK, 'the link that was pasted is saved with the place')
    pop_js = "pins.eachLayer((m) => { const pp = m.getPopup && m.getPopup(); if (pp && /오사카 공항/.test(pp.getContent())) m.openPopup(); })"
    A.wait_for_function("!document.querySelector('.newplace')")  # the form popup is still fading out right after saving
    A.evaluate(pop_js)
    A.wait_for_selector('.leaflet-popup a[href^="https://www.google.com/maps/place/"]')
    check('구글 지도에서 보기' in A.inner_text('.leaflet-popup'), 'the pin popup opens the saved link')
    pid_osaka = A.evaluate("PLACES.find((p) => p.name.startsWith('오사카')).id")
    A.keyboard.press('Escape')
    A.click('[data-tab=route]')
    A.click(f'#tab-route [data-add="{pid_osaka}"]')
    A.wait_for_selector('#tab-route .plink')
    check(A.get_attribute('#tab-route .plink', 'href') == LINK and A.get_attribute('#tab-route .plink', 'rel') == 'noopener noreferrer',
          'the route card has a 🔗 that opens the saved link safely')

    A.wait_for_function("!document.querySelector('.newplace')")  # the popup fades out for ~200ms after saving
    A.click('.linkbtn')
    A.fill('.linkform [name=link]', 'https://evil.example/maps/place/x/@1,1,1z')
    A.click('.linkform button')
    A.wait_for_function("document.body.innerText.includes('링크만 읽을 수')")
    check(A.locator('.newplace').count() == 0, 'a link outside Google is refused with a message and opens no form')
    # Kakao Map's link format carries the position itself (no network needed); the box stays open after a refused link
    check(A.is_visible('.linkform'), 'the link box stays open after a refused link so it can be corrected')
    A.fill('.linkform [name=link]', 'https://map.kakao.com/link/map/%EC%B9%B4%EC%B9%B4%EC%98%A4%ED%8C%90%EA%B5%90%EC%98%A4%ED%94%BC%EC%8A%A4,37.402056,127.108212')
    A.click('.linkform button')
    A.wait_for_selector('.newplace [name=name]')
    check(A.input_value('.newplace [name=name]') == '카카오판교오피스' and '37.40206' in A.inner_text('.newplace'), 'a Kakao Map link fills in the name and position')
    A.keyboard.press('Escape')
    A.evaluate("callServer('deletePlace', PLACES.find((p) => p.own).id).then(() => refreshExpenses())")
    A.wait_for_function("!PLACES.some((p) => p.own)")

    check(not errs, f'no page errors {errs}')
    b.close()

print('FAILS:', fails)
sys.exit(1 if fails else 0)

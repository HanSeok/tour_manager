"""Candidate-slot E2E (prototype mode, sample data): closed/visited flow, tentative pick, timeline, sort.
Run: python3 -m http.server 8600 -d prototype &  python3 prototype/e2e_slots.py http://127.0.0.1:8600/
Needs python playwright and a Chrome (CHROME env, default /usr/bin/google-chrome).
"""
import os
import sys

BASE = (sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:8600/').rstrip('/') + '/'
CHROME = os.environ.get('CHROME', '/usr/bin/google-chrome')
from playwright.sync_api import sync_playwright
fails = []
def check(ok, msg):
    print(('PASS ' if ok else 'FAIL ') + msg)
    if not ok: fails.append(msg)
with sync_playwright() as p:
    b = p.chromium.launch(executable_path=CHROME, args=['--no-sandbox'])
    errs = []
    # ---- planning phase: no visit/closed buttons ----
    pg = b.new_page(viewport={'width':1100,'height':900}); pg.on('pageerror', lambda e: errs.append(str(e)))
    pg.goto(BASE + '?demo=1'); pg.wait_for_timeout(1000)
    pg.click('[data-date="2027-04-12"]'); pg.wait_for_timeout(300)
    check(pg.locator('.card.slot').count() == 2, 'lunch slot + rest block rendered (2 slot cards)')
    check(pg.locator('[data-closed]').count() == 0 and pg.locator('[data-visited]').count() == 0, 'planning phase: no visited/closed buttons')
    pg.close()
    # ---- live phase on the demo day ----
    pg = b.new_page(viewport={'width':1100,'height':1100}); pg.on('pageerror', lambda e: errs.append(str(e)))
    pg.goto(BASE + '?demo=1&today=2027-04-12'); pg.wait_for_timeout(1000)
    pg.click('[data-tab=route]'); pg.click('[data-date="2027-04-12"]'); pg.wait_for_timeout(300)
    chosen = lambda: pg.locator('.cand.chosen b').first.inner_text() if pg.locator('.cand.chosen').count() else None
    badge = lambda: pg.locator('.cand.chosen .badge').first.inner_text() if pg.locator('.cand.chosen .badge').count() else ''
    check(pg.locator('.cands .cand').count() == 3, '3 lunch candidates listed')
    check('휴무' in pg.locator('.cand', has_text='소바집').inner_text(), 'Monday: soba shows closed note')
    check('카페' in (chosen() or '') and '임시' in badge(), 'tentative pick = cafe (soba closed on Monday; steak house is on its break → ~2h wait)')
    check('1시간 59분 대기' in pg.locator('.cand', has_text='스테이크집').inner_text(), 'steak house shows its wait')
    # confirm cafe (unknown hours) -> confirmed beats tentative
    pg.click('[data-slot-confirm="seed-lunch"][data-place="s-cafe"]'); pg.wait_for_timeout(200)
    check('카페' in (chosen() or '') and '확정' in badge(), 'confirm cafe -> chosen cafe, badge 확정')
    # cafe is closed -> excluded, falls back to best remaining (steak)
    pg.click('[data-closed="seed-lunch"][data-place="s-cafe"]'); pg.wait_for_timeout(200)
    check(pg.locator('.cand.out').count() == 1 and '스테이크' in (chosen() or ''), 'cafe closed -> struck out, pick falls back to steak')
    pg.click('[data-closed="seed-lunch"][data-place="s-steak"]'); pg.wait_for_timeout(200)
    check(pg.locator('.cand.out').count() == 2 and '소바' in (chosen() or ''), 'steak closed too -> only soba left (still shows closed note)')
    pg.click('[data-closed="seed-lunch"][data-place="s-soba"]'); pg.wait_for_timeout(200)
    check('모두 닫혔' in pg.locator('.card.slot').first.inner_text(), 'all closed -> asks for a new candidate')
    # add a new candidate from the list
    pg.select_option('[data-cand-add="seed-lunch"]', 's-kokusai'); pg.wait_for_timeout(200)
    check(pg.locator('.cands .cand').count() == 4 and '국제거리' in (chosen() or ''), 'new candidate added from the place list and picked')
    # undo steak closing
    pg.click('[data-reopen="seed-lunch"][data-place="s-steak"]'); pg.wait_for_timeout(200)
    check(pg.locator('.cand.out').count() == 2, 'reopen steak -> back in play')
    pg.click('[data-visited="seed-lunch"][data-place="s-steak"]'); pg.wait_for_timeout(200)
    check('방문함' in pg.locator('.card.slot').first.inner_text() and '스테이크' in (chosen() or ''), 'visited steak -> slot marked 방문함')
    # plain stop visited
    pg.click('[data-visited="p:s-churaumi"]'); pg.wait_for_timeout(200)
    check('방문함' in pg.locator('.card:not(.slot)', has_text='츄라우미').first.inner_text(), 'plain stop can be marked visited')
    # rest block + new slot
    check(pg.locator('.num.fixed', has_text='☕').count() == 1, 'rest block (no candidates) shown with ☕')
    pg.select_option('#slotLabel', '저녁'); pg.click('[data-slot-new]'); pg.wait_for_timeout(200)
    check(pg.locator('.card.slot').count() == 3, 'new 저녁 slot added')
    # sort with a place-less block must not break
    pos = "state.routes['2027-04-12'].findIndex(s => s.sid === 'seed-rest')"
    before = pg.evaluate(pos)
    n_items = pg.evaluate("state.routes['2027-04-12'].length")
    pg.click('[data-sort]'); pg.wait_for_timeout(200)
    check(pg.locator('.card.slot').count() == 3 and pg.locator('.num.fixed', has_text='☕').count() == 2, 'sort keeps all slots (rest + empty 저녁 both render as place-less blocks)')
    check(pg.evaluate(pos) == before and pg.evaluate("state.routes['2027-04-12'].length") == n_items, 'sort: place-less block keeps its position, no item lost')
    # timeline
    pg.click('[data-tab=timeline]'); pg.wait_for_timeout(300)
    day = pg.locator('.tl-day', has_text='4/12').inner_text()
    check('✅' in day and '스테이크' in day, 'timeline: visited steak')
    check('닫혀 있었음' in day and pg.locator('.tl-day li.muted').count() >= 2, 'timeline: closed attempts shown muted')
    check('츄라우미' in day, 'timeline: plain visited stop appears')
    check(day.count('스테이크') == 1, 'timeline: visited slot appears once (not also as a plan line)')
    # persistence
    saved = pg.evaluate("JSON.parse(localStorage.getItem('trip.visits')).length")
    check(saved >= 4, f'visits persisted to localStorage ({saved})')
    check(not errs, f'no page errors {errs}')
    b.close()
print('FAILS:', fails)
sys.exit(1 if fails else 0)

"""README scenario captures: Apps Script bundle + the E2E fake server → PNG screenshots and GIFs in docs/images/.

Run: python3 apps-script/build.py && python3 docs/capture.py
Needs: python playwright, /usr/bin/google-chrome, ffmpeg. Sample data only (data.js public landmarks), no personal data.
"""
import functools
import http.server
import json
import pathlib
import shutil
import subprocess
import sys
import tempfile
import threading
import time

from playwright.sync_api import sync_playwright

ROOT = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT / 'apps-script'))
from fake_gas import bundle  # noqa: E402  (shared with e2e_gas.py)

OUT = ROOT / 'docs' / 'images'
DESKTOP = {'width': 1280, 'height': 760}
MOBILE = {'width': 390, 'height': 780}
STEP_MS = 900
GIF_FPS = 7
GIF_COLORS = 96
TRIP = {'title': '오키나와 가족 여행', 'subtitle': '렌터카', 'payers': ['나', '동생', '엄마']}

# A visible cursor: GIFs need to show *where* the user tapped.
CURSOR = """
const c = document.createElement('div');
c.id = '__cursor';
c.style.cssText = 'position:fixed;z-index:99999;width:22px;height:22px;margin:-11px 0 0 -11px;border-radius:50%;'
  + 'background:rgba(226,87,76,.35);border:2px solid #e2574c;pointer-events:none;transition:left .35s,top .35s,transform .15s;left:-40px;top:-40px';
document.body.appendChild(c);
"""


def tap(page, selector):
    page.locator(selector).first.scroll_into_view_if_needed()
    box = page.locator(selector).first.bounding_box()
    page.evaluate("([x, y]) => { const c = document.getElementById('__cursor'); c.style.display = ''; c.style.left = x + 'px'; c.style.top = y + 'px'; }",
                  [box['x'] + box['width'] / 2, box['y'] + box['height'] / 2])
    page.wait_for_timeout(400)
    page.evaluate("document.getElementById('__cursor').style.transform = 'scale(.7)'")
    page.locator(selector).first.click()
    page.wait_for_timeout(150)
    page.evaluate("document.getElementById('__cursor').style.transform = ''")
    page.wait_for_timeout(STEP_MS)


def type_slowly(page, selector, text):
    tap(page, selector)
    page.locator(selector).first.press_sequentially(text, delay=120)
    page.wait_for_timeout(STEP_MS)


def open_page(browser, url, seed=None, access=None, viewport=DESKTOP, video_dir=None, mobile=False):
    ctx = browser.new_context(viewport=viewport, is_mobile=mobile, has_touch=mobile, device_scale_factor=2 if not video_dir else 1,
                              record_video_dir=video_dir, record_video_size=viewport if video_dir else None)
    page = ctx.new_page()
    page.on('dialog', lambda d: d.accept())
    page.goto(url)
    page.evaluate("([s, a]) => { localStorage.clear(); sessionStorage.clear(); sessionStorage.setItem('fakeSeed', s); if (a) sessionStorage.setItem('fakeAccess', a); }",
                  [json.dumps({'realisticTravel': True, 'configPatch': TRIP, **(seed or {})}, ensure_ascii=False), json.dumps(access) if access else ''])
    page.reload()
    page.wait_for_function("document.querySelector('.mock').innerText.match(/캘린더 일정|설정이 필요/)")
    page.wait_for_timeout(1200)  # map tiles
    page.evaluate(CURSOR)
    return ctx, page


def to_gif(webm, gif, width, skip_sec=0.0):
    """webm → palette GIF; skip_sec drops the page-loading part at the start of the recording."""
    palette = gif.with_suffix('.png.palette.png')
    vf = f'fps={GIF_FPS},scale={width}:-1:flags=lanczos'
    trim = ['-ss', f'{skip_sec:.2f}']
    subprocess.run(['ffmpeg', '-y', '-loglevel', 'error', *trim, '-i', str(webm), '-vf', f'{vf},palettegen=max_colors={GIF_COLORS}:stats_mode=diff', str(palette)], check=True)
    subprocess.run(['ffmpeg', '-y', '-loglevel', 'error', *trim, '-i', str(webm), '-i', str(palette),
                    '-lavfi', f'{vf}[x];[x][1:v]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle', str(gif)], check=True)
    palette.unlink()


def record(browser, url, name, scenario, viewport, width, mobile=False, **kw):
    with tempfile.TemporaryDirectory() as vid:
        t0 = time.monotonic()
        ctx, page = open_page(browser, url, viewport=viewport, video_dir=vid, mobile=mobile, **kw)
        loaded = time.monotonic() - t0
        page.wait_for_timeout(STEP_MS)  # one still frame of the starting screen
        scenario(page)
        page.wait_for_timeout(1500)
        ctx.close()  # flushes the video
        webm = next(pathlib.Path(vid).glob('*.webm'))
        to_gif(webm, OUT / f'{name}.gif', width, skip_sec=loaded)
    print(f'gif  {name}.gif  {(OUT / f"{name}.gif").stat().st_size // 1024} KB')


MAX_PNG_WIDTH = 1600


def shot(page, name, full_page=False):
    page.evaluate("document.getElementById('__cursor').style.display = 'none'")
    path = OUT / f'{name}.png'
    page.screenshot(path=str(path), full_page=full_page)
    if shutil.which('convert'):  # ImageMagick: 256-colour PNG, ≤ 1600 px wide → ~3x smaller for the repo
        subprocess.run(['convert', str(path), '-resize', f'{MAX_PNG_WIDTH}x>', '-strip', '-colors', '256', f'PNG8:{path}'], check=True)
    print(f'png  {name}.png  {path.stat().st_size // 1024} KB')


def place(page, name):
    return page.evaluate(f"PLACES.find(p => p.name.includes({name!r})).id")


# ---------- scenarios ----------

def plan_route(page):
    """여행 전: 셋째 날(월) 동선 — 장소 추가 → 월요일 휴무 경고 → 짧은 동선 정렬 → 캘린더 등록."""
    tap(page, '[data-date="2027-04-12"]')
    for name in ('만자모', '츄라우미 수족관', '소바집', '코우리 대교'):
        tap(page, f'#tab-route .chips.pick [data-add="{place(page, name)}"]')
    page.locator('#tab-route .timeline').scroll_into_view_if_needed()
    page.wait_for_timeout(STEP_MS)
    tap(page, '[data-sort]')
    tap(page, '[data-register]')
    page.wait_for_selector('[data-unregister]')
    page.wait_for_timeout(STEP_MS)


def log_expense(page):
    """여행 중(휴대폰): 지출 3건 입력 → 정산이 바로 갱신."""
    tap(page, '[data-tab="money"]')
    for amount, category, item, payer in (('2400', '식비', '소바', '나'), ('5600', '관광', '수족관 입장권', '동생'), ('980', '교통', '주차', '엄마')):
        type_slowly(page, '#amt', amount)
        tap(page, f'[data-category="{category}"]')
        type_slowly(page, '#item', item)
        tap(page, f'[data-payer="{payer}"]')
        tap(page, '#expForm .submit')
        page.wait_for_function('document.querySelector(".submit").innerText.includes("기록하기")')
    page.locator('.settle').scroll_into_view_if_needed()
    page.wait_for_timeout(STEP_MS * 2)


def main():
    if not shutil.which('ffmpeg'):
        raise SystemExit('ffmpeg not found')
    OUT.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        pathlib.Path(tmp, 'index.html').write_text(bundle(), encoding='utf-8')
        srv = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(http.server.SimpleHTTPRequestHandler, directory=tmp))
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        url = f'http://127.0.0.1:{srv.server_address[1]}/index.html'
        with sync_playwright() as pw:
            browser = pw.chromium.launch(executable_path='/usr/bin/google-chrome', args=['--no-sandbox'])

            record(browser, url, 'route-planning', plan_route, DESKTOP, 800)
            record(browser, url, 'expense-mobile', log_expense, MOBILE, 360, mobile=True)

            # stills (2x)
            ctx, page = open_page(browser, url)
            plan_route(page)
            tap(page, '[data-tab="plan"]')
            shot(page, 'calendar-plan')
            ctx.close()

            ctx, page = open_page(browser, url, viewport=MOBILE, mobile=True)
            log_expense(page)
            shot(page, 'settlement-mobile', full_page=True)
            ctx.close()

            ctx, page = open_page(browser, url, viewport=MOBILE, mobile=True, access={'required': True, 'key': ''})
            shot(page, 'view-only-mobile')
            ctx.close()

            ctx, page = open_page(browser, url, viewport=MOBILE, mobile=True)
            tap(page, '[data-tab="money"]')
            page.evaluate('fake.offline = true')
            type_slowly(page, '#amt', '1200')
            type_slowly(page, '#item', '편의점')
            tap(page, '#expForm .submit')
            page.locator('.ledger').scroll_into_view_if_needed()
            shot(page, 'offline-queue-mobile')
            ctx.close()

            ctx, page = open_page(browser, url, seed={'config': {'missing': ['My Maps 링크', '환율'], 'invalid': ['시작일: 있는 날짜를 YYYY-MM-DD 형식으로 입력하세요']}})
            shot(page, 'setup-check')
            ctx.close()
            browser.close()
        srv.shutdown()


if __name__ == '__main__':
    main()

"""Runs the real Places.js readMyMaps_() on a real KML file inside Chrome (XmlService etc. shimmed),
then converts with the client's placeFromMyMaps(). If a local prototype/places.js (personal CSV import,
git-ignored) exists, also compares every place with it.
Usage: python3 apps-script/kml_check.py <kml_file>
"""
import json
import pathlib
import sys
from playwright.sync_api import sync_playwright

ROOT = pathlib.Path(__file__).resolve().parent
PROTO = ROOT.parent / 'prototype'
kml = pathlib.Path(sys.argv[1]).read_text(encoding='utf-8')

SHIM = r"""
const wrap = (el) => el && ({
  getChild: (n) => wrap([...el.children].find((c) => c.localName === n)),
  getChildren: (n) => [...el.children].filter((c) => c.localName === n).map(wrap),
  getText: () => el.textContent,
  getAttribute: (n) => ({ getValue: () => el.getAttribute(n) }),
  getNamespace: () => null,
});
globalThis.XmlService = { parse: (s) => ({ getRootElement: () => wrap(new DOMParser().parseFromString(s, 'application/xml').documentElement) }) };
globalThis.UrlFetchApp = { fetch: () => ({ getResponseCode: () => 200, getContentText: () => window.__KML }) };
globalThis.CacheService = { getScriptCache: () => ({ get: () => null, put: () => {} }) };
globalThis.geocodeCalls = 0;
"""

with sync_playwright() as pw:
    browser = pw.chromium.launch(executable_path='/usr/bin/google-chrome', args=['--no-sandbox'])
    page = browser.new_page()
    page.set_content('<html><body></body></html>')
    page.evaluate('k => { window.__KML = k; }', kml)
    page.add_script_tag(content=SHIM)
    code = (ROOT / 'Places.js').read_text(encoding='utf-8')
    stubs = "function geocode_() { geocodeCalls++; return null; }\nfunction config_() { return { mid: 'test' }; }"
    page.add_script_tag(content=f'window.readMyMaps_ = (() => {{\n{code}\n{stubs}\nreturn readMyMaps_;\n}})();')
    has_csv = (PROTO / 'places.js').exists()
    for f in ('route.js', 'schedule.js', 'hours.js', 'data.js'):
        page.add_script_tag(content=(PROTO / f).read_text(encoding='utf-8'))
    page.add_script_tag(content=(PROTO / 'places.js').read_text(encoding='utf-8').replace('const PLACES', 'window.CSV_PLACES') if has_csv else 'window.CSV_PLACES = null;')
    app = (PROTO / 'app.js').read_text(encoding='utf-8')
    fn = app[app.index('function layerOfType'):app.index('// ponytail: swaps')]
    page.add_script_tag(content="const GMAPS = 'https://www.google.com/maps';\n" + fn)
    result = page.evaluate("""() => {
      const raw = readMyMaps_();
      if (raw.error) return { error: raw.error };
      const conv = raw.places.map(placeFromMyMaps);
      if (!CSV_PLACES) return { count: raw.places.length, geocodeCalls, noCsv: true, withHours: conv.filter((p) => p.hours).length };
      const csv = Object.fromEntries(CSV_PLACES.filter((p) => conv.some((c) => c.id === p.id)).map((p) => [p.id, p]));
      const diff = (k) => conv.filter((p) => csv[p.id] && JSON.stringify(csv[p.id][k]) !== JSON.stringify(p[k])).map((p) => p.name);
      return { count: raw.places.length, geocodeCalls, idMissing: conv.filter((p) => !csv[p.id]).length,
               hoursDiff: diff('hours'), layerDiff: diff('layer'), latDiff: conv.filter((p) => csv[p.id] && Math.abs(csv[p.id].lat - p.lat) > 1e-6).length };
    }""")
    browser.close()

print(json.dumps(result, ensure_ascii=False))
imported_ok = not result.get('hoursDiff') and not result.get('layerDiff') and not result.get('latDiff')
ok = result.get('count', 0) > 0 and (result.get('noCsv') or imported_ok)
print(f"{'PASS' if ok else 'FAIL'} real KML → {result.get('count')} places" + ('' if result.get('noCsv') else f" (CSV-imported ones identical; {result.get('idMissing')} new pins)"))
sys.exit(0 if ok else 1)

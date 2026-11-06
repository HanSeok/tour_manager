"""Bundle prototype/ (the Node-tested source) into Apps Script HTML files.

prototype/index.html  -> apps-script/index.html  (<script src="x.js"> -> <?!= include('js_x') ?>)
prototype/x.js        -> apps-script/js_x.html   (<script>…</script>)
Run: python3 -I apps-script/build.py   (then: cd apps-script && npx @google/clasp push)
"""
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parent
SRC = ROOT.parent / 'prototype'
TEMPLATE_LITERAL = re.compile(r'`[^`]*`')
LOCAL_SCRIPT = re.compile(r'<script src="([\w-]+)\.js"></script>')


def main():
    index = (SRC / 'index.html').read_text(encoding='utf-8')
    names = LOCAL_SCRIPT.findall(index)
    for name in names:
        body = (SRC / f'{name}.js').read_text(encoding='utf-8')
        if '</script' in body or '<?' in body:
            raise SystemExit(f'{name}.js contains "</script" or "<?" — would break the HtmlService template')
        # HtmlService strips "//" comments naively: a "//" inside a `template literal` truncates the code.
        bad = [m.group(0)[:60] for m in TEMPLATE_LITERAL.finditer(body) if '//' in m.group(0)]
        if bad:
            raise SystemExit(f'{name}.js: "//" inside template literal breaks on Apps Script: {bad}')
        (ROOT / f'js_{name}.html').write_text(f'<script>\n{body}\n</script>\n', encoding='utf-8')
    stale = [f for f in ROOT.glob('js_*.html') if f.stem[3:] not in names]
    for f in stale:  # e.g. an old bundle that is no longer referenced (clasp push would upload it)
        f.unlink()
    out = LOCAL_SCRIPT.sub(lambda m: f"<?!= include('js_{m.group(1)}') ?>", index)
    (ROOT / 'index.html').write_text(out, encoding='utf-8')
    print(f'built index.html + {len(names)} scripts: {", ".join(names)}')


if __name__ == '__main__':
    main()

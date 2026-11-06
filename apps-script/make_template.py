"""Create the public template spreadsheet (empty 설정 + 안내 tabs) in the clasp user's Drive.

Settings rows and guide lines come from Config.js (single source), so the template never drifts from the code.
Usage: python3 apps-script/make_template.py <out_json>   → prints/writes {"id": ..., "url": ...}
Then:  clasp create-script --parentId <id> --rootDir apps-script  (see README "개발자")
"""
import io
import json
import os
import pathlib
import subprocess
import sys
import urllib.parse
import urllib.request
import uuid

import openpyxl
from openpyxl.styles import Font, PatternFill

ROOT = pathlib.Path(__file__).resolve().parent
TEMPLATE_NAME = '여행 대시보드 (템플릿)'

EXTRACT = r"""
const fs = require('fs'); const vm = require('vm');
const lines = [];
const fakeSheet = { getRange: () => ({ setValues: (v) => lines.push(...v), setFontWeight: () => {} }), setColumnWidth: () => {} };
const ctx = { SpreadsheetApp: {}, Utilities: {}, console };
vm.createContext(ctx);
vm.runInContext(fs.readFileSync(process.argv[1], 'utf8') + '\nthis.F = CONFIG_FIELDS; writeGuide_(this.S);', Object.assign(ctx, { S: fakeSheet }));
process.stdout.write(JSON.stringify({ fields: ctx.F, guide: lines.map((r) => r[0]) }));
"""


def extract():
    out = subprocess.run(['node', '-e', EXTRACT, str(ROOT / 'Config.js')], capture_output=True, text=True, check=True)
    return json.loads(out.stdout)


def build_xlsx(spec):
    wb = openpyxl.Workbook()
    ws = wb.active
    ws.title = '설정'
    ws.append(['항목', '값', '설명'])
    for c in ws[1]:
        c.font = Font(bold=True)
    for key, label, required, default, desc in spec['fields']:
        ws.append([label, default, f"{'[필수] ' if required else '[선택] '}{desc}"])
        ws.cell(ws.max_row, 2).number_format = '0.####' if label == '환율' else '@'  # 환율 may be a GOOGLEFINANCE formula
        ws.cell(ws.max_row, 2).fill = PatternFill('solid', fgColor='FFF8E1')
    ws.column_dimensions['A'].width = 24
    ws.column_dimensions['B'].width = 36
    ws.column_dimensions['C'].width = 90
    ws.freeze_panes = 'A2'
    guide = wb.create_sheet('안내')
    for line in spec['guide']:
        guide.append([line])
    guide['A1'].font = Font(bold=True)
    guide.column_dimensions['A'].width = 140
    buf = io.BytesIO()
    wb.save(buf)
    return buf.getvalue()


def access_token():
    tok = json.load(open(os.path.expanduser('~/.clasprc.json')))['tokens']['default']
    data = urllib.parse.urlencode({'client_id': tok['client_id'], 'client_secret': tok['client_secret'],
                                   'refresh_token': tok['refresh_token'], 'grant_type': 'refresh_token'}).encode()
    return json.load(urllib.request.urlopen('https://oauth2.googleapis.com/token', data))['access_token']


def upload(xlsx):
    boundary = uuid.uuid4().hex
    meta = json.dumps({'name': TEMPLATE_NAME, 'mimeType': 'application/vnd.google-apps.spreadsheet'}).encode()
    body = (f'--{boundary}\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n'.encode() + meta
            + f'\r\n--{boundary}\r\nContent-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet\r\n\r\n'.encode()
            + xlsx + f'\r\n--{boundary}--'.encode())
    req = urllib.request.Request('https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id,webViewLink',
                                 data=body, headers={'Authorization': f'Bearer {access_token()}',
                                                     'Content-Type': f'multipart/related; boundary={boundary}'})
    return json.load(urllib.request.urlopen(req))


if __name__ == '__main__':
    spec = extract()
    info = upload(build_xlsx(spec))
    result = {'id': info['id'], 'url': info['webViewLink'], 'copy': f"https://docs.google.com/spreadsheets/d/{info['id']}/copy"}
    print(json.dumps(result, ensure_ascii=False))
    if len(sys.argv) > 1:
        pathlib.Path(sys.argv[1]).write_text(json.dumps(result), encoding='utf-8')

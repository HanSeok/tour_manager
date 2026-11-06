// Owner-only sheet-menu actions. Every one calls SpreadsheetApp.getUi() FIRST: web users can call any
// public function through google.script.run, and getUi() throws in the web-app context before anything changes.
const EDIT_KEY_PROP = 'EDIT_KEY';
const EDIT_KEY_RE = /^[0-9a-f]{32}$/;

const editKey_ = () => {
  const k = PropertiesService.getScriptProperties().getProperty(EDIT_KEY_PROP);
  return k && EDIT_KEY_RE.test(k) ? k : '';
};

/** Gate for every server write. No EDIT_KEY set → open (link holders edit, as before). */
function requireEdit_(key) {
  const k = editKey_();
  if (k && key !== k) fail_('보기 전용 링크입니다. 편집하려면 편집 링크(?k=…)로 여세요');
}

const newEditKey_ = () => Utilities.getUuid().replace(/-/g, '');

function showEditLinks() {
  const ui = SpreadsheetApp.getUi();
  let k = editKey_();
  if (!k) {
    k = newEditKey_();
    PropertiesService.getScriptProperties().setProperty(EDIT_KEY_PROP, k);
  }
  ui.alert('보기·편집 링크', editLinksText_(k), ui.ButtonSet.OK);
}

function rotateEditKey() {
  const ui = SpreadsheetApp.getUi();
  if (ui.alert('편집 키 새로 만들기', '지금까지 나눠 준 편집 링크는 더 이상 편집할 수 없게 됩니다. 계속할까요?', ui.ButtonSet.YES_NO) !== ui.Button.YES) return;
  const k = newEditKey_();
  PropertiesService.getScriptProperties().setProperty(EDIT_KEY_PROP, k);
  ui.alert('새 편집 링크', editLinksText_(k), ui.ButtonSet.OK);
}

function editLinksText_(k) {
  const url = ScriptApp.getService().getUrl();
  if (!url) return '아직 웹 앱으로 배포되지 않았습니다. 확장 프로그램 → Apps Script → 배포 → 새 배포 후 다시 여세요.';
  return `보기 링크 (일정·지출 보기만):\n${url}\n\n편집 링크 (지출 입력·동선 수정):\n${url}?k=${k}\n\n편집 링크는 같이 여행하는 사람에게만 보내세요.`;
}

function createTripCalendar() {
  const ui = SpreadsheetApp.getUi();
  const owner = SpreadsheetApp.getActive().getOwner();
  const me = Session.getActiveUser().getEmail();
  // The web app runs as the deployer (normally the sheet owner): a calendar in another editor's account is invisible to it.
  if (owner && me && owner.getEmail() !== me) return ui.alert(`이 메뉴는 스프레드시트 소유자(${owner.getEmail()})가 실행해야 합니다. 대시보드는 소유자 계정으로 캘린더를 읽습니다.`);
  const sheet = SpreadsheetApp.getActive().getSheetByName(CONFIG_SHEET);
  if (!sheet) return ui.alert('먼저 메뉴 [설정·안내 탭 만들기]를 실행하세요.');
  const row = sheet.getRange(1, 1, sheet.getLastRow(), 1).getValues().findIndex((r) => String(r[0]).trim() === '캘린더 ID') + 1;
  if (row < 1) return ui.alert("설정 탭에 '캘린더 ID' 행이 없습니다. 항목 칸에 '캘린더 ID'를 추가한 뒤 다시 실행하세요.");
  const current = String(sheet.getRange(row, 2).getValue()).trim();
  if (current && ui.alert('캘린더 ID가 이미 있습니다', '새 캘린더를 만들어 바꿀까요?', ui.ButtonSet.YES_NO) !== ui.Button.YES) return;
  const title = String(config_().title || '여행');
  const cal = CalendarApp.createCalendar(`${title} (여행 대시보드)`, { timeZone: config_().timezone || Session.getScriptTimeZone() });
  sheet.getRange(row, 2).setNumberFormat('@').setValue(cal.getId());
  configMemo_ = null;
  ui.alert('여행 전용 캘린더를 만들었습니다', `Google 캘린더에 '${cal.getName()}'가 생겼습니다. 항공편·숙박·예약 일정을 이 캘린더로 옮기거나 새로 넣으세요.`, ui.ButtonSet.OK);
}

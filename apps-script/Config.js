// Trip settings live in the "설정" tab (항목 | 값 | 설명). Everything trip-specific comes from here.
const CONFIG_SHEET = '설정';
const GUIDE_SHEET = '안내';
const MAX_TRIP_DAYS = 31;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
const TIME_RE = /^([01]?\d|2[0-3]):[0-5]\d$/; // "9:00" is fine (typed or sheet-converted)
const normTime_ = (s) => (TIME_RE.test(s) ? s.padStart(5, '0') : s);
const RESERVED_SHEETS = ['설정', '안내', '_이동시간', '_영업시간'];
const MAX_BUFFER_MIN = 600;
// 2027-02-30 passes DATE_RE and Date.parse (rolls over) → require an exact round trip
const isRealDate_ = (s) => DATE_RE.test(s) && new Date(`${s}T00:00:00Z`).toISOString().slice(0, 10) === s;

// [key, 항목 label, required, template value, 설명]
const CONFIG_FIELDS = [
  ['title', '여행 제목', true, '', '화면 상단 제목. 예: 오키나와 여행'],
  ['start', '시작일', true, '', 'YYYY-MM-DD. 예: 2027-04-10'],
  ['end', '종료일', true, '', 'YYYY-MM-DD. 예: 2027-04-14'],
  ['timezone', '현지 시간대', true, '', 'IANA 시간대. 예: Asia/Tokyo, Asia/Bangkok, Europe/Paris, America/Los_Angeles'],
  ['destKeywords', '목적지 키워드', true, '', '쉼표로 구분. 항공편 일정 제목의 → 오른쪽에 있으면 도착편, 왼쪽이면 출국편. Gmail 자동 일정(Flight to Naha)도 인식하도록 영문 지명도 넣으세요. 예: 오키나와, 나하, Naha, OKA'],
  ['myMapsUrl', 'My Maps 링크', true, '', 'My Maps 지도 주소 (공유: 링크가 있는 모든 사용자 보기)'],
  ['localCurrency', '현지 통화 이름', true, '', '예: 엔, 바트, 유로'],
  ['localSymbol', '현지 통화 기호', true, '', '예: ¥, ฿, €'],
  ['homeSymbol', '환산 통화 기호', true, '₩', '합계를 환산해 보여줄 통화 기호'],
  ['rate', '환율', true, '', '현지 통화 1 = 환산 통화 얼마. 예: 9.3 또는 =GOOGLEFINANCE("CURRENCY:JPYKRW") (자동)'],
  ['payers', '결제자', true, '나, 배우자', '쉼표로 구분'],
  ['methods', '결제수단', true, '카드, 현금', '쉼표로 구분'],
  ['expenseSheet', '지출 시트 이름', true, '여행_지출', '이 이름의 탭에 지출이 기록됩니다 (없으면 자동 생성)'],
  ['subtitle', '부제', false, '', '제목 옆 짧은 메모. 예: 렌터카'],
  ['arrivalBufferMin', '입국 소요(분)', false, '60', '도착편 착륙 후 출발까지 걸리는 시간'],
  ['flightLeadMin', '출국편 공항 도착(분 전)', false, '90', '출국편 출발 몇 분 전까지 공항에 도착할지'],
  ['defaultStart', '기본 출발 시각', false, '09:00', '숙소에서 출발하는 날의 기본 시각 (HH:MM)'],
  ['travelMode', '이동수단', false, '자동차', '자동차 / 대중교통 / 도보 중 하나. 이동시간과 길찾기 링크에 쓰입니다'],
  ['currencyDecimals', '현지 통화 소수 자릿수', false, '0', '0~2. 유로·달러처럼 센트가 있으면 2'],
  ['homeDecimals', '환산 통화 소수 자릿수', false, '0', '0~2. 원화는 0, 달러는 2'],
  ['calendarId', '캘린더 ID', false, '', '비우면 기본 캘린더. 메뉴 [여행 대시보드 → 여행 전용 캘린더 만들기]를 권장 (개인 일정이 노출되지 않음)'],
  ['tileUrl', '지도 타일 주소', false, '', '비우면 OpenStreetMap. 예: https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}.png?api_key=키'],
];
const TRAVEL_MODES = { 자동차: 'driving', 대중교통: 'transit', 도보: 'walking' };
const RATE_LABEL = '환율';

const isTileUrl_ = (u) => /^https:\/\/[^\s"'<>`]+$/.test(String(u || '')) && ['{z}', '{x}', '{y}'].every((t) => String(u).includes(t));
const splitList_ = (s) => String(s || '').split(/[,，]/).map((x) => x.trim()).filter(Boolean);
const toInt_ = (s, fallback) => (String(s).trim() === '' || !Number.isFinite(Number(s)) ? fallback : Math.round(Number(s)));
// Sheets may auto-convert typed dates/times into Date objects → back to the text the user meant.
function cellText_(v) {
  if (!(v instanceof Date)) return String(v == null ? '' : v).trim();
  const tz = SpreadsheetApp.getActive().getSpreadsheetTimeZone();
  return Utilities.formatDate(v, tz, v.getFullYear() < 1900 ? 'HH:mm' : 'yyyy-MM-dd');
}

/** Read + validate settings. Memoized per execution. → {…values, offset, mid, days, missing[], invalid[]} */
let configMemo_ = null;
function config_() {
  if (configMemo_) return configMemo_;
  const sheet = SpreadsheetApp.getActive().getSheetByName(CONFIG_SHEET);
  const rows = sheet ? sheet.getRange(1, 1, Math.max(sheet.getLastRow(), 1), 2).getValues() : [];
  const byLabel = Object.fromEntries(rows.map((r) => [cellText_(r[0]), cellText_(r[1])]));
  const raw = Object.fromEntries(CONFIG_FIELDS.map(([key, label, , def]) => [key, byLabel[label] !== undefined ? byLabel[label] : def]));
  configMemo_ = validateConfig_(raw, Boolean(sheet));
  return configMemo_;
}

function validateConfig_(raw, hasSheet) {
  const missing = hasSheet ? CONFIG_FIELDS.filter(([key, , required]) => required && !raw[key]).map(([, label]) => label) : ['설정 탭'];
  const invalid = [];
  const bad = (label, why) => invalid.push(`${label}: ${why}`);
  if (raw.start && !isRealDate_(raw.start)) bad('시작일', '있는 날짜를 YYYY-MM-DD 형식으로 입력하세요');
  if (raw.end && !isRealDate_(raw.end)) bad('종료일', '있는 날짜를 YYYY-MM-DD 형식으로 입력하세요');
  const days = isRealDate_(raw.start) && isRealDate_(raw.end)
    ? Math.round((Date.parse(raw.end) - Date.parse(raw.start)) / 86400000) + 1 : 0;
  if (raw.start && raw.end && (days < 1 || days > MAX_TRIP_DAYS)) bad('종료일', `시작일 이후 ${MAX_TRIP_DAYS}일 이내여야 합니다`);
  const rate = Number(raw.rate);
  if (raw.rate && !(rate > 0)) bad('환율', '0보다 큰 숫자여야 합니다');
  const midMatch = String(raw.myMapsUrl).match(/[?&]mid=([\w-]+)/);
  if (raw.myMapsUrl && !midMatch) bad('My Maps 링크', "주소에 'mid=' 가 없습니다");
  let offset = '+00:00';
  if (raw.timezone) {
    try {
      new Intl.DateTimeFormat('en-US', { timeZone: raw.timezone }); // RangeError for unknown zones (e.g. Asia/Tokio)
      const at = new Date(`${isRealDate_(raw.start) ? raw.start : '2026-01-01'}T12:00:00Z`);
      offset = Utilities.formatDate(at, raw.timezone, 'XXX').replace(/^Z$/, '+00:00');
    } catch (err) {
      bad('현지 시간대', 'Asia/Tokyo 같은 IANA 시간대 이름이어야 합니다');
    }
  }
  const listFields = [['destKeywords', '목적지 키워드'], ['payers', '결제자'], ['methods', '결제수단']];
  listFields.forEach(([key, label]) => { if (raw[key] && !splitList_(raw[key]).length) bad(label, '쉼표로 구분한 값이 하나 이상 있어야 합니다'); });
  if (RESERVED_SHEETS.includes(raw.expenseSheet)) bad('지출 시트 이름', `'${raw.expenseSheet}'는 대시보드가 쓰는 탭 이름입니다`);
  if (raw.defaultStart && !TIME_RE.test(raw.defaultStart)) bad('기본 출발 시각', 'HH:MM 형식이어야 합니다 (예: 09:00)');
  [['arrivalBufferMin', '입국 소요(분)'], ['flightLeadMin', '출국편 공항 도착(분 전)']].forEach(([key, label]) => {
    const v = Number(raw[key]);
    if (String(raw[key]).trim() !== '' && !(Number.isFinite(v) && v >= 0 && v <= MAX_BUFFER_MIN)) bad(label, `0~${MAX_BUFFER_MIN} 사이의 숫자여야 합니다`);
  });
  if (raw.travelMode && !TRAVEL_MODES[raw.travelMode]) bad('이동수단', `${Object.keys(TRAVEL_MODES).join(' / ')} 중 하나여야 합니다`);
  [['currencyDecimals', '현지 통화 소수 자릿수'], ['homeDecimals', '환산 통화 소수 자릿수']].forEach(([key, label]) => {
    if (String(raw[key] || '').trim() !== '' && !/^[0-2]$/.test(String(raw[key]).trim())) bad(label, '0, 1, 2 중 하나여야 합니다');
  });
  const decimalsOf = (key) => Math.min(2, Math.max(0, toInt_(raw[key], 0)));
  if (raw.calendarId && (/\s/.test(raw.calendarId) || raw.calendarId.length > 200)) bad('캘린더 ID', '공백 없는 캘린더 ID여야 합니다 (캘린더 설정 → 캘린더 통합)');
  if (raw.tileUrl && !isTileUrl_(raw.tileUrl)) bad('지도 타일 주소', 'https:// 로 시작하고 {z} {x} {y} 가 들어 있어야 합니다');
  const defaultStart = TIME_RE.test(raw.defaultStart) ? normTime_(raw.defaultStart) : '09:00';
  return {
    title: raw.title, subtitle: raw.subtitle, start: raw.start, end: raw.end, days,
    timezone: raw.timezone || 'UTC', offset, // ponytail: one offset for the whole trip; a DST switch mid-trip shifts times by 1h
    destKeywords: splitList_(raw.destKeywords),
    mid: midMatch ? midMatch[1] : '',
    localCurrency: raw.localCurrency, localSymbol: raw.localSymbol, homeSymbol: raw.homeSymbol, rate,
    payers: splitList_(raw.payers), methods: splitList_(raw.methods), expenseSheet: raw.expenseSheet,
    arrivalBufferMin: toInt_(raw.arrivalBufferMin, 60), flightLeadMin: toInt_(raw.flightLeadMin, 90),
    defaultStart,
    travelMode: TRAVEL_MODES[raw.travelMode] || 'driving',
    currencyDecimals: decimalsOf('currencyDecimals'),
    homeDecimals: decimalsOf('homeDecimals'),
    calendarId: raw.calendarId || '',
    tileUrl: isTileUrl_(raw.tileUrl) ? raw.tileUrl : '',
    missing, invalid,
  };
}

const isConfigured_ = (cfg) => !cfg.missing.length && !cfg.invalid.length;

/** Client-safe subset (no secrets live in config anyway). */
function publicConfig_(cfg) {
  const { mid, calendarId, ...rest } = cfg;
  return { ...rest, hasMyMaps: Boolean(mid) };
}

/* ---------- first-run setup: 설정 + 안내 tabs, sheet menu ---------- */

function onOpen() {
  SpreadsheetApp.getUi().createMenu('여행 대시보드')
    .addItem('설정·안내 탭 만들기', 'setup')
    .addItem('여행 전용 캘린더 만들기', 'createTripCalendar')
    .addItem('보기·편집 링크 보기', 'showEditLinks')
    .addItem('편집 키 새로 만들기 (이전 편집 링크 무효)', 'rotateEditKey')
    .addToUi();
}

/** Menu / editor entry point. Takes no values: web users can call any public function. */
function setup() {
  SpreadsheetApp.getUi(); // throws for web callers (google.script.run) before anything is created
  return setup_({});
}

/** Creates the 설정 and 안내 tabs if missing (existing values are never overwritten). */
function setup_(values) {
  const ss = SpreadsheetApp.getActive();
  let sheet = ss.getSheetByName(CONFIG_SHEET);
  if (!sheet) {
    sheet = ss.insertSheet(CONFIG_SHEET, 0);
    const preset = values || {};
    const rows = CONFIG_FIELDS.map(([key, label, required, def, desc]) => [label, preset[key] !== undefined ? preset[key] : def, `${required ? '[필수] ' : '[선택] '}${desc}`]);
    sheet.getRange(1, 1, 1, 3).setValues([['항목', '값', '설명']]).setFontWeight('bold');
    sheet.getRange(2, 2, rows.length, 1).setNumberFormat('@').setBackground('#fff8e1'); // text before values: no formulas
    const rateRow = CONFIG_FIELDS.findIndex(([, label]) => label === RATE_LABEL) + 2;
    sheet.getRange(rateRow, 2).setNumberFormat('0.####'); // owner-only cell: a GOOGLEFINANCE formula must evaluate
    sheet.getRange(2, 1, rows.length, 3).setValues(rows);
    sheet.setFrozenRows(1);
    sheet.setColumnWidth(1, 170);
    sheet.setColumnWidth(2, 260);
    sheet.setColumnWidth(3, 520);
  }
  if (!ss.getSheetByName(GUIDE_SHEET)) writeGuide_(ss.insertSheet(GUIDE_SHEET, 1));
  configMemo_ = null;
  return { created: true };
}

function writeGuide_(sheet) {
  const lines = [
    ['여행 대시보드 사용법 (자세한 설명: https://github.com/chongjae/tour_manager)'],
    [''],
    ['1. [설정] 탭의 노란 칸(값)을 채웁니다. [필수] 항목이 비어 있으면 대시보드가 설정 안내만 보여줍니다.'],
    ['2. 메뉴 확장 프로그램 → Apps Script → 오른쪽 위 [배포] → [새 배포] → 유형: 웹 앱'],
    ['   · 다음 사용자 인증 정보로 실행: 나 / 액세스 권한: Google 계정이 있는 모든 사용자 → [배포]'],
    ['   · 권한 검토 → 계정 선택 → 고급 → (안전하지 않음)으로 이동 → 허용  (직접 만든 스크립트라 뜨는 경고입니다)'],
    ['3. 배포 후 나오는 웹 앱 URL이 대시보드 주소입니다. 같이 여행하는 사람에게 이 주소만 보내면 됩니다.'],
    ['   · 주의: 주소를 아는 Google 로그인 사용자는 누구나 일정·지출을 볼 수 있습니다.'],
    ['   · 편집을 제한하려면 메뉴 [여행 대시보드 → 보기·편집 링크 보기]: 보기 링크로는 바꿀 수 없고, 편집 링크(?k=…)만 지출·동선을 바꿉니다.'],
    ['   · 개인 일정을 숨기려면 메뉴 [여행 대시보드 → 여행 전용 캘린더 만들기] 후 항공·숙박·예약 일정을 그 캘린더에 넣으세요.'],
    ['4. 장소: My Maps(mymaps.google.com)에 지도를 만들고 장소를 추가 → 공유: 링크가 있는 모든 사용자 보기 → 설정의 [My Maps 링크]에 주소 입력'],
    ['5. 캘린더(기본 캘린더) 일정 규칙'],
    ['   · 항공편: 제목에 화살표 → 를 넣고 도착지/출발지에 [목적지 키워드]를 씁니다. 예: "KE741 인천 → 오키나와(나하)"'],
    ['   · 숙박: 체크인~체크아웃을 하나의 여러 날 일정으로, 위치에 숙소 주소를 넣습니다.'],
    ['   · 그 밖에 시간·위치가 있는 일정(예약 등)은 동선에 고정 경유지로 들어갑니다.'],
    ['6. (선택) 새 장소 영업시간 자동 조회: Google Cloud에서 Places API (New) 키를 만들어 Apps Script → 프로젝트 설정 → 스크립트 속성에 PLACES_API_KEY 로 저장'],
  ];
  sheet.getRange(1, 1, lines.length, 1).setValues(lines);
  sheet.getRange(1, 1).setFontWeight('bold');
  sheet.setColumnWidth(1, 900);
}

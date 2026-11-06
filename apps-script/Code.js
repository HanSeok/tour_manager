// Travel dashboard — Apps Script server core (bound to the trip spreadsheet). Trip values: Config.js / "설정" tab.
const ROUTE_TAG = '#trip-route';
const ROUTE_TAG_RE = /^#(?:trip|okinawa)-route\b/; // description must START with it; okinawa-route = first (Okinawa-only) version
const CATEGORIES = ['식비', '교통', '숙박', '쇼핑', '관광', '기타'];
const STATE_KEYS = ['routes', 'starts', 'startFrom', 'eventPlace'];
const MAX_PROP_CHARS = 8500; // Script Properties: 9 KB per value
const TRAVEL_SHEET = '_이동시간';
const MAX_TRAVEL_BATCH = 25;
const MAX_AMOUNT = 100000000;
const GEOCODE_CACHE_SEC = 6 * 60 * 60;

function doGet() {
  const title = config_().title || '여행 대시보드';
  return HtmlService.createTemplateFromFile('index').evaluate()
    .setTitle(title)
    .addMetaTag('viewport', 'width=device-width, initial-scale=1');
}

function include(name) {
  return HtmlService.createHtmlOutputFromFile(name).getContent();
}

function withLock_(fn) {
  const lock = LockService.getScriptLock();
  lock.waitLock(15000);
  try { return fn(); } finally { lock.releaseLock(); }
}

function fail_(message) { throw new Error(message); }

/** Text from web users must never become a formula in the owner's sheet (=IMPORTXML, =IMAGE … can leak data). */
const safeCell_ = (v) => (typeof v === 'string' && /^[=+\-@\t\r]/.test(v) ? `'${v}` : v);

function requireConfig_() {
  const cfg = config_();
  if (!isConfigured_(cfg)) fail_('설정 탭이 아직 완성되지 않았습니다');
  return cfg;
}

/** First call from the page. Unconfigured → only the config (the page shows a setup checklist). */
function bootstrap() {
  const cfg = config_();
  if (!isConfigured_(cfg)) return { config: publicConfig_(cfg) };
  const sheet = ensureTripSheet_();
  return { config: publicConfig_(cfg), sheet, events: getTripEvents_(), expenses: readExpenses_(), state: readState_(), places: getPlaces_() };
}

/* ---------- shared UI state (routes etc.) ---------- */

function readState_() {
  const props = PropertiesService.getScriptProperties();
  return STATE_KEYS.reduce((acc, k) => {
    const raw = props.getProperty(`state.${k}`);
    if (!raw) return acc;
    try {
      const v = JSON.parse(raw);
      return STATE_SHAPES[k](v) ? { ...acc, [k]: v } : acc;
    } catch (err) {
      console.error(`state.${k} unreadable: ${err}`);
      return acc;
    }
  }, {});
}

// Shape checks so one bad write can't break the page for everyone (state is shared).
const isPlainObj_ = (v) => v !== null && typeof v === 'object' && !Array.isArray(v);
const isShortStr_ = (v) => typeof v === 'string' && v.length > 0 && v.length <= 200;
const STATE_SHAPES = {
  routes: (v) => isPlainObj_(v) && Object.entries(v).every(([d, list]) => DATE_RE.test(d) && Array.isArray(list) && list.length <= 60
    && list.every((s) => isPlainObj_(s) && (isShortStr_(s.ev) || (isShortStr_(s.id) && Number.isFinite(s.stayMin) && s.stayMin >= 0 && s.stayMin <= 1440)))),
  starts: (v) => isPlainObj_(v) && Object.entries(v).every(([d, m]) => DATE_RE.test(d) && Number.isInteger(m) && m >= 0 && m < 1440),
  startFrom: (v) => isPlainObj_(v) && Object.entries(v).every(([d, id]) => DATE_RE.test(d) && isShortStr_(id)),
  eventPlace: (v) => isPlainObj_(v) && Object.entries(v).every(([k, id]) => k.length <= 300 && typeof id === 'string' && id.length <= 200),
};

function saveState(key, value) {
  if (!STATE_KEYS.includes(key)) fail_(`알 수 없는 상태 키: ${key}`);
  if (!STATE_SHAPES[key](value)) fail_(`저장 형식이 올바르지 않습니다: ${key}`);
  const json = JSON.stringify(value);
  if (json.length > MAX_PROP_CHARS) fail_('저장할 동선 데이터가 너무 큽니다');
  PropertiesService.getScriptProperties().setProperty(`state.${key}`, json);
  return true;
}

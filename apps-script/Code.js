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

const VERSION = '2.0.0'; // keep equal to /VERSION (server.test.mjs checks)
const VERSION_URL = 'https://raw.githubusercontent.com/chongjae/tour_manager/main/VERSION';
const VERSION_CACHE_SEC = 6 * 60 * 60;

function doGet(e) {
  const title = config_().title || '여행 대시보드';
  const k = editKey_();
  const given = String((e && e.parameter && e.parameter.k) || '');
  const page = HtmlService.createTemplateFromFile('index');
  // Only an exact match is echoed back (hex-only, so safe inside the inline <script>).
  page.editAccess = JSON.stringify({ required: Boolean(k), key: k && given === k ? k : '' });
  return page.evaluate()
    .setTitle(title)
    .addMetaTag('viewport', 'width=device-width, initial-scale=1');
}

/** Latest released version from GitHub (cached). null when unknown — never blocks the page. */
function latestVersion_() {
  const cache = CacheService.getScriptCache();
  const hit = cache.get('latestVersion');
  if (hit) return hit === '-' ? null : hit;
  let v = null;
  try {
    const res = UrlFetchApp.fetch(VERSION_URL, { muteHttpExceptions: true });
    const text = res.getContentText().trim();
    v = res.getResponseCode() === 200 && /^\d+\.\d+\.\d+$/.test(text) ? text : null;
  } catch (err) {
    console.warn(`version check failed: ${err}`);
  }
  cache.put('latestVersion', v || '-', VERSION_CACHE_SEC);
  return v;
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

/** First call from the page. Unconfigured → config + self-check results (the page shows a setup checklist). */
function bootstrap() {
  const cfg = config_();
  const version = { current: VERSION, latest: latestVersion_() };
  if (!isConfigured_(cfg)) return { config: publicConfig_(cfg), version, checks: diagnose() };
  const sheet = ensureTripSheet_();
  return { config: publicConfig_(cfg), version, sheet, events: getTripEvents_(), expenses: readExpenses_(), state: readState_(), places: getPlaces_() };
}

/** Cheap re-read of what companions change (shared state, expenses, calendar). */
function refresh() {
  requireConfig_();
  return { state: readState_(), expenses: readExpenses_(), events: getTripEvents_() };
}

/** Setup self-check → [{label, ok, detail}]. ok: true | false | null (optional, not set). Reveals no secrets.
 *  Cached briefly: anyone with the link can call it, and it fetches My Maps. */
function diagnose() {
  const cache = CacheService.getScriptCache();
  const hit = cache.get('diagnose');
  if (hit) return JSON.parse(hit);
  const out = diagnoseUncached_();
  cache.put('diagnose', JSON.stringify(out), DIAGNOSE_CACHE_SEC);
  return out;
}

function diagnoseUncached_() {
  const cfg = config_();
  const check = (label, fn) => {
    try { return { label, ...fn() }; } catch (err) { return { label, ok: false, detail: String(err.message || err).slice(0, 160) }; }
  };
  return [
    check('설정 탭', () => (isConfigured_(cfg) ? { ok: true, detail: '필수 항목 완료' } : { ok: false, detail: `빈 항목 ${cfg.missing.length}개 · 형식 오류 ${cfg.invalid.length}개` })),
    check('My Maps 공유', () => {
      if (!cfg.mid) return { ok: false, detail: 'My Maps 링크가 없습니다' };
      const code = UrlFetchApp.fetch(`https://www.google.com/maps/d/kml?mid=${encodeURIComponent(cfg.mid)}&forcekml=1`, { muteHttpExceptions: true }).getResponseCode();
      return code === 200 ? { ok: true, detail: '읽기 가능' } : { ok: false, detail: `HTTP ${code} — 공유를 '링크가 있는 모든 사용자 보기'로` };
    }),
    check('캘린더', () => (cfg.calendarId ? (calendar_(), { ok: true, detail: '여행 전용 캘린더 연결됨' }) : { ok: null, detail: '기본 캘린더 (여행 전용 캘린더 권장)' })),
    check('Places API 키', () => (PropertiesService.getScriptProperties().getProperty('PLACES_API_KEY') ? { ok: true, detail: '있음' } : { ok: null, detail: '없음 (선택: 영업시간 자동 조회)' })),
    check('편집 키', () => (editKey_() ? { ok: true, detail: '보기·편집 링크 분리됨' } : { ok: null, detail: '없음 (선택: 링크를 가진 누구나 편집)' })),
  ];
}

/* ---------- shared UI state (routes etc.) ---------- */

const ROUTE_DAY_PREFIX = 'state.routes.';
const DATE_KEYED_STATE = ['routes', 'starts', 'startFrom'];
const DIAGNOSE_CACHE_SEC = 60;

function readProp_(props, name) {
  const raw = props.getProperty(name);
  if (!raw) return undefined;
  try { return JSON.parse(raw); } catch (err) { console.error(`${name} unreadable: ${err}`); return undefined; }
}

/** routes: legacy single property + per-date properties (per-date wins). */
function readRoutes_(props) {
  const legacy = readProp_(props, 'state.routes');
  const base = STATE_SHAPES.routes(legacy) ? legacy : {};
  const days = Object.entries(props.getProperties())
    .filter(([name]) => name.startsWith(ROUTE_DAY_PREFIX))
    .map(([name, raw]) => [name.slice(ROUTE_DAY_PREFIX.length), (() => { try { return JSON.parse(raw); } catch (err) { return null; } })()])
    .filter(([d, list]) => STATE_SHAPES.routes({ [d]: list }));
  return { ...base, ...Object.fromEntries(days) };
}

function readState_() {
  const props = PropertiesService.getScriptProperties();
  return STATE_KEYS.reduce((acc, k) => {
    const v = k === 'routes' ? readRoutes_(props) : readProp_(props, `state.${k}`);
    return v !== undefined && STATE_SHAPES[k](v) ? { ...acc, [k]: v } : acc;
  }, {});
}

/** {a:1,b:2} + {b:null,c:3} → {a:1,c:3} */
const applyPatch_ = (base, patch) => Object.entries(patch).reduce(
  (acc, [k, v]) => (v === null ? Object.fromEntries(Object.entries(acc).filter(([x]) => x !== k)) : { ...acc, [k]: v }), base);

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

/** Merges `patch` ({subkey: value|null}) into shared state `key` under the lock → merged value.
 *  Companions editing different days no longer overwrite each other. */
function saveState(key, patch, editKey) {
  requireEdit_(editKey);
  if (!STATE_KEYS.includes(key)) fail_(`알 수 없는 상태 키: ${key}`);
  if (!isPlainObj_(patch) || Object.keys(patch).length > MAX_TRIP_DAYS * 4) fail_(`저장 형식이 올바르지 않습니다: ${key}`);
  return withLock_(() => {
    const props = PropertiesService.getScriptProperties();
    const current = readState_()[key] || {};
    const merged = applyPatch_(current, patch);
    if (!STATE_SHAPES[key](merged)) fail_(`저장 형식이 올바르지 않습니다: ${key}`);
    const cfg = requireConfig_();
    const isOutside = (d) => d < cfg.start || d > cfg.end;
    if (DATE_KEYED_STATE.includes(key) && Object.entries(patch).some(([d, v]) => v !== null && isOutside(d))) fail_('날짜가 여행 기간이 아닙니다');
    if (key === 'routes') {
      const writes = Object.fromEntries(Object.entries(merged).map(([d, list]) => [`${ROUTE_DAY_PREFIX}${d}`, JSON.stringify(list)]));
      if (Object.values(writes).some((json) => json.length > MAX_PROP_CHARS)) fail_('하루 동선 데이터가 너무 큽니다');
      props.setProperties(writes); // keeps other properties
      Object.keys(current).filter((d) => !(d in merged)).forEach((d) => props.deleteProperty(`${ROUTE_DAY_PREFIX}${d}`));
      props.deleteProperty('state.routes'); // legacy single key, now migrated
    } else {
      const json = JSON.stringify(merged);
      if (json.length > MAX_PROP_CHARS) fail_('저장할 데이터가 너무 큽니다');
      props.setProperty(`state.${key}`, json);
    }
    return merged;
  });
}

// Dashboard UI. Apps Script mode: data/config from the server (remote.js). Prototype mode: SAMPLE_* from data.js.
const $ = (s) => document.querySelector(s);
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const pad = (n) => String(n).padStart(2, '0');
const fmt = (m) => `${pad(Math.floor(m / 60))}:${pad(m % 60)}`;
const fmtClock = (m) => (m === 1440 ? '24:00' : m > 1440 ? `${fmt(m - 1440)}(+1)` : fmt(m));
const dur = (m) => (m < 60 ? `${m}분` : `${Math.floor(m / 60)}시간${m % 60 ? ` ${m % 60}분` : ''}`);
// Trip settings (설정 tab). applyConfig() recomputes everything derived from them.
let CFG = SAMPLE_CONFIG;
let DATES = [];
const local = (n) => {
  const d = CFG.currencyDecimals || 0;
  return `${CFG.localSymbol}${n.toLocaleString('ko-KR', { minimumFractionDigits: d, maximumFractionDigits: d })}`;
};
/** Amount already in the home currency → "₩1,234" / "$12.50" */
const homeFmt = (v) => {
  const d = CFG.homeDecimals || 0;
  return `${CFG.homeSymbol}${(Math.round(v * 10 ** d) / 10 ** d).toLocaleString('ko-KR', { minimumFractionDigits: d, maximumFractionDigits: d })}`;
};
const home = (n) => homeFmt(n * CFG.rate); // local amount → home
const sum = (xs) => xs.reduce((a, e) => a + e.amount, 0);
const WD = ['일', '월', '화', '수', '목', '금', '토'];
const DEFAULT_STAY = { 관광: 90, 식당: 60, 쇼핑: 90, 숙소: 30, 교통: 15 };
const KIND_ICON = { flightIn: '✈️', flightOut: '✈️', hotelIn: '🏨', hotelOut: '🏨', fixed: '📌', route: '📍' };
const MAX_AMOUNT_DIGITS = 8;
const MAX_STAY_MIN = 720;
const NO_PLACES_MSG = '아직 장소가 없습니다. 설정한 My Maps 지도에 가고 싶은 곳을 추가하면 여기에서 동선을 짤 수 있습니다 (반영까지 최대 10분).';
// Apps Script strips double-slash text inside template literals, so URL prefixes live in plain strings.
const GMAPS = 'https://www.google.com/maps';
const OSM_TILES = 'https://tile.openstreetmap.org/{z}/{x}/{y}.png';
const OSM_CREDIT = '© OpenStreetMap contributors';
const UPDATE_GUIDE_URL = 'https://github.com/chongjae/tour_manager#업데이트';
const REFRESH_MS = 3 * 60 * 1000;
const BOOT_CACHE = 'boot';
const MODE_ICON = { driving: '🚗', transit: '🚆', walking: '🚶' };
const MODE_NAME = { driving: '운전', transit: '대중교통', walking: '도보' };
const PLACES = [];
const byId = {};
const startsList = () => Object.values(byId).filter((p) => p.layer === '숙소' || p.layer === '교통');
const isAirport = (p) => p.primaryType === 'airport' || /공항|airport/i.test(p.category || '');
let AIRPORT = null; // set by setPlaces(); flights snap to it
let hasFitted = false; // fit the map once (a second, fresher load must not jump the view)
const isoOf = (d) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
function tripDates(start, days) {
  return Array.from({ length: days }, (_, i) => {
    const d = new Date(`${start}T00:00:00`);
    d.setDate(d.getDate() + i);
    return isoOf(d);
  });
}
const weekday = (iso) => new Date(`${iso}T00:00:00`).getDay();
const md = (iso) => `${+iso.slice(5, 7)}/${+iso.slice(8)}`;
const layerColor = (l) => getComputedStyle(document.documentElement).getPropertyValue(`--c-${l}`).trim();

const store = {
  get(k, d) { try { return JSON.parse(localStorage.getItem(`trip.${k}`)) ?? d; } catch { return d; } },
  set(k, v) { try { localStorage.setItem(`trip.${k}`, JSON.stringify(v)); } catch { /* private mode: in-memory only */ } },
};

// "Today" on the trip's clock (a companion at home in another zone still sees the trip's date).
const todayTrip = () => {
  const forced = !IS_GAS && new URLSearchParams(location.search).get('today'); // prototype only: ?today=2027-04-11 previews a phase
  if (/^\d{4}-\d{2}-\d{2}$/.test(forced || '')) return forced;
  try { return new Intl.DateTimeFormat('en-CA', { timeZone: CFG.timezone }).format(new Date()); } catch { return isoOf(new Date()); }
};
const PERSISTED = ['routes', 'starts', 'startFrom', 'expenses', 'eventPlace', 'calendarAdds', 'draft', 'outbox', 'visits'];
const EMPTY_DRAFT = { amount: '', category: '식비', item: '', payer: '', method: '', date: '', currency: 'local' };
const PERSISTED_DEFAULT = { expenses: IS_GAS ? [] : SEED_EXPENSES, outbox: [], draft: EMPTY_DRAFT, visits: [] };
let state = {
  tab: 'route', date: '', layer: '전체', error: '',
  ...Object.fromEntries(PERSISTED.map((k) => [k, store.get(k, k in PERSISTED_DEFAULT ? PERSISTED_DEFAULT[k] : {})])),
  saving: false, busy: '', editing: '', checks: null, bootFailed: false,
  isStale: false, // showing the cached copy from the last visit while the server loads → read-only
};
state = { ...state, draft: { ...EMPTY_DRAFT, ...state.draft } };
// Slots (candidate places per time block) live in state.routes, which the Apps Script server only accepts as single places:
// they are on in the prototype and wait for the Go server. ?demo=1 previews a sample day (not saved until you change something).
const SLOTS_ON = !IS_GAS;
if (SLOTS_ON && new URLSearchParams(location.search).has('demo')) state = { ...state, routes: SEED_ROUTES, visits: [] };
const isEditable = () => CAN_EDIT && !state.isStale;

function applyConfig(cfg) {
  CFG = cfg;
  DATES = tripDates(cfg.start, cfg.days);
  Schedule.configure(cfg);
  const pick = (list, cur) => (list.includes(cur) ? cur : list[0]);
  state = {
    ...state,
    date: DATES.includes(state.date) ? state.date : DATES[0],
    tab: state.tabChosen ? state.tab : Timeline.phaseOf(todayTrip(), cfg.start, cfg.end) === 'done' ? 'timeline' : state.tab, // finished trip opens on its story
    draft: { ...state.draft, payer: pick(cfg.payers, state.draft.payer), method: pick(cfg.methods, state.draft.method),
      date: state.draft.date && state.draft.date <= cfg.end ? state.draft.date : (DATES.includes(todayTrip()) ? todayTrip() : DATES[0]) },
  };
  setTiles(cfg.tileUrl);
  document.title = cfg.title;
  $('#tripTitle').innerHTML = `${esc(cfg.title)} <small>${md(cfg.start)} – ${md(cfg.end)} · ${cfg.days - 1}박 ${cfg.days}일${cfg.subtitle ? ` · ${esc(cfg.subtitle)}` : ''}</small>`;
}

function setState(patch) {
  state = { ...state, ...patch };
  PERSISTED.filter((k) => k in patch).forEach((k) => { store.set(k, state[k]); syncState(k, state[k]); });
  render();
}
const setRoute = (list) => setState({ routes: { ...state.routes, [state.date]: list } });
const omit = (obj, key) => Object.fromEntries(Object.entries(obj).filter(([k]) => k !== key));

/* ---------- calendar ↔ route ---------- */
// Prototype "calendar" = mock EVENTS + route events the user registered.
let EVENTS = [];
const NEAR_KM = 0.3;
// Calendar location (geocoded) → nearest saved place within 300 m (lodging first for stays), else an ad-hoc place.
function placeForCoords(e) {
  if (e.lat == null || e.lng == null) return null;
  const near = PLACES.map((p) => [p, Route.haversineKm(p, e)]).filter(([, d]) => d <= NEAR_KM).sort((a, b) => a[1] - b[1]).map(([p]) => p);
  const isStay = ['hotelIn', 'hotelOut'].includes(Schedule.kindOf(e));
  const hit = (isStay && near.find((p) => p.layer === '숙소')) || near[0];
  if (hit) return hit.id;
  const id = `loc:${e.lat.toFixed(5)},${e.lng.toFixed(5)}`;
  byId[id] = byId[id] || { id, name: (e.location || e.title).split(',')[0], layer: '숙소', category: '캘린더 위치', lat: e.lat, lng: e.lng, hours: null,
    url: `${GMAPS}/search/?api=1&query=${e.lat},${e.lng}` };
  return id;
}
// My Maps placemark (+ server-looked-up hours) → app place. Imported CSV columns arrive as ExtendedData.
// Places API primaryType → dashboard layer (used when the pin has no 분류 column / layer name).
function layerOfType(type) {
  const t = String(type || '');
  if (/restaurant|cafe|coffee|bakery|bar|food|meal|dessert|ramen|sushi|izakaya/.test(t)) return '식당';
  if (/lodging|hotel|resort|hostel|inn|guest_house/.test(t)) return '숙소';
  if (/airport|parking|car_rental|gas_station|bus_station|train_station|transit/.test(t)) return '교통';
  if (/store|shop|mall|market|supermarket/.test(t)) return '쇼핑';
  return null;
}

function placeFromMyMaps(m) {
  const d = m.data || {};
  const layer = [d['분류'], m.folder].find((l) => LAYERS.includes(l)) || layerOfType(m.primaryType) || '관광';
  return {
    id: d.place_id || m.placeId || `mm:${m.lat.toFixed(5)},${m.lng.toFixed(5)}`,
    name: m.name, layer, category: d['카테고리'] || 'My Maps',
    rating: d['평점'] ? Number(d['평점']) : null, reviews: null, price: d['가격대'] || '', memo: d['메모'] || '',
    lat: m.lat, lng: m.lng, primaryType: m.primaryType || '',
    hours: d['영업시간'] ? Hours.fromText(d['영업시간']) : Hours.fromPeriods(m.periods),
    url: String(d['지도링크'] || '').startsWith('https:') ? d['지도링크'] : `${GMAPS}/search/?api=1&query=${m.lat},${m.lng}`, // https only (no javascript:)
  };
}

// ponytail: swaps the shared PLACES/byId in place (they are module-level consts used everywhere).
function setPlaces(list) {
  PLACES.splice(0, PLACES.length, ...list);
  Object.keys(byId).filter((k) => !k.startsWith('loc:')).forEach((k) => { delete byId[k]; });
  PLACES.forEach((p) => { byId[p.id] = p; });
  AIRPORT = PLACES.find(isAirport) || null;
  if (PLACES.length && !hasFitted) map.fitBounds(PLACES.map((p) => [p.lat, p.lng]), { padding: [30, 30] });
  hasFitted = hasFitted || PLACES.length > 0;
}

function loadEvents(raw) {
  EVENTS = Schedule.expandEvents(raw).map((e) => ({ ...e, placeId: e.placeId || placeForCoords(e) }));
}
// Apps Script: registered routes are real calendar events (kind 'route'); prototype: kept in calendarAdds.
const allEvents = () => (IS_GAS ? EVENTS : [...EVENTS, ...Object.values(state.calendarAdds).flat()]);
const isFlight = (e) => ['flightIn', 'flightOut'].includes(Schedule.kindOf(e));
const placeOf = (e) => state.eventPlace[e.id] || e.placeId || (isFlight(e) && AIRPORT ? AIRPORT.id : null);

const isLocated = (p) => Boolean(p) && p.lat != null;
const visitsOn = (date) => state.visits.filter((v) => v.date === date);

/** Current clock time on the trip's timezone (HH:mm). */
function nowTrip() {
  try {
    const p = new Intl.DateTimeFormat('en-GB', { timeZone: CFG.timezone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date());
    return { time: `${p.find((x) => x.type === 'hour').value}:${p.find((x) => x.type === 'minute').value}` };
  } catch { const d = new Date(); return { time: `${pad(d.getHours())}:${pad(d.getMinutes())}` }; }
}

/** A slot stop → the place it resolved to (or a place-less block) + what the cards need to draw it. */
function resolvedStop(raw, leg, item, visits) {
  const norm = Slots.normalizeItem(item);
  const visitedId = ([...Slots.candidateResults(visits, norm.sid)].find(([k, r]) => k && r.result === 'visited') || [])[0] || null;
  const slot = { ...leg.slot, sid: norm.sid, label: raw.label, cands: norm.cands.filter((id) => byId[id]), pickId: raw.pickId, status: Slots.statusOf(item, visits), visitedId };
  if (leg.id == null) return { id: null, name: raw.label || '쉬는 시간', noPlace: true, stayMin: raw.stayMin, slot, item, status: slot.status };
  return { ...byId[leg.id], stayMin: raw.stayMin, slot, item, status: slot.status };
}

function planFor(date) {
  const events = allEvents(), wd = weekday(date);
  const auto = Schedule.dayStart(date, events, placeOf, AIRPORT && AIRPORT.id);
  const start = byId[state.startFrom[date]] || byId[auto.placeId] || AIRPORT || startsList()[0] || PLACES[0];
  const startMin = state.starts[date] ?? auto.min;
  if (!start) return { date, wd, auto, start: null, startMin, order: [], stops: [], isCustom: false, legs: [], endMin: startMin, travelTotal: 0 };
  const fixed = Schedule.fixedStops(date, events, placeOf, auto.eventId).filter((f) => byId[f.placeId]);
  const fixedBy = Object.fromEntries(fixed.map((f) => [f.ev, f]));
  const visits = visitsOn(date);
  const order = Schedule.mergeOrder((state.routes[date] || []).filter((s) => s.ev || Slots.isSlotItem(s) || byId[s.id]), fixed);
  const raw = order.map((s) => (s.ev ? { ...byId[fixedBy[s.ev].placeId], ...fixedBy[s.ev] } : Slots.isSlotItem(s) ? Slots.toStop(s, byId, visits) : { ...byId[s.id], stayMin: s.stayMin }));
  const isCustom = date in state.startFrom || date in state.starts;
  const planned = Route.planRoute({
    weekday: wd, startMin, start, stops: raw,
    travelFn: (a, b, t) => travelMin(a, b, date, t),
    candTravelFn: (a, b) => Route.mockTravelMin(a, b, CFG.travelMode), // candidates are compared with the cheap estimate
  });
  const stops = raw.map((s, i) => (s.cands ? resolvedStop(s, planned.legs[i], order[i], visits) : s.fixed ? s : { ...s, item: order[i], status: Slots.statusOf(order[i], visits) }));
  return { date, wd, auto, start, startMin, order, stops, isCustom, ...planned };
}

function routeEvents(plan) {
  return plan.legs.flatMap((l, i) => {
    const s = plan.stops[i];
    if (s.fixed) return [];
    const label = s.slot && s.slot.label ? `${s.slot.label} · ` : '';
    const title = s.noPlace ? `📍 ${s.name}` : `📍 ${label}${s.name}${s.slot && s.slot.tentative && s.slot.status !== 'visited' ? ' (미정)' : ''}`;
    return [{
      id: `r-${plan.date}-${i}`, kind: 'route', date: plan.date, placeId: s.id || undefined, location: s.noPlace ? '' : s.name,
      start: fmtClock(l.arrive + l.wait), end: fmtClock(l.depart), title,
      startMin: l.arrive + l.wait, endMin: l.depart,
    }];
  });
}

const registeredOn = (date) => allEvents().filter((e) => e.kind === 'route' && e.date === date);
const routeSignature = (list) => JSON.stringify(list.map((e) => [e.title, e.start, e.end]));

function regStatus(plan) {
  const saved = registeredOn(plan.date);
  if (!saved.length) return plan.stops.some((s) => !s.fixed) ? 'none' : 'empty';
  return routeSignature(saved) === routeSignature(routeEvents(plan)) ? 'synced' : 'changed';
}

function registerRoute(plan, items) {
  if (!IS_GAS) return setState({ calendarAdds: { ...state.calendarAdds, [plan.date]: items } });
  setState({ busy: items.length ? '캘린더에 등록 중…' : '등록 취소 중…' });
  const call = items.length ? callWrite('registerRoute', plan.date, items) : callWrite('unregisterRoute', plan.date);
  return call
    .then((events) => { loadEvents(events); setState({ busy: '' }); })
    .catch((e) => { setState({ busy: '' }); showError(`캘린더 등록 실패: ${e.message}`); });
}

function hoursText(p, wd) {
  if (!p.hours) return '영업시간 정보 없음';
  const w = p.hours[wd] || [];
  if (!w.length) return `${WD[wd]}요일 휴무`;
  return w.map(([o, c]) => (o === 0 && c === 1440 ? '24시간' : `${fmt(o)}–${fmtClock(c)}`)).join(', ');
}

function gmapsUrl(start, stops) {
  if (!stops.length) return GMAPS;
  const q = (p) => `${p.lat},${p.lng}`;
  const wp = stops.slice(0, -1).map(q).join('|');
  return `${GMAPS}/dir/?api=1&origin=${q(start)}&destination=${q(stops[stops.length - 1])}${wp ? `&waypoints=${encodeURIComponent(wp)}` : ''}&travelmode=${CFG.travelMode || 'driving'}`;
}

/* ---------- map ---------- */
if (typeof L === 'undefined') throw new Error('지도 라이브러리(Leaflet)를 불러오지 못했습니다');
const map = L.map('map', { zoomControl: false });
L.control.zoom({ position: 'bottomright' }).addTo(map);
let tiles = null;
// 설정 › 지도 타일 주소 (OSM policy: no hard-coded tile.openstreetmap.org for distributed apps). Empty → OSM.
function setTiles(url) {
  const src = url || OSM_TILES;
  if (tiles && tiles._url === src) return;
  if (tiles) tiles.remove();
  const host = url ? ` · ${esc(new URL(url.replace(/\{s\}/, 'a')).hostname)}` : '';
  tiles = L.tileLayer(src, { attribution: `${OSM_CREDIT}${host}`, maxZoom: 19 }).addTo(map);
}
map.setView([20, 0], 2);
const pins = L.layerGroup().addTo(map);
const pinIcon = (color, label) => L.divIcon({
  className: '', html: `<div class="pin" style="background:${color}"><span>${label}</span></div>`,
  iconSize: [26, 26], iconAnchor: [13, 26], popupAnchor: [0, -24],
});

function renderMap(plan) {
  pins.clearLayers();
  if (!plan.start) return;
  const label = {};
  let n = 0;
  plan.stops.forEach((s) => { if (s.id) label[s.id] = s.fixed ? KIND_ICON[s.kind] : String(++n); });
  const initial = (p) => p.layer.slice(0, 1); // shape-independent of colour: 식/관/쇼/숙/교
  label[plan.start.id] = '출';
  const userIds = new Set(plan.stops.filter((s) => !s.fixed).map((s) => s.id));
  Object.values(byId).forEach((p) => {
    const isOnRoute = p.id in label;
    const visible = state.layer === '전체' || state.layer === p.layer || isOnRoute;
    const action = !isEditable() ? '' : userIds.has(p.id) ? `<button data-del-id="${esc(p.id)}">동선에서 빼기</button>`
      : isOnRoute ? '' : `<button data-add="${esc(p.id)}">동선에 추가</button>`;
    L.marker([p.lat, p.lng], { icon: pinIcon(layerColor(p.layer), label[p.id] ?? initial(p)), opacity: visible ? 1 : 0.25, zIndexOffset: isOnRoute ? 1000 : 0 })
      .bindPopup(`<div class="pop"><b>${esc(p.name)}</b><p>${esc(p.category)}${p.rating ? ` · ★${p.rating} (${p.reviews ?? 0})` : ''}${p.price ? ` · ${esc(p.price)}` : ''}<br>${md(plan.date)}(${WD[plan.wd]}) ${esc(hoursText(p, plan.wd))}</p>${action}<a href="${esc(p.url)}" target="_blank" rel="noopener">Google 지도 ↗</a></div>`)
      .addTo(pins);
  });
  const pts = [plan.start, ...plan.stops].filter(isLocated).map((p) => [p.lat, p.lng]);
  if (pts.length > 1) L.polyline(pts, { color: '#1d2a2e', weight: 3, dashArray: '6 6', opacity: 0.8 }).addTo(pins);
}
// Leaflet swallows some clicks inside popups → bind directly when opened.
map.on('popupopen', (e) => {
  const b = e.popup.getElement().querySelector('button');
  if (b) b.addEventListener('click', () => { map.closePopup(); handleButton(b.dataset); });
});

/* ---------- route tab ---------- */
const moveCtrl = (i) => `<button data-up="${i}" aria-label="위로">↑</button><button data-down="${i}" aria-label="아래로">↓</button>`;

function stopCard(l, s, i, num, wd) {
  const badges = [
    l.warn === 'CLOSED' && '<span class="badge CLOSED">도착하면 영업 종료/휴무</span>',
    l.warn === 'SHORT' && `<span class="badge SHORT">희망 체류보다 ${dur(s.stayMin - l.availMin)} 부족</span>`,
    l.warn === 'NO_HOURS' && '<span class="badge NO_HOURS">영업시간 미상</span>',
    l.wait > 0 && `<span class="badge WAIT">개점까지 ${dur(l.wait)} 대기</span>`,
  ].filter(Boolean).join('');
  return `<li class="node"><span class="num">${num}</span><div class="card ${l.warn ? `warn-${l.warn}` : ''}">
    <div class="row"><span><b>${esc(s.name)}</b>${s.status === 'visited' ? ' <span class="badge OK">✅ 방문함</span>' : ''}</span><span class="ctrl">${moveCtrl(i)}<button data-del="${i}" aria-label="삭제">✕</button></span></div>
    <div class="times"><div><small>도착</small><b>${fmtClock(l.arrive)}</b></div>
      <div class="avail"><small>머물 수 있는 시간</small><b>${l.availMin == null ? '제한 없음' : dur(l.availMin)}</b></div>
      <div><small>출발</small><b>${fmtClock(l.depart)}</b></div></div>
    <div class="meta"><span>${s.hours ? '영업 ' : ''}${esc(hoursText(s, wd))}</span>
      <label>체류 <input type="number" min="0" max="${MAX_STAY_MIN}" step="10" data-stay="${i}" value="${s.stayMin}">분</label>${badges}${visitControls(s)}</div>
  </div></li>`;
}

/* ---------- slots: several candidate places for one time block ---------- */
const isLive = () => phaseNow() !== 'planning'; // visit / closed records only make sense once the trip has started
const SLOT_LABELS = ['점심', '저녁', '카페', '간식', '휴식'];
const STATUS_BADGE = {
  decided: '<span class="badge OK">확정</span>', open: '<span class="badge WAIT">미정</span>',
  visited: '<span class="badge OK">✅ 방문함</span>', skipped: '<span class="badge SHORT">⏭️ 건너뜀</span>',
};
const candNote = (c) => (c.warn === 'CLOSED' ? '도착하면 휴무/마감' : c.warn === 'SHORT' ? `머물 수 있는 시간 ${dur(c.availMin)}` : c.warn === 'NO_HOURS' ? '영업시간 미상' : c.wait > 0 ? `개점까지 ${dur(c.wait)} 대기` : '영업 중');

/** "방문함 / 닫혀 있음" for a plain (single-place) stop. */
function visitControls(s) {
  if (!SLOTS_ON || !isLive() || s.fixed || !s.id || s.status === 'visited') return '';
  const a = `data-place="${esc(s.id)}"`;
  return `<span class="edit-only vis"><button class="mini" data-visited="p:${esc(s.id)}" ${a}>✅ 방문함</button><button class="mini" data-closed="p:${esc(s.id)}" ${a}>🚫 닫혀 있음</button></span>`;
}

function candRow(s, id, l) {
  const sl = s.slot, p = byId[id], c = sl.candidates.find((x) => x.id === id), sid = esc(sl.sid), pid = esc(id);
  const out = sl.excluded.includes(id), chosen = l.id === id, picked = sl.pickId === id && !out, done = sl.status === 'visited';
  const tag = out ? '<span class="badge CLOSED">🚫 닫힘</span>'
    : chosen ? (done ? '<span class="badge OK">✅ 방문함</span>' : sl.tentative ? '<span class="badge WAIT">임시 선택</span>' : '<span class="badge OK">확정</span>') : '';
  const live = isLive();
  const acts = done ? '' : out ? `<button class="mini" data-reopen="${sid}" data-place="${pid}">다시 열기</button>` : [
    !picked && `<button class="mini" data-slot-confirm="${sid}" data-place="${pid}">확정</button>`,
    live && `<button class="mini" data-visited="${sid}" data-place="${pid}">✅ 방문함</button>`,
    live && `<button class="mini" data-closed="${sid}" data-place="${pid}">🚫 닫혀 있음</button>`,
    `<button class="mini" data-cand-del="${sid}" data-place="${pid}" aria-label="후보에서 빼기">✕</button>`,
  ].filter(Boolean).join('');
  return `<li class="cand ${out ? 'out' : ''} ${chosen ? 'chosen' : ''}"><div><b>${esc(p ? p.name : id)}</b> ${tag}${!out && c ? `<small>${candNote(c)}</small>` : ''}</div><span class="acts edit-only">${acts}</span></li>`;
}

function slotCard(l, s, i, num, wd) {
  const sl = s.slot, sid = esc(sl.sid);
  const badges = [
    l.warn === 'NO_CANDIDATE' && '<span class="badge CLOSED">후보가 모두 닫혔습니다 — 새 후보를 추가하세요</span>',
    l.warn === 'CLOSED' && '<span class="badge CLOSED">도착하면 영업 종료/휴무</span>',
    l.warn === 'SHORT' && `<span class="badge SHORT">희망 체류보다 ${dur(s.stayMin - l.availMin)} 부족</span>`,
    l.wait > 0 && `<span class="badge WAIT">개점까지 ${dur(l.wait)} 대기</span>`,
  ].filter(Boolean).join('');
  const times = s.noPlace
    ? `<div class="times"><div><small>시작</small><b>${fmtClock(l.arrive)}</b></div><div><small>끝</small><b>${fmtClock(l.depart)}</b></div></div>`
    : `<div class="times"><div><small>도착</small><b>${fmtClock(l.arrive)}</b></div><div class="avail"><small>머물 수 있는 시간</small><b>${l.availMin == null ? '제한 없음' : dur(l.availMin)}</b></div><div><small>출발</small><b>${fmtClock(l.depart)}</b></div></div>`;
  const options = PLACES.filter((p) => !sl.cands.includes(p.id)).map((p) => `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('');
  const skip = sl.status === 'skipped' ? `<button class="mini edit-only" data-reopen="${sid}" data-place="">다시 열기</button>`
    : isLive() && sl.status !== 'visited' ? `<button class="mini edit-only" data-skip="${sid}">⏭️ 건너뜀</button>` : '';
  const title = `${sl.label ? `<b>${esc(sl.label)}</b>` : ''}${s.noPlace ? '' : ` → <b>${esc(s.name)}</b>`}`;
  return `<li class="node"><span class="num ${s.noPlace ? 'fixed' : ''}">${s.noPlace ? '☕' : num}</span><div class="card slot ${l.warn ? `warn-${l.warn}` : ''}">
    <div class="row"><span>${title} ${STATUS_BADGE[sl.status]}</span><span class="ctrl">${moveCtrl(i)}<button data-del="${i}" aria-label="삭제">✕</button></span></div>
    ${times}
    ${sl.cands.length ? `<ul class="cands">${sl.cands.map((id) => candRow(s, id, l)).join('')}</ul>` : '<p class="regnote">후보 없이 쉬는 시간으로 잡혀 있습니다. 후보를 추가하면 갈 곳이 됩니다</p>'}
    <div class="meta"><select class="cand-add edit-only" data-cand-add="${sid}" aria-label="후보 추가"><option value="">＋ 후보 추가</option>${options}</select>
      <label>체류 <input type="number" min="0" max="${MAX_STAY_MIN}" step="10" data-stay="${i}" value="${s.stayMin}">분</label>${badges}${skip}</div></div></li>`;
}

function slotToolbar() {
  if (!SLOTS_ON) return '';
  return `<div class="slot-new"><select id="slotLabel" aria-label="후보 묶음 종류">${SLOT_LABELS.map((x) => `<option>${x}</option>`).join('')}</select>
    <button class="mini" data-slot-new="1">＋ 후보 묶음 추가</button><small>같은 시간대에 갈 만한 곳을 여러 곳 두고, 현지에서 한 곳을 고릅니다</small></div>`;
}
function fixedCard(l, s, i) {
  const isEnd = s.fixed.isEnd;
  const status = l.warn === 'LATE' ? `<span class="badge CLOSED">${dur(l.lateMin)} 늦음 — 앞 일정을 줄이세요</span>`
    : s.kind === 'hotelIn' ? (l.wait > 0 ? `<span class="badge WAIT">체크인까지 ${dur(l.wait)} 남음</span>` : '<span class="badge OK">도착 즉시 체크인 가능</span>')
      : `<span class="badge OK">여유 ${dur(l.slackMin)}</span>`;
  const label = s.kind === 'flightOut' ? `공항 도착 기한` : s.kind === 'hotelIn' ? '체크인 시작' : '예약 시각';
  return `<li class="node"><span class="num fixed">${KIND_ICON[s.kind]}</span><div class="card fixed ${l.warn ? 'warn-LATE' : ''}">
    <div class="row"><b>${esc(s.title)}</b><span class="cal">캘린더</span><span class="ctrl">${isEnd ? '' : moveCtrl(i)}</span></div>
    <div class="meta" style="margin-top:2px">📍 ${esc(s.name)}${s.kind === 'flightOut' ? ` · 출발 ${CFG.flightLeadMin}분 전 도착 기준` : ''}</div>
    <div class="times"><div><small>도착</small><b>${fmtClock(l.arrive)}</b></div><div><small>${label}</small><b>${fmtClock(s.fixed.start)}</b></div>
      <div><small>출발</small><b>${isEnd ? '—' : fmtClock(l.depart)}</b></div></div>
    <div class="meta">${status}</div></div></li>`;
}

function startCard(plan) {
  const note = plan.isCustom
    ? '직접 설정함 <button class="mini" data-reset-start="1">캘린더 기준으로 되돌리기</button>'
    : plan.auto.placeId ? `📅 ${esc(plan.auto.reason)}`
      : '<span style="color:var(--coral)">⚠ 숙소 일정에 장소가 연결되지 않았습니다 → 일정 탭에서 연결</span>';
  return `<li class="node"><span class="num start">출</span><div class="card"><div class="row">
    <select class="start-place" id="startPlace" aria-label="출발지">${[...new Set([plan.start, ...startsList()])].map((p) => `<option value="${esc(p.id)}" ${p === plan.start ? 'selected' : ''}>${esc(p.name)}</option>`).join('')}</select>
    <span class="ctrl"><input type="time" class="start-time" id="startTime" value="${fmt(plan.startMin)}" aria-label="출발 시각"></span></div>
    <div class="meta">${note}</div></div></li>`;
}

const REG_BUTTON = {
  none: '<button class="submit cal-btn" data-register="1">📅 이 동선을 캘린더에 등록</button>',
  changed: '<button class="submit cal-btn" data-register="1">📅 바뀐 동선으로 캘린더 다시 등록</button><p class="regnote">이전에 등록한 동선 일정은 교체됩니다</p>',
  synced: '<div class="synced">📅 캘린더에 등록됨 ✓ <button class="mini" data-unregister="1">등록 취소</button></div>',
  empty: '',
};

function renderRoute(plan) {
  if (!plan.start) {
    $('#tab-route').innerHTML = `<div class="empty">${NO_PLACES_MSG}</div>`;
    return;
  }
  const userIds = new Set(plan.stops.filter((s) => !s.fixed).map((s) => s.id));
  const dates = DATES.map((d) => {
    const p = planFor(d), n = p.stops.filter((s) => !s.fixed && !s.noPlace).length, st = regStatus(p);
    return `<button data-date="${d}" aria-pressed="${d === state.date}">${md(d)}<small>${WD[weekday(d)]} · ${n}곳${st === 'synced' ? ' ✓' : ''}</small></button>`;
  }).join('');
  const filters = ['전체', ...LAYERS].map((l) => `<button class="chip" data-layer="${l}" aria-pressed="${l === state.layer}">${l}</button>`).join('');
  const chips = PLACES.filter((p) => state.layer === '전체' || p.layer === state.layer)
    .map((p) => `<button class="chip ${userIds.has(p.id) ? 'in' : ''}" data-add="${esc(p.id)}" title="${esc(hoursText(p, plan.wd))}"><i class="dot" style="background:${layerColor(p.layer)}"></i>${esc(p.name)}</button>`).join('');
  let num = 0;
  const prevPlace = (i) => { for (let k = i - 1; k >= 0; k -= 1) if (isLocated(plan.stops[k])) return plan.stops[k]; return plan.start; };
  const legs = plan.legs.map((l, i) => {
    const s = plan.stops[i];
    if (s.noPlace) return slotCard(l, s, i, '', plan.wd); // no place, no travel
    const prev = prevPlace(i);
    const t0 = l.arrive - l.travelMin;
    const drive = isEstimated(prev, s, plan.date, t0) ? `~${dur(l.travelMin)} <small>(직선거리 추정${isTravelLoadingFor(prev, s, plan.date, t0) ? ' · 경로 조회 중' : ''})</small>` : dur(l.travelMin);
    const legUrl = `${GMAPS}/dir/?api=1&origin=${prev.lat},${prev.lng}&destination=${s.lat},${s.lng}&travelmode=${CFG.travelMode || 'driving'}`;
    const card = s.fixed ? fixedCard(l, s, i) : s.slot ? slotCard(l, s, i, ++num, plan.wd) : stopCard(l, s, i, ++num, plan.wd);
    return `<li class="drive"><a href="${legUrl}" target="_blank" rel="noopener" title="Google 지도에서 이 구간 실시간 소요시간 보기">${MODE_ICON[CFG.travelMode] || '🚗'} ${drive} <span class="ext">지도앱 ↗</span></a></li>${card}`;
  }).join('');
  const warnCount = plan.legs.filter((l) => ['CLOSED', 'SHORT', 'LATE'].includes(l.warn)).length;
  const footer = plan.legs.length
    ? `<div class="summary"><div><small>종료 예상</small><b>${fmtClock(plan.endMin)}</b></div><div><small>총 ${MODE_NAME[CFG.travelMode] || '운전'}</small><b>${dur(plan.travelTotal)}</b></div><div><small>경고</small><b>${warnCount}건</b></div></div>
       ${state.busy ? `<div class="synced">${esc(state.busy)}</div>` : `<div class="edit-only">${REG_BUTTON[regStatus(plan)]}</div>`}
       <p style="text-align:center"><a class="link" style="text-decoration:none;display:inline-block" target="_blank" rel="noopener" href="${gmapsUrl(plan.start, plan.stops.filter(isLocated))}">Google 지도 앱으로 길찾기 →</a></p>`
    : '';
  const empty = userIds.size ? '' : `<div class="empty">${isEditable() ? '아래 장소나 지도의 핀을 누르면 동선에 추가됩니다. 캘린더 일정(📌🏨✈️)은 자동으로 들어갑니다' : '아직 이 날 동선이 없습니다'}</div>`;
  const sortBtn = userIds.size >= 2 ? '<p class="edit-only" style="text-align:right;margin:0 0 8px"><button class="mini" data-sort="1">📏 짧은 동선으로 정렬</button></p>' : '';
  $('#tab-route').innerHTML = `
    <div class="dates">${dates}</div>
    <h2>${md(state.date)} (${WD[plan.wd]}) 동선</h2>
    ${sortBtn}<ol class="timeline">${startCard(plan)}${legs}</ol>${empty}${footer}
    <div class="edit-only"><h2>장소 추가 · 누르면 동선에 붙음 (숙소·공항 도착 전)</h2>
    ${slotToolbar()}
    <div class="chips" style="margin-bottom:8px">${filters}</div>
    ${PLACES.length ? `<div class="chips pick">${chips}</div>` : `<div class="empty">${NO_PLACES_MSG}</div>`}</div>`;
}

/* ---------- plan tab (calendar first) ---------- */
function placeSelect(e) {
  const cur = placeOf(e) || '';
  return `<select class="ev-place ${cur ? '' : 'unlinked'}" data-ev-place="${esc(e.id)}" aria-label="일정 장소">
    <option value="">📍 장소 연결 안 됨</option>${PLACES.map((p) => `<option value="${esc(p.id)}" ${p.id === cur ? 'selected' : ''}>📍 ${esc(p.name)}</option>`).join('')}</select>`;
}

const REG_TEXT = { none: '동선 미등록', changed: '동선 변경됨 · 재등록 필요', synced: '동선 캘린더 등록됨', empty: '동선 없음' };

function renderPlan() {
  $('#tab-plan').innerHTML = DATES.map((d, i) => {
    const plan = planFor(d), st = regStatus(plan);
    const evs = allEvents().filter((e) => e.date === d).sort((a, b) => a.start.localeCompare(b.start));
    const items = evs.map((e) => {
      const k = Schedule.kindOf(e);
      return `<li class="${k === 'route' ? 'mine' : ''}"><time>${e.start}</time><span>${KIND_ICON[k]}</span>
        <span>${esc(k === 'route' ? e.title.replace(/^📍\s*/, '') : e.title)}${e.end ? ` <small style="color:var(--muted)">~${e.end}</small>` : ''}${k === 'route' ? '' : placeSelect(e)}</span></li>`;
    }).join('') || '<li><time></time><span></span><span style="color:var(--muted)">캘린더 일정 없음</span></li>';
    const pending = st === 'none' || st === 'changed'
      ? `<li class="pending"><time></time><span>✎</span><span>${plan.stops.filter((s) => !s.fixed).map((s) => esc(s.name)).join(' → ')}</span></li>` : '';
    return `<article class="daycard"><header><b>Day ${i + 1}</b> · ${md(d)} (${WD[weekday(d)]})</header><ul>${items}${pending}</ul>
      <footer><span class="st-${st}">${REG_TEXT[st]}</span><button class="link" data-goto="${d}">동선 →</button></footer></article>`;
  }).join('') + `<p class="footnote">일정 = Google 캘린더${IS_GAS ? '' : ' (예시)'}. 여기서 연결한 장소는 대시보드에만 공유 저장되고 캘린더 일정은 바뀌지 않습니다.</p>
    ${IS_GAS ? `<p class="footnote"><button class="mini" data-diagnose="1">⚙ 설정 점검</button></p>${checksList(state.checks)}` : ''}`;
}

const CHECK_MARK = { true: '✅', false: '❌', null: '➖' };
function checksList(checks) {
  if (!checks) return '';
  return `<ul class="checks">${checks.map((c) => `<li><span>${CHECK_MARK[c.ok]}</span><b>${esc(c.label)}</b><small>${esc(c.detail || '')}</small></li>`).join('')}</ul>`;
}

/* ---------- timeline tab (places + payments + photos, by day) ---------- */
const phaseNow = () => Timeline.phaseOf(todayTrip(), CFG.start, CFG.end);
const allPhotos = () => (IS_GAS ? [] : SAMPLE_PHOTOS); // Apps Script mode has no photo store yet

function timelineItems() {
  const stops = DATES.flatMap((date) => {
    const plan = planFor(date);
    // a visited / skipped slot is told by its visit record instead of the plan
    return plan.stops.flatMap((s, i) => (s.fixed || !plan.legs[i] || ['visited', 'skipped'].includes(s.status) ? [] : [{
      date, min: plan.legs[i].arrive, kind: 'place', icon: s.noPlace ? '☕' : KIND_ICON.route,
      name: `${s.slot && s.slot.label ? `${s.slot.label} · ` : ''}${s.name}${s.slot && s.slot.tentative && !s.noPlace ? ' (미정)' : ''}`,
    }]));
  });
  const marks = { visited: ['✅', ''], closed: ['🚫', ' · 닫혀 있었음'], skipped: ['⏭️', ' · 건너뜀'] };
  const visits = state.visits.filter((v) => DATES.includes(v.date) && marks[v.result]).map((v) => ({
    date: v.date, min: Timeline.toMin(v.time), kind: 'place', icon: marks[v.result][0], muted: v.result !== 'visited',
    name: `${byId[v.placeId] ? byId[v.placeId].name : v.label || '일정'}${marks[v.result][1]}`,
  }));
  const events = allEvents().filter((e) => e.kind !== 'route' && DATES.includes(e.date))
    .map((e) => ({ date: e.date, min: Timeline.toMin(e.start), kind: 'place', icon: KIND_ICON[Schedule.kindOf(e)] || '📌', name: e.title }));
  const spent = allExpenses().filter((e) => DATES.includes(e.date)).map((e) => ({ ...e, min: Timeline.toMin(e.time), kind: 'expense' }));
  const photos = allPhotos().filter((p) => DATES.includes(p.date)).map((p) => ({ ...p, min: Timeline.toMin(p.time), kind: 'photo' }));
  return [...stops, ...visits, ...events, ...spent, ...photos];
}

const PHASE_BANNER = {
  planning: (d) => `<b>🗺️ 계획 중</b> 여행까지 D-${d} · 동선 탭에서 하루 일정을 짜 보세요`,
  live: () => '<b>🧳 진행 중</b> 오늘 쓴 돈과 다녀온 곳이 여기에 날짜별로 쌓입니다',
  done: () => '<b>📖 완료된 여행</b> 사진과 정산은 지금도 추가할 수 있어요',
};

function entryHtml(e) {
  const t = e.min == null ? '' : fmt(e.min % 1440);
  if (e.kind === 'place') return `<li class="${e.muted ? 'muted' : ''}"><time>${t}</time><span class="tl-ico">${e.icon}</span><span>${esc(e.name)}</span></li>`;
  if (e.kind === 'expense') return `<li><time>${t}</time><span class="tl-ico">💳</span><span><span class="amt">${local(e.amount)}</span>${esc(e.item)}<small>${esc(e.category)} · ${esc(e.payer)} · ${esc(e.method)}</small></span></li>`;
  return `<li><time>${t}</time><span class="tl-ico">📷</span><span><div class="tl-photos"><div class="tl-photo" style="--tone:${esc(e.tone || '#ccc')}"><span>📷</span>${esc(e.caption || '')}</div></div></span></li>`;
}

function renderTimeline() {
  const phase = phaseNow();
  const days = Math.round((Date.parse(`${CFG.start}T00:00:00Z`) - Date.parse(`${todayTrip()}T00:00:00Z`)) / 86400000);
  const banner = `<div class="phase ${phase}">${PHASE_BANNER[phase](days)}</div>`;
  const body = Timeline.buildTimeline(DATES, timelineItems()).map((day, i) => {
    const list = day.entries.map(entryHtml).join('') || '<li><span></span><span></span><span class="none">기록 없음</span></li>';
    const add = phase === 'planning' ? '' : '<footer><button type="button" disabled title="Google Photos 연동 후 사용할 수 있습니다">📷 사진 추가 (준비 중)</button></footer>';
    return `<article class="tl-day"><header><b>Day ${i + 1} · ${md(day.date)} (${WD[weekday(day.date)]})</b><small>${day.expenseTotal ? local(day.expenseTotal) : ''}</small></header><ol>${list}</ol>${add}</article>`;
  }).join('');
  $('#tab-timeline').innerHTML = banner + body;
}

/* ---------- shell ---------- */
function renderHeader() {
  const days = Math.round((Date.parse(`${CFG.start}T00:00:00Z`) - Date.parse(`${todayTrip()}T00:00:00Z`)) / 86400000);
  $('#dday').textContent = phaseNow() === 'done' ? '완료' : days > 0 ? `D-${days}` : days === 0 ? 'D-DAY' : `Day ${1 - days}`;
  const ex = allExpenses();
  const total = sum(ex);
  $('#spentJpy').textContent = local(total);
  $('#spentKrw').textContent = `≈ ${homeFmt(ex.reduce((a, e) => a + homeOf(e), 0))} · ${CFG.localSymbol}1 = ${CFG.homeSymbol}${CFG.rate}`;
  $('#roNote').hidden = isEditable();
  $('#roNote').textContent = state.bootFailed ? '서버에 연결하지 못해 지난번 데이터를 보여 줍니다 (읽기 전용) · 연결되면 새로고침하세요'
    : state.isStale ? '지난번 데이터 표시 중 · 새로 불러오는 중…' : '보기 전용 링크 · 편집하려면 편집 링크로 여세요';
}

function render() {
  const plan = planFor(state.date);
  document.querySelectorAll('.tabs button').forEach((b) => b.setAttribute('aria-selected', b.dataset.tab === state.tab));
  ['route', 'money', 'plan', 'timeline'].forEach((t) => { $(`#tab-${t}`).hidden = t !== state.tab; });
  document.body.classList.toggle('readonly', !isEditable());
  renderHeader(); renderRoute(plan); renderMoney(); renderPlan(); renderTimeline(); renderMap(plan);
  flushTravel();
}

function handleButton(ds) {
  if (ds.tab) return setState({ tab: ds.tab, tabChosen: true });
  if (ds.date) return setState({ date: ds.date });
  if (ds.goto) return setState({ tab: 'route', date: ds.goto });
  if (ds.layer) return setState({ layer: ds.layer });
  if (ds.toastClose) return closeToast(ds.toastClose);
  if (ds.diagnose) return runDiagnose();
  if (ds.currency) return setState({ draft: { ...state.draft, currency: ds.currency, amount: cleanAmount(state.draft.amount, ds.currency === 'home' ? CFG.homeDecimals || 0 : CFG.currencyDecimals || 0) } });
  for (const k of ['category', 'payer', 'method']) if (ds[k]) return setState({ draft: { ...state.draft, [k]: ds[k] } });
  if (!isEditable()) return; // everything below changes shared data
  if (ds.editCancel) return setState({ editing: '', draft: clearedDraft(), error: '' });
  if (ds.expEdit) return startEdit(ds.expEdit);
  if (ds.expDel) return deleteExpense(ds.expDel);
  const plan = planFor(state.date), order = plan.order;
  const isEnd = (i) => Boolean(plan.stops[i] && plan.stops[i].fixed && plan.stops[i].fixed.isEnd);
  const move = (i, j) => {
    if (j < 0 || j >= order.length || isEnd(i) || isEnd(j)) return;
    const r = [...order];
    [r[i], r[j]] = [r[j], r[i]];
    setRoute(r);
  };
  if (SLOTS_ON) { // candidate slots + visit records
    const sidOf = (s) => (Slots.normalizeItem(s) || {}).sid;
    const at = (sid) => order.findIndex((s) => sidOf(s) === sid);
    const patch = (sid, fn, extra = {}) => {
      const k = at(sid);
      return setState({ ...(k < 0 ? {} : { routes: { ...state.routes, [state.date]: order.map((s, j) => (j === k ? fn(s) : s)) } }), ...extra });
    };
    const rec = (sid, place, result, label) => ({ id: newId(), sid, date: state.date, placeId: place || null, result, label: label || '', time: nowTrip().time, at: new Date().toISOString() });
    const nameOf = (place) => (byId[place] ? byId[place].name : '');
    if (ds.slotNew) {
      const label = ($('#slotLabel') || {}).value || '점심';
      const n = order.reduce((last, s, i) => (s.ev ? last : i + 1), 0);
      return setRoute([...order.slice(0, n), Slots.newSlot({ sid: newId(), label, stayMin: label === '휴식' ? 40 : 60 }), ...order.slice(n)]);
    }
    if (ds.slotConfirm) return patch(ds.slotConfirm, (s) => Slots.confirm(s, ds.place));
    if (ds.candDel) return patch(ds.candDel, (s) => Slots.removeCandidate(s, ds.place));
    if (ds.visited) {
      const k = at(ds.visited);
      const extra = { visits: [...state.visits, rec(ds.visited, ds.place, 'visited', nameOf(ds.place))] };
      return k >= 0 && Slots.isSlotItem(order[k]) ? patch(ds.visited, (s) => Slots.confirm(s, ds.place), extra) : setState(extra);
    }
    if (ds.closed) {
      return patch(ds.closed, (s) => { const n = Slots.normalizeItem(s); return n.pick === ds.place ? Slots.clearPick(n) : n; },
        { visits: [...state.visits, rec(ds.closed, ds.place, 'closed', nameOf(ds.place))] });
    }
    if (ds.skip) {
      const it = order[at(ds.skip)];
      return setState({ visits: [...state.visits, rec(ds.skip, null, 'skipped', (it && Slots.normalizeItem(it).label) || '')] });
    }
    if (ds.reopen) { // undo a closed / skipped record
      return setState({ visits: state.visits.filter((v) => !(v.sid === ds.reopen && v.date === state.date && (v.placeId || '') === (ds.place || '') && v.result !== 'visited')) });
    }
  }
  if (ds.add) {
    if (order.some((s) => s.id === ds.add)) return;
    // after the last user stop (first stop → before calendar items); user can move it with ↑↓
    const n = order.reduce((last, s, i) => (s.ev ? last : i + 1), 0);
    return setRoute([...order.slice(0, n), { id: ds.add, stayMin: DEFAULT_STAY[byId[ds.add].layer] }, ...order.slice(n)]);
  }
  if (ds.delId) return setRoute(order.filter((s) => s.id !== ds.delId));
  if (ds.del) return setRoute(order.filter((_, i) => i !== +ds.del));
  if (ds.up) return move(+ds.up, +ds.up - 1);
  if (ds.down) return move(+ds.down, +ds.down + 1);
  if (ds.sort) return setRoute(sortNearest(plan));
  if (ds.register) return registerRoute(plan, routeEvents(plan));
  if (ds.unregister) return IS_GAS ? registerRoute(plan, []) : setState({ calendarAdds: omit(state.calendarAdds, state.date) });
  if (ds.resetStart) return setState({ starts: omit(state.starts, state.date), startFrom: omit(state.startFrom, state.date) });
}

/** User stops re-ordered to a shorter path; calendar items keep their slots. */
function sortNearest(plan) {
  // place-less blocks (rest) keep their position; everything with a location is re-ordered around them
  const at = plan.stops.map((s, i) => i).filter((i) => isLocated(plan.stops[i]));
  const perm = Route.sortStops(plan.start, at.map((i) => plan.stops[i]), at.map((i) => Boolean(plan.order[i].ev)));
  const out = [...plan.order];
  at.forEach((i, k) => { out[i] = plan.order[at[perm[k]]]; });
  return out;
}

document.addEventListener('click', (e) => {
  const b = e.target.closest('button');
  if (b && !b.closest('.leaflet-popup')) handleButton(b.dataset);
});
document.addEventListener('input', (e) => {
  const t = e.target;
  if (t.id === 'amt') {
    t.value = cleanAmount(t.value, draftDecimals(state.draft));
    state = { ...state, draft: { ...state.draft, amount: t.value } };
    const v = Number(t.value);
    $('#amtKrw').textContent = !v ? '' : state.draft.currency === 'home' ? `≈ ${local(v / CFG.rate)}` : `≈ ${home(v)}`;
    store.set('draft', state.draft);
  }
  if (t.id === 'item') { state = { ...state, draft: { ...state.draft, item: t.value } }; store.set('draft', state.draft); }
});
document.addEventListener('change', (e) => {
  const t = e.target;
  if (t.id === 'expDate') return setState({ draft: { ...state.draft, date: t.value === PRE_TRIP ? preTripDate() : t.value } });
  if (!isEditable()) return render(); // read-only: snap inputs back
  if (t.dataset.stay !== undefined) {
    const v = Math.max(0, Math.min(MAX_STAY_MIN, parseInt(t.value, 10) || 0));
    return setRoute(planFor(state.date).order.map((s, k) => (k === +t.dataset.stay ? { ...s, stayMin: v } : s)));
  }
  if (t.dataset.evPlace !== undefined) return setState({ eventPlace: { ...state.eventPlace, [t.dataset.evPlace]: t.value } });
  if (SLOTS_ON && t.dataset.candAdd !== undefined) {
    if (!t.value) return;
    const order = planFor(state.date).order;
    const k = order.findIndex((s) => (Slots.normalizeItem(s) || {}).sid === t.dataset.candAdd);
    if (k < 0) return;
    try { return setState({ routes: { ...state.routes, [state.date]: order.map((s, j) => (j === k ? Slots.addCandidate(s, t.value) : s)) } }); } catch (e) { showError(e.message); return render(); }
  }
  if (t.id === 'startTime' && t.value) return setState({ starts: { ...state.starts, [state.date]: Schedule.toMin(t.value) } });
  if (t.id === 'startPlace') return setState({ startFrom: { ...state.startFrom, [state.date]: t.value } });
});
document.addEventListener('submit', addExpense);

function showBanner(html) { $('.mock').innerHTML = html; }

/* errors stay until closed (the status banner is overwritten by the next status) */
let toastSeq = 0;
function showError(text) {
  const id = String(++toastSeq);
  const box = $('#toast');
  if ([...box.children].some((el) => el.dataset.text === text)) return; // same error again → keep one
  const el = document.createElement('div');
  el.className = 'toast-item';
  el.dataset.id = id;
  el.dataset.text = text;
  el.setAttribute('role', 'alert');
  el.innerHTML = `<span>⚠ ${esc(text)}</span><button data-toast-close="${id}" aria-label="닫기">✕</button>`;
  box.appendChild(el);
}
function closeToast(id) {
  const el = document.querySelector(`.toast-item[data-id="${id}"]`);
  if (el) el.remove();
}

function runDiagnose() {
  setState({ checks: [{ label: '점검 중…', ok: null, detail: '' }] });
  callServer('diagnose').then((checks) => setState({ checks })).catch((e) => showError(`점검 실패: ${e.message}`));
}

function showSetup(cfg, checks) {
  const items = [...cfg.missing.map((m) => `<li><b>${esc(m)}</b> 값이 비어 있습니다</li>`), ...cfg.invalid.map((m) => `<li>${esc(m)}</li>`)].join('');
  showBanner('설정이 필요합니다');
  $('.layout').innerHTML = `<section class="setup"><h2>처음 설정</h2>
    <p>스프레드시트의 <b>설정</b> 탭을 채운 뒤 이 페이지를 새로고침하세요. 설정 탭이 없으면 스프레드시트 메뉴 <b>여행 대시보드 → 설정·안내 탭 만들기</b>를 누르세요.</p>
    <ul>${items}</ul>
    ${checks ? `<h2>자가 점검</h2>${checksList(checks)}` : ''}
    <p>자세한 안내: 스프레드시트의 <b>안내</b> 탭</p></section>`;
}

// placeList: app-shaped places (prototype passes samples; Apps Script converts My Maps placemarks).
function startApp(res, placeList, { isStale = false } = {}) {
  applyConfig(res.config);
  const mm = res.places || { places: [] };
  setPlaces(placeList || mm.places.map(placeFromMyMaps));
  loadEvents(res.events);
  const remote = res.state || {};
  const shared = Object.fromEntries(SYNC_KEYS.filter((k) => k in remote).map((k) => [k, remote[k]]));
  // Keys with unsent local edits (failed save, tab closed within the debounce): the edit is rebased onto the fresh
  // server value and re-sent — only the user's own change, never a diff that would delete companions' days.
  const dirty = isStale ? [] : dirtyKeys();
  const oldSynced = store.get(SYNCED, {});
  const localDirty = Object.fromEntries(dirty.map((k) => [k, rebase(oldSynced[k], store.get(k, {}), remote[k])]));
  if (!isStale) setSynced(remote);
  state = { ...state, ...shared, ...localDirty, expenses: res.expenses, isStale, bootFailed: false };
  if (!isStale) SYNC_KEYS.filter((k) => !dirty.includes(k)).forEach((k) => store.set(k, state[k])); // the stale copy never overwrites local data
  const placeNote = mm.error ? `⚠ ${mm.error}` : `My Maps 장소 ${mm.places.length}곳`;
  const airportNote = AIRPORT ? [] : ['⚠ My Maps에 공항이 없어 항공편 일정을 출발지로 쓰지 못합니다'];
  const v = res.version;
  const update = v && v.latest && v.latest !== v.current
    ? ` · <a href="${UPDATE_GUIDE_URL}" target="_blank" rel="noopener">새 버전 ${esc(v.latest)} 있음 (지금 ${esc(v.current)}) → 업데이트 방법</a>` : '';
  const parts = [isStale ? '지난번 데이터' : `캘린더 일정 ${res.events.length}개`, placeNote, ...airportNote, ...(mm.notes || []), CAN_EDIT ? '동선은 모두에게 공유 저장' : '보기 전용'];
  showBanner(parts.map(esc).join(' · ') + update);
  render();
  dirty.forEach((k) => syncState(k, state[k])); // only the difference to the server is sent
}

/** Server value for a shared key, with no local edit pending → show it (no re-sync: it is already the server's). */
function adoptShared(key, value) {
  state = { ...state, [key]: value };
  store.set(key, value);
  render();
}

/** Companions' changes: re-read on return to the tab and every few minutes. Keys with a pending save stay local. */
function refreshShared() {
  if (!IS_GAS || state.isStale || document.hidden) return;
  callServer('refresh').then((res) => {
    const remote = res.state || {};
    // in-flight keys wait for their own response; every other key = server value + this page's unsent edits (rebase)
    const keys = SYNC_KEYS.filter((k) => !syncInFlight.has(k));
    const fresh = Object.fromEntries(keys.map((k) => [k, rebase(synced[k], state[k], remote[k])]));
    keys.forEach((k) => setSyncedKey(k, remote[k] || {}));
    loadEvents(res.events);
    state = { ...state, ...fresh, expenses: state.saving ? state.expenses : res.expenses };
    keys.filter((k) => dirtyKeys().includes(k)).forEach((k) => syncState(k, state[k])); // retry a previously failed save
    render();
  }).catch((e) => console.warn(`refresh failed: ${e.message}`));
}

const BOOT_RETRIES = 2;
const BOOT_RETRY_MS = 3000;

function boot(retriesLeft) {
  callServer('bootstrap')
    .then((res) => {
      const cfg = res.config;
      if (cfg.missing.length || cfg.invalid.length) {
        state = { ...state, isStale: false };
        $('#roNote').hidden = true;
        return showSetup(cfg, res.checks);
      }
      store.set(BOOT_CACHE, res);
      startApp(res);
      flushOutbox();
      setInterval(refreshShared, REFRESH_MS);
      document.addEventListener('visibilitychange', refreshShared);
      return null;
    })
    .catch((e) => {
      if (retriesLeft > 0) return setTimeout(() => boot(retriesLeft - 1), BOOT_RETRY_MS);
      showError(`불러오기 실패: ${e.message}`);
      if (!state.isStale) return showBanner('불러오기 실패');
      return setState({ bootFailed: true }); // keep the cached copy, read-only, with an honest notice
    });
}

function bootCache() {
  const c = store.get(BOOT_CACHE, null);
  return c && c.config && Array.isArray(c.events) && Array.isArray(c.expenses) ? c : null;
}

travelCache = store.get('travel', {});
if (IS_GAS) {
  const cached = bootCache();
  if (cached) {
    try { startApp(cached, null, { isStale: true }); } catch (e) { console.warn(`cached view skipped: ${e.message}`); }
  } else {
    showBanner('불러오는 중…');
  }
  boot(BOOT_RETRIES);
} else {
  startApp({ config: SAMPLE_CONFIG, places: null, events: MOCK_RAW_EVENTS, expenses: state.expenses, state: {} }, SAMPLE_PLACES);
  showBanner('로컬 프로토타입 · 샘플 데이터 · 이동시간은 직선거리 추정');
}

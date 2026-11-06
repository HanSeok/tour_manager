// Built-in Maps service (no API key): geocoding + traffic-aware driving times, cached in a hidden tab.

function geocode_(address) {
  const cache = CacheService.getScriptCache();
  const key = `geo:${Utilities.base64EncodeWebSafe(Utilities.computeDigest(Utilities.DigestAlgorithm.MD5, address))}`;
  const hit = cache.get(key);
  if (hit) return JSON.parse(hit);
  try {
    const res = Maps.newGeocoder().setLanguage('ko').geocode(address);
    const loc = res.status === 'OK' && res.results.length ? res.results[0].geometry.location : null;
    const out = loc ? { lat: loc.lat, lng: loc.lng } : null;
    cache.put(key, JSON.stringify(out), GEOCODE_CACHE_SEC);
    return out;
  } catch (err) {
    console.error(`geocode failed for "${address}": ${err}`);
    return null;
  }
}

function travelSheet_() {
  const ss = SpreadsheetApp.getActive();
  let sheet = ss.getSheetByName(TRAVEL_SHEET);
  if (!sheet) {
    sheet = ss.insertSheet(TRAVEL_SHEET, ss.getSheets().length);
    sheet.getRange('A:A').setNumberFormat('@');
    sheet.getRange(1, 1, 1, 3).setValues([['key', 'minutes', 'updated']]);
    sheet.hideSheet();
  }
  return sheet;
}


/** Driving minutes for [{key, from:{lat,lng}, to:{lat,lng}}] → {key: minutes|null}. Cached in a hidden sheet. */
const TRAVEL_KEY_RE = /^(-?\d{1,3}\.\d{5}),(-?\d{1,3}\.\d{5})>(-?\d{1,3}\.\d{5}),(-?\d{1,3}\.\d{5})(?:@(\d{4}-\d{2}-\d{2})\+(\d{1,4}))?$/;
const TRAFFIC_BUCKET_MIN = 60; // must match prototype/remote.js
const MAX_DIRECTIONS_PER_DAY = 500; // stays under the Maps service daily quota; protects against URL-holder abuse

/** The key is the only input trusted: coordinates and departure time are re-derived from it (no cache poisoning). */
function parseTravelKey_(key, cfg) {
  const m = typeof key === 'string' && key.match(TRAVEL_KEY_RE);
  if (!m) return null;
  const [, aLat, aLng, bLat, bLng, date, min] = m;
  const pair = { key, from: { lat: Number(aLat), lng: Number(aLng) }, to: { lat: Number(bLat), lng: Number(bLng) }, depart: null };
  if (date) {
    if (date < cfg.start || date > cfg.end || Number(min) % TRAFFIC_BUCKET_MIN) return null;
    pair.depart = new Date(new Date(`${date}T00:00:00${cfg.offset}`).getTime() + (Number(min) + TRAFFIC_BUCKET_MIN / 2) * 60000).toISOString();
  }
  return pair;
}

/** Counts Directions calls per day in Script Properties; false once the daily cap is reached. */
function takeDirectionsBudget_(n) {
  const props = PropertiesService.getScriptProperties();
  const key = `quota.directions.${Utilities.formatDate(new Date(), 'UTC', 'yyyy-MM-dd')}`;
  const used = Number(props.getProperty(key) || 0);
  if (used + n > MAX_DIRECTIONS_PER_DAY) return false;
  props.setProperty(key, String(used + n));
  return true;
}

function getTravelTimes(pairs) {
  const cfg = requireConfig_();
  if (!Array.isArray(pairs) || pairs.length > MAX_TRAVEL_BATCH) fail_('이동시간 요청 형식이 올바르지 않습니다');
  const valid = pairs.map((p) => parseTravelKey_(p && p.key, cfg)).filter(Boolean);
  return withLock_(() => {
    const sheet = travelSheet_();
    const n = sheet.getLastRow() - 1;
    const known = n > 0 ? Object.fromEntries(sheet.getRange(2, 1, n, 2).getValues().map((r) => [String(r[0]), r[1] === '' ? null : Number(r[1])])) : {};
    const now = Utilities.formatDate(new Date(), config_().timezone, 'yyyy-MM-dd HH:mm');
    // undefined = transient failure (not cached, retried later); null = no drivable route (cached)
    const missing = valid.filter((p) => !(p.key in known));
    if (missing.length && !takeDirectionsBudget_(missing.length)) fail_('오늘 이동시간 조회 한도를 넘었습니다. 내일 다시 시도하세요');
    const fresh = missing.map((p) => [p.key, drivingMinutes_(p.from, p.to, p.depart), now])
      .filter((r) => r[1] !== undefined);
    if (fresh.length) sheet.getRange(sheet.getLastRow() + 1, 1, fresh.length, 3).setValues(fresh.map((r) => [r[0], r[1] == null ? '' : r[1], r[2]]));
    const all = { ...known, ...Object.fromEntries(fresh.map((r) => [r[0], r[1]])) };
    return Object.fromEntries(valid.map((p) => [p.key, all[p.key] == null ? null : all[p.key]]));
  });
}

/** Driving minutes; with a future `depart` (ISO) the traffic-aware duration_in_traffic is used (built-in, free). */
function drivingMinutes_(from, to, depart) {
  try {
    const finder = Maps.newDirectionFinder()
      .setOrigin(Number(from.lat), Number(from.lng))
      .setDestination(Number(to.lat), Number(to.lng))
      .setMode(Maps.DirectionFinder.Mode.DRIVING);
    const when = depart ? new Date(depart) : null;
    if (when && !Number.isNaN(when.getTime()) && when.getTime() > Date.now()) finder.setDepart(when);
    const res = finder.getDirections();
    const leg = res.routes && res.routes[0] && res.routes[0].legs[0];
    if (!leg) return null;
    return Math.ceil((leg.duration_in_traffic || leg.duration).value / 60);
  } catch (err) {
    console.error(`directions failed: ${err}`);
    return undefined;
  }
}

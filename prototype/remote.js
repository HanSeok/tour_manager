// Bridge to the Apps Script server. In the local prototype (no google.script.run) everything stays local.
const IS_GAS = typeof google !== 'undefined' && Boolean(google.script && google.script.run);
const SYNC_KEYS = ['routes', 'starts', 'startFrom', 'eventPlace'];
const SYNC_DELAY_MS = 400;
const TRAVEL_BATCH = 25;
const TRAFFIC_BUCKET_MIN = 60; // traffic-aware times are looked up per departure hour

function callServer(fn, ...args) {
  return new Promise((resolve, reject) => {
    google.script.run
      .withSuccessHandler(resolve)
      .withFailureHandler((e) => reject(new Error(e && e.message ? e.message : String(e))))[fn](...args);
  });
}

/* shared UI state → Script Properties (debounced per key) */
const syncTimers = {};
function syncState(key, value) {
  if (!IS_GAS || !SYNC_KEYS.includes(key)) return;
  clearTimeout(syncTimers[key]);
  syncTimers[key] = setTimeout(() => {
    callServer('saveState', key, value).catch((e) => showBanner(`동선 저장 실패: ${esc(e.message)}`));
  }, SYNC_DELAY_MS);
}

/* driving times: cache-first, misses fetched in batches, then re-render */
let travelCache = {}; // key → minutes | null (null = no route / failed → keep estimate)
const travelPending = new Map();
let isTravelLoading = false;
// Departure bucket: date + hour of the trip day (minutes may exceed 1440 → next day). Sent as the bucket's mid-point.
function departBucket(date, t) {
  if (!date || t == null) return null;
  const m = Math.floor(t / TRAFFIC_BUCKET_MIN) * TRAFFIC_BUCKET_MIN;
  const iso = new Date(new Date(`${date}T00:00:00${CFG.offset}`).getTime() + (m + TRAFFIC_BUCKET_MIN / 2) * 60000).toISOString();
  return { tag: `${date}+${m}`, iso };
}
const travelKey = (a, b, dep) => `${a.lat.toFixed(5)},${a.lng.toFixed(5)}>${b.lat.toFixed(5)},${b.lng.toFixed(5)}${dep ? `@${dep.tag}` : ''}`;
const isEstimated = (a, b, date, t) => travelCache[travelKey(a, b, departBucket(date, t))] == null;

function travelMin(a, b, date, t) {
  const dep = departBucket(date, t);
  const k = travelKey(a, b, dep);
  if (travelCache[k] != null) return travelCache[k];
  if (IS_GAS && !(k in travelCache)) {
    travelPending.set(k, { key: k, from: { lat: a.lat, lng: a.lng }, to: { lat: b.lat, lng: b.lng }, depart: dep ? dep.iso : null });
  }
  return Route.mockTravelMin(a, b);
}

function flushTravel() {
  if (!IS_GAS || isTravelLoading || !travelPending.size) return;
  const batch = [...travelPending.values()].slice(0, TRAVEL_BATCH);
  batch.forEach((p) => travelPending.delete(p.key));
  isTravelLoading = true;
  callServer('getTravelTimes', batch)
    .then((res) => { travelCache = { ...travelCache, ...res }; })
    .catch((e) => {
      travelCache = { ...travelCache, ...Object.fromEntries(batch.map((p) => [p.key, null])) };
      showBanner(`이동시간 조회 실패 (직선거리 추정으로 표시): ${esc(e.message)}`);
    })
    .finally(() => {
      store.set('travel', Object.fromEntries(Object.entries(travelCache).filter(([, v]) => v != null)));
      isTravelLoading = false;
      render();
    });
}

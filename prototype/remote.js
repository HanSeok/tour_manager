// Bridge to the server. Three modes: Apps Script (google.script.run), the Go server (fetch /api, `TM_API`), or the
// local prototype (no server: everything stays in the browser).
const IS_GAS = typeof google !== 'undefined' && Boolean(google.script && google.script.run);
const IS_API = !IS_GAS && typeof window !== 'undefined' && window.TM_API === true;
const IS_SERVER = IS_GAS || IS_API; // expenses live on a server
// Injected by doGet (Apps Script only): {required, key}. No key required → everyone with the link edits (v1 behaviour).
const ACCESS = (typeof window !== 'undefined' && window.EDIT_ACCESS) || { required: false, key: '' };
// Go server: set from `me.role` after bootstrap (read-only until we know who is signed in).
let CAN_EDIT = IS_API ? false : (!ACCESS.required || Boolean(ACCESS.key));
let ME = null; // Go server: {email, name, role}
let MEMBERS = {}; // Go server: lower-case e-mail → {name, role}
const ROLE_LABEL = { owner: '소유자', editor: '편집자', viewer: '보기' };
const whoName = (email) => {
  const m = MEMBERS[String(email || '').toLowerCase()];
  return m ? m.name : String(email || '').split('@')[0];
};
const SYNC_KEYS = ['routes', 'starts', 'startFrom', 'eventPlace'];
const SYNC_DELAY_MS = 400;
const TRAVEL_BATCH = 25;
const TRAFFIC_BUCKET_MIN = 60; // traffic-aware times are looked up per departure hour
const MODE_SUFFIX = { driving: '', transit: '!t', walking: '!w' }; // must match apps-script/Maps.js

function callServer(fn, ...args) {
  if (IS_API) return callApi(fn, args);
  return new Promise((resolve, reject) => {
    google.script.run
      .withSuccessHandler(resolve)
      .withFailureHandler((e) => reject(new Error(e && e.message ? e.message : String(e))))[fn](...args);
  });
}

/** Go server: POST /api/<fn> {args} → {ok, data} | {ok:false, error}. The error carries the HTTP status (409 = conflict). */
async function callApi(fn, args) {
  let res;
  try {
    res = await fetch(`/api/${fn}`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin', body: JSON.stringify({ args }) });
  } catch (e) { throw new Error('Failed to fetch'); } // offline → isNetworkError() queues the write
  let body = null;
  try { body = await res.json(); } catch (e) { /* not JSON */ }
  if (!res.ok || !body || !body.ok) {
    const err = new Error((body && body.error) || `HTTP ${res.status}`);
    err.status = res.status;
    throw err;
  }
  return body.data;
}
/** Every server write goes through here. Apps Script: the edit key is always the last argument. Go server: the session decides. */
const callWrite = (fn, ...args) => (IS_API ? callServer(fn, ...args) : callServer(fn, ...args, ACCESS.key));
const isNetworkError = (err) => (typeof navigator !== 'undefined' && navigator.onLine === false)
  || /network|HTTP 0|connection|timed? ?out|Failed to fetch/i.test(String(err && err.message));

/* shared UI state → Script Properties. Only changed sub-keys (days / event ids) are sent; the server merges them,
   so companions editing different days never overwrite each other. */
// key → what THIS page last agreed with the server on. Never the server's merged value while a newer local edit
// is pending: a companion's day in `synced` but not in `state` would be diffed as "deleted" (null) and erased.
let synced = {};
// Keys edited locally but not yet confirmed by the server (persisted: survives a reload / closed tab).
const DIRTY = 'dirty';
// `synced` is persisted too: after a reload the user's unsent change = diff(old synced, local copy), re-applied on top
// of the FRESH server value (rebase). Diffing the fresh server value against the old copy would erase companions' days.
const SYNCED = 'synced';
const setSyncedKey = (key, value) => { synced = { ...synced, [key]: value }; store.set(SYNCED, synced); };
/** Local edits of `key` (vs. what we last agreed with the server) re-applied on top of `remote`. */
const rebase = (base, local, remote) => applyPatch(remote || {}, diffPatch(base || {}, local || {}));
const syncTimers = {};
const syncInFlight = new Set();
const isSyncPending = (key) => Boolean(syncTimers[key]) || syncInFlight.has(key);

/** {a:1,b:2} + {b:null,c:3} → {a:1,c:3} (same rule as the server's applyPatch_) */
const applyPatch = (base, patch) => Object.entries(patch).reduce(
  (acc, [k, v]) => (v === null ? Object.fromEntries(Object.entries(acc).filter(([x]) => x !== k)) : { ...acc, [k]: v }), base || {});

/** {a:1,b:2} vs {a:1,b:3,c:4} → {b:3,c:4}; removed sub-keys → null */
function diffPatch(before, after) {
  const b = before || {}, a = after || {};
  const changed = Object.keys(a).filter((k) => JSON.stringify(a[k]) !== JSON.stringify(b[k])).map((k) => [k, a[k]]);
  const removed = Object.keys(b).filter((k) => !(k in a)).map((k) => [k, null]);
  return Object.fromEntries([...changed, ...removed]);
}

function setSynced(remote) {
  synced = Object.fromEntries(SYNC_KEYS.map((k) => [k, (remote && remote[k]) || {}]));
  store.set(SYNCED, synced);
}

const dirtyKeys = () => store.get(DIRTY, []).filter((k) => SYNC_KEYS.includes(k));
const setDirty = (key, isDirty) => store.set(DIRTY, [...new Set([...dirtyKeys().filter((k) => k !== key), ...(isDirty ? [key] : [])])]);

function syncState(key, value) {
  if (!IS_GAS || !SYNC_KEYS.includes(key)) return;
  setDirty(key, true);
  clearTimeout(syncTimers[key]);
  syncTimers[key] = setTimeout(() => {
    delete syncTimers[key];
    const patch = diffPatch(synced[key], value);
    if (!Object.keys(patch).length) { setDirty(key, false); return; }
    syncInFlight.add(key);
    callWrite('saveState', key, patch)
      .then((merged) => {
        syncInFlight.delete(key);
        if (isSyncPending(key)) { // a newer local edit is waiting: only record what we sent
          setSyncedKey(key, applyPatch(synced[key], patch));
          return;
        }
        setSyncedKey(key, merged); // nothing newer locally → adopt the server view (companions' days too)
        setDirty(key, false);
        adoptShared(key, merged);
      })
      .catch((e) => { syncInFlight.delete(key); showError(`동선 저장 실패: ${e.message}`); });
  }, SYNC_DELAY_MS);
}

/* travel times: cache-first, misses fetched in batches, then re-render */
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
const travelKey = (a, b, dep) => `${a.lat.toFixed(5)},${a.lng.toFixed(5)}>${b.lat.toFixed(5)},${b.lng.toFixed(5)}${dep ? `@${dep.tag}` : ''}${MODE_SUFFIX[CFG.travelMode] || ''}`;
const isEstimated = (a, b, date, t) => travelCache[travelKey(a, b, departBucket(date, t))] == null;
const isTravelLoadingFor = (a, b, date, t) => IS_GAS && !(travelKey(a, b, departBucket(date, t)) in travelCache);

function travelMin(a, b, date, t) {
  const dep = departBucket(date, t);
  const k = travelKey(a, b, dep);
  if (travelCache[k] != null) return travelCache[k];
  if (IS_GAS && !(k in travelCache)) {
    travelPending.set(k, { key: k, from: { lat: a.lat, lng: a.lng }, to: { lat: b.lat, lng: b.lng }, depart: dep ? dep.iso : null });
  }
  return Route.mockTravelMin(a, b, CFG.travelMode);
}

function flushTravel() {
  if (!IS_GAS || isTravelLoading || !travelPending.size) return;
  const batch = [...travelPending.values()].slice(0, TRAVEL_BATCH);
  batch.forEach((p) => travelPending.delete(p.key));
  isTravelLoading = true;
  callServer('getTravelTimes', batch)
    // a key the server did not answer is settled as null (estimate): re-asking it every render would loop forever
    .then((res) => { travelCache = { ...travelCache, ...Object.fromEntries(batch.map((p) => [p.key, res && res[p.key] != null ? res[p.key] : null])) }; })
    .catch((e) => {
      travelCache = { ...travelCache, ...Object.fromEntries(batch.map((p) => [p.key, null])) };
      showError(`이동시간 조회 실패 (직선거리 추정으로 표시): ${e.message}`);
    })
    .finally(() => {
      store.set('travel', Object.fromEntries(Object.entries(travelCache).filter(([, v]) => v != null)));
      isTravelLoading = false;
      render();
    });
}

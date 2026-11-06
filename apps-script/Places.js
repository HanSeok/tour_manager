/* ---------- places: My Maps (KML) + Places API (New) hours for places without them ---------- */

const KML_CACHE_SEC = 10 * 60;
const HOURS_SHEET = '_영업시간';
const MAX_PLACES_LOOKUPS = 10; // per page load, bounds latency and cost
const PLACES_SEARCH_URL = 'https://places.googleapis.com/v1/places:searchText';
const PLACE_LOOKUP_RADIUS_M = 300;

/** → {places:[{name,lat,lng,folder,description,data,placeId?,periods?}], error?, notes[]} */
function getPlaces_() {
  const kml = readMyMaps_();
  if (kml.error) return { places: [], error: kml.error, notes: [] };
  return enrichHours_(kml.places);
}

function readMyMaps_() {
  try {
    return readMyMapsUnsafe_();
  } catch (err) { // network / XML errors must not take the whole page down
    console.error(`My Maps read failed: ${err}`);
    return { error: `My Maps를 읽는 중 오류가 났습니다: ${String(err.message || err).slice(0, 120)}` };
  }
}

function readMyMapsUnsafe_() {
  const cache = CacheService.getScriptCache();
  const cacheKey = `mymaps:${config_().mid}`;
  const hit = cache.get(cacheKey);
  if (hit) return { places: JSON.parse(hit) };
  const mid = config_().mid;
  const res = UrlFetchApp.fetch(`https://www.google.com/maps/d/kml?mid=${encodeURIComponent(mid)}&forcekml=1`, { muteHttpExceptions: true });
  const body = res.getContentText('UTF-8');
  if (res.getResponseCode() !== 200 || !body.includes('<kml')) {
    return { error: `My Maps를 읽지 못했습니다 (HTTP ${res.getResponseCode()}). 지도 공유를 '링크가 있는 모든 사용자 보기'로 설정해 주세요` };
  }
  const root = XmlService.parse(body).getRootElement();
  const ns = root.getNamespace();
  const text = (el, name) => { const c = el && el.getChild(name, ns); return c ? c.getText().trim() : ''; };
  const places = [];
  const collect = (el, folder) => {
    el.getChildren('Placemark', ns).forEach((pm) => {
      const ext = pm.getChild('ExtendedData', ns);
      const data = ext ? Object.fromEntries(ext.getChildren('Data', ns).filter((d) => d.getAttribute('name')).map((d) => [d.getAttribute('name').getValue(), text(d, 'value')])) : {};
      const pos = placemarkPosition_(pm.getChild('Point', ns) ? text(pm.getChild('Point', ns), 'coordinates') : '', data, text(pm, 'address'));
      if (pos) places.push({ name: text(pm, 'name'), lat: pos.lat, lng: pos.lng, folder, description: text(pm, 'description'), data });
    });
    el.getChildren('Folder', ns).forEach((f) => collect(f, text(f, 'name')));
  };
  collect(root.getChild('Document', ns) || root, '');
  try { cache.put(cacheKey, JSON.stringify(places), KML_CACHE_SEC); } catch (err) { console.warn(`kml cache skipped: ${err}`); }
  return { places };
}

/** CSV-imported pins come without <Point> (only <address> + columns) → Point, then 위도/경도 columns, then geocode. */
function placemarkPosition_(coordinates, data, address) {
  const [lng, lat] = coordinates ? coordinates.split(',').map(Number) : [NaN, NaN];
  if (Number.isFinite(lat) && Number.isFinite(lng)) return { lat, lng };
  const cLat = Number(data['위도']), cLng = Number(data['경도']);
  if (data['위도'] && Number.isFinite(cLat) && Number.isFinite(cLng)) return { lat: cLat, lng: cLng };
  return address ? geocode_(address) : null;
}

function hoursSheet_() {
  const ss = SpreadsheetApp.getActive();
  let sheet = ss.getSheetByName(HOURS_SHEET);
  if (!sheet) {
    sheet = ss.insertSheet(HOURS_SHEET, ss.getSheets().length);
    sheet.getRange('A:A').setNumberFormat('@');
    sheet.getRange(1, 1, 1, 5).setValues([['key', 'placeId', 'periods', 'updated', 'primaryType']]);
    sheet.hideSheet();
  }
  return sheet;
}

function enrichHours_(places) {
  const needs = (p) => !p.data['영업시간']; // new pins (and CSV rows that had no hours)
  const keyOf = (p) => `${p.name}|${p.lat.toFixed(4)},${p.lng.toFixed(4)}`;
  if (!places.some(needs)) return { places, notes: [] };
  const apiKey = PropertiesService.getScriptProperties().getProperty('PLACES_API_KEY');
  const sheet = withLock_(hoursSheet_); // creation must not race
  const n = sheet.getLastRow() - 1;
  const known = n > 0 ? Object.fromEntries(sheet.getRange(2, 1, n, 5).getValues()
    .map((r) => [String(r[0]), { placeId: String(r[1]), periods: r[2] ? JSON.parse(r[2]) : [], primaryType: String(r[4] || '') }])) : {};
  const notes = [];
  const fresh = [];
  // new pins (no imported place_id) first, so the per-load lookup budget goes to them; API calls run outside the lock
  const queue = places.filter((p) => needs(p) && !known[keyOf(p)]).sort((a, b) => Number(Boolean(a.data.place_id)) - Number(Boolean(b.data.place_id)));
  queue.slice(0, apiKey ? MAX_PLACES_LOOKUPS : 0).forEach((p) => {
    const found = searchPlace_(apiKey, p, notes);
    if (!found) return;
    known[keyOf(p)] = found;
    fresh.push([keyOf(p), found.placeId, JSON.stringify(found.periods), new Date(), found.primaryType].map(safeCell_));
  });
  if (fresh.length) withLock_(() => sheet.getRange(sheet.getLastRow() + 1, 1, fresh.length, 5).setValues(fresh));
  if (!apiKey) notes.push('PLACES_API_KEY가 없어 새 장소 영업시간을 조회하지 못했습니다');
  const out = places.map((p) => {
    const hit = needs(p) && known[keyOf(p)];
    return hit ? { ...p, placeId: hit.placeId, periods: hit.periods, primaryType: hit.primaryType } : p;
  });
  return { places: out, notes };
}

/** Text Search (New) near the pin → {placeId, periods[]} | null (null on API error → retried next load). */
function searchPlace_(apiKey, p, notes) {
  const res = UrlFetchApp.fetch(PLACES_SEARCH_URL, {
    method: 'post', contentType: 'application/json', muteHttpExceptions: true,
    headers: { 'X-Goog-Api-Key': apiKey, 'X-Goog-FieldMask': 'places.id,places.regularOpeningHours,places.primaryType' },
    payload: JSON.stringify({
      textQuery: p.name, languageCode: 'ko', maxResultCount: 1,
      locationBias: { circle: { center: { latitude: p.lat, longitude: p.lng }, radius: PLACE_LOOKUP_RADIUS_M } },
    }),
  });
  if (res.getResponseCode() !== 200) {
    const msg = `Places API 오류 ${res.getResponseCode()}`;
    if (!notes.includes(msg)) notes.push(msg);
    console.error(`${msg}: ${res.getContentText().slice(0, 300)}`);
    return null;
  }
  const hit = (JSON.parse(res.getContentText()).places || [])[0];
  return { placeId: hit ? hit.id : '', periods: hit && hit.regularOpeningHours ? hit.regularOpeningHours.periods || [] : [], primaryType: hit && hit.primaryType || '' };
}

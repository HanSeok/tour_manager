// Calendar: trip events from the default calendar + dashboard-registered route events.

const dayStart_ = (date) => new Date(`${date}T00:00:00${config_().offset}`);
const atMin_ = (date, min) => new Date(dayStart_(date).getTime() + min * 60000);

function getTripEvents_() {
  return CalendarApp.getDefaultCalendar()
    .getEvents(dayStart_(config_().start), atMin_(config_().end, 24 * 60))
    .filter((ev) => !ev.isAllDayEvent())
    .map(toEvent_);
}

function toEvent_(ev) {
  const tz = config_().timezone;
  const fmt = (d, p) => Utilities.formatDate(d, tz, p);
  const desc = ev.getDescription() || '';
  const isRoute = ROUTE_TAG_RE.test(desc);
  const location = ev.getLocation() || '';
  const coords = location && !isRoute ? geocode_(location) : null;
  const placeMatch = desc.match(/place:(\S+)/);
  return {
    id: ev.getId(), title: ev.getTitle(), kind: isRoute ? 'route' : undefined,
    startDate: fmt(ev.getStartTime(), 'yyyy-MM-dd'), start: fmt(ev.getStartTime(), 'HH:mm'),
    endDate: fmt(ev.getEndTime(), 'yyyy-MM-dd'), end: fmt(ev.getEndTime(), 'HH:mm'),
    location, lat: coords ? coords.lat : null, lng: coords ? coords.lng : null,
    placeId: placeMatch ? placeMatch[1] : undefined,
  };
}

function routeEventsOn_(cal, date) {
  return cal.getEvents(dayStart_(date), atMin_(date, 48 * 60))
    .filter((ev) => ROUTE_TAG_RE.test(ev.getDescription() || '')
      && Utilities.formatDate(ev.getStartTime(), config_().timezone, 'yyyy-MM-dd') === date);
}

/** Replaces the dashboard-registered events of `date` with `items` [{title,startMin,endMin,location,placeId}]. */
function registerRoute(date, items) {
  const cfg = requireConfig_();
  if (!DATE_RE.test(String(date)) || date < cfg.start || date > cfg.end) fail_('날짜가 여행 기간이 아닙니다');
  if (!Array.isArray(items) || items.length > 30) fail_('등록할 일정 목록이 올바르지 않습니다');
  const clean = items.map((it) => {
    const s = Number(it.startMin), e = Number(it.endMin);
    if (!Number.isFinite(s) || !Number.isFinite(e) || s < 0 || e < s || e > 48 * 60) fail_('일정 시각이 올바르지 않습니다');
    return { title: String(it.title || '').slice(0, 100), s, e, location: String(it.location || '').slice(0, 200), placeId: String(it.placeId || '').slice(0, 120) };
  });
  return withLock_(() => {
    const cal = CalendarApp.getDefaultCalendar();
    routeEventsOn_(cal, date).forEach((ev) => ev.deleteEvent());
    clean.forEach((it) => cal.createEvent(it.title, atMin_(date, it.s), atMin_(date, it.e), {
      location: it.location, description: `${ROUTE_TAG} place:${it.placeId}\n여행 대시보드에서 등록한 동선입니다. 대시보드에서 다시 등록하면 교체됩니다.`,
    }));
    return getTripEvents_();
  });
}

function unregisterRoute(date) {
  requireConfig_();
  if (!DATE_RE.test(String(date))) fail_('날짜 형식이 올바르지 않습니다');
  return withLock_(() => {
    routeEventsOn_(CalendarApp.getDefaultCalendar(), date).forEach((ev) => ev.deleteEvent());
    return getTripEvents_();
  });
}

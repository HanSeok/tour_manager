package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"tourmanager/internal/drive"
	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
	"tourmanager/internal/trips"
)

// fakeTrips stands in for the Drive-backed service: it "creates" the trip by writing the settings into the grid.
type fakeTrips struct {
	grid *sheets.MemGrid
	err  error
	got  trips.Request
	list []drive.Trip

	current  string
	onSwitch func(id string)
}

func (f *fakeTrips) Create(_ context.Context, email, _ string, req trips.Request) (drive.Trip, error) {
	f.got = req
	if f.err != nil {
		return drive.Trip{}, f.err
	}
	rows := tripconfig.RowsFor(map[string]string{"title": req.Title, "start": req.Start, "end": req.End, "timezone": "Asia/Tokyo",
		"destKeywords": "x", "myMapsUrl": "https://www.google.com/maps/d/edit?mid=S", "localCurrency": "엔", "localSymbol": "¥", "rate": "9"})
	_, _ = f.grid.EnsureTab(context.Background(), tripconfig.SheetName, rows[0])
	for _, r := range rows[1:] {
		_ = f.grid.Append(context.Background(), tripconfig.SheetName, []any{r[0], r[1], r[2]})
	}
	return drive.Trip{Title: req.Title, SheetID: "sheet9", FolderID: "f9"}, nil
}

func (f *fakeTrips) List() ([]drive.Trip, error) { return f.list, nil }

func (f *fakeTrips) Current() (string, error) { return f.current, nil }

func (f *fakeTrips) Switch(id string) error {
	if f.err != nil {
		return f.err
	}
	for _, t := range f.list {
		if t.SheetID == id {
			f.current = id
			if f.onSwitch != nil {
				f.onSwitch(id)
			}
			return nil
		}
	}
	return trips.ErrUnknownTrip
}

// noTripGrid answers every read the way the server does before the first trip exists.
type noTripGrid struct{ *sheets.MemGrid }

func (noTripGrid) Rows(context.Context, string) ([][]string, error)    { return nil, sheets.ErrNoTrip }
func (noTripGrid) RawRows(context.Context, string) ([][]string, error) { return nil, sheets.ErrNoTrip }

func tripRouter(role members.Role, g sheets.Grid, tm TripManager) *Router {
	dir := members.NewDirectory("o@x.com", func(context.Context) ([][]string, error) { return nil, nil }, time.Minute)
	tr := &Trip{Grid: g, Members: dir, Expenses: &expenses.Service{Grid: g}, Trips: tm, CfgTTL: time.Hour}
	rt := NewRouter(func(*http.Request) (Principal, bool) {
		return Principal{Email: "o@x.com", Name: "나", Role: role}, true
	}, "")
	tr.Register(rt)
	return rt
}

const newTripBody = `{"args":[{"title":"제주","start":"2027-05-01","end":"2027-05-03"}]}`

func TestCreateTripOwnerGetsTheTripAndTheDashboardSwitchesToIt(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	tm := &fakeTrips{grid: g}
	rt := tripRouter(members.Owner, g, tm)

	_, out := call(rt, "bootstrap", `{"args":[]}`, nil) // caches "not configured" for an hour
	if m := out["data"].(map[string]any)["config"].(map[string]any)["missing"].([]any); len(m) == 0 {
		t.Fatal("precondition: the trip should not be configured yet")
	}

	code, out := call(rt, "createTrip", newTripBody, nil)
	if code != 200 {
		t.Fatalf("createTrip: %d %v", code, out)
	}
	if tr := out["data"].(map[string]any)["trip"].(map[string]any); tr["sheetId"] != "sheet9" || tr["title"] != "제주" {
		t.Errorf("trip = %v", tr)
	}
	if tm.got.Title != "제주" || tm.got.Start != "2027-05-01" {
		t.Errorf("request not passed through: %+v", tm.got)
	}
	_, out = call(rt, "bootstrap", `{"args":[]}`, nil)
	cfg := out["data"].(map[string]any)["config"].(map[string]any)
	if cfg["title"] != "제주" || len(cfg["missing"].([]any)) != 0 {
		t.Errorf("the new trip must show immediately, not after the settings cache expires: %v", cfg)
	}
}

func TestCreateTripIsOwnerOnly(t *testing.T) {
	for _, role := range []members.Role{members.Editor, members.Viewer} {
		g := sheets.NewMemGrid(nil)
		tm := &fakeTrips{grid: g}
		if code, _ := call(tripRouter(role, g, tm), "createTrip", newTripBody, nil); code != 403 {
			t.Errorf("%s: %d", role, code)
		}
		if tm.got.Title != "" {
			t.Errorf("%s reached the service", role)
		}
		if code, _ := call(tripRouter(role, g, tm), "listTrips", `{"args":[]}`, nil); code != 403 {
			t.Errorf("%s listTrips: %d", role, code)
		}
	}
}

func TestCreateTripInputErrorIs422WithTheMessage(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	rt := tripRouter(members.Owner, g, &fakeTrips{grid: g, err: &trips.InputError{Msg: "올바르지 않음: 시작일"}})
	code, out := call(rt, "createTrip", newTripBody, nil)
	if code != 422 || !strings.Contains(out["error"].(string), "시작일") {
		t.Errorf("%d %v", code, out)
	}
}

func TestCreateTripGoogleErrorShowsTheOwnerTheReasonAndTheFix(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	err := errors.New("drive POST /drive/v3/files: 403 Google Drive API has not been used in project 1 before or it is disabled")
	code, out := call(tripRouter(members.Owner, g, &fakeTrips{grid: g, err: err}), "createTrip", newTripBody, nil)
	msg, _ := out["error"].(string)
	if code != 502 || !strings.Contains(msg, "Drive API has not been used") || !strings.Contains(msg, "사용 설정") {
		t.Errorf("%d %q", code, msg)
	}
}

func TestCreateTripWithoutGoogleExplainsItself(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	code, out := call(tripRouter(members.Owner, g, nil), "createTrip", newTripBody, nil)
	if code != 412 || out["error"] == nil {
		t.Errorf("%d %v", code, out)
	}
}

func TestBootstrapBeforeTheFirstTripAsksTheOwnerToCreateOne(t *testing.T) {
	g := noTripGrid{sheets.NewMemGrid(nil)}
	code, out := call(tripRouter(members.Owner, g, &fakeTrips{}), "bootstrap", `{"args":[]}`, nil)
	data, _ := out["data"].(map[string]any)
	if code != 200 || data["newTrip"] != true {
		t.Fatalf("owner: %d %v", code, out)
	}
	if data["connect"] != nil {
		t.Errorf("not a connection problem: %v", data["connect"])
	}
	if d, _ := data["tripDefaults"].(map[string]any); d["timezone"] != "Asia/Tokyo" || d["rate"] != "9.3" {
		t.Errorf("the form needs the defaults the server will apply: %v", data["tripDefaults"])
	}
	if ps, _ := data["tripPresets"].([]any); len(ps) < 10 || ps[0].(map[string]any)["timezone"] == nil {
		t.Errorf("the form needs the destination choices: %v", data["tripPresets"])
	}
	cs := checksOf(t, out)
	if len(cs) == 0 || cs[len(cs)-1]["ok"] != false {
		t.Errorf("checks = %v", cs)
	}
	code, out = call(tripRouter(members.Editor, g, &fakeTrips{}), "bootstrap", `{"args":[]}`, nil)
	if code != 503 || !strings.Contains(out["error"].(string), "여행") {
		t.Errorf("editor: %d %v", code, out)
	}
}

func TestListTripsReturnsWhatTheRegistryKnows(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	tm := &fakeTrips{grid: g, list: []drive.Trip{{Title: "A", SheetID: "a"}, {Title: "B", SheetID: "b"}}}
	code, out := call(tripRouter(members.Owner, g, tm), "listTrips", `{"args":[]}`, nil)
	list, _ := out["data"].(map[string]any)["trips"].([]any)
	if code != 200 || len(list) != 2 {
		t.Errorf("%d %v", code, out)
	}
}

// swGrid is the spreadsheet of whichever trip is current: switching trips swaps what it reads.
type swGrid struct{ *sheets.MemGrid }

func tripGrid(title string) *sheets.MemGrid {
	g := sheets.NewMemGrid(nil)
	rows := tripconfig.RowsFor(map[string]string{"title": title, "start": "2027-05-01", "end": "2027-05-03", "timezone": "Asia/Tokyo",
		"destKeywords": "x", "myMapsUrl": "https://www.google.com/maps/d/edit?mid=S", "localCurrency": "엔", "localSymbol": "¥", "rate": "9"})
	_, _ = g.EnsureTab(context.Background(), tripconfig.SheetName, rows[0])
	for _, r := range rows[1:] {
		_ = g.Append(context.Background(), tripconfig.SheetName, []any{r[0], r[1], r[2]})
	}
	return g
}

func switchSetup() (*swGrid, *fakeTrips) {
	a, b := tripGrid("오사카"), tripGrid("제주")
	sw := &swGrid{a}
	tm := &fakeTrips{list: []drive.Trip{{Title: "오사카", SheetID: "a"}, {Title: "제주", SheetID: "b"}}, current: "a"}
	tm.onSwitch = func(id string) {
		if id == "b" {
			sw.MemGrid = b
		} else {
			sw.MemGrid = a
		}
	}
	return sw, tm
}

func TestSwitchTripShowsTheOtherTripAtOnce(t *testing.T) {
	sw, tm := switchSetup()
	rt := tripRouter(members.Owner, sw, tm)
	_, out := call(rt, "bootstrap", `{"args":[]}`, nil) // caches the settings of the first trip
	if out["data"].(map[string]any)["config"].(map[string]any)["title"] != "오사카" {
		t.Fatal("precondition")
	}
	code, out := call(rt, "switchTrip", `{"args":["b"]}`, nil)
	if code != 200 || tm.current != "b" {
		t.Fatalf("switchTrip: %d %v current=%s", code, out, tm.current)
	}
	_, out = call(rt, "bootstrap", `{"args":[]}`, nil)
	data := out["data"].(map[string]any)
	if data["config"].(map[string]any)["title"] != "제주" {
		t.Errorf("must show the new trip right away, not after the settings cache expires: %v", data["config"])
	}
	if data["currentTrip"] != "b" {
		t.Errorf("currentTrip = %v", data["currentTrip"])
	}
}

func TestSwitchTripIsOwnerOnlyAndNeverReachesTheService(t *testing.T) {
	for _, role := range []members.Role{members.Editor, members.Viewer} {
		sw, tm := switchSetup()
		if code, _ := call(tripRouter(role, sw, tm), "switchTrip", `{"args":["b"]}`, nil); code != 403 {
			t.Errorf("%s: %d", role, code)
		}
		if tm.current != "a" {
			t.Errorf("%s switched the trip", role)
		}
	}
}

func TestSwitchTripUnknownOrMissingIdIsRefused(t *testing.T) {
	sw, tm := switchSetup()
	rt := tripRouter(members.Owner, sw, tm)
	if code, _ := call(rt, "switchTrip", `{"args":["nope"]}`, nil); code != 404 {
		t.Errorf("unknown trip: %d", code)
	}
	for _, body := range []string{`{"args":[]}`, `{"args":[""]}`, `{"args":[5]}`} {
		if code, _ := call(rt, "switchTrip", body, nil); code != 422 {
			t.Errorf("%s: %d", body, code)
		}
	}
	if tm.current != "a" {
		t.Errorf("current changed to %s", tm.current)
	}
	if code, _ := call(tripRouter(members.Owner, sw, nil), "switchTrip", `{"args":["b"]}`, nil); code != 412 {
		t.Errorf("no trip manager: %d", code)
	}
}

func TestBootstrapListsTripsForTheOwnerOnly(t *testing.T) {
	sw, tm := switchSetup()
	_, out := call(tripRouter(members.Owner, sw, tm), "bootstrap", `{"args":[]}`, nil)
	data := out["data"].(map[string]any)
	if l, _ := data["trips"].([]any); len(l) != 2 || data["currentTrip"] != "a" {
		t.Errorf("owner: trips=%v current=%v", data["trips"], data["currentTrip"])
	}
	if data["tripDefaults"] == nil || data["tripPresets"] == nil {
		t.Errorf("the owner can start another trip from the dashboard, so the form data comes along")
	}
	_, out = call(tripRouter(members.Editor, sw, tm), "bootstrap", `{"args":[]}`, nil)
	data = out["data"].(map[string]any)
	if data["trips"] != nil || data["tripPresets"] != nil {
		t.Errorf("an editor must not get the list of trips: %v", data["trips"])
	}
	if data["currentTrip"] != "a" {
		t.Errorf("every browser needs to know which trip it is looking at: %v", data["currentTrip"])
	}
}

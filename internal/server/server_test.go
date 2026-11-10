package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"tourmanager/internal/api"
	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
)

func devServer(t *testing.T) http.Handler {
	t.Helper()
	h, err := New(Config{
		BaseURL: "http://localhost:8080", OwnerEmail: "dev-owner@example.com", DataDir: t.TempDir(),
		SessionSecret: []byte(strings.Repeat("s", 32)), DevLogin: true, FixtureDir: "../../dev/fixtures", Version: "t",
		Static: fstest.MapFS{"index.html": {Data: []byte("<h1>dashboard</h1>")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, h http.Handler, email string) []*http.Cookie {
	t.Helper()
	rec := do(h, "GET", "/auth/dev?email="+email, "", nil)
	if rec.Code != 302 {
		t.Fatalf("dev login %s: %d %s", email, rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()
}

func TestAnonymousIsKeptOutButHealthzIsOpen(t *testing.T) {
	h := devServer(t)
	if rec := do(h, "GET", "/healthz", "", nil); rec.Code != 200 {
		t.Errorf("healthz %d", rec.Code)
	}
	if rec := do(h, "GET", "/", "", nil); rec.Code != 401 {
		t.Errorf("anonymous page: %d", rec.Code)
	}
	if rec := do(h, "POST", "/api/bootstrap", `{"args":[]}`, nil); rec.Code != 401 {
		t.Errorf("anonymous api: %d", rec.Code)
	}
}

func TestMemberSeesDashboardAndBootstrap(t *testing.T) {
	h := devServer(t)
	cookies := login(t, h, "dev-editor@example.com")
	page := do(h, "GET", "/", "", cookies)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "dashboard") {
		t.Fatalf("page: %d %s", page.Code, page.Body.String())
	}
	if page.Header().Get("X-Frame-Options") != "DENY" {
		t.Error("security headers missing")
	}
	// map tiles are fetched from another site that insists on a Referer: it must not be stripped completely
	if rp := page.Header().Get("Referrer-Policy"); rp != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy %q would strip the Referer the OSM tile servers require", rp)
	}
	rec := do(h, "POST", "/api/bootstrap", `{"args":[]}`, cookies)
	var out struct {
		OK   bool `json:"ok"`
		Data struct {
			Config struct {
				Title   string   `json:"title"`
				Days    int      `json:"days"`
				Offset  string   `json:"offset"`
				Missing []string `json:"missing"`
				Invalid []string `json:"invalid"`
			} `json:"config"`
			Me struct {
				Email string `json:"email"`
				Role  string `json:"role"`
				Name  string `json:"name"`
			} `json:"me"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.OK {
		t.Fatalf("bootstrap: %d %s", rec.Code, rec.Body.String())
	}
	c := out.Data.Config
	if c.Title != "샘플 여행" || c.Days != 5 || c.Offset != "+09:00" || len(c.Missing) != 0 || len(c.Invalid) != 0 {
		t.Errorf("config: %+v", c)
	}
	if out.Data.Me.Role != "editor" || out.Data.Me.Name != "동행" {
		t.Errorf("me: %+v", out.Data.Me)
	}
}

func TestNonMemberCannotSignInAndForgedCookieFails(t *testing.T) {
	h := devServer(t)
	if rec := do(h, "GET", "/auth/dev?email=stranger@example.com", "", nil); rec.Code != 403 {
		t.Errorf("stranger: %d", rec.Code)
	}
	forged := []*http.Cookie{{Name: "tm_session", Value: "eyJlIjoiZGV2LW93bmVyQGV4YW1wbGUuY29tIiwieCI6OTk5OTk5OTk5OX0.AAAA"}}
	if rec := do(h, "POST", "/api/bootstrap", `{"args":[]}`, forged); rec.Code != 401 {
		t.Errorf("forged session: %d", rec.Code)
	}
}

func TestConfigValidation(t *testing.T) {
	ok := Config{BaseURL: "https://trip.example.com", OwnerEmail: "o@x.com", SessionSecret: []byte(strings.Repeat("s", 32)),
		ClientID: "id", ClientSecret: "s", SpreadsheetID: "sid", Static: fstest.MapFS{}}
	if _, err := New(ok); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	mut := map[string]func(*Config){
		"short secret":        func(c *Config) { c.SessionSecret = []byte("short") },
		"dev login on public": func(c *Config) { c.DevLogin = true; c.ClientID = "" },
		"no google client":    func(c *Config) { c.ClientID = "" },
		"no owner":            func(c *Config) { c.OwnerEmail = "" },
		"bad url":             func(c *Config) { c.BaseURL = "trip.example.com" },
	}
	for name, f := range mut {
		c := ok
		f(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestWithoutOwnerConnectionTheOwnerGetsTheConnectScreen(t *testing.T) {
	h, err := New(Config{BaseURL: "http://localhost:8080", OwnerEmail: "o@x.com", DataDir: t.TempDir(),
		SessionSecret: []byte(strings.Repeat("s", 32)), ClientID: "id", ClientSecret: "s", SpreadsheetID: "sid",
		Static: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	// no Google round-trip in this test: forge a valid owner session with the same secret
	cookie := &http.Cookie{Name: "tm_session"}
	rec := httptest.NewRecorder()
	signerCookie(t, rec, "o@x.com")
	cookie.Value = rec.Result().Cookies()[0].Value
	resp := do(h, "POST", "/api/bootstrap", `{"args":[]}`, []*http.Cookie{cookie})
	// the owner is not sent to an error page: bootstrap answers with the setup screen's data and the link to connect
	body := resp.Body.String()
	if resp.Code != 200 || !strings.Contains(body, `"connect":"/auth/owner"`) || !strings.Contains(body, "연결되지 않았습니다") {
		t.Errorf("want the setup data with a connect link, got %d %s", resp.Code, body)
	}
}

type reply struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

func call(t *testing.T, h http.Handler, cookies []*http.Cookie, fn, args string) (int, reply) {
	t.Helper()
	rec := do(h, "POST", "/api/"+fn, `{"args":`+args+`}`, cookies)
	var r reply
	_ = json.Unmarshal(rec.Body.Bytes(), &r)
	return rec.Code, r
}

type expense struct {
	ID, Date, Time, Item, Payer, CreatedBy, UpdatedBy, UpdatedAt string
	Amount                                                       float64
}

func list(t *testing.T, r reply) []expense {
	t.Helper()
	var out []expense
	if err := json.Unmarshal(r.Data, &out); err != nil {
		t.Fatalf("list: %v %s", err, r.Data)
	}
	return out
}

const eid = "22222222-2222-4222-8222-222222222222"

func TestExpensesEndToEndWithRolesAndConflicts(t *testing.T) {
	h := devServer(t)
	editor := login(t, h, "dev-editor@example.com")
	owner := login(t, h, "dev-owner@example.com")
	viewer := login(t, h, "dev-viewer@example.com")
	add := `[{"id":"` + eid + `","date":"2027-04-11","category":"식비","item":"소바","amount":1200,"payer":"나","method":"카드"}]`

	if code, r := call(t, h, viewer, "addExpense", add); code != 403 {
		t.Fatalf("viewer must not write: %d %s", code, r.Error)
	}
	code, r := call(t, h, editor, "addExpense", add)
	if code != 200 {
		t.Fatalf("add: %d %s", code, r.Error)
	}
	got := list(t, r)
	if len(got) != 1 || got[0].CreatedBy != "dev-editor@example.com" || got[0].Item != "소바" || got[0].Amount != 1200 || got[0].UpdatedAt == "" {
		t.Fatalf("%+v", got)
	}
	// retry with the same id: still one row
	if _, r = call(t, h, editor, "addExpense", add); len(list(t, r)) != 1 {
		t.Errorf("retry must be idempotent")
	}
	// everyone, viewers included, sees it via refresh and bootstrap
	if _, r = call(t, h, viewer, "refresh", `[]`); len(list(t, reply{Data: mustField(t, r, "expenses")})) != 1 {
		t.Errorf("viewer refresh must list the expense")
	}
	// the owner edits the editor's entry (any editor may), with the version they saw
	seen := got[0].UpdatedAt
	upd := `["` + eid + `",{"date":"2027-04-11","category":"식비","item":"소바 곱빼기","amount":1500,"payer":"나","method":"카드"},"` + seen + `"]`
	code, r = call(t, h, owner, "updateExpense", upd)
	if code != 200 || list(t, r)[0].UpdatedBy != "dev-owner@example.com" || list(t, r)[0].CreatedBy != "dev-editor@example.com" {
		t.Fatalf("update: %d %s %s", code, r.Error, r.Data)
	}
	// the editor is still looking at the old version → 409 and nothing changes
	code, r = call(t, h, editor, "deleteExpense", `["`+eid+`","`+seen+`"]`)
	if code != 409 || !strings.Contains(r.Error, "먼저 수정") {
		t.Fatalf("stale delete: %d %q", code, r.Error)
	}
	_, r = call(t, h, viewer, "refresh", `[]`)
	if e := list(t, reply{Data: mustField(t, r, "expenses")}); len(e) != 1 || e[0].Amount != 1500 {
		t.Fatalf("a rejected delete must not change data: %+v", e)
	}
	// validation → 422 with the message; unknown id → 404
	if code, r = call(t, h, editor, "addExpense", `[{"date":"2027-04-11","category":"식비","amount":-5,"payer":"나","method":"카드"}]`); code != 422 || !strings.Contains(r.Error, "금액") {
		t.Errorf("bad amount: %d %q", code, r.Error)
	}
	if code, _ = call(t, h, editor, "deleteExpense", `["nope"]`); code != 404 {
		t.Errorf("unknown id: %d", code)
	}
	// delete with the current version works
	_, r = call(t, h, owner, "refresh", `[]`)
	cur := list(t, reply{Data: mustField(t, r, "expenses")})[0].UpdatedAt
	if code, r = call(t, h, owner, "deleteExpense", `["`+eid+`","`+cur+`"]`); code != 200 || len(list(t, r)) != 0 {
		t.Fatalf("delete: %d %s", code, r.Error)
	}
}

func mustField(t *testing.T, r reply, name string) json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(r.Data, &m); err != nil || m[name] == nil {
		t.Fatalf("no %q in %s (%v)", name, r.Data, err)
	}
	return m[name]
}

func TestBootstrapCreatesExpenseTabAndListsExistingRows(t *testing.T) {
	h := devServer(t)
	cookies := login(t, h, "dev-viewer@example.com")
	code, r := call(t, h, cookies, "bootstrap", `[]`)
	if code != 200 {
		t.Fatalf("%d %s", code, r.Error)
	}
	if e := list(t, reply{Data: mustField(t, r, "expenses")}); len(e) != 0 {
		t.Errorf("a fresh trip has no expenses: %+v", e)
	}
}

func TestExpenseCallsNeedAConfiguredTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/멤버.json", []byte(`[["이메일","표시이름","역할"],["e@example.com","에","편집자"]]`), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := New(Config{BaseURL: "http://localhost:8080", OwnerEmail: "o@example.com", DataDir: t.TempDir(), SessionSecret: []byte(strings.Repeat("s", 32)),
		DevLogin: true, FixtureDir: dir, Static: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	cookies := login(t, h, "e@example.com")
	if code, r := call(t, h, cookies, "addExpense", `[{"date":"2027-04-11","amount":1}]`); code != 412 || !strings.Contains(r.Error, "설정") {
		t.Errorf("want 412 asking for the settings, got %d %q", code, r.Error)
	}
	code, r := call(t, h, cookies, "bootstrap", `[]`)
	var cfg struct{ Missing []string }
	_ = json.Unmarshal(mustField(t, r, "config"), &cfg)
	if code != 200 || len(cfg.Missing) == 0 {
		t.Errorf("bootstrap still answers so the setup screen can show what is missing: %d %+v", code, cfg)
	}
}

func TestServerModeScriptAndSampleFlag(t *testing.T) {
	h := devServer(t)
	rec := do(h, "GET", "/servermode.js", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "TM_API = true") || !strings.Contains(rec.Header().Get("Content-Type"), "javascript") {
		t.Errorf("the page learns it is served by this server from here: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	cookies := login(t, h, "dev-viewer@example.com")
	_, r := call(t, h, cookies, "bootstrap", `[]`)
	var sample bool
	_ = json.Unmarshal(mustField(t, r, "sample"), &sample)
	if !sample {
		t.Error("dev fixtures must flag sample=true so the dashboard shows its sample calendar and places")
	}
}

func TestRealSheetsBootstrapIsNotSample(t *testing.T) {
	// a server wired to Google (no fixtures) must never ask the dashboard to show sample places
	g := sheets.NewMemGrid(nil)
	tr := &api.Trip{Grid: g, Members: members.NewDirectory("o@example.com", func(context.Context) ([][]string, error) { return nil, nil }, time.Minute),
		Expenses: &expenses.Service{Grid: g}}
	rt := api.NewRouter(func(*http.Request) (api.Principal, bool) {
		return api.Principal{Email: "o@example.com", Name: "o", Role: members.Owner}, true
	}, "")
	tr.Register(rt)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/bootstrap", strings.NewReader(`{"args":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rt.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"sample":false`) {
		t.Errorf("sample must be false: %s", rec.Body.String())
	}
}

func TestPlacesEndToEndWithRoles(t *testing.T) {
	h := devServer(t)
	editor := login(t, h, "dev-editor@example.com")
	viewer := login(t, h, "dev-viewer@example.com")
	const pid = "44444444-4444-4444-8444-444444444444"
	place := `{"id":"` + pid + `","name":"슈리성","layer":"관광","lat":26.217,"lng":127.7195,"memo":"아침에"}`

	if code, _ := call(t, h, viewer, "addPlace", "["+place+"]"); code != 403 {
		t.Errorf("viewer add: %d", code)
	}
	code, r := call(t, h, editor, "addPlace", "["+place+"]")
	if code != 200 || !strings.Contains(string(r.Data), `"createdBy":"dev-editor@example.com"`) {
		t.Fatalf("editor add: %d %s", code, r.Data)
	}
	_, r = call(t, h, viewer, "bootstrap", "[]")
	if !strings.Contains(string(r.Data), `"tripPlaces":[{"id":"`+pid+`"`) {
		t.Errorf("a viewer sees the place after bootstrap: %s", r.Data)
	}
	if code, _ := call(t, h, editor, "deletePlace", `["`+pid+`"]`); code != 200 {
		t.Errorf("delete: %d", code)
	}
	_, r = call(t, h, viewer, "refresh", "[]")
	if !strings.Contains(string(r.Data), `"tripPlaces":[]`) {
		t.Errorf("refresh after delete: %s", r.Data)
	}
}

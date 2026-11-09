package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
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
		"no sheet":            func(c *Config) { c.SpreadsheetID = "" },
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

func TestWithoutOwnerConnectionBootstrapExplainsWhy(t *testing.T) {
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
	if resp.Code != 502 || !strings.Contains(resp.Body.String(), "소유자 Google 연결") {
		t.Errorf("want 502 with a hint, got %d %s", resp.Code, resp.Body.String())
	}
}

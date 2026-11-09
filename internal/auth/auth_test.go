package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"tourmanager/internal/members"
)

func TestSignerRoundTripTamperAndExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := Signer{Secret: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }}
	v := s.Encode("a@x.com", time.Hour)
	if e, ok := s.Decode(v); !ok || e != "a@x.com" {
		t.Fatalf("round trip: %q %v", e, ok)
	}
	if _, ok := s.Decode(v + "x"); ok {
		t.Error("tampered signature accepted")
	}
	other := Signer{Secret: []byte("another-secret-another-secret-00"), Now: s.Now}
	if _, ok := other.Decode(v); ok {
		t.Error("cookie signed with another secret accepted")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := s.Decode(v); ok {
		t.Error("expired cookie accepted")
	}
	for _, bad := range []string{"", "abc", ".", "a.b.c"} {
		if _, ok := s.Decode(bad); ok {
			t.Errorf("garbage %q accepted", bad)
		}
	}
}

// fakeGoogle answers the token exchange and userinfo calls.
func fakeGoogle(t *testing.T, email string, verified bool, refresh string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "refresh_token": refresh, "expires_in": 3600})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" {
			http.Error(w, "no", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"email": email, "email_verified": verified, "name": "N"})
	})
	return httptest.NewServer(mux)
}

func newGoogle(t *testing.T, gs *httptest.Server, tokens TokenStore) *Google {
	t.Helper()
	cfg := func(path string) *oauth2.Config {
		return &oauth2.Config{ClientID: "id", ClientSecret: "sec", RedirectURL: "http://localhost:8080" + path,
			Endpoint: oauth2.Endpoint{AuthURL: gs.URL + "/auth", TokenURL: gs.URL + "/token"}}
	}
	dir := members.NewDirectory("owner@x.com", func(context.Context) ([][]string, error) {
		return [][]string{{"ed@x.com", "에디", "편집자"}}, nil
	}, time.Minute)
	return &Google{Client: cfg("/auth/callback"), OwnerClient: cfg("/auth/owner/callback"), UserinfoURL: gs.URL + "/userinfo",
		HTTP: gs.Client(), Signer: Signer{Secret: []byte("0123456789abcdef0123456789abcdef")}, Dir: dir, Tokens: tokens}
}

// start runs Login and returns the state cookie + state value.
func start(t *testing.T, g *Google) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	g.Login(rec, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	st := loc.Query().Get("state")
	if rec.Code != 302 || st == "" {
		t.Fatalf("login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == stateCookie {
			return c, st
		}
	}
	t.Fatal("no state cookie")
	return nil, ""
}

func callback(g *Google, c *http.Cookie, state string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=c&state="+url.QueryEscape(state), nil)
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	g.Callback(rec, req)
	return rec
}

func TestLoginCallbackMemberGetsSession(t *testing.T) {
	gs := fakeGoogle(t, "ED@x.com", true, "")
	defer gs.Close()
	g := newGoogle(t, gs, nil)
	c, st := start(t, g)
	rec := callback(g, c, st)
	if rec.Code != 302 {
		t.Fatalf("want redirect, got %d %s", rec.Code, rec.Body.String())
	}
	var sess string
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == CookieName {
			sess = ck.Value
			if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
				t.Error("session cookie must be HttpOnly + SameSite=Lax")
			}
		}
	}
	if e, ok := g.Signer.Decode(sess); !ok || e != "ED@x.com" {
		t.Errorf("session: %q %v", e, ok)
	}
}

func TestCallbackRejectsStrangerUnverifiedAndBadState(t *testing.T) {
	gs := fakeGoogle(t, "stranger@x.com", true, "")
	g := newGoogle(t, gs, nil)
	c, st := start(t, g)
	if rec := callback(g, c, st); rec.Code != 403 {
		t.Errorf("non-member: %d", rec.Code)
	}
	gs.Close()

	gs2 := fakeGoogle(t, "ed@x.com", false, "")
	defer gs2.Close()
	g2 := newGoogle(t, gs2, nil)
	c2, st2 := start(t, g2)
	if rec := callback(g2, c2, st2); rec.Code != 401 {
		t.Errorf("unverified e-mail: %d", rec.Code)
	}
	if rec := callback(g2, c2, "forged"); rec.Code != 400 {
		t.Errorf("state mismatch: %d", rec.Code)
	}
	if rec := callback(g2, nil, st2); rec.Code != 400 {
		t.Errorf("missing state cookie: %d", rec.Code)
	}
}

func TestOwnerConnectStoresRefreshTokenOnlyForOwner(t *testing.T) {
	gs := fakeGoogle(t, "owner@x.com", true, "rt-123")
	defer gs.Close()
	store := FileTokenStore{Path: filepath.Join(t.TempDir(), "sub", "owner_token.json")}
	g := newGoogle(t, gs, store)

	// an editor cannot start it
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/auth/owner", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: g.Signer.Encode("ed@x.com", time.Hour)})
	g.OwnerStart(rec, req)
	if rec.Code != 403 {
		t.Fatalf("editor: %d", rec.Code)
	}

	ownerCookie := &http.Cookie{Name: CookieName, Value: g.Signer.Encode("owner@x.com", time.Hour)}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/owner", nil)
	req.AddCookie(ownerCookie)
	g.OwnerStart(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != 302 || loc.Query().Get("access_type") != "offline" || loc.Query().Get("prompt") != "consent" {
		t.Fatalf("owner start: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	var stateCk *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == ownerStateName {
			stateCk = c
		}
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/auth/owner/callback?code=c&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	req.AddCookie(ownerCookie)
	req.AddCookie(stateCk)
	g.OwnerCallback(rec, req)
	if rec.Code != 302 {
		t.Fatalf("owner callback: %d %s", rec.Code, rec.Body.String())
	}
	tok, err := store.Load()
	if err != nil || tok == nil || tok.RefreshToken != "rt-123" {
		t.Fatalf("stored token: %+v %v", tok, err)
	}
}

func TestOwnerConnectRejectsDifferentGoogleAccount(t *testing.T) {
	gs := fakeGoogle(t, "someone-else@x.com", true, "rt")
	defer gs.Close()
	store := FileTokenStore{Path: filepath.Join(t.TempDir(), "t.json")}
	g := newGoogle(t, gs, store)
	ownerCookie := &http.Cookie{Name: CookieName, Value: g.Signer.Encode("owner@x.com", time.Hour)}
	st := &http.Cookie{Name: ownerStateName, Value: "s1"}
	req := httptest.NewRequest(http.MethodGet, "/auth/owner/callback?code=c&state=s1", nil)
	req.AddCookie(ownerCookie)
	req.AddCookie(st)
	rec := httptest.NewRecorder()
	g.OwnerCallback(rec, req)
	if rec.Code != 403 {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if tok, _ := store.Load(); tok != nil {
		t.Error("token must not be stored for a different account")
	}
}

func TestFileTokenStorePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "t.json")
	s := FileTokenStore{Path: p}
	if tok, err := s.Load(); tok != nil || err != nil {
		t.Fatalf("empty store: %v %v", tok, err)
	}
	if err := s.Save(&oauth2.Token{RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	fi, err := osStat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v %v", fi, err)
	}
}

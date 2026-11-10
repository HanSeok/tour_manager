package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"golang.org/x/oauth2"

	"tourmanager/internal/auth"
	"tourmanager/internal/drive"
	"tourmanager/internal/sheets"
	"tourmanager/internal/trips"
)

func tripsModeConfig(t *testing.T) Config {
	return Config{BaseURL: "http://localhost:8080", OwnerEmail: "o@x.com", DataDir: t.TempDir(),
		SessionSecret: []byte(strings.Repeat("s", 32)), ClientID: "id", ClientSecret: "s",
		Static: fstest.MapFS{"index.html": {Data: []byte("x")}}}
}

func TestNoSheetIdIsFineWhenTripsAreCreatedFromTheDashboard(t *testing.T) {
	if _, err := New(tripsModeConfig(t)); err != nil {
		t.Fatalf("trips mode (no TRIP_SHEET_ID) rejected: %v", err)
	}
	c := tripsModeConfig(t)
	c.ClientID, c.ClientSecret, c.DevLogin = "", "", true
	if _, err := New(c); err == nil {
		t.Error("without Google and without fixtures there is nothing to run on")
	}
}

func ownerRedirect(t *testing.T, cfg Config) string {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	signerCookie(t, rec, "o@x.com")
	resp := do(h, "GET", "/auth/owner", "", rec.Result().Cookies())
	if resp.Code != http.StatusFound {
		t.Fatalf("/auth/owner: %d %s", resp.Code, resp.Body.String())
	}
	u, err := url.Parse(resp.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("scope")
}

func TestOwnerConsentAsksForDriveFileOnlyWhenTheServerCreatesTheTrips(t *testing.T) {
	scope := ownerRedirect(t, tripsModeConfig(t))
	if !strings.Contains(scope, "https://www.googleapis.com/auth/drive.file") {
		t.Errorf("trips mode needs drive.file: %q", scope)
	}
	if strings.Contains(scope, "auth/spreadsheets") || strings.Contains(scope, "auth/drive ") || strings.HasSuffix(scope, "auth/drive") {
		t.Errorf("must not ask for the broad sheet/drive scopes: %q", scope)
	}

	legacy := tripsModeConfig(t)
	legacy.SpreadsheetID = "sid"
	scope = ownerRedirect(t, legacy)
	if !strings.Contains(scope, "https://www.googleapis.com/auth/spreadsheets") || strings.Contains(scope, "drive") {
		t.Errorf("a given TRIP_SHEET_ID keeps the spreadsheets scope only: %q", scope)
	}
}

type recordingTransport struct {
	mu   sync.Mutex
	urls []string
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.Path)
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: http.NoBody, Request: req}, nil
}

func TestOwnerGridFollowsTheCurrentTripInTheRegistry(t *testing.T) {
	dir := t.TempDir()
	tokens := auth.FileTokenStore{Path: filepath.Join(dir, "owner_token.json")}
	reg := &trips.Registry{Path: filepath.Join(dir, "trips.json")}
	rec := &recordingTransport{}
	og := &ownerGrid{tokens: tokens, httpOverride: &http.Client{Transport: rec}, sheetID: registryCurrent(reg)}
	ctx := context.Background()

	if _, err := og.Rows(ctx, "설정"); !errors.Is(err, sheets.ErrNotConnected) {
		t.Fatalf("not connected comes first: %v", err)
	}
	if err := tokens.Save(&oauth2.Token{RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := og.Rows(ctx, "설정"); !errors.Is(err, sheets.ErrNoTrip) {
		t.Fatalf("connected but no trip: %v", err)
	}
	if len(rec.urls) != 0 {
		t.Errorf("no request without a trip: %v", rec.urls)
	}

	for _, id := range []string{"sheetA", "sheetB"} {
		if err := reg.Add(drive.Trip{Title: id, SheetID: id}); err != nil {
			t.Fatal(err)
		}
		_, _ = og.Rows(ctx, "설정")
		last := rec.urls[len(rec.urls)-1]
		if !strings.HasPrefix(last, "/v4/spreadsheets/"+id+"/") {
			t.Errorf("after switching to %s the grid still reads %s", id, last)
		}
	}
}

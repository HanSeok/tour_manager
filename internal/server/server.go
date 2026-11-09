// Package server wires the pieces into one http.Handler: Google sign-in, the member check, the JSON API and the
// static dashboard.
package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"tourmanager/internal/api"
	"tourmanager/internal/audit"
	"tourmanager/internal/auth"
	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
)

const (
	membersTTL = time.Minute      // a removed member loses access within about this long
	configTTL  = 15 * time.Second // an edit of the 설정 tab shows up within about this long
	// Sheets is the only Google scope the owner grants for now; calendar/drive are added when those features land.
	scopeSheets = "https://www.googleapis.com/auth/spreadsheets"
)

type Config struct {
	BaseURL       string // public URL, e.g. https://trip.example.com (cookies are Secure when https)
	OwnerEmail    string
	SpreadsheetID string
	DataDir       string // holds the owner token for now
	SessionSecret []byte // >= 32 bytes
	ClientID      string
	ClientSecret  string
	DevLogin      bool   // sign in without Google (localhost only)
	FixtureDir    string // read settings/members from JSON files instead of Google (development)
	Version       string
	Static        fs.FS // the dashboard files
}

func (c Config) validate() error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("BASE_URL 이 올바르지 않습니다 (예: https://trip.example.com)")
	}
	if !strings.Contains(c.OwnerEmail, "@") {
		return errors.New("OWNER_EMAIL 이 필요합니다")
	}
	if len(c.SessionSecret) < 32 {
		return errors.New("SESSION_SECRET 은 32자 이상이어야 합니다")
	}
	if c.DevLogin {
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" {
			return errors.New("DEV_LOGIN 은 localhost 에서만 켤 수 있습니다")
		}
	} else if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET 이 필요합니다 (개발 중이면 DEV_LOGIN=1)")
	}
	if c.FixtureDir == "" && c.SpreadsheetID == "" {
		return errors.New("TRIP_SHEET_ID 가 필요합니다 (개발 중이면 FIXTURE_DIR)")
	}
	if c.Static == nil {
		return errors.New("정적 파일이 없습니다")
	}
	return nil
}

// ownerGrid reads and writes the spreadsheet with the owner's stored refresh token. Until the owner has connected
// Google every call fails with a message that says what to do.
type ownerGrid struct {
	cfg           *oauth2.Config
	tokens        auth.TokenStore
	spreadsheetID string

	mu      sync.Mutex
	refresh string
	client  *sheets.API
}

func (o *ownerGrid) api() (*sheets.API, error) {
	tok, err := o.tokens.Load()
	if err != nil {
		return nil, err
	}
	if tok == nil || tok.RefreshToken == "" {
		return nil, sheets.ErrNotConnected
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.client == nil || o.refresh != tok.RefreshToken {
		// the client outlives the request that created it, so it must not use that request's context
		src := o.cfg.TokenSource(context.Background(), &oauth2.Token{RefreshToken: tok.RefreshToken})
		o.client = &sheets.API{HTTP: oauth2.NewClient(context.Background(), oauth2.ReuseTokenSource(nil, src)), SpreadsheetID: o.spreadsheetID}
		o.refresh = tok.RefreshToken
	}
	return o.client, nil
}

// status reports whether the owner has connected Google (for the diagnose screen).
func (o *ownerGrid) status(context.Context) (bool, string) {
	tok, err := o.tokens.Load()
	switch {
	case err != nil:
		return false, "저장된 연결 정보를 읽지 못했습니다"
	case tok == nil || tok.RefreshToken == "":
		return false, "아직 연결되지 않았습니다. 소유자로 로그인한 뒤 /auth/owner 를 여세요"
	}
	return true, "연결됨"
}

func (o *ownerGrid) Rows(ctx context.Context, tab string) ([][]string, error) {
	a, err := o.api()
	if err != nil {
		return nil, err
	}
	return a.Rows(ctx, tab)
}

func (o *ownerGrid) RawRows(ctx context.Context, tab string) ([][]string, error) {
	a, err := o.api()
	if err != nil {
		return nil, err
	}
	return a.RawRows(ctx, tab)
}

func (o *ownerGrid) Append(ctx context.Context, tab string, row []any) error {
	a, err := o.api()
	if err != nil {
		return err
	}
	return a.Append(ctx, tab, row)
}

func (o *ownerGrid) UpdateRow(ctx context.Context, tab string, n int, row []any) error {
	a, err := o.api()
	if err != nil {
		return err
	}
	return a.UpdateRow(ctx, tab, n, row)
}

func (o *ownerGrid) DeleteRow(ctx context.Context, tab string, n int) error {
	a, err := o.api()
	if err != nil {
		return err
	}
	return a.DeleteRow(ctx, tab, n)
}

func (o *ownerGrid) EnsureTab(ctx context.Context, tab string, header []string) (bool, error) {
	a, err := o.api()
	if err != nil {
		return false, err
	}
	return a.EnsureTab(ctx, tab, header)
}

func New(cfg Config) (http.Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	secure := strings.HasPrefix(cfg.BaseURL, "https://")
	tokens := auth.FileTokenStore{Path: filepath.Join(cfg.DataDir, "owner_token.json")}

	var grid sheets.Grid
	conn := func(context.Context) (bool, string) {
		return true, "개발용 샘플 시트 (메모리, 재시작하면 초기화)"
	}
	var ownerCfg *oauth2.Config
	if cfg.ClientID != "" {
		ownerCfg = auth.NewConfig(cfg.ClientID, cfg.ClientSecret, cfg.BaseURL, "/auth/owner/callback", []string{"openid", "email", scopeSheets})
	}
	if cfg.FixtureDir != "" { // development: the fixtures become an in-memory spreadsheet (changes are lost on restart)
		mem, err := sheets.LoadFixtures(cfg.FixtureDir)
		if err != nil {
			return nil, err
		}
		grid = mem
	} else {
		og := &ownerGrid{cfg: ownerCfg, tokens: tokens, spreadsheetID: cfg.SpreadsheetID}
		grid, conn = og, og.status
	}
	src := sheets.Source(grid)

	dir := members.NewDirectory(cfg.OwnerEmail, func(ctx context.Context) ([][]string, error) {
		rows, err := src.Rows(ctx, members.SheetName)
		if errors.Is(err, sheets.ErrNoSheet) {
			return nil, nil // no 멤버 tab yet: only the owner can sign in
		}
		return rows, err
	}, membersTTL)

	signer := auth.Signer{Secret: cfg.SessionSecret}
	g := &auth.Google{Signer: signer, Dir: dir, Tokens: tokens, Secure: secure, OwnerClient: ownerCfg}
	if cfg.ClientID != "" {
		g.Client = auth.NewConfig(cfg.ClientID, cfg.ClientSecret, cfg.BaseURL, "/auth/callback", []string{"openid", "email", "profile"})
	}

	who := func(r *http.Request) (api.Principal, bool) {
		email, ok := signer.EmailFromRequest(r)
		if !ok {
			return api.Principal{}, false
		}
		m, ok := dir.Lookup(r.Context(), email) // re-checked on every request: removal takes effect without a new login
		if !ok {
			return api.Principal{}, false
		}
		return api.Principal{Email: m.Email, Name: m.Name, Role: m.Role}, true
	}

	rt := api.NewRouter(who, cfg.BaseURL)
	(&api.Trip{
		Grid: grid, Members: dir, Version: cfg.Version, CfgTTL: configTTL, Sample: cfg.FixtureDir != "", Conn: conn,
		Expenses: &expenses.Service{Grid: grid, Log: &audit.GridLog{Grid: grid}},
	}).Register(rt)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	if g.Client != nil {
		mux.HandleFunc("/auth/login", g.Login)
		mux.HandleFunc("/auth/callback", g.Callback)
		mux.HandleFunc("/auth/owner", g.OwnerStart)
		mux.HandleFunc("/auth/owner/callback", g.OwnerCallback)
	}
	mux.HandleFunc("/auth/logout", g.Logout)
	if cfg.DevLogin {
		mux.HandleFunc("/auth/dev", g.DevLogin)
	}
	mux.Handle("/api/", rt)
	// Tells the page it is served by this server (the static copy of this file says false).
	mux.HandleFunc("/servermode.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("window.TM_API = true;\n"))
	})

	files := http.FileServer(http.FS(cfg.Static))
	loginPath := "/auth/login"
	if g.Client == nil {
		loginPath = "/auth/dev"
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := who(r); !ok {
			if g.Client == nil {
				http.Error(w, "로그인이 필요합니다: "+loginPath+"?email=<멤버 이메일>", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, loginPath, http.StatusFound)
			return
		}
		files.ServeHTTP(w, r)
	})
	return secureHeaders(mux), nil
}

// StaticFS picks the dashboard files: a directory on disk (live editing) or the embedded copy.
func StaticFS(dir string, embedded fs.FS) fs.FS {
	if dir != "" {
		return os.DirFS(dir)
	}
	return embedded
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// Not "same-origin": the map tiles come from another site (OpenStreetMap), whose usage policy requires a valid
		// Referer and answers 403 "Access blocked" without one. This sends only our origin to other sites.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

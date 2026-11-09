package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"tourmanager/internal/members"
)

const (
	stateCookie    = "tm_oauth_state"
	userinfoURL    = "https://openidconnect.googleapis.com/v1/userinfo"
	SessionTTL     = 14 * 24 * time.Hour
	ownerStateName = "tm_owner_state"
)

// Google handles "Sign in with Google" for everyone (identity only: openid, email, profile) and the owner's
// one-time consent that lets the server read and write the trip's Google data.
type Google struct {
	Client      *oauth2.Config // sign-in: redirect → /auth/callback
	OwnerClient *oauth2.Config // owner consent: redirect → /auth/owner/callback (nil = disabled)
	UserinfoURL string         // override in tests
	HTTP        *http.Client   // override in tests
	Signer      Signer
	Dir         *members.Directory
	Tokens      TokenStore
	Secure      bool // Secure cookies (https)
}

func NewConfig(clientID, secret, baseURL, path string, scopes []string) *oauth2.Config {
	return &oauth2.Config{ClientID: clientID, ClientSecret: secret, Endpoint: google.Endpoint,
		RedirectURL: strings.TrimRight(baseURL, "/") + path, Scopes: scopes}
}

func (g *Google) ctx(r *http.Request) context.Context {
	if g.HTTP != nil {
		return context.WithValue(r.Context(), oauth2.HTTPClient, g.HTTP)
	}
	return r.Context()
}

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (g *Google) setState(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/auth", MaxAge: 600, HttpOnly: true, Secure: g.Secure, SameSite: http.SameSiteLaxMode})
}

// state check: the cookie set at login must match the value Google sends back (CSRF protection).
func (g *Google) takeState(w http.ResponseWriter, r *http.Request, name string) bool {
	c, err := r.Cookie(name)
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/auth", MaxAge: -1})
	return err == nil && c.Value != "" && c.Value == r.URL.Query().Get("state")
}

func (g *Google) Login(w http.ResponseWriter, r *http.Request) {
	st := randomState()
	g.setState(w, stateCookie, st)
	http.Redirect(w, r, g.Client.AuthCodeURL(st, oauth2.SetAuthURLParam("prompt", "select_account")), http.StatusFound)
}

type userinfo struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func (g *Google) fetchUser(ctx context.Context, cfg *oauth2.Config, code string) (userinfo, *oauth2.Token, error) {
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return userinfo{}, nil, fmt.Errorf("exchange: %w", err)
	}
	u := g.UserinfoURL
	if u == "" {
		u = userinfoURL
	}
	resp, err := cfg.Client(ctx, tok).Get(u)
	if err != nil {
		return userinfo{}, nil, err
	}
	defer resp.Body.Close()
	var ui userinfo
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&ui) != nil {
		return userinfo{}, nil, fmt.Errorf("userinfo: status %d", resp.StatusCode)
	}
	return ui, tok, nil
}

func (g *Google) Callback(w http.ResponseWriter, r *http.Request) {
	if !g.takeState(w, r, stateCookie) {
		http.Error(w, "로그인 요청이 올바르지 않습니다. 다시 시도하세요.", http.StatusBadRequest)
		return
	}
	ui, _, err := g.fetchUser(g.ctx(r), g.Client, r.URL.Query().Get("code"))
	if err != nil || !ui.EmailVerified || ui.Email == "" {
		http.Error(w, "Google 로그인에 실패했습니다.", http.StatusUnauthorized)
		return
	}
	if _, ok := g.Dir.Lookup(r.Context(), ui.Email); !ok {
		http.Error(w, "이 여행의 멤버가 아닙니다. 소유자에게 멤버 등록을 요청하세요: "+ui.Email, http.StatusForbidden)
		return
	}
	g.Signer.SetCookie(w, ui.Email, SessionTTL, g.Secure)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (g *Google) Logout(w http.ResponseWriter, r *http.Request) {
	ClearCookie(w, g.Secure)
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// OwnerStart begins the owner's consent (offline access → refresh token). Only a signed-in owner may start it.
func (g *Google) OwnerStart(w http.ResponseWriter, r *http.Request) {
	if g.OwnerClient == nil {
		http.Error(w, "소유자 연결이 설정되지 않았습니다.", http.StatusNotFound)
		return
	}
	if !g.requireOwner(w, r) {
		return
	}
	st := randomState()
	g.setState(w, ownerStateName, st)
	http.Redirect(w, r, g.OwnerClient.AuthCodeURL(st, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent")), http.StatusFound)
}

func (g *Google) OwnerCallback(w http.ResponseWriter, r *http.Request) {
	if g.OwnerClient == nil || !g.requireOwner(w, r) {
		return
	}
	if !g.takeState(w, r, ownerStateName) {
		http.Error(w, "요청이 올바르지 않습니다. 다시 시도하세요.", http.StatusBadRequest)
		return
	}
	ui, tok, err := g.fetchUser(g.ctx(r), g.OwnerClient, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "Google 연결에 실패했습니다.", http.StatusBadGateway)
		return
	}
	email, _ := g.Signer.EmailFromRequest(r)
	if !strings.EqualFold(ui.Email, email) {
		http.Error(w, "로그인한 계정과 다른 계정으로 연결할 수 없습니다.", http.StatusForbidden)
		return
	}
	if tok.RefreshToken == "" {
		http.Error(w, "refresh token 을 받지 못했습니다. 계정의 앱 접근 권한을 해제한 뒤 다시 시도하세요.", http.StatusBadGateway)
		return
	}
	if err := g.Tokens.Save(&oauth2.Token{RefreshToken: tok.RefreshToken}); err != nil {
		http.Error(w, "토큰을 저장하지 못했습니다.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?owner=connected", http.StatusFound)
}

func (g *Google) requireOwner(w http.ResponseWriter, r *http.Request) bool {
	email, ok := g.Signer.EmailFromRequest(r)
	if ok {
		if m, found := g.Dir.Lookup(r.Context(), email); found && m.Role.CanManage() {
			return true
		}
	}
	http.Error(w, "소유자만 사용할 수 있습니다.", http.StatusForbidden)
	return false
}

// DevLogin signs in any e-mail that is a member, without Google. Only mounted when DEV_LOGIN=1 on localhost.
func (g *Google) DevLogin(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	if _, ok := g.Dir.Lookup(r.Context(), email); !ok {
		http.Error(w, "멤버가 아닙니다: "+url.QueryEscape(email), http.StatusForbidden)
		return
	}
	g.Signer.SetCookie(w, email, SessionTTL, false)
	http.Redirect(w, r, "/", http.StatusFound)
}

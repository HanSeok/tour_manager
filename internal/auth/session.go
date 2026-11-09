// Package auth: signed session cookies and Google sign-in.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const CookieName = "tm_session"

type payload struct {
	Email string `json:"e"`
	Exp   int64  `json:"x"`
}

// Signer issues and verifies stateless session values: base64url(json).base64url(hmac-sha256).
// Authorization is re-checked against the member list on every request, so removing a member ends their access
// even while their cookie is still valid.
type Signer struct {
	Secret []byte
	Now    func() time.Time
}

func (s Signer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Signer) mac(msg string) string {
	h := hmac.New(sha256.New, s.Secret)
	h.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s Signer) Encode(email string, ttl time.Duration) string {
	b, _ := json.Marshal(payload{Email: email, Exp: s.now().Add(ttl).Unix()})
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + s.mac(body)
}

func (s Signer) Decode(v string) (string, bool) {
	body, sig, ok := strings.Cut(v, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(body))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	var p payload
	if json.Unmarshal(raw, &p) != nil || p.Email == "" || s.now().Unix() >= p.Exp {
		return "", false
	}
	return p.Email, true
}

// EmailFromRequest returns the signed-in e-mail, if any.
func (s Signer) EmailFromRequest(r *http.Request) (string, bool) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	return s.Decode(c.Value)
}

func (s Signer) SetCookie(w http.ResponseWriter, email string, ttl time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: s.Encode(email, ttl), Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

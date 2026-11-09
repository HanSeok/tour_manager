// Package api is the JSON-over-HTTP surface the dashboard calls: POST /api/<function> {"args":[...]}.
// Function names follow the old Apps Script server so the front end only changes its transport.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"tourmanager/internal/members"
)

// Principal is the signed-in member making the call.
type Principal struct {
	Email string       `json:"email"`
	Name  string       `json:"name"`
	Role  members.Role `json:"role"`
}

type Permission int

const (
	PermRead Permission = iota
	PermWrite
	PermManage
)

func (p Principal) Can(need Permission) bool {
	switch need {
	case PermRead:
		return p.Role == members.Owner || p.Role == members.Editor || p.Role == members.Viewer
	case PermWrite:
		return p.Role.CanWrite()
	case PermManage:
		return p.Role.CanManage()
	}
	return false
}

// Error carries the HTTP status and the message shown to the user.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func Errf(status int, msg string) *Error { return &Error{Status: status, Msg: msg} }

type Handler func(ctx context.Context, who Principal, args []json.RawMessage) (any, error)

type endpoint struct {
	need Permission
	run  Handler
}

// Authenticate resolves the caller from the request (nil error + ok=false → not signed in or no longer a member).
type Authenticate func(r *http.Request) (Principal, bool)

type Router struct {
	auth      Authenticate
	origin    string // expected Origin host (e.g. "localhost:8080"); empty skips the check
	endpoints map[string]endpoint
}

func NewRouter(auth Authenticate, baseURL string) *Router {
	host := ""
	if u, err := url.Parse(baseURL); err == nil {
		host = u.Host
	}
	return &Router{auth: auth, origin: host, endpoints: map[string]endpoint{}}
}

func (rt *Router) Register(name string, need Permission, h Handler) {
	rt.endpoints[name] = endpoint{need, h}
}

type envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func write(w http.ResponseWriter, status int, e envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/")
	ep, ok := rt.endpoints[name]
	if !ok {
		write(w, http.StatusNotFound, envelope{Error: "알 수 없는 기능입니다: " + name})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		write(w, http.StatusMethodNotAllowed, envelope{Error: "POST 만 지원합니다"})
		return
	}
	// A cross-site form cannot send JSON, and a cross-site fetch carries a foreign Origin: both are refused.
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		write(w, http.StatusUnsupportedMediaType, envelope{Error: "Content-Type 은 application/json 이어야 합니다"})
		return
	}
	if o := r.Header.Get("Origin"); o != "" && rt.origin != "" {
		if u, err := url.Parse(o); err != nil || u.Host != rt.origin {
			write(w, http.StatusForbidden, envelope{Error: "허용되지 않은 출처입니다"})
			return
		}
	}
	who, ok := rt.auth(r)
	if !ok {
		write(w, http.StatusUnauthorized, envelope{Error: "로그인이 필요합니다"})
		return
	}
	if !who.Can(ep.need) {
		write(w, http.StatusForbidden, envelope{Error: "권한이 없습니다"})
		return
	}
	var req struct {
		Args []json.RawMessage `json:"args"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		write(w, http.StatusUnprocessableEntity, envelope{Error: "요청 형식이 올바르지 않습니다"})
		return
	}
	data, err := ep.run(r.Context(), who, req.Args)
	if err != nil {
		var ae *Error
		if errors.As(err, &ae) {
			write(w, ae.Status, envelope{Error: ae.Msg})
			return
		}
		write(w, http.StatusInternalServerError, envelope{Error: "서버 오류가 발생했습니다"})
		return
	}
	write(w, http.StatusOK, envelope{OK: true, Data: data})
}

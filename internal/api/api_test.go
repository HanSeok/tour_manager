package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
)

func newRouter(role members.Role, signedIn bool) *Router {
	rt := NewRouter(func(r *http.Request) (Principal, bool) {
		if !signedIn {
			return Principal{}, false
		}
		return Principal{Email: "a@x.com", Name: "a", Role: role}, true
	}, "http://localhost:8080")
	rt.Register("read", PermRead, func(context.Context, Principal, []json.RawMessage) (any, error) { return "r", nil })
	rt.Register("write", PermWrite, func(_ context.Context, _ Principal, args []json.RawMessage) (any, error) { return len(args), nil })
	rt.Register("manage", PermManage, func(context.Context, Principal, []json.RawMessage) (any, error) { return "m", nil })
	rt.Register("fail", PermRead, func(context.Context, Principal, []json.RawMessage) (any, error) { return nil, Errf(409, "충돌") })
	rt.Register("boom", PermRead, func(context.Context, Principal, []json.RawMessage) (any, error) { return nil, context.DeadlineExceeded })
	return rt
}

func call(rt *Router, fn, body string, hdr map[string]string) (int, map[string]any) {
	req := httptest.NewRequest(http.MethodPost, "/api/"+fn, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestPermissionMatrix(t *testing.T) {
	cases := []struct {
		role members.Role
		fn   string
		want int
	}{
		{members.Viewer, "read", 200}, {members.Viewer, "write", 403}, {members.Viewer, "manage", 403},
		{members.Editor, "read", 200}, {members.Editor, "write", 200}, {members.Editor, "manage", 403},
		{members.Owner, "read", 200}, {members.Owner, "write", 200}, {members.Owner, "manage", 200},
	}
	for _, c := range cases {
		if got, _ := call(newRouter(c.role, true), c.fn, `{"args":[]}`, nil); got != c.want {
			t.Errorf("%s %s: got %d want %d", c.role, c.fn, got, c.want)
		}
	}
}

func TestUnauthenticatedAndUnknown(t *testing.T) {
	if code, _ := call(newRouter(members.Owner, false), "read", `{}`, nil); code != 401 {
		t.Errorf("want 401, got %d", code)
	}
	if code, _ := call(newRouter(members.Owner, true), "nope", `{}`, nil); code != 404 {
		t.Errorf("want 404, got %d", code)
	}
}

func TestCSRFGuards(t *testing.T) {
	rt := newRouter(members.Owner, true)
	if code, _ := call(rt, "read", `{}`, map[string]string{"Content-Type": "text/plain"}); code != 415 {
		t.Errorf("non-json: %d", code)
	}
	if code, _ := call(rt, "read", `{}`, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("foreign origin: %d", code)
	}
	if code, _ := call(rt, "read", `{}`, map[string]string{"Origin": "http://localhost:8080"}); code != 200 {
		t.Errorf("own origin: %d", code)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/read", nil)
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, req)
	if rec.Code != 405 {
		t.Errorf("GET: %d", rec.Code)
	}
}

func TestErrorMapping(t *testing.T) {
	rt := newRouter(members.Owner, true)
	code, out := call(rt, "fail", `{}`, nil)
	if code != 409 || out["error"] != "충돌" {
		t.Errorf("typed error: %d %v", code, out)
	}
	code, out = call(rt, "boom", `{}`, nil)
	if code != 500 || strings.Contains(out["error"].(string), "deadline") {
		t.Errorf("internal errors must not leak details: %d %v", code, out)
	}
	if code, _ := call(rt, "read", `not json`, nil); code != 422 {
		t.Errorf("bad body: %d", code)
	}
	code, out = call(rt, "write", `{"args":[1,"a"]}`, nil)
	if code != 200 || out["data"] != float64(2) {
		t.Errorf("args: %d %v", code, out)
	}
}

type staticSource map[string][][]string

func (s staticSource) Rows(_ context.Context, tab string) ([][]string, error) {
	if r, ok := s[tab]; ok {
		return r, nil
	}
	return nil, sheets.ErrNoSheet
}

func TestBootstrapShapeAndNotConfigured(t *testing.T) {
	dir := members.NewDirectory("o@x.com", func(context.Context) ([][]string, error) { return nil, nil }, time.Minute)
	tr := &Trip{Source: staticSource{}, Members: dir, Version: "test"}
	rt := NewRouter(func(*http.Request) (Principal, bool) {
		return Principal{Email: "o@x.com", Name: "o", Role: members.Owner}, true
	}, "")
	tr.Register(rt)
	code, out := call(rt, "bootstrap", `{"args":[]}`, nil)
	if code != 200 {
		t.Fatalf("code %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	cfg := data["config"].(map[string]any)
	if missing := cfg["missing"].([]any); len(missing) != 1 || missing[0] != "설정 탭" {
		t.Errorf("a missing 설정 tab is 'not configured', got %v", cfg["missing"])
	}
	if data["me"].(map[string]any)["role"] != "owner" || data["version"] != "test" {
		t.Errorf("me/version: %v", data)
	}
	for _, k := range []string{"events", "expenses", "visits", "photos"} {
		if _, ok := data[k].([]any); !ok {
			t.Errorf("%s must be an array", k)
		}
	}
	if _, leaked := cfg["calendarId"]; leaked {
		t.Error("calendarId must not reach the client")
	}
}

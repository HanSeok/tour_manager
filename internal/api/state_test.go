package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/places"
	"tourmanager/internal/sheets"
	"tourmanager/internal/state"
)

func stateRouter(role members.Role, g sheets.Grid, withState bool) *Router {
	dir := members.NewDirectory("o@x.com", func(context.Context) ([][]string, error) { return nil, nil }, time.Minute)
	tr := &Trip{Grid: g, Members: dir, Expenses: &expenses.Service{Grid: g}, Places: &places.Service{Grid: g}}
	if withState {
		tr.State = &state.Service{Grid: g}
	}
	rt := NewRouter(func(*http.Request) (Principal, bool) { return Principal{Email: "w@x.com", Name: "w", Role: role}, true }, "")
	tr.Register(rt)
	return rt
}

const day = "2027-04-10" // inside configuredGrid's trip (04-10 … 04-12)

func TestSavedStateComesBackInBootstrapAndRefresh(t *testing.T) {
	rt := stateRouter(members.Editor, configuredGrid(), true)
	code, out := call(rt, "saveState", `{"args":["starts",{"`+day+`":540}]}`, nil)
	if code != 200 || out["data"].(map[string]any)[day] != float64(540) {
		t.Fatalf("%d %v", code, out)
	}
	for _, fn := range []string{"bootstrap", "refresh"} {
		_, out = call(rt, fn, `{"args":[]}`, nil)
		st, _ := out["data"].(map[string]any)["state"].(map[string]any)
		starts, _ := st["starts"].(map[string]any)
		if starts[day] != float64(540) {
			t.Errorf("%s: state = %v", fn, st)
		}
	}
}

func TestStateErrorsMapToStatuses(t *testing.T) {
	g := configuredGrid()
	rt := stateRouter(members.Editor, g, true)
	if code, out := call(rt, "saveState", `{"args":["starts",{"`+day+`":9999}]}`, nil); code != 422 {
		t.Errorf("bad value: %d %v", code, out)
	}
	if code, out := call(rt, "saveState", `{"args":["starts",{"2030-01-01":540}]}`, nil); code != 422 || !strings.Contains(out["error"].(string), "여행 기간") {
		t.Errorf("outside the trip: %d %v", code, out)
	}
	if code, _ := call(rt, "saveState", `{"args":["nope",{}]}`, nil); code != 422 {
		t.Errorf("unknown key: %d", code)
	}
	if code, _ := call(rt, "saveState", `{"args":["starts"]}`, nil); code != 422 {
		t.Errorf("no patch: %d", code)
	}
	if code, _ := call(stateRouter(members.Viewer, g, true), "saveState", `{"args":["starts",{"`+day+`":540}]}`, nil); code != 403 {
		t.Errorf("viewer: %d", code)
	}
	if code, _ := call(stateRouter(members.Editor, g, false), "saveState", `{"args":["starts",{"`+day+`":540}]}`, nil); code != 412 {
		t.Errorf("not available: %d", code)
	}
	if code, _ := call(stateRouter(members.Editor, sheets.NewMemGrid(nil), true), "saveState", `{"args":["starts",{"`+day+`":540}]}`, nil); code != 412 {
		t.Errorf("unconfigured trip: %d", code)
	}
}

func TestViewersSeeTheSharedStateButCannotChangeIt(t *testing.T) {
	g := configuredGrid()
	_, _ = call(stateRouter(members.Editor, g, true), "saveState", `{"args":["starts",{"`+day+`":540}]}`, nil)
	_, out := call(stateRouter(members.Viewer, g, true), "bootstrap", `{"args":[]}`, nil)
	st, _ := out["data"].(map[string]any)["state"].(map[string]any)
	if starts, _ := st["starts"].(map[string]any); starts[day] != float64(540) {
		t.Errorf("a viewer follows the plan: %v", st)
	}
}

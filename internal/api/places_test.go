package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"errors"
	"tourmanager/internal/expenses"
	"tourmanager/internal/maplink"
	"tourmanager/internal/members"
	"tourmanager/internal/places"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

func configuredGrid() *sheets.MemGrid {
	rows := tripconfig.RowsFor(map[string]string{"title": "t", "start": "2027-04-10", "end": "2027-04-12", "timezone": "Asia/Tokyo",
		"destKeywords": "x", "myMapsUrl": "https://www.google.com/maps/d/edit?mid=S", "localCurrency": "엔", "localSymbol": "¥", "rate": "9"})
	return sheets.NewMemGrid(map[string][][]string{tripconfig.SheetName: rows})
}

type fakeLinks struct {
	res maplink.Result
	err error
	got string
}

func (f *fakeLinks) Resolve(_ context.Context, text string) (maplink.Result, error) {
	f.got = text
	return f.res, f.err
}

func placesRouter(role members.Role, g sheets.Grid) *Router { return placesRouterWith(role, g, nil) }

func placesRouterWith(role members.Role, g sheets.Grid, links PlaceLinks) *Router {
	dir := members.NewDirectory("o@x.com", func(context.Context) ([][]string, error) { return nil, nil }, time.Minute)
	tr := &Trip{Grid: g, Members: dir, Expenses: &expenses.Service{Grid: g}, Places: &places.Service{Grid: g}, Links: links}
	rt := NewRouter(func(*http.Request) (Principal, bool) { return Principal{Email: "w@x.com", Name: "w", Role: role}, true }, "")
	tr.Register(rt)
	return rt
}

const pid = "33333333-3333-4333-8333-333333333333"

func placeBody(name string) string {
	return `{"args":[{"id":"` + pid + `","name":"` + name + `","layer":"관광","lat":26.2,"lng":127.7,"memo":""}]}`
}

func placesOf(out map[string]any) []any {
	l, _ := out["data"].([]any)
	return l
}

func TestAddPlaceReturnsTheListAndBootstrapAndRefreshCarryIt(t *testing.T) {
	rt := placesRouter(members.Editor, configuredGrid())
	code, out := call(rt, "addPlace", placeBody("슈리성"), nil)
	list := placesOf(out)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["name"] != "슈리성" || list[0].(map[string]any)["createdBy"] != "w@x.com" {
		t.Fatalf("%d %v", code, out)
	}
	for _, fn := range []string{"bootstrap", "refresh"} {
		_, out = call(rt, fn, `{"args":[]}`, nil)
		tp, _ := out["data"].(map[string]any)["tripPlaces"].([]any)
		if len(tp) != 1 || tp[0].(map[string]any)["id"] != pid {
			t.Errorf("%s: tripPlaces = %v", fn, out["data"].(map[string]any)["tripPlaces"])
		}
	}
}

func TestViewersCannotChangePlacesButSeeThem(t *testing.T) {
	g := configuredGrid()
	_, _ = call(placesRouter(members.Editor, g), "addPlace", placeBody("슈리성"), nil)
	v := placesRouter(members.Viewer, g)
	for _, fn := range []string{"addPlace", "updatePlace", "deletePlace"} {
		if code, _ := call(v, fn, placeBody("x"), nil); code != 403 {
			t.Errorf("%s as viewer: %d", fn, code)
		}
	}
	_, out := call(v, "bootstrap", `{"args":[]}`, nil)
	if tp, _ := out["data"].(map[string]any)["tripPlaces"].([]any); len(tp) != 1 {
		t.Errorf("a viewer still sees the places: %v", tp)
	}
}

func TestPlaceErrorsMapToStatuses(t *testing.T) {
	g := configuredGrid()
	rt := placesRouter(members.Editor, g)
	if code, out := call(rt, "addPlace", `{"args":[{"name":"","layer":"관광","lat":1,"lng":1}]}`, nil); code != 422 || !strings.Contains(out["error"].(string), "이름") {
		t.Errorf("validation: %d %v", code, out)
	}
	if code, _ := call(rt, "deletePlace", `{"args":["`+pid+`"]}`, nil); code != 404 {
		t.Errorf("missing: %d", code)
	}
	_, out := call(rt, "addPlace", placeBody("슈리성"), nil)
	seen := out["data"].([]any)[0].(map[string]any)["updatedAt"].(string)
	body, _ := json.Marshal(map[string]any{"args": []any{pid, map[string]any{"id": pid, "name": "먼저 고침", "layer": "관광", "lat": 26.2, "lng": 127.7}, nil}})
	if code, _ := call(rt, "updatePlace", string(body), nil); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if code, out := call(rt, "deletePlace", `{"args":["`+pid+`","`+seen+`"]}`, nil); code != 409 {
		t.Errorf("stale delete: %d %v", code, out)
	}
	if code, _ := call(rt, "deletePlace", `{"args":["`+pid+`"]}`, nil); code != 200 {
		t.Errorf("delete: %d", code)
	}
}

func TestPlacesNeedAConfiguredTrip(t *testing.T) {
	rt := placesRouter(members.Editor, sheets.NewMemGrid(nil))
	if code, _ := call(rt, "addPlace", placeBody("x"), nil); code != 412 {
		t.Errorf("code = %d", code)
	}
}

func TestResolvePlaceLinkReturnsTheNameAndPosition(t *testing.T) {
	f := &fakeLinks{res: maplink.Result{Name: "오사카 공항", Lat: 34.78, Lng: 135.43, Layer: "교통", Address: "일본 오사카"}}
	rt := placesRouterWith(members.Editor, configuredGrid(), f)
	code, out := call(rt, "resolvePlaceLink", `{"args":["공유 https://maps.app.goo.gl/x"]}`, nil)
	d, _ := out["data"].(map[string]any)
	if code != 200 || d["name"] != "오사카 공항" || d["lat"] != 34.78 || d["lng"] != 135.43 || d["layer"] != "교통" || d["address"] != "일본 오사카" || f.got != "공유 https://maps.app.goo.gl/x" {
		t.Errorf("%d %v got=%q", code, out, f.got)
	}
}

func TestResolvePlaceLinkErrors(t *testing.T) {
	g := configuredGrid()
	rt := placesRouterWith(members.Editor, g, &fakeLinks{err: &maplink.InputError{Msg: "구글 지도 링크만 읽을 수 있습니다"}})
	if code, out := call(rt, "resolvePlaceLink", `{"args":["x"]}`, nil); code != 422 || !strings.Contains(out["error"].(string), "구글 지도") {
		t.Errorf("input error: %d %v", code, out)
	}
	rt = placesRouterWith(members.Editor, g, &fakeLinks{err: errors.New("maplink: dial tcp: i/o timeout")})
	if code, out := call(rt, "resolvePlaceLink", `{"args":["x"]}`, nil); code != 502 || strings.Contains(out["error"].(string), "dial") {
		t.Errorf("network error must not leak internals: %d %v", code, out)
	}
	if code, _ := call(placesRouterWith(members.Editor, g, &fakeLinks{}), "resolvePlaceLink", `{"args":[]}`, nil); code != 422 {
		t.Errorf("no argument: %d", code)
	}
	if code, _ := call(placesRouterWith(members.Viewer, g, &fakeLinks{}), "resolvePlaceLink", `{"args":["x"]}`, nil); code != 403 {
		t.Errorf("viewer: %d", code)
	}
	if code, _ := call(placesRouter(members.Editor, g), "resolvePlaceLink", `{"args":["x"]}`, nil); code != 412 {
		t.Errorf("not available: %d", code)
	}
}

func TestPlaceLinkRoundTripsThroughTheServer(t *testing.T) {
	rt := placesRouter(members.Editor, configuredGrid())
	body := `{"args":[{"id":"` + pid + `","name":"공항","layer":"교통","lat":34.78,"lng":135.43,"memo":"","link":"https://maps.app.goo.gl/waTe1ppztvxpCJWXA"}]}`
	if code, out := call(rt, "addPlace", body, nil); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	_, out := call(rt, "refresh", `{"args":[]}`, nil)
	tp, _ := out["data"].(map[string]any)["tripPlaces"].([]any)
	if len(tp) != 1 || tp[0].(map[string]any)["link"] != "https://maps.app.goo.gl/waTe1ppztvxpCJWXA" {
		t.Errorf("tripPlaces = %v", tp)
	}
	bad := strings.Replace(body, "https://maps.app.goo.gl/waTe1ppztvxpCJWXA", "javascript:alert(1)", 1)
	if code, out := call(rt, "addPlace", strings.Replace(bad, pid, "55555555-5555-4555-8555-555555555555", 1), nil); code != 422 {
		t.Errorf("an unsafe link must be refused: %d %v", code, out)
	}
}

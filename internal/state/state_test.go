package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tourmanager/internal/sheets"
)

func svc() (*Service, *sheets.MemGrid) {
	g := sheets.NewMemGrid(nil)
	at := time.Date(2027, 4, 11, 5, 0, 0, 0, time.UTC)
	return &Service{Grid: g, Now: func() time.Time { at = at.Add(time.Second); return at }}, g
}

func patch(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(v)
	}
	return out
}

const d1, d2 = "2027-04-10", "2027-04-11"

var inTrip = func(d string) bool { return d >= d1 && d <= "2027-04-12" }

func save(t *testing.T, s *Service, who, key string, kv map[string]string) map[string]json.RawMessage {
	t.Helper()
	got, err := s.Save(context.Background(), who, key, patch(kv), inTrip)
	if err != nil {
		t.Fatalf("save %s %v: %v", key, kv, err)
	}
	return got
}

func TestSavedDaysComeBackFromAllAndTheMergedResult(t *testing.T) {
	s, _ := svc()
	merged := save(t, s, "a@x.com", "routes", map[string]string{d1: `[{"id":"p1","stayMin":60}]`})
	if string(merged[d1]) != `[{"id":"p1","stayMin":60}]` {
		t.Fatalf("merged = %v", merged)
	}
	all, err := s.All(context.Background())
	if err != nil || string(all["routes"][d1]) != `[{"id":"p1","stayMin":60}]` {
		t.Fatalf("%v %v", all, err)
	}
	if len(all["visits"]) != 0 {
		t.Errorf("a key nobody wrote is empty: %v", all)
	}
}

func TestCompanionsEditingDifferentDaysDoNotOverwriteEachOther(t *testing.T) {
	s, _ := svc()
	save(t, s, "a@x.com", "routes", map[string]string{d1: `[{"id":"a","stayMin":30}]`})
	merged := save(t, s, "b@x.com", "routes", map[string]string{d2: `[{"id":"b","stayMin":30}]`})
	if len(merged) != 2 || string(merged[d1]) != `[{"id":"a","stayMin":30}]` {
		t.Fatalf("the first companion's day must survive: %v", merged)
	}
}

func TestNullRemovesADayAndReplacesDoNotDuplicateRows(t *testing.T) {
	s, g := svc()
	save(t, s, "a@x.com", "routes", map[string]string{d1: `[{"id":"a","stayMin":30}]`})
	save(t, s, "a@x.com", "routes", map[string]string{d1: `[{"id":"a","stayMin":45}]`})
	rows, _ := g.RawRows(context.Background(), Tab)
	if len(rows) != 2 { // header + one row for the day
		t.Fatalf("rewriting a day must update its row, got %d rows: %v", len(rows), rows)
	}
	merged := save(t, s, "a@x.com", "routes", map[string]string{d1: `null`})
	if len(merged) != 0 {
		t.Errorf("null removes the day: %v", merged)
	}
	if rows, _ = g.RawRows(context.Background(), Tab); len(rows) != 1 {
		t.Errorf("the row is gone: %v", rows)
	}
}

func TestEachKeyKeepsItsOwnShape(t *testing.T) {
	s, _ := svc()
	ctx := context.Background()
	bad := []struct {
		key string
		kv  map[string]string
		why string
	}{
		{"nope", map[string]string{d1: `1`}, "unknown key"},
		{"routes", map[string]string{"not-a-date": `[]`}, "key must be a date"},
		{"routes", map[string]string{d1: `"x"`}, "a day is a list"},
		{"routes", map[string]string{d1: `[1]`}, "a stop is an object"},
		{"routes", map[string]string{d1: `[{"stayMin":10}]`}, "a stop names a place, slot or event"},
		{"routes", map[string]string{"2027-05-01": `[]`}, "outside the trip"},
		{"starts", map[string]string{d1: `1500`}, "minute of the day"},
		{"starts", map[string]string{d1: `"09:00"`}, "minutes are a number"},
		{"startFrom", map[string]string{d1: `""`}, "a place id"},
		{"visits", map[string]string{d1: `{"a":1}`}, "visits are a list"},
		{"eventPlace", map[string]string{"k": `5`}, "a place id string"},
	}
	for _, c := range bad {
		_, err := s.Save(ctx, "a@x.com", c.key, patch(c.kv), inTrip)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s (%s %v): want a validation error, got %v", c.why, c.key, c.kv, err)
		}
	}
	ok := []struct {
		key string
		kv  map[string]string
	}{
		{"routes", map[string]string{d1: `[{"ev":"abc"},{"id":"p","stayMin":0},{"sid":"s1","label":"점심","cands":["a","b"],"pickId":"a"}]`}},
		{"starts", map[string]string{d1: `540`}},
		{"startFrom", map[string]string{d1: `place-1`}},
		{"visits", map[string]string{d1: `[{"sid":"s1","date":"2027-04-10","result":"visited","time":"12:10"}]`}},
		{"eventPlace", map[string]string{"cal:1": `"p1"`}},
	}
	for _, c := range ok {
		if c.key == "startFrom" {
			c.kv[d1] = `"place-1"`
		}
		if _, err := s.Save(ctx, "a@x.com", c.key, patch(c.kv), inTrip); err != nil {
			t.Errorf("%s %v should be accepted: %v", c.key, c.kv, err)
		}
	}
}

func TestSizeLimitsProtectTheSheet(t *testing.T) {
	s, _ := svc()
	huge := `[` + strings.Repeat(`{"id":"p","stayMin":1},`, 70) + `{"id":"p","stayMin":1}]`
	if _, err := s.Save(context.Background(), "a@x.com", "routes", patch(map[string]string{d1: huge}), inTrip); err == nil {
		t.Error("more than 60 stops in a day must be refused")
	}
	big := `[{"sid":"s","label":"` + strings.Repeat("가", 30000) + `"}]`
	if _, err := s.Save(context.Background(), "a@x.com", "routes", patch(map[string]string{d1: big}), inTrip); err == nil {
		t.Error("a day larger than a cell may hold must be refused")
	}
}

func TestARejectedPatchChangesNothing(t *testing.T) {
	s, _ := svc()
	save(t, s, "a@x.com", "starts", map[string]string{d1: `540`})
	_, err := s.Save(context.Background(), "a@x.com", "starts", patch(map[string]string{d2: `600`, "2027-04-12": `9999`}), inTrip)
	if err == nil {
		t.Fatal("expected an error")
	}
	all, _ := s.All(context.Background())
	if len(all["starts"]) != 1 {
		t.Errorf("all-or-nothing: %v", all["starts"])
	}
}

func TestConcurrentSavesFromCompanionsAreAllKept(t *testing.T) {
	s, _ := svc()
	var wg sync.WaitGroup
	for _, d := range []string{d1, d2, "2027-04-12"} {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			_, _ = s.Save(context.Background(), "a@x.com", "starts", patch(map[string]string{d: `540`}), inTrip)
		}(d)
	}
	wg.Wait()
	if all, _ := s.All(context.Background()); len(all["starts"]) != 3 {
		t.Errorf("%v", all["starts"])
	}
}

func TestTheSheetShowsWhoChangedWhat(t *testing.T) {
	s, g := svc()
	save(t, s, "a@x.com", "starts", map[string]string{d1: `540`})
	rows, _ := g.RawRows(context.Background(), Tab)
	if rows[0][0] != "키" || rows[1][0] != "starts" || rows[1][1] != d1 || rows[1][2] != "540" || rows[1][3] != "a@x.com" || rows[1][4] == "" {
		t.Errorf("%v", rows)
	}
}

func TestRowsOfAnUnknownKeyOrBrokenJSONAreIgnored(t *testing.T) {
	g := sheets.NewMemGrid(map[string][][]string{Tab: {Header, {"mystery", "x", "1", "", ""}, {"starts", d1, "{broken", "", ""}, {"starts", d2, "600", "", ""}}})
	all, err := (&Service{Grid: g}).All(context.Background())
	if err != nil || len(all["starts"]) != 1 || string(all["starts"][d2]) != "600" {
		t.Fatalf("a hand-edited sheet must not break the page: %v %v", all, err)
	}
}

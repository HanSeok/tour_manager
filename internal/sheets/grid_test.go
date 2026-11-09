package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSheets is a tiny in-memory Google Sheets: values.get / append / update, metadata and batchUpdate.
type fakeSheets struct {
	mu      sync.Mutex
	tabs    map[string][][]any
	ids     map[string]int64
	next    int64
	queries []string // valueInputOption seen on writes
}

func newFake() (*fakeSheets, *httptest.Server) {
	f := &fakeSheets{tabs: map[string][][]any{}, ids: map[string]int64{}, next: 100}
	return f, httptest.NewServer(f)
}

func (f *fakeSheets) tabOf(rng string) (string, string) { // "'tab'!A:Z" → tab, "A:Z"
	i := strings.LastIndex(rng, "'!")
	return strings.ReplaceAll(rng[1:i], "''", "'"), rng[i+2:]
}

func (f *fakeSheets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const root = "/v4/spreadsheets/sid"
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(code int, msg string) {
		reply(code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
	}
	path := r.URL.Path
	switch {
	case path == root && r.Method == http.MethodGet:
		var list []any
		for t, id := range f.ids {
			list = append(list, map[string]any{"properties": map[string]any{"sheetId": id, "title": t}})
		}
		reply(200, map[string]any{"sheets": list})
	case path == root+":batchUpdate" && r.Method == http.MethodPost:
		var body struct {
			Requests []map[string]json.RawMessage `json:"requests"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, q := range body.Requests {
			if raw, ok := q["addSheet"]; ok {
				var a struct {
					Properties struct{ Title string } `json:"properties"`
				}
				_ = json.Unmarshal(raw, &a)
				f.next++
				f.ids[a.Properties.Title], f.tabs[a.Properties.Title] = f.next, nil
			}
			if raw, ok := q["deleteDimension"]; ok {
				var d struct {
					Range struct {
						SheetID    int64 `json:"sheetId"`
						Dimension  string
						StartIndex int `json:"startIndex"`
						EndIndex   int `json:"endIndex"`
					} `json:"range"`
				}
				_ = json.Unmarshal(raw, &d)
				for t, id := range f.ids {
					if id == d.Range.SheetID && d.Range.Dimension == "ROWS" {
						rows := f.tabs[t]
						f.tabs[t] = append(rows[:d.Range.StartIndex], rows[d.Range.EndIndex:]...)
					}
				}
			}
		}
		reply(200, map[string]any{})
	case strings.HasPrefix(path, root+"/values/"):
		rng := strings.TrimPrefix(path, root+"/values/")
		appendCall := strings.HasSuffix(rng, ":append")
		rng = strings.TrimSuffix(rng, ":append")
		tab, a1 := f.tabOf(rng)
		rows, ok := f.tabs[tab]
		if _, known := f.ids[tab]; !known {
			fail(400, "Unable to parse range: "+rng)
			return
		}
		switch {
		case r.Method == http.MethodGet:
			if len(rows) == 0 {
				reply(200, map[string]any{})
				return
			}
			reply(200, map[string]any{"values": rows})
		case r.Method == http.MethodPost && appendCall:
			f.queries = append(f.queries, r.URL.Query().Get("valueInputOption"))
			var b struct{ Values [][]any }
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.tabs[tab] = append(rows, b.Values...)
			reply(200, map[string]any{})
		case r.Method == http.MethodPut:
			f.queries = append(f.queries, r.URL.Query().Get("valueInputOption"))
			var b struct{ Values [][]any }
			_ = json.NewDecoder(r.Body).Decode(&b)
			n, _ := strconv.Atoi(strings.TrimPrefix(a1, "A"))
			for len(f.tabs[tab]) < n {
				f.tabs[tab] = append(f.tabs[tab], nil)
			}
			f.tabs[tab][n-1] = b.Values[0]
			reply(200, map[string]any{})
		default:
			fail(405, "bad call")
		}
		_ = ok
	default:
		fail(404, "no route "+path)
	}
}

// gridContract is the behaviour every Grid must have, run against the real client (via the fake) and MemGrid.
func gridContract(t *testing.T, g Grid) {
	t.Helper()
	ctx := context.Background()
	if _, err := g.RawRows(ctx, "지출"); !errors.Is(err, ErrNoSheet) {
		t.Fatalf("missing tab: want ErrNoSheet, got %v", err)
	}
	created, err := g.EnsureTab(ctx, "지출", []string{"날짜", "금액"})
	if err != nil || !created {
		t.Fatalf("ensure: %v %v", created, err)
	}
	if created, err = g.EnsureTab(ctx, "지출", []string{"날짜", "금액"}); err != nil || created {
		t.Fatalf("ensure again must not recreate or rewrite: %v %v", created, err)
	}
	for _, row := range [][]any{{"2027-04-11", 100000000.0}, {"=1+1", 9.3}, {"2027-04-12", 1240.0}} {
		if err := g.Append(ctx, "지출", row); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := g.RawRows(ctx, "지출")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"날짜", "금액"}, {"2027-04-11", "100000000"}, {"=1+1", "9.3"}, {"2027-04-12", "1240"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows=%v\nwant=%v", rows, want)
	}
	if err := g.UpdateRow(ctx, "지출", 3, []any{"edited", 5.0}); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteRow(ctx, "지출", 2); err != nil {
		t.Fatal(err)
	}
	rows, _ = g.RawRows(ctx, "지출")
	want = [][]string{{"날짜", "금액"}, {"edited", "5"}, {"2027-04-12", "1240"}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("after update+delete rows=%v\nwant=%v", rows, want)
	}
	if err := g.DeleteRow(ctx, "지출", 1); err == nil {
		t.Error("the header row must not be deletable")
	}
	if err := g.Append(ctx, "없는탭", []any{"x"}); err == nil {
		t.Error("append to a missing tab must fail")
	}
}

func TestMemGridContract(t *testing.T) { gridContract(t, NewMemGrid(nil)) }

func TestAPIGridContractAndRawWrites(t *testing.T) {
	f, srv := newFake()
	defer srv.Close()
	gridContract(t, &API{HTTP: srv.Client(), Base: srv.URL, SpreadsheetID: "sid"})
	for _, q := range f.queries {
		if q != "RAW" {
			t.Errorf("every write must use valueInputOption=RAW (formulas never evaluated), got %q", q)
		}
	}
	if len(f.queries) == 0 {
		t.Error("no writes seen")
	}
}

func TestCellString(t *testing.T) {
	for in, want := range map[any]string{nil: "", "a": "a", 1e8: "100000000", 9.3: "9.3", 1240.0: "1240", 3: "3", true: "true", 0.0000001: "0.0000001"} {
		if got := CellString(in); got != want {
			t.Errorf("CellString(%v)=%q want %q", in, got, want)
		}
	}
}

func TestLoadFixtures(t *testing.T) {
	g, err := LoadFixtures("../../dev/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := g.Rows(context.Background(), "설정")
	if err != nil || len(rows) < 5 {
		t.Fatalf("%v %v", rows, err)
	}
}

package drive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDrive is just enough of the Drive v3 files API: create and list (name / parent / mimeType / appProperties filters).
type fakeDrive struct {
	mu    sync.Mutex
	files []map[string]any
	fail  int    // when set, every request answers with this status
	query string // the last list query
	pages int    // list answers this many files per page (0 = all)
}

func (f *fakeDrive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.fail != 0 {
		w.WriteHeader(f.fail)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Google Drive API has not been used in project 1 before or it is disabled."}}`))
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/drive/v3/files":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		in["id"] = "id" + string(rune('A'+len(f.files)))
		f.files = append(f.files, in)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": in["id"], "name": in["name"]})
	case r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files":
		f.query = r.URL.Query().Get("q")
		var out []map[string]any
		for _, file := range f.files {
			if matches(file, f.query) {
				out = append(out, map[string]any{"id": file["id"], "name": file["name"], "appProperties": file["appProperties"], "parents": file["parents"]})
			}
		}
		next := ""
		if f.pages > 0 {
			start := 0
			if t := r.URL.Query().Get("pageToken"); t != "" {
				start = len(t) // token = "x" repeated
			}
			end := start + f.pages
			if end < len(out) {
				next = strings.Repeat("x", end)
			} else {
				end = len(out)
			}
			out = out[start:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": out, "nextPageToken": next})
	default:
		http.NotFound(w, r)
	}
}

// matches understands only the clauses this package writes.
func matches(file map[string]any, q string) bool {
	const tagged = "appProperties has { key='tourmanager' and value='trip' }"
	if strings.Contains(q, tagged) {
		props, _ := file["appProperties"].(map[string]any)
		if props["tourmanager"] != "trip" {
			return false
		}
		q = strings.Replace(q, tagged, "trashed = false", 1)
	}
	for _, c := range strings.Split(q, " and ") {
		switch {
		case strings.HasPrefix(c, "name = '"):
			if file["name"] != strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(c, "name = '"), "'"), `\'`, "'") {
				return false
			}
		case strings.HasPrefix(c, "mimeType = '"):
			if file["mimeType"] != strings.TrimSuffix(strings.TrimPrefix(c, "mimeType = '"), "'") {
				return false
			}
		case strings.HasSuffix(c, "' in parents"):
			p, _ := file["parents"].([]any)
			want := strings.TrimSuffix(strings.TrimPrefix(c, "'"), "' in parents")
			if len(p) != 1 || p[0] != want {
				return false
			}
		case c == "trashed = false":
		default:
			return false
		}
	}
	return true
}

func newClient(t *testing.T, f *fakeDrive) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &Client{HTTP: srv.Client(), Base: srv.URL}
}

func byMime(f *fakeDrive, mime string) map[string]any {
	for _, file := range f.files {
		if file["mimeType"] == mime {
			return file
		}
	}
	return nil
}

func byName(f *fakeDrive, name string) map[string]any {
	for _, file := range f.files {
		if file["name"] == name {
			return file
		}
	}
	return nil
}

func TestCreateTripBuildsTheFolderTreeWithTheSheetInside(t *testing.T) {
	f := &fakeDrive{}
	c := newClient(t, f)

	trip, err := c.CreateTrip(context.Background(), "오키나와 2027")
	if err != nil {
		t.Fatal(err)
	}

	root, folder, photos, sheet := byName(f, RootFolderName), byName(f, "오키나와 2027"), byName(f, PhotosFolderName), byMime(f, sheetMime)
	if root == nil || folder == nil || photos == nil {
		t.Fatalf("tree missing: %v", f.files)
	}
	if folder["mimeType"] != folderMime || folder["parents"].([]any)[0] != root["id"] {
		t.Errorf("trip folder must sit in the root folder: %v", folder)
	}
	if photos["parents"].([]any)[0] != folder["id"] {
		t.Errorf("photos folder must sit in the trip folder: %v", photos)
	}
	// the spreadsheet is created by Drive (not the Sheets API) so it can be put in the folder at birth
	if sheet["mimeType"] != sheetMime || sheet["parents"].([]any)[0] != folder["id"] || sheet["name"] != "오키나와 2027" {
		t.Errorf("sheet wrong: %v", sheet)
	}
	if sheet["appProperties"].(map[string]any)["tourmanager"] != "trip" {
		t.Errorf("sheet must be tagged so the trip list can be rebuilt: %v", sheet)
	}
	if trip.SheetID != sheet["id"] || trip.FolderID != folder["id"] || trip.PhotosFolderID != photos["id"] || trip.Title != "오키나와 2027" {
		t.Errorf("trip = %+v", trip)
	}
}

func TestSecondTripReusesTheRootFolder(t *testing.T) {
	f := &fakeDrive{}
	c := newClient(t, f)
	for _, title := range []string{"여행 A", "여행 B"} {
		if _, err := c.CreateTrip(context.Background(), title); err != nil {
			t.Fatal(err)
		}
	}
	roots := 0
	for _, file := range f.files {
		if file["name"] == RootFolderName {
			roots++
		}
	}
	if roots != 1 {
		t.Errorf("root folder created %d times, want 1", roots)
	}
}

func TestBlankOrUnsafeTitlesAreRejectedBeforeAnyCall(t *testing.T) {
	f := &fakeDrive{}
	c := newClient(t, f)
	for _, title := range []string{"", "   ", strings.Repeat("가", 101)} {
		if _, err := c.CreateTrip(context.Background(), title); err == nil {
			t.Errorf("title %q accepted", title)
		}
	}
	if len(f.files) != 0 {
		t.Errorf("nothing should have been created: %v", f.files)
	}
}

func TestTitleIsTrimmedAndSlashesAreHarmless(t *testing.T) {
	f := &fakeDrive{}
	c := newClient(t, f)
	trip, err := c.CreateTrip(context.Background(), "  제주 / 가을  ")
	if err != nil {
		t.Fatal(err)
	}
	if trip.Title != "제주 / 가을" || byName(f, "제주 / 가을") == nil {
		t.Errorf("title = %q", trip.Title)
	}
}

func TestListTripsFindsTaggedSheetsAcrossPages(t *testing.T) {
	f := &fakeDrive{pages: 2}
	c := newClient(t, f)
	for _, title := range []string{"A", "B", "C"} {
		if _, err := c.CreateTrip(context.Background(), title); err != nil {
			t.Fatal(err)
		}
	}
	// a stray, untagged spreadsheet must not show up
	f.files = append(f.files, map[string]any{"id": "stray", "name": "가계부", "mimeType": sheetMime})

	trips, err := c.ListTrips(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(trips) != 3 {
		t.Fatalf("trips = %+v", trips)
	}
	for _, tr := range trips {
		if tr.SheetID == "" || tr.FolderID == "" || tr.Title == "" {
			t.Errorf("incomplete: %+v", tr)
		}
	}
}

func TestGoogleErrorsKeepTheStatusAndMessage(t *testing.T) {
	f := &fakeDrive{fail: http.StatusForbidden}
	c := newClient(t, f)
	_, err := c.CreateTrip(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "Drive API has not been used") {
		t.Errorf("err = %v", err)
	}
}

func TestFolderNamesInQueriesAreEscaped(t *testing.T) {
	f := &fakeDrive{}
	c := newClient(t, f)
	if _, err := c.findFolder(context.Background(), "O'Brien", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.query, `name = 'O\'Brien'`) {
		t.Errorf("query = %s", f.query)
	}
}

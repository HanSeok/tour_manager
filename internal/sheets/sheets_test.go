package sheets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAPIRowsParsesValuesAndPadsNothing(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		_, _ = w.Write([]byte(`{"values":[["항목","값"],["환율",9.3],["부제"]]}`))
	}))
	defer srv.Close()
	a := &API{HTTP: srv.Client(), Base: srv.URL, SpreadsheetID: "sid"}
	rows, err := a.Rows(context.Background(), "설정")
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"항목", "값"}, {"환율", "9.3"}, {"부제"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows=%v", rows)
	}
	if gotQuery != "valueRenderOption=FORMATTED_VALUE" || gotPath == "" {
		t.Errorf("path=%s query=%s", gotPath, gotQuery)
	}
}

func TestAPIRowsMissingTabAndErrors(t *testing.T) {
	code, body := 400, `{"error":{"code":400,"message":"Unable to parse range: '멤버'!A:Z"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	a := &API{HTTP: srv.Client(), Base: srv.URL, SpreadsheetID: "sid"}
	if _, err := a.Rows(context.Background(), "멤버"); !errors.Is(err, ErrNoSheet) {
		t.Fatalf("want ErrNoSheet, got %v", err)
	}
	code, body = 403, `{"error":{"code":403,"message":"The caller does not have permission"}}`
	_, err := a.Rows(context.Background(), "설정")
	if err == nil || errors.Is(err, ErrNoSheet) {
		t.Fatalf("403 must be a real error, got %v", err)
	}
}

func TestFileSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "설정.json"), []byte(`[["여행 제목","t"]]`), 0o600); err != nil {
		t.Fatal(err)
	}
	f := FileSource{Dir: dir}
	rows, err := f.Rows(context.Background(), "설정")
	if err != nil || len(rows) != 1 || rows[0][1] != "t" {
		t.Fatalf("%v %v", rows, err)
	}
	if _, err := f.Rows(context.Background(), "멤버"); !errors.Is(err, ErrNoSheet) {
		t.Fatalf("want ErrNoSheet, got %v", err)
	}
}

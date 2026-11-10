package sheets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Grid is what the data services (expenses, history, ...) need from a spreadsheet: read, append, replace and
// delete rows, and make sure a tab exists. Row numbers are 1-based like in the sheet (row 1 = header).
type Grid interface {
	Source
	RawRows(ctx context.Context, tab string) ([][]string, error)
	Append(ctx context.Context, tab string, row []any) error
	UpdateRow(ctx context.Context, tab string, n int, row []any) error
	DeleteRow(ctx context.Context, tab string, n int) error
	// EnsureTab creates the tab (with `header` as row 1) when it is missing, or writes the header into an empty tab.
	EnsureTab(ctx context.Context, tab string, header []string) (created bool, err error)
}

// CellString renders a cell value the way the sheet shows plain data: numbers without exponent notation.
func CellString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

func quoteTab(tab string) string { return "'" + strings.ReplaceAll(tab, "'", "''") + "'" }

/* ---------- Google Sheets ---------- */

func (a *API) call(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return resp.StatusCode, fmt.Errorf("sheets %s %s: %d %s", method, path, resp.StatusCode, msg)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (a *API) sheetPath(suffix string) string {
	return "/v4/spreadsheets/" + url.PathEscape(a.SpreadsheetID) + suffix
}

// Values are written RAW: the sheet stores exactly what we send and never evaluates it as a formula.
func (a *API) Append(ctx context.Context, tab string, row []any) error {
	rng := url.PathEscape(quoteTab(tab) + "!A:A")
	_, err := a.call(ctx, http.MethodPost, a.sheetPath("/values/"+rng+":append?valueInputOption=RAW&insertDataOption=INSERT_ROWS"), map[string]any{"values": [][]any{row}}, nil)
	return err
}

func (a *API) UpdateRow(ctx context.Context, tab string, n int, row []any) error {
	if n < 1 {
		return errors.New("row must be >= 1")
	}
	rng := url.PathEscape(quoteTab(tab) + "!A" + strconv.Itoa(n))
	_, err := a.call(ctx, http.MethodPut, a.sheetPath("/values/"+rng+"?valueInputOption=RAW"), map[string]any{"values": [][]any{row}}, nil)
	return err
}

// PutRows writes several rows starting at row n in one call (RAW, like every write here).
func (a *API) PutRows(ctx context.Context, tab string, n int, rows [][]any) error {
	if n < 1 {
		return errors.New("row must be >= 1")
	}
	if len(rows) == 0 {
		return nil
	}
	rng := url.PathEscape(quoteTab(tab) + "!A" + strconv.Itoa(n))
	_, err := a.call(ctx, http.MethodPut, a.sheetPath("/values/"+rng+"?valueInputOption=RAW"), map[string]any{"values": rows}, nil)
	return err
}

type tabInfo struct {
	SheetID int64
	Title   string
}

func (a *API) tabs(ctx context.Context) ([]tabInfo, error) {
	var out struct {
		Sheets []struct {
			Properties struct {
				SheetID int64  `json:"sheetId"`
				Title   string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if _, err := a.call(ctx, http.MethodGet, a.sheetPath("?fields=sheets.properties(sheetId,title)"), nil, &out); err != nil {
		return nil, err
	}
	list := make([]tabInfo, 0, len(out.Sheets))
	for _, s := range out.Sheets {
		list = append(list, tabInfo{s.Properties.SheetID, s.Properties.Title})
	}
	return list, nil
}

func (a *API) DeleteRow(ctx context.Context, tab string, n int) error {
	if n < 2 {
		return errors.New("the header row cannot be deleted")
	}
	list, err := a.tabs(ctx)
	if err != nil {
		return err
	}
	for _, t := range list {
		if t.Title == tab {
			req := map[string]any{"requests": []any{map[string]any{"deleteDimension": map[string]any{
				"range": map[string]any{"sheetId": t.SheetID, "dimension": "ROWS", "startIndex": n - 1, "endIndex": n}}}}}
			_, err := a.call(ctx, http.MethodPost, a.sheetPath(":batchUpdate"), req, nil)
			return err
		}
	}
	return ErrNoSheet
}

// RenameFirstTab renames the first tab (the empty "시트1" a new spreadsheet comes with) so no blank tab is left over.
// A tab that already has the wanted name is left alone.
func (a *API) RenameFirstTab(ctx context.Context, title string) error {
	list, err := a.tabs(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}
	for _, t := range list {
		if t.Title == title {
			return nil
		}
	}
	req := map[string]any{"requests": []any{map[string]any{"updateSheetProperties": map[string]any{
		"properties": map[string]any{"sheetId": list[0].SheetID, "title": title}, "fields": "title"}}}}
	_, err = a.call(ctx, http.MethodPost, a.sheetPath(":batchUpdate"), req, nil)
	return err
}

func (a *API) EnsureTab(ctx context.Context, tab string, header []string) (bool, error) {
	list, err := a.tabs(ctx)
	if err != nil {
		return false, err
	}
	exists := false
	for _, t := range list {
		exists = exists || t.Title == tab
	}
	created := false
	if !exists {
		req := map[string]any{"requests": []any{map[string]any{"addSheet": map[string]any{"properties": map[string]any{"title": tab}}}}}
		if _, err := a.call(ctx, http.MethodPost, a.sheetPath(":batchUpdate"), req, nil); err != nil {
			return false, err
		}
		created = true
	}
	rows, err := a.rows(ctx, tab, "FORMATTED_VALUE")
	if err != nil {
		return created, err
	}
	if len(rows) == 0 {
		cells := make([]any, len(header))
		for i, h := range header {
			cells[i] = h
		}
		return created, a.UpdateRow(ctx, tab, 1, cells)
	}
	return created, nil
}

/* ---------- in memory (development and tests) ---------- */

// MemGrid is a Grid held in memory: tabs of string rows. It makes the whole server runnable without Google.
type MemGrid struct {
	mu   sync.Mutex
	tabs map[string][][]string
}

func NewMemGrid(seed map[string][][]string) *MemGrid {
	g := &MemGrid{tabs: map[string][][]string{}}
	for k, v := range seed {
		g.tabs[k] = cloneRows(v)
	}
	return g
}

// LoadFixtures reads every <tab>.json in dir into a MemGrid.
func LoadFixtures(dir string) (*MemGrid, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	seed := map[string][][]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var rows [][]string
		if err := json.Unmarshal(b, &rows); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		seed[strings.TrimSuffix(filepath.Base(f), ".json")] = rows
	}
	return NewMemGrid(seed), nil
}

func cloneRows(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = append([]string(nil), r...)
	}
	return out
}

func (g *MemGrid) Rows(_ context.Context, tab string) ([][]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, ok := g.tabs[tab]
	if !ok {
		return nil, ErrNoSheet
	}
	return cloneRows(rows), nil
}

func (g *MemGrid) RawRows(ctx context.Context, tab string) ([][]string, error) {
	return g.Rows(ctx, tab)
}

func cells(row []any) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = CellString(c)
	}
	return out
}

func (g *MemGrid) Append(_ context.Context, tab string, row []any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.tabs[tab]; !ok {
		return ErrNoSheet
	}
	g.tabs[tab] = append(g.tabs[tab], cells(row))
	return nil
}

func (g *MemGrid) UpdateRow(_ context.Context, tab string, n int, row []any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, ok := g.tabs[tab]
	if !ok {
		return ErrNoSheet
	}
	if n < 1 || n > len(rows)+1 {
		return fmt.Errorf("row %d out of range", n)
	}
	if n == len(rows)+1 {
		g.tabs[tab] = append(rows, cells(row))
		return nil
	}
	rows[n-1] = cells(row)
	return nil
}

func (g *MemGrid) PutRows(_ context.Context, tab string, n int, rows [][]any) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	cur, ok := g.tabs[tab]
	if !ok {
		return ErrNoSheet
	}
	if n < 1 || n > len(cur)+1 {
		return fmt.Errorf("row %d out of range", n)
	}
	for i, r := range rows {
		if n-1+i < len(cur) {
			cur[n-1+i] = cells(r)
		} else {
			cur = append(cur, cells(r))
		}
	}
	g.tabs[tab] = cur
	return nil
}

func (g *MemGrid) RenameFirstTab(context.Context, string) error { return nil } // a MemGrid has no default tab

func (g *MemGrid) DeleteRow(_ context.Context, tab string, n int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, ok := g.tabs[tab]
	if !ok {
		return ErrNoSheet
	}
	if n < 2 || n > len(rows) {
		return fmt.Errorf("row %d out of range", n)
	}
	g.tabs[tab] = append(rows[:n-1], rows[n:]...)
	return nil
}

func (g *MemGrid) EnsureTab(_ context.Context, tab string, header []string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, ok := g.tabs[tab]
	if !ok || len(rows) == 0 {
		g.tabs[tab] = [][]string{append([]string(nil), header...)}
		return !ok, nil
	}
	return false, nil
}

var (
	_ Grid = (*API)(nil)
	_ Grid = (*MemGrid)(nil)
)

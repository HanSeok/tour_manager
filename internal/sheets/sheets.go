// Package sheets reads tabs of the trip spreadsheet. Everything the server needs for now is a plain values.get,
// so this talks to the REST endpoint directly instead of pulling in the large Google API client.
package sheets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoSheet means the tab does not exist.
var ErrNoSheet = errors.New("sheet not found")

// Source gives the rows of the settings and members tabs. A nil slice with a nil error is not used:
// a missing tab is ErrNoSheet, an empty tab is an empty slice.
type Source interface {
	Rows(ctx context.Context, tab string) ([][]string, error)
}

// API reads from Google Sheets with an authorized client (the owner's token).
type API struct {
	HTTP          *http.Client
	Base          string // default https://sheets.googleapis.com
	SpreadsheetID string
}

type valuesResponse struct {
	Values [][]any `json:"values"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Rows returns the tab as displayed (formatted values) — right for the settings tab.
func (a *API) Rows(ctx context.Context, tab string) ([][]string, error) {
	return a.rows(ctx, tab, "FORMATTED_VALUE")
}

// RawRows returns unformatted values (numbers without thousands separators) — right for data tabs.
func (a *API) RawRows(ctx context.Context, tab string) ([][]string, error) {
	return a.rows(ctx, tab, "UNFORMATTED_VALUE")
}

func (a *API) base() string {
	if a.Base != "" {
		return a.Base
	}
	return "https://sheets.googleapis.com"
}

func (a *API) rows(ctx context.Context, tab, render string) ([][]string, error) {
	rng := quoteTab(tab) + "!A:Z"
	u := fmt.Sprintf("%s/v4/spreadsheets/%s/values/%s?valueRenderOption=%s", a.base(), url.PathEscape(a.SpreadsheetID), url.PathEscape(rng), render)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var vr valuesResponse
	_ = json.Unmarshal(body, &vr)
	if resp.StatusCode == http.StatusBadRequest && vr.Error != nil && strings.Contains(vr.Error.Message, "Unable to parse range") {
		return nil, ErrNoSheet
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if vr.Error != nil {
			msg = vr.Error.Message
		}
		return nil, fmt.Errorf("sheets %s: %d %s", tab, resp.StatusCode, msg)
	}
	rows := make([][]string, 0, len(vr.Values))
	for _, r := range vr.Values {
		row := make([]string, len(r))
		for i, c := range r {
			row[i] = CellString(c)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// FileSource reads <Dir>/<tab>.json (an array of rows) — for local development and tests without Google.
type FileSource struct{ Dir string }

func (f FileSource) Rows(_ context.Context, tab string) ([][]string, error) {
	b, err := os.ReadFile(filepath.Join(f.Dir, tab+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSheet
	}
	if err != nil {
		return nil, err
	}
	var rows [][]string
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, fmt.Errorf("%s.json: %w", tab, err)
	}
	return rows, nil
}

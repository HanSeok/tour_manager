// Package drive creates the Google Drive side of a trip: a folder tree
//
//	여행 대시보드/<여행 제목>/{<여행 제목>(spreadsheet), 사진/}
//
// in the owner's My Drive. The spreadsheet is created through Drive (not the Sheets API) because only Drive can put
// a new file into a folder at creation. Files are tagged with appProperties so the trip list can be rebuilt from Drive
// alone. With the drive.file scope this only ever sees files the app created.
package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	RootFolderName   = "여행 대시보드"
	PhotosFolderName = "사진"
	folderMime       = "application/vnd.google-apps.folder"
	sheetMime        = "application/vnd.google-apps.spreadsheet"
	maxTitleRunes    = 100
	tagKey           = "tourmanager"
	tagTrip          = "trip"
)

// Trip is what the server remembers about one trip's Google files.
type Trip struct {
	Title          string `json:"title"`
	SheetID        string `json:"sheetId"`
	FolderID       string `json:"folderId"`
	PhotosFolderID string `json:"photosFolderId,omitempty"`
}

// Client talks to the Drive v3 REST API with an authorized HTTP client (the owner's token).
type Client struct {
	HTTP *http.Client
	Base string // default https://www.googleapis.com
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return "https://www.googleapis.com"
}

// CreateTrip makes the folder tree and the (empty) spreadsheet. It does not fill the sheet's tabs.
func (c *Client) CreateTrip(ctx context.Context, title string) (Trip, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Trip{}, errors.New("여행 제목을 입력하세요")
	}
	if utf8.RuneCountInString(title) > maxTitleRunes {
		return Trip{}, fmt.Errorf("여행 제목은 %d자까지 입니다", maxTitleRunes)
	}
	root, err := c.ensureFolder(ctx, RootFolderName, "root")
	if err != nil {
		return Trip{}, err
	}
	folder, err := c.create(ctx, map[string]any{"name": title, "mimeType": folderMime, "parents": []string{root}})
	if err != nil {
		return Trip{}, err
	}
	sheet, err := c.create(ctx, map[string]any{
		"name": title, "mimeType": sheetMime, "parents": []string{folder},
		"appProperties": map[string]string{tagKey: tagTrip},
	})
	if err != nil {
		return Trip{}, err
	}
	photos, err := c.create(ctx, map[string]any{"name": PhotosFolderName, "mimeType": folderMime, "parents": []string{folder}})
	if err != nil {
		return Trip{}, err
	}
	return Trip{Title: title, SheetID: sheet, FolderID: folder, PhotosFolderID: photos}, nil
}

// ListTrips returns every spreadsheet this app created for a trip (rebuilds the list if the server forgot it).
func (c *Client) ListTrips(ctx context.Context) ([]Trip, error) {
	q := "mimeType = '" + sheetMime + "' and appProperties has { key='" + tagKey + "' and value='" + tagTrip + "' } and trashed = false"
	var trips []Trip
	token := ""
	for {
		v := url.Values{"q": {q}, "pageSize": {"100"}, "fields": {"nextPageToken,files(id,name,parents)"}}
		if token != "" {
			v.Set("pageToken", token)
		}
		var out struct {
			Files []struct {
				ID      string   `json:"id"`
				Name    string   `json:"name"`
				Parents []string `json:"parents"`
			} `json:"files"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.call(ctx, http.MethodGet, "/drive/v3/files?"+v.Encode(), nil, &out); err != nil {
			return nil, err
		}
		for _, f := range out.Files {
			t := Trip{Title: f.Name, SheetID: f.ID}
			if len(f.Parents) > 0 {
				t.FolderID = f.Parents[0]
			}
			trips = append(trips, t)
		}
		if out.NextPageToken == "" {
			return trips, nil
		}
		token = out.NextPageToken
	}
}

func (c *Client) ensureFolder(ctx context.Context, name, parent string) (string, error) {
	if id, err := c.findFolder(ctx, name, parent); err != nil || id != "" {
		return id, err
	}
	return c.create(ctx, map[string]any{"name": name, "mimeType": folderMime, "parents": []string{parent}})
}

// findFolder returns the id of the first live folder called name (inside parent when given), or "".
func (c *Client) findFolder(ctx context.Context, name, parent string) (string, error) {
	q := "name = '" + escapeQuery(name) + "' and mimeType = '" + folderMime + "' and trashed = false"
	if parent != "" {
		q += " and '" + escapeQuery(parent) + "' in parents"
	}
	var out struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	v := url.Values{"q": {q}, "pageSize": {"1"}, "fields": {"files(id)"}}
	if err := c.call(ctx, http.MethodGet, "/drive/v3/files?"+v.Encode(), nil, &out); err != nil {
		return "", err
	}
	if len(out.Files) == 0 {
		return "", nil
	}
	return out.Files[0].ID, nil
}

func (c *Client) create(ctx context.Context, meta map[string]any) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/drive/v3/files?fields=id", meta, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("drive: no id in the create response")
	}
	return out.ID, nil
}

// escapeQuery escapes a literal for a Drive query string ('...').
func escapeQuery(s string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
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
		return fmt.Errorf("drive %s %s: %d %s", method, strings.SplitN(path, "?", 2)[0], resp.StatusCode, msg)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

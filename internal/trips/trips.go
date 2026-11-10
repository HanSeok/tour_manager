// Package trips creates a trip (Drive folder + spreadsheet with every tab set up) and remembers which trips exist.
// The registry is only a pointer list: if it is lost, drive.Client.ListTrips can rebuild it from Drive.
package trips

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tourmanager/internal/audit"
	"tourmanager/internal/drive"
	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/places"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

// myMapsPlaceholder satisfies the [필수] My Maps setting until places are connected (the format is all that is checked).
const myMapsPlaceholder = "https://www.google.com/maps/d/edit?mid=SAMPLE"

// Request is what the "새 여행" form sends. Empty optional fields get the template defaults.
type Request struct {
	Title         string `json:"title"`
	Start         string `json:"start"`
	End           string `json:"end"`
	Timezone      string `json:"timezone"`
	DestKeywords  string `json:"destKeywords"`
	LocalCurrency string `json:"localCurrency"`
	LocalSymbol   string `json:"localSymbol"`
	HomeSymbol    string `json:"homeSymbol"`
	Rate          string `json:"rate"`
	Payers        string `json:"payers"`
}

// InputError means the request itself is wrong (nothing was created).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Drive is the part of drive.Client the service needs.
type Drive interface {
	CreateTrip(ctx context.Context, title string) (drive.Trip, error)
	ListTrips(ctx context.Context) ([]drive.Trip, error)
}

// Sheet is one trip spreadsheet: the data grid plus what only a brand new sheet needs.
type Sheet interface {
	sheets.Grid
	RenameFirstTab(ctx context.Context, title string) error
	PutRows(ctx context.Context, tab string, n int, rows [][]any) error
}

// ErrUnknownTrip means the sheet id is not one of the trips this server created.
var ErrUnknownTrip = errors.New("알 수 없는 여행입니다")

type Service struct {
	Drive Drive
	Open  func(sheetID string) (Sheet, error) // the owner's access to that spreadsheet
	Reg   *Registry
}

// Defaults are what the form shows greyed out and what a blank field gets. Only the title and the dates are required.
func Defaults() map[string]string {
	return map[string]string{"timezone": "Asia/Tokyo", "localCurrency": "엔", "localSymbol": "¥", "rate": "9.3", "payers": "나, 동행"}
}

func (r Request) values() map[string]string {
	v := map[string]string{
		"title": strings.TrimSpace(r.Title), "start": strings.TrimSpace(r.Start), "end": strings.TrimSpace(r.End),
		"myMapsUrl": myMapsPlaceholder,
	}
	def := Defaults()
	set := func(k, val string) {
		if val = strings.TrimSpace(val); val != "" {
			v[k] = val
		} else if d, ok := def[k]; ok {
			v[k] = d
		}
	}
	set("timezone", r.Timezone)
	set("localCurrency", r.LocalCurrency)
	set("localSymbol", r.LocalSymbol)
	set("rate", r.Rate)
	set("destKeywords", r.DestKeywords)
	if v["destKeywords"] == "" {
		v["destKeywords"] = v["title"] // the settings need at least one keyword; the title is a fair start
	}
	set("homeSymbol", r.HomeSymbol)
	set("payers", r.Payers)
	return v
}

// Current is the sheet id of the trip the dashboard shows.
func (s *Service) Current() (string, error) { return s.Reg.Current() }

// Switch makes another known trip the one everybody sees.
func (s *Service) Switch(sheetID string) error { return s.Reg.SetCurrent(sheetID) }

// Create validates the request, then makes the folder, the spreadsheet and its tabs, and registers the trip as current.
// Invalid input fails before anything is created in Google.
func (s *Service) Create(ctx context.Context, ownerEmail, ownerName string, req Request) (drive.Trip, error) {
	rows := tripconfig.RowsFor(req.values())
	cfg := tripconfig.Parse(rows, true)
	if !cfg.Configured() {
		return drive.Trip{}, &InputError{Msg: inputProblems(cfg)}
	}
	t, err := s.Drive.CreateTrip(ctx, cfg.Title)
	if err != nil {
		return drive.Trip{}, err
	}
	sh, err := s.Open(t.SheetID)
	if err == nil {
		err = s.provision(ctx, sh, cfg, rows, ownerEmail, ownerName)
	}
	if err != nil {
		return drive.Trip{}, fmt.Errorf("여행 시트(%s)는 만들었지만 탭을 구성하지 못했습니다: %w", t.SheetID, err)
	}
	if err := s.Reg.Add(t); err != nil {
		return drive.Trip{}, fmt.Errorf("여행 시트(%s)는 만들었지만 목록에 저장하지 못했습니다: %w", t.SheetID, err)
	}
	return t, nil
}

func (s *Service) provision(ctx context.Context, sh Sheet, cfg tripconfig.Config, rows [][]string, ownerEmail, ownerName string) error {
	if err := sh.RenameFirstTab(ctx, tripconfig.SheetName); err != nil {
		return err
	}
	if _, err := sh.EnsureTab(ctx, tripconfig.SheetName, rows[0]); err != nil {
		return err
	}
	cells := make([][]any, 0, len(rows)-1)
	for _, r := range rows[1:] {
		c := make([]any, len(r))
		for i, v := range r {
			c[i] = v
		}
		cells = append(cells, c)
	}
	if err := sh.PutRows(ctx, tripconfig.SheetName, 2, cells); err != nil {
		return err
	}
	if ownerName = strings.TrimSpace(ownerName); ownerName == "" {
		ownerName = ownerEmail
	}
	if _, err := sh.EnsureTab(ctx, members.SheetName, []string{"이메일", "표시이름", "역할"}); err != nil {
		return err
	}
	if err := sh.PutRows(ctx, members.SheetName, 2, [][]any{{ownerEmail, ownerName, "소유자"}}); err != nil {
		return err
	}
	if _, err := sh.EnsureTab(ctx, cfg.ExpenseSheet, expenses.Header(cfg)); err != nil {
		return err
	}
	if _, err := sh.EnsureTab(ctx, places.Tab, places.Header); err != nil {
		return err
	}
	_, err := sh.EnsureTab(ctx, audit.Tab, audit.Header)
	return err
}

// List is the trips this server knows (newest last).
func (s *Service) List() ([]drive.Trip, error) { return s.Reg.List() }

func inputProblems(cfg tripconfig.Config) string {
	var parts []string
	if len(cfg.Missing) > 0 {
		parts = append(parts, "비어 있음: "+strings.Join(cfg.Missing, ", "))
	}
	if len(cfg.Invalid) > 0 {
		parts = append(parts, "올바르지 않음: "+strings.Join(cfg.Invalid, ", "))
	}
	return strings.Join(parts, " / ")
}

/* ---------- registry ---------- */

type state struct {
	Trips   []drive.Trip `json:"trips"`
	Current string       `json:"current"`
}

// Registry is a small JSON file: the trips this server knows and which one the dashboard shows.
type Registry struct {
	Path string
	mu   sync.Mutex
}

func (r *Registry) load() (state, error) {
	b, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return state{}, nil
	}
	if err != nil {
		return state{}, err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}, fmt.Errorf("%s 를 읽지 못했습니다(손상됨): %w", r.Path, err)
	}
	return st, nil
}

func (r *Registry) save(st state) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path) // atomic: a crash never leaves half a file
}

func (r *Registry) List() ([]drive.Trip, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := r.load()
	if err != nil {
		return nil, err
	}
	return st.Trips, nil
}

// Current is the sheet id of the trip the dashboard shows ("" when there is none yet).
func (r *Registry) Current() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := r.load()
	return st.Current, err
}

// Add remembers a trip (replacing an entry with the same sheet) and makes it the current one.
func (r *Registry) Add(t drive.Trip) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := r.load()
	if err != nil {
		return err // a damaged file is never overwritten
	}
	replaced := false
	for i := range st.Trips {
		if st.Trips[i].SheetID == t.SheetID {
			st.Trips[i], replaced = t, true
		}
	}
	if !replaced {
		st.Trips = append(st.Trips, t)
	}
	st.Current = t.SheetID
	return r.save(st)
}

func (r *Registry) SetCurrent(sheetID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := r.load()
	if err != nil {
		return err
	}
	for _, t := range st.Trips {
		if t.SheetID == sheetID {
			st.Current = sheetID
			return r.save(st)
		}
	}
	return ErrUnknownTrip
}

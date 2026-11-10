package trips

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	_ "time/tzdata" // the presets' zones must resolve on a machine without tzdata, like the server binary does

	"tourmanager/internal/audit"
	"tourmanager/internal/drive"
	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

type fakeDrive struct {
	calls []string
	err   error
}

func (f *fakeDrive) CreateTrip(_ context.Context, title string) (drive.Trip, error) {
	f.calls = append(f.calls, title)
	if f.err != nil {
		return drive.Trip{}, f.err
	}
	return drive.Trip{Title: title, SheetID: "sheet1", FolderID: "folder1", PhotosFolderID: "photos1"}, nil
}

func (f *fakeDrive) ListTrips(context.Context) ([]drive.Trip, error) { return nil, nil }

type env struct {
	svc  *Service
	drv  *fakeDrive
	grid *sheets.MemGrid
	reg  *Registry
}

func setup(t *testing.T) env {
	t.Helper()
	grid := sheets.NewMemGrid(nil)
	drv := &fakeDrive{}
	reg := &Registry{Path: filepath.Join(t.TempDir(), "trips.json")}
	svc := &Service{Drive: drv, Reg: reg, Open: func(string) (Sheet, error) { return grid, nil }}
	return env{svc, drv, grid, reg}
}

func valid() Request {
	return Request{Title: "오키나와 2027", Start: "2027-04-10", End: "2027-04-14", Timezone: "Asia/Tokyo",
		DestKeywords: "오키나와, 나하", LocalCurrency: "엔", LocalSymbol: "¥", Rate: "9.3"}
}

func TestCreateBuildsAConfiguredTripSheet(t *testing.T) {
	e := setup(t)
	trip, err := e.svc.Create(context.Background(), "owner@example.com", "나", valid())
	if err != nil {
		t.Fatal(err)
	}
	if trip.SheetID != "sheet1" || len(e.drv.calls) != 1 || e.drv.calls[0] != "오키나와 2027" {
		t.Fatalf("trip=%+v calls=%v", trip, e.drv.calls)
	}
	ctx := context.Background()

	rows, err := e.grid.RawRows(ctx, tripconfig.SheetName)
	if err != nil {
		t.Fatal(err)
	}
	cfg := tripconfig.Parse(rows, true)
	if !cfg.Configured() || cfg.Title != "오키나와 2027" || cfg.Days != 5 {
		t.Fatalf("settings not usable: %+v", cfg)
	}
	mrows, err := e.grid.Rows(ctx, members.SheetName)
	if err != nil || len(mrows) != 2 || mrows[1][0] != "owner@example.com" || mrows[1][1] != "나" || mrows[1][2] != "소유자" {
		t.Fatalf("members tab = %v (%v)", mrows, err)
	}
	if got := members.ParseRows(mrows[1:]); len(got) != 1 || got[0].Role != members.Owner {
		t.Errorf("owner row not understood by the members parser: %+v", got)
	}
	erows, err := e.grid.Rows(ctx, cfg.ExpenseSheet)
	if err != nil || len(erows) != 1 || strings.Join(erows[0], "|") != strings.Join(expenses.Header(cfg), "|") {
		t.Errorf("expense tab = %v (%v)", erows, err)
	}
	hrows, err := e.grid.Rows(ctx, audit.Tab)
	if err != nil || len(hrows) != 1 || strings.Join(hrows[0], "|") != strings.Join(audit.Header, "|") {
		t.Errorf("history tab = %v (%v)", hrows, err)
	}
}

func TestCreateRegistersTheTripAndMakesItCurrent(t *testing.T) {
	e := setup(t)
	if _, err := e.svc.Create(context.Background(), "o@x.com", "나", valid()); err != nil {
		t.Fatal(err)
	}
	list, _ := e.reg.List()
	cur, _ := e.reg.Current()
	if len(list) != 1 || list[0].SheetID != "sheet1" || list[0].FolderID != "folder1" || cur != "sheet1" {
		t.Errorf("list=%+v current=%q", list, cur)
	}
}

func TestInvalidInputIsRejectedBeforeAnythingIsCreatedInGoogle(t *testing.T) {
	cases := map[string]func(*Request){
		"날짜 형식":   func(r *Request) { r.Start = "4월 10일" },
		"끝이 시작 전": func(r *Request) { r.End = "2027-04-01" },
		"제목 없음":   func(r *Request) { r.Title = "  " },
		"시간대":     func(r *Request) { r.Timezone = "Mars/Base" },
		"환율":      func(r *Request) { r.Rate = "abc" },
	}
	for name, mut := range cases {
		e := setup(t)
		r := valid()
		mut(&r)
		_, err := e.svc.Create(context.Background(), "o@x.com", "나", r)
		var in *InputError
		if !errors.As(err, &in) {
			t.Errorf("%s: want InputError, got %v", name, err)
			continue
		}
		if len(e.drv.calls) != 0 {
			t.Errorf("%s: Google was called for invalid input", name)
		}
		if list, _ := e.reg.List(); len(list) != 0 {
			t.Errorf("%s: registered", name)
		}
	}
}

func TestDriveFailureRegistersNothing(t *testing.T) {
	e := setup(t)
	e.drv.err = errors.New("drive POST /drive/v3/files: 403 Drive API has not been used")
	_, err := e.svc.Create(context.Background(), "o@x.com", "나", valid())
	if err == nil || !strings.Contains(err.Error(), "Drive API") {
		t.Fatalf("err = %v", err)
	}
	if list, _ := e.reg.List(); len(list) != 0 {
		t.Errorf("registered: %+v", list)
	}
}

type brokenSheet struct{ Sheet }

func (brokenSheet) PutRows(context.Context, string, int, [][]any) error { return errors.New("boom") }

func TestSheetFailureIsReportedWithTheCreatedSheetAndNotRegistered(t *testing.T) {
	e := setup(t)
	e.svc.Open = func(string) (Sheet, error) { return brokenSheet{e.grid}, nil }
	_, err := e.svc.Create(context.Background(), "o@x.com", "나", valid())
	if err == nil || !strings.Contains(err.Error(), "sheet1") {
		t.Fatalf("the error must say which sheet was left behind: %v", err)
	}
	if list, _ := e.reg.List(); len(list) != 0 {
		t.Errorf("a half-built trip must not be registered: %+v", list)
	}
}

func TestOptionalFieldsGetUsableDefaults(t *testing.T) {
	e := setup(t)
	r := valid()
	r.DestKeywords, r.HomeSymbol, r.Payers = "", "", ""
	if _, err := e.svc.Create(context.Background(), "o@x.com", "", r); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.grid.RawRows(context.Background(), tripconfig.SheetName)
	cfg := tripconfig.Parse(rows, true)
	if !cfg.Configured() || len(cfg.DestKeywords) == 0 || cfg.HomeSymbol == "" || len(cfg.Payers) == 0 {
		t.Errorf("cfg = %+v", cfg)
	}
	mrows, _ := e.grid.Rows(context.Background(), members.SheetName)
	if mrows[1][1] == "" {
		t.Error("owner display name must default to something (the e-mail)")
	}
}

func TestRegistry(t *testing.T) {
	reg := &Registry{Path: filepath.Join(t.TempDir(), "sub", "trips.json")}
	if list, err := reg.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty registry: %v %v", list, err)
	}
	if cur, err := reg.Current(); err != nil || cur != "" {
		t.Fatalf("no current yet: %q %v", cur, err)
	}
	a, b := drive.Trip{Title: "A", SheetID: "a"}, drive.Trip{Title: "B", SheetID: "b"}
	for _, tr := range []drive.Trip{a, b, a} { // a twice: no duplicate
		if err := reg.Add(tr); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := reg.List()
	if len(list) != 2 {
		t.Fatalf("list = %+v", list)
	}
	if cur, _ := reg.Current(); cur != "a" { // the last Add wins, including a re-add
		t.Errorf("current = %q", cur)
	}
	if err := reg.SetCurrent("b"); err != nil {
		t.Fatal(err)
	}
	if cur, _ := reg.Current(); cur != "b" {
		t.Errorf("current = %q", cur)
	}
	if err := reg.SetCurrent("zzz"); err == nil {
		t.Error("unknown trip accepted")
	}
	// survives a new process
	again := &Registry{Path: reg.Path}
	if cur, _ := again.Current(); cur != "b" {
		t.Errorf("not persisted: %q", cur)
	}
}

func TestRegistryReportsACorruptFileInsteadOfLosingTheList(t *testing.T) {
	p := filepath.Join(t.TempDir(), "trips.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := &Registry{Path: p}
	if _, err := reg.List(); err == nil {
		t.Error("corrupt file read as empty")
	}
	if err := reg.Add(drive.Trip{SheetID: "a"}); err == nil {
		t.Error("a corrupt file must not be overwritten")
	}
	if b, _ := os.ReadFile(p); string(b) != "{not json" {
		t.Errorf("file was overwritten: %s", b)
	}
}

func TestOnlyTitleAndDatesAreNeededTheRestGetsTheShownDefaults(t *testing.T) {
	e := setup(t)
	if _, err := e.svc.Create(context.Background(), "o@x.com", "나", Request{Title: "제주", Start: "2027-05-01", End: "2027-05-03"}); err != nil {
		t.Fatalf("a trip with only a title and dates must be creatable: %v", err)
	}
	rows, _ := e.grid.RawRows(context.Background(), tripconfig.SheetName)
	cfg := tripconfig.Parse(rows, true)
	d := Defaults()
	if !cfg.Configured() || cfg.Timezone != d["timezone"] || cfg.LocalCurrency != d["localCurrency"] || cfg.LocalSymbol != d["localSymbol"] ||
		cfg.Rate != 9.3 || strings.Join(cfg.Payers, ", ") != d["payers"] {
		t.Errorf("defaults not applied: %+v vs %v", cfg, d)
	}
	if d["timezone"] != "Asia/Tokyo" || d["localCurrency"] != "엔" || d["localSymbol"] != "¥" || d["rate"] != "9.3" {
		t.Errorf("the defaults the form shows changed: %v", d)
	}
}

func TestTypedValuesBeatTheDefaults(t *testing.T) {
	e := setup(t)
	r := Request{Title: "방콕", Start: "2027-05-01", End: "2027-05-03", Timezone: "Asia/Bangkok", LocalCurrency: "바트", LocalSymbol: "฿", Rate: "38"}
	if _, err := e.svc.Create(context.Background(), "o@x.com", "나", r); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.grid.RawRows(context.Background(), tripconfig.SheetName)
	cfg := tripconfig.Parse(rows, true)
	if cfg.Timezone != "Asia/Bangkok" || cfg.LocalCurrency != "바트" || cfg.LocalSymbol != "฿" || cfg.Rate != 38 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestDefaultsReturnsACopy(t *testing.T) {
	Defaults()["timezone"] = "Mars/Base"
	if Defaults()["timezone"] != "Asia/Tokyo" {
		t.Error("callers can change the defaults")
	}
}

func TestEveryPresetMakesAConfiguredTrip(t *testing.T) {
	list := Presets()
	if len(list) < 10 {
		t.Fatalf("too few destinations to be useful: %d", len(list))
	}
	seen := map[string]bool{}
	for _, p := range list {
		if p.Name == "" || seen[p.Name] {
			t.Errorf("name empty or duplicated: %q", p.Name)
		}
		seen[p.Name] = true
		e := setup(t)
		r := Request{Title: "t", Start: "2027-05-01", End: "2027-05-02", Timezone: p.Timezone, LocalCurrency: p.Currency, LocalSymbol: p.Symbol, Rate: p.Rate}
		if _, err := e.svc.Create(context.Background(), "o@x.com", "나", r); err != nil {
			t.Errorf("%s: %v", p.Name, err)
			continue
		}
		rows, _ := e.grid.RawRows(context.Background(), tripconfig.SheetName)
		if cfg := tripconfig.Parse(rows, true); !cfg.Configured() || cfg.Rate <= 0 {
			t.Errorf("%s: not usable: %+v", p.Name, cfg)
		}
	}
}

func TestTheDefaultsAreOnePreset(t *testing.T) {
	d := Defaults()
	for _, p := range Presets() {
		if p.Timezone == d["timezone"] && p.Currency == d["localCurrency"] && p.Symbol == d["localSymbol"] && p.Rate == d["rate"] {
			return
		}
	}
	t.Errorf("no preset matches the defaults %v: a blank form and the first choice must agree", d)
}

func TestPresetsReturnsACopy(t *testing.T) {
	Presets()[0].Name = "바뀜"
	if Presets()[0].Name == "바뀜" {
		t.Error("callers can change the list")
	}
}

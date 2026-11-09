package expenses

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"tourmanager/internal/audit"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

// Column order is fixed (reads are positional). The first ten columns are the Apps Script version's,
// so a sheet written by either server reads in both; the last three are author tracking.
const (
	colDate = iota
	colTime
	colCategory
	colItem
	colAmount
	colHome
	colPayer
	colMethod
	colID
	colCurrency
	colCreatedBy
	colUpdatedBy
	colUpdatedAt
	numCols
)

// Expense is one stored entry, as the dashboard receives it.
type Expense struct {
	ID        string  `json:"id"`
	Date      string  `json:"date"`
	Time      string  `json:"time"`
	Category  string  `json:"category"`
	Item      string  `json:"item"`
	Amount    float64 `json:"amount"`
	Home      float64 `json:"home"`
	Payer     string  `json:"payer"`
	Method    string  `json:"method"`
	Currency  string  `json:"currency"` // "local" | "home" (the currency it was typed in)
	CreatedBy string  `json:"createdBy"`
	UpdatedBy string  `json:"updatedBy"`
	UpdatedAt string  `json:"updatedAt"` // pass back as baseUpdatedAt when editing or deleting
}

type Kind int

const (
	NotFound Kind = iota + 1
	Conflict
)

// Error is a non-validation failure the caller maps to a status (404 / 409).
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func Header(cfg tripconfig.Config) []string {
	return []string{"날짜", "시간", "분류", "내용", "금액(" + cfg.LocalCurrency + ")", "환산(" + cfg.HomeSymbol + ")", "결제자", "결제수단", "ID", "입력 통화", "기록자", "수정자", "수정시각"}
}

var unescapeRe = regexp.MustCompile(`^'[=+\-@\t\r]`)

// unescape undoes the apostrophe the Apps Script version put in front of formula-looking text.
func unescape(v string) string {
	if unescapeRe.MatchString(v) {
		return v[1:]
	}
	return v
}

func cell(row []string, i int) string {
	if i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

func num(s string) float64 {
	f, ok := number(s)
	if !ok {
		return 0
	}
	return f
}

// A sheet may have turned a typed date / time into a number (days since 1899-12-30, or a fraction of a day).
func sheetDate(s string) string {
	if f, ok := number(s); ok && s != "" && f > 20000 && f < 80000 {
		return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(f)).Format("2006-01-02")
	}
	return s
}

func sheetTime(s string) string {
	if f, ok := number(s); ok && s != "" && f >= 0 && f < 1 {
		m := int(f*1440 + 0.5)
		return fmt.Sprintf("%02d:%02d", m/60%24, m%60)
	}
	return s
}

func fromRow(cfg tripconfig.Config, row []string) (Expense, bool) {
	if cell(row, colID) == "" {
		return Expense{}, false
	}
	cur := "local"
	if cell(row, colCurrency) == cfg.HomeSymbol {
		cur = "home"
	}
	item := ""
	if colItem < len(row) {
		item = unescape(row[colItem])
	}
	return Expense{
		ID: cell(row, colID), Date: sheetDate(cell(row, colDate)), Time: sheetTime(cell(row, colTime)), Category: cell(row, colCategory), Item: item,
		Amount: num(cell(row, colAmount)), Home: num(cell(row, colHome)), Payer: cell(row, colPayer), Method: cell(row, colMethod),
		Currency: cur, CreatedBy: cell(row, colCreatedBy), UpdatedBy: cell(row, colUpdatedBy), UpdatedAt: cell(row, colUpdatedAt),
	}, true
}

func toRow(c Cleaned, id, clock, createdBy, updatedBy, updatedAt string) []any {
	return []any{c.Date, clock, c.Category, c.Item, c.Amount, c.Home, c.Payer, c.Method, id, c.Currency, createdBy, updatedBy, updatedAt}
}

// Service holds the write path. One server process serialises its writes; the row is looked up again by ID
// right before every replace / delete, so a row that moved (someone sorted the sheet) is never overwritten blindly.
type Service struct {
	Grid sheets.Grid
	Log  audit.Log
	Now  func() time.Time

	mu sync.Mutex
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// Ensure creates the expense tab with its header when needed.
func (s *Service) Ensure(ctx context.Context, cfg tripconfig.Config) error {
	_, err := s.Grid.EnsureTab(ctx, cfg.ExpenseSheet, Header(cfg))
	return err
}

// rows reads the expense tab. A missing tab (first use, or someone deleted it) is created with its header.
func (s *Service) rows(ctx context.Context, cfg tripconfig.Config) ([][]string, error) {
	rows, err := s.Grid.RawRows(ctx, cfg.ExpenseSheet)
	if errors.Is(err, sheets.ErrNoSheet) {
		if _, err := s.Grid.EnsureTab(ctx, cfg.ExpenseSheet, Header(cfg)); err != nil {
			return nil, err
		}
		rows, err = s.Grid.RawRows(ctx, cfg.ExpenseSheet)
		return rows, err
	}
	return rows, err
}

func (s *Service) List(ctx context.Context, cfg tripconfig.Config) ([]Expense, error) {
	rows, err := s.rows(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return Parse(cfg, rows), nil
}

// Parse reads expense rows (header first) the way the service does.
func Parse(cfg tripconfig.Config, rows [][]string) []Expense {
	out := []Expense{}
	for i, r := range rows {
		if i == 0 {
			continue // header
		}
		if e, ok := fromRow(cfg, r); ok {
			out = append(out, e)
		}
	}
	return out
}

// find returns the 1-based sheet row of an ID, or 0.
func find(rows [][]string, id string) int {
	for i := 1; i < len(rows); i++ {
		if cell(rows[i], colID) == id {
			return i + 1
		}
	}
	return 0
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *Service) record(ctx context.Context, who, id, action string, before, after *Expense) {
	if s.Log == nil {
		return
	}
	js := func(e *Expense) string {
		if e == nil {
			return ""
		}
		b, _ := json.Marshal(e)
		return string(b)
	}
	// History is best effort: the data change already happened and must not be reported as failed.
	if err := s.Log.Record(ctx, audit.Entry{At: s.now(), Email: who, Tab: "지출", ItemID: id, Action: action, Before: js(before), After: js(after)}); err != nil {
		log.Printf("audit: %s %s %s: %v", action, id, who, err)
	}
}

// Add stores a new entry. An ID that is already in the sheet is not added again (a retried request is harmless).
func (s *Service) Add(ctx context.Context, cfg tripconfig.Config, who string, in Input) ([]Expense, error) {
	c, err := Clean(cfg, in)
	if err != nil {
		return nil, err
	}
	id := in.ID
	if !IsClientID(id) {
		id = newUUID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if find(rows, id) == 0 {
		loc, lerr := time.LoadLocation(cfg.Timezone)
		if lerr != nil {
			loc = time.UTC
		}
		at := stamp(s.now())
		if err := s.Grid.Append(ctx, cfg.ExpenseSheet, toRow(c, id, s.now().In(loc).Format("15:04"), who, who, at)); err != nil {
			return nil, err
		}
		e, _ := fromRow(cfg, anyToStrings(toRow(c, id, s.now().In(loc).Format("15:04"), who, who, at)))
		s.record(ctx, who, id, "add", nil, &e)
	}
	return s.List(ctx, cfg)
}

func anyToStrings(row []any) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = sheets.CellString(c)
	}
	return out
}

// check: base == nil → last write wins; otherwise it must equal the row's current UpdatedAt.
func conflict(cur Expense, base *string) error {
	if base != nil && *base != cur.UpdatedAt {
		return &Error{Kind: Conflict, Msg: "다른 사람이 먼저 수정했습니다. 새로고침 후 다시 시도하세요"}
	}
	return nil
}

// Update replaces an entry (keeping its original time and creator).
func (s *Service) Update(ctx context.Context, cfg tripconfig.Config, who, id string, in Input, base *string) ([]Expense, error) {
	c, err := Clean(cfg, in)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx, cfg)
	if err != nil {
		return nil, err
	}
	n := find(rows, id)
	if n == 0 {
		return nil, &Error{Kind: NotFound, Msg: "이미 삭제된 지출입니다"}
	}
	before, _ := fromRow(cfg, rows[n-1])
	if err := conflict(before, base); err != nil {
		return nil, err
	}
	createdBy := before.CreatedBy
	if createdBy == "" {
		createdBy = who
	}
	row := toRow(c, id, before.Time, createdBy, who, stamp(s.now()))
	if err := s.Grid.UpdateRow(ctx, cfg.ExpenseSheet, n, row); err != nil {
		return nil, err
	}
	after, _ := fromRow(cfg, anyToStrings(row))
	s.record(ctx, who, id, "update", &before, &after)
	return s.List(ctx, cfg)
}

// Delete removes an entry; its last values stay in the change history.
func (s *Service) Delete(ctx context.Context, cfg tripconfig.Config, who, id string, base *string) ([]Expense, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx, cfg)
	if err != nil {
		return nil, err
	}
	n := find(rows, id)
	if n == 0 {
		return nil, &Error{Kind: NotFound, Msg: "이미 삭제된 지출입니다"}
	}
	before, _ := fromRow(cfg, rows[n-1])
	if err := conflict(before, base); err != nil {
		return nil, err
	}
	if err := s.Grid.DeleteRow(ctx, cfg.ExpenseSheet, n); err != nil {
		return nil, err
	}
	s.record(ctx, who, id, "delete", &before, nil)
	return s.List(ctx, cfg)
}

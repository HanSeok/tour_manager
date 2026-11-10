// Package places stores the places a trip's members add themselves (tap the map, name it) in the 장소 tab.
// My Maps is read-only for us, so these live in the owner's spreadsheet next to the expenses, with the same rules:
// RAW writes, a client-made id for idempotent adds, a per-row version for conflict detection, and a change history.
package places

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"tourmanager/internal/audit"
	"tourmanager/internal/maplink"
	"tourmanager/internal/sheets"
)

const (
	Tab         = "장소"
	MaxPlaces   = 500
	maxNameLen  = 60
	maxMemoLen  = 200
	stampLayout = "2006-01-02T15:04:05.000Z"
)

// Header is the first row of the 장소 tab. Reads are positional.
var Header = []string{"ID", "이름", "분류", "위도", "경도", "메모", "기록자", "수정자", "수정시각", "링크", "영업시간"}

const (
	colID = iota
	colName
	colLayer
	colLat
	colLng
	colMemo
	colCreatedBy
	colUpdatedBy
	colUpdatedAt
	colLink
	colHours
)

// Layers are the categories the dashboard offers (colour and filter).
var Layers = []string{"식당", "관광", "쇼핑", "숙소", "교통"}

var (
	numberRe   = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)
	clientIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Place is one stored place, as the dashboard receives it.
type Place struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Layer     string        `json:"layer"`
	Lat       float64       `json:"lat"`
	Lng       float64       `json:"lng"`
	Memo      string        `json:"memo"`
	CreatedBy string        `json:"createdBy"`
	UpdatedBy string        `json:"updatedBy"`
	UpdatedAt string        `json:"updatedAt"`       // pass back as baseUpdatedAt when editing or deleting
	Link      string        `json:"link"`            // where the place came from (a Google / Kakao map link), "" if it was tapped on the map
	Hours     maplink.Hours `json:"hours,omitempty"` // opening hours when the source gave them (Kakao); nil = unknown
}

// Input is what the dashboard submits. Coordinates arrive as JSON numbers or numeric strings.
type Input struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Layer string        `json:"layer"`
	Lat   any           `json:"lat"`
	Lng   any           `json:"lng"`
	Memo  string        `json:"memo"`
	Link  string        `json:"link"`
	Hours maplink.Hours `json:"hours"`
}

// ValidationError is a user-facing input problem (HTTP 422).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

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

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case string:
		s := strings.TrimSpace(x)
		if !numberRe.MatchString(s) {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil && !math.IsInf(f, 0)
	}
	return 0, false
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

type cleaned struct {
	Name, Layer, Memo, Link string
	Lat, Lng                float64
	Hours                   maplink.Hours
}

func clean(in Input) (cleaned, error) {
	name := strings.TrimSpace(in.Name)
	memo := strings.TrimSpace(in.Memo)
	link := strings.TrimSpace(in.Link)
	switch {
	case name == "":
		return cleaned{}, invalid("장소 이름을 입력하세요")
	case utf8.RuneCountInString(name) > maxNameLen:
		return cleaned{}, invalid("장소 이름은 %d자까지 입니다", maxNameLen)
	case hasControl(name):
		return cleaned{}, invalid("장소 이름에 줄바꿈이나 제어 문자를 쓸 수 없습니다")
	case utf8.RuneCountInString(memo) > maxMemoLen:
		return cleaned{}, invalid("메모는 %d자까지 입니다", maxMemoLen)
	case link != "" && !maplink.AllowedLink(link):
		return cleaned{}, invalid("링크는 구글 지도·카카오맵 주소(https)만 저장할 수 있습니다")
	}
	ok := false
	for _, l := range Layers {
		ok = ok || l == in.Layer
	}
	if !ok {
		return cleaned{}, invalid("분류는 %s 중 하나여야 합니다", strings.Join(Layers, ", "))
	}
	hours, okHours := maplink.CleanHours(in.Hours)
	if !okHours {
		return cleaned{}, invalid("영업시간이 올바르지 않습니다")
	}
	lat, okLat := number(in.Lat)
	lng, okLng := number(in.Lng)
	switch {
	case !okLat || !okLng:
		return cleaned{}, invalid("위치(위도·경도)가 올바르지 않습니다")
	case lat < -90 || lat > 90 || lng < -180 || lng > 180:
		return cleaned{}, invalid("위치가 범위를 벗어났습니다")
	case lat == 0 && lng == 0:
		return cleaned{}, invalid("위치를 지도에서 골라 주세요") // 0,0 is what a missing value looks like
	}
	return cleaned{Name: name, Layer: in.Layer, Memo: memo, Link: link, Lat: lat, Lng: lng, Hours: hours}, nil
}

func cell(row []string, i int) string {
	if i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

func fromRow(row []string) (Place, bool) {
	if cell(row, colID) == "" {
		return Place{}, false
	}
	lat, _ := number(cell(row, colLat))
	lng, _ := number(cell(row, colLng))
	name := ""
	if colName < len(row) {
		name = row[colName] // not trimmed away: what was stored is what comes back
	}
	return Place{ID: cell(row, colID), Name: name, Layer: cell(row, colLayer), Lat: lat, Lng: lng, Memo: cell(row, colMemo),
		CreatedBy: cell(row, colCreatedBy), UpdatedBy: cell(row, colUpdatedBy), UpdatedAt: cell(row, colUpdatedAt), Link: cell(row, colLink), Hours: hoursOfCell(cell(row, colHours))}, true
}

// hoursOfCell reads the 영업시간 cell. The sheet can be edited by hand, so anything that is not clean hours is "unknown".
func hoursOfCell(cell string) maplink.Hours {
	if cell == "" {
		return nil
	}
	var h maplink.Hours
	if json.Unmarshal([]byte(cell), &h) != nil {
		return nil
	}
	h, _ = maplink.CleanHours(h)
	return h
}

func toRow(c cleaned, id, createdBy, updatedBy, updatedAt string) []any {
	hours := ""
	if len(c.Hours) > 0 {
		b, _ := json.Marshal(c.Hours) // weekday keys sort as text: "0" … "6"
		hours = string(b)
	}
	return []any{id, c.Name, c.Layer, c.Lat, c.Lng, c.Memo, createdBy, updatedBy, updatedAt, c.Link, hours}
}

func anyToStrings(row []any) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = sheets.CellString(c)
	}
	return out
}

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

func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

// Service holds the write path. One server process serialises its writes; the row is looked up again by ID right before
// every replace / delete, so a row that moved (someone sorted the sheet) is never overwritten blindly.
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

// rows reads the tab; a missing tab (a trip made before places existed) is created with its header.
func (s *Service) rows(ctx context.Context) ([][]string, error) {
	rows, err := s.Grid.RawRows(ctx, Tab)
	if errors.Is(err, sheets.ErrNoSheet) {
		if _, err := s.Grid.EnsureTab(ctx, Tab, Header); err != nil {
			return nil, err
		}
		return s.Grid.RawRows(ctx, Tab)
	}
	if err == nil && len(rows) > 0 && len(rows[0]) < len(Header) { // a tab made before a column was added: complete its header
		head := make([]any, len(Header))
		for i, h := range Header {
			head[i] = h
		}
		if err := s.Grid.UpdateRow(ctx, Tab, 1, head); err != nil {
			return nil, err
		}
		rows[0] = append([]string(nil), Header...)
	}
	return rows, err
}

func parse(rows [][]string) []Place {
	out := []Place{}
	for i, r := range rows {
		if i == 0 {
			continue
		}
		if p, ok := fromRow(r); ok {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) List(ctx context.Context) ([]Place, error) {
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	return parse(rows), nil
}

func (s *Service) record(ctx context.Context, who, id, action string, before, after *Place) {
	if s.Log == nil {
		return
	}
	js := func(p *Place) string {
		if p == nil {
			return ""
		}
		b, _ := json.Marshal(p)
		return string(b)
	}
	// History is best effort: the data change already happened and must not be reported as failed.
	if err := s.Log.Record(ctx, audit.Entry{At: s.now(), Email: who, Tab: Tab, ItemID: id, Action: action, Before: js(before), After: js(after)}); err != nil {
		log.Printf("audit: %s %s %s: %v", action, id, who, err)
	}
}

// Add stores a new place. An ID already in the sheet is not added again (a retried request is harmless).
func (s *Service) Add(ctx context.Context, who string, in Input) ([]Place, error) {
	c, err := clean(in)
	if err != nil {
		return nil, err
	}
	id := in.ID
	if !clientIDRe.MatchString(id) {
		id = newUUID()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	if find(rows, id) == 0 {
		if len(rows)-1 >= MaxPlaces {
			return nil, invalid("장소는 %d곳까지 둘 수 있습니다. 안 쓰는 장소를 지워 주세요", MaxPlaces)
		}
		row := toRow(c, id, who, who, stamp(s.now()))
		if err := s.Grid.Append(ctx, Tab, row); err != nil {
			return nil, err
		}
		p, _ := fromRow(anyToStrings(row))
		s.record(ctx, who, id, "add", nil, &p)
	}
	return s.List(ctx)
}

func conflict(cur Place, base *string) error {
	if base != nil && *base != cur.UpdatedAt {
		return &Error{Kind: Conflict, Msg: "다른 사람이 먼저 수정했습니다. 새로고침 후 다시 시도하세요"}
	}
	return nil
}

// Update replaces a place (keeping its creator). base == nil → last write wins; otherwise it must be the version the editor saw.
func (s *Service) Update(ctx context.Context, who, id string, in Input, base *string) ([]Place, error) {
	c, err := clean(in)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	n := find(rows, id)
	if n == 0 {
		return nil, &Error{Kind: NotFound, Msg: "이미 삭제된 장소입니다"}
	}
	before, _ := fromRow(rows[n-1])
	if err := conflict(before, base); err != nil {
		return nil, err
	}
	createdBy := before.CreatedBy
	if createdBy == "" {
		createdBy = who
	}
	// The version must change on every write, or a stale edit holding the old value would pass the conflict check.
	at := stamp(s.now())
	if before.UpdatedAt != "" && at <= before.UpdatedAt {
		if prev, perr := time.Parse(stampLayout, before.UpdatedAt); perr == nil {
			at = stamp(prev.Add(time.Millisecond))
		}
	}
	row := toRow(c, id, createdBy, who, at)
	if err := s.Grid.UpdateRow(ctx, Tab, n, row); err != nil {
		return nil, err
	}
	after, _ := fromRow(anyToStrings(row))
	s.record(ctx, who, id, "update", &before, &after)
	return s.List(ctx)
}

// Delete removes a place; its last values stay in the change history.
func (s *Service) Delete(ctx context.Context, who, id string, base *string) ([]Place, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	n := find(rows, id)
	if n == 0 {
		return nil, &Error{Kind: NotFound, Msg: "이미 삭제된 장소입니다"}
	}
	before, _ := fromRow(rows[n-1])
	if err := conflict(before, base); err != nil {
		return nil, err
	}
	if err := s.Grid.DeleteRow(ctx, Tab, n); err != nil {
		return nil, err
	}
	s.record(ctx, who, id, "delete", &before, nil)
	return s.List(ctx)
}

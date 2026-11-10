// Package state keeps what companions plan together — the day routes, where each day starts, the places a calendar event
// was matched to, and the visit records — in the "동선" tab, one row per day (or event) so a sheet owner can read and fix it.
//
// The dashboard sends only what it changed ({day: value | null}); the server merges that into what is stored, under one
// lock, so two companions editing different days never overwrite each other. Rewriting the same day is last-writer-wins.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"tourmanager/internal/audit"
	"tourmanager/internal/sheets"
)

// Tab is the sheet tab; Header its first row. Reads are positional.
const Tab = "동선"

var Header = []string{"키", "하위키", "값", "수정자", "수정시각"}

const (
	colKey = iota
	colSub
	colValue
	colBy
	colAt
)

const (
	stampLayout = "2006-01-02T15:04:05.000Z"
	maxStops    = 60
	maxVisits   = 200
	maxCell     = 20000 // one day's JSON; a sheet cell holds 50000 characters
	maxPatch    = 120
	maxShort    = 200
	maxEventKey = 300
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Keys are the shared pieces of state, each a map from a sub-key (a date, or a calendar event) to a JSON value.
var Keys = []string{"routes", "starts", "startFrom", "eventPlace", "visits"}

// dateKeyed keys have a date as sub-key, and so can be checked against the trip's dates.
var dateKeyed = map[string]bool{"routes": true, "starts": true, "startFrom": true, "visits": true}

// ValidationError is a user-facing input problem (HTTP 422).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

type object = map[string]json.RawMessage

func isShort(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil || s == "" || utf8.RuneCountInString(s) > maxShort {
		return "", false
	}
	return s, true
}

func asObject(raw json.RawMessage) (object, bool) {
	var o object
	return o, json.Unmarshal(raw, &o) == nil && o != nil
}

func asList(raw json.RawMessage) ([]json.RawMessage, bool) {
	var l []json.RawMessage
	return l, json.Unmarshal(raw, &l) == nil && l != nil
}

// checkValue says whether `raw` is an acceptable value of key. One bad write must not break the page for everyone.
func checkValue(key, sub string, raw json.RawMessage) error {
	bad := func(what string) error {
		return invalid("저장 형식이 올바르지 않습니다: %s (%s)", key, what)
	}
	if len(raw) > maxCell {
		return invalid("하루 분량의 데이터가 너무 큽니다")
	}
	switch key {
	case "routes":
		stops, ok := asList(raw)
		if !ok {
			return bad("하루 동선은 목록이어야 합니다")
		}
		if len(stops) > maxStops {
			return invalid("하루 동선은 %d곳까지 둘 수 있습니다", maxStops)
		}
		for _, s := range stops {
			o, ok := asObject(s)
			if !ok {
				return bad("동선의 각 항목은 객체여야 합니다")
			}
			_, hasEv := isShort(o["ev"])
			_, hasID := isShort(o["id"])
			_, hasSid := isShort(o["sid"])
			if !hasEv && !hasID && !hasSid {
				return bad("동선 항목에 장소나 일정이 없습니다")
			}
			if st, ok := o["stayMin"]; ok {
				var n float64
				if json.Unmarshal(st, &n) != nil || n < 0 || n > 1440 {
					return bad("체류 시간")
				}
			}
		}
	case "starts":
		var n float64
		if json.Unmarshal(raw, &n) != nil || n != float64(int(n)) || n < 0 || n >= 1440 {
			return bad("하루의 분(0~1439)이어야 합니다")
		}
	case "startFrom":
		if _, ok := isShort(raw); !ok {
			return bad("출발 장소 ID")
		}
	case "eventPlace":
		if _, ok := isShort(raw); !ok || len(sub) > maxEventKey {
			return bad("장소 ID")
		}
	case "visits":
		l, ok := asList(raw)
		if !ok || len(l) > maxVisits {
			return bad("방문 기록은 목록이어야 합니다")
		}
		for _, v := range l {
			if _, ok := asObject(v); !ok {
				return bad("방문 기록의 각 항목은 객체여야 합니다")
			}
		}
	}
	return nil
}

func known(key string) bool {
	for _, k := range Keys {
		if k == key {
			return true
		}
	}
	return false
}

// Service is the read and write path. One server process serialises its writes.
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

func cell(row []string, i int) string {
	if i < len(row) {
		return strings.TrimSpace(row[i])
	}
	return ""
}

// rows reads the tab; a missing tab (a trip made before shared routes existed) is created with its header.
func (s *Service) rows(ctx context.Context) ([][]string, error) {
	rows, err := s.Grid.RawRows(ctx, Tab)
	if errors.Is(err, sheets.ErrNoSheet) {
		if _, err := s.Grid.EnsureTab(ctx, Tab, Header); err != nil {
			return nil, err
		}
		return s.Grid.RawRows(ctx, Tab)
	}
	return rows, err
}

// parse reads every row into key → sub-key → value. Rows of an unknown key, with a broken value, or repeated are skipped
// (the sheet is hand-editable: a typo must not take the page down).
func parse(rows [][]string) map[string]object {
	out := map[string]object{}
	for _, k := range Keys {
		out[k] = object{}
	}
	for i := 1; i < len(rows); i++ {
		key, sub, val := cell(rows[i], colKey), cell(rows[i], colSub), cell(rows[i], colValue)
		if !known(key) || sub == "" || !json.Valid([]byte(val)) {
			continue
		}
		if dateKeyed[key] && !dateRe.MatchString(sub) {
			continue
		}
		if checkValue(key, sub, json.RawMessage(val)) != nil {
			continue
		}
		out[key][sub] = json.RawMessage(val)
	}
	return out
}

// All returns the whole shared state: every key, empty when nothing was saved yet.
func (s *Service) All(ctx context.Context) (map[string]object, error) {
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	return parse(rows), nil
}

func (s *Service) record(ctx context.Context, who, key, sub, action, before, after string) {
	if s.Log == nil {
		return
	}
	// History is best effort: the data change already happened and must not be reported as failed.
	if err := s.Log.Record(ctx, audit.Entry{At: s.now(), Email: who, Tab: Tab, ItemID: key + ":" + sub, Action: action, Before: before, After: after}); err != nil {
		log.Printf("audit: %s %s:%s %s: %v", action, key, sub, who, err)
	}
}

func rowOf(key, sub, val, who string, at time.Time) []any {
	return []any{key, sub, val, who, at.UTC().Format(stampLayout)}
}

// Save merges patch ({sub-key: value | null}) into `key` and returns the merged value of that key. inTrip tells which dates
// belong to the trip. Nothing is written unless the whole patch is acceptable.
func (s *Service) Save(ctx context.Context, who, key string, patch map[string]json.RawMessage, inTrip func(date string) bool) (map[string]json.RawMessage, error) {
	if !known(key) {
		return nil, invalid("알 수 없는 상태 키: %s", key)
	}
	if len(patch) > maxPatch {
		return nil, invalid("한 번에 저장할 수 있는 양을 넘었습니다")
	}
	for sub, raw := range patch {
		if sub == "" {
			return nil, invalid("저장 형식이 올바르지 않습니다: %s", key)
		}
		if dateKeyed[key] {
			if !dateRe.MatchString(sub) {
				return nil, invalid("날짜 형식이 올바르지 않습니다: %s", sub)
			}
			if inTrip != nil && !inTrip(sub) && string(raw) != "null" {
				return nil, invalid("날짜가 여행 기간이 아닙니다: %s", sub)
			}
		}
		if string(raw) == "null" {
			continue
		}
		if err := checkValue(key, sub, raw); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.rows(ctx)
	if err != nil {
		return nil, err
	}
	at := s.now()
	at0 := map[string]int{} // sub-key → sheet row number (1-based) of this key
	for i := 1; i < len(rows); i++ {
		if cell(rows[i], colKey) == key {
			if _, dup := at0[cell(rows[i], colSub)]; !dup {
				at0[cell(rows[i], colSub)] = i + 1
			}
		}
	}
	subs := make([]string, 0, len(patch))
	for sub := range patch {
		subs = append(subs, sub)
	}
	sort.Strings(subs)
	var deletes []int
	for _, sub := range subs {
		raw, n := patch[sub], at0[sub]
		before := ""
		if n > 0 {
			before = cell(rows[n-1], colValue)
		}
		switch {
		case string(raw) == "null" && n > 0:
			deletes = append(deletes, n)
			s.record(ctx, who, key, sub, "delete", before, "")
		case string(raw) == "null":
		case n > 0:
			if err := s.Grid.UpdateRow(ctx, Tab, n, rowOf(key, sub, string(raw), who, at)); err != nil {
				return nil, err
			}
			s.record(ctx, who, key, sub, "update", before, string(raw))
		default:
			if err := s.Grid.Append(ctx, Tab, rowOf(key, sub, string(raw), who, at)); err != nil {
				return nil, err
			}
			s.record(ctx, who, key, sub, "add", "", string(raw))
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(deletes))) // bottom first, so the rows above keep their numbers
	for _, n := range deletes {
		if err := s.Grid.DeleteRow(ctx, Tab, n); err != nil {
			return nil, err
		}
	}
	rows, err = s.Grid.RawRows(ctx, Tab)
	if err != nil {
		return nil, err
	}
	return parse(rows)[key], nil
}

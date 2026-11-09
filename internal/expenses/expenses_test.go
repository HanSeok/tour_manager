package expenses

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"tourmanager/internal/audit"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

func cfgOf(mut func(*tripconfig.Config)) tripconfig.Config {
	c := tripconfig.Config{
		Start: "2027-04-10", End: "2027-04-14", Timezone: "Asia/Tokyo", CurrencyDecimals: 2, HomeDecimals: 0, Rate: 1450,
		Payers: []string{"나", "동행"}, Methods: []string{"카드", "현금"}, LocalCurrency: "유로", HomeSymbol: "₩", ExpenseSheet: "지출",
	}
	if mut != nil {
		mut(&c)
	}
	return c
}

func in(mut func(*Input)) Input {
	i := Input{Date: "2027-04-11", Category: "식비", Payer: "나", Method: "카드", Amount: 4.5}
	if mut != nil {
		mut(&i)
	}
	return i
}

func TestCleanDecimalsHomeCurrencyAndDates(t *testing.T) { // ported from server.test.mjs "cleanExpense_"
	c := cfgOf(nil)
	got, err := Clean(c, in(nil))
	if err != nil || fmt.Sprint(got.Amount, "|", got.Home, "|", got.Item, "|", got.Currency) != "4.5|6525|식비|유로" {
		t.Fatalf("%+v %v", got, err)
	}
	if g, _ := Clean(c, in(func(i *Input) { i.Amount = 1.1 })); g.Amount != 1.1 {
		t.Errorf("float 1.1: %v", g.Amount)
	}
	if _, err := Clean(c, in(func(i *Input) { i.Amount = 4.555 })); err == nil || !strings.Contains(err.Error(), "소수 2자리") {
		t.Errorf("4.555: %v", err)
	}
	if _, err := Clean(c, in(func(i *Input) { i.Amount = []any{"2027-04-11"} })); err == nil || !strings.Contains(err.Error(), "금액") {
		t.Errorf("array amount: %v", err)
	}
	h, err := Clean(c, in(func(i *Input) { i.Amount = 290000.0; i.Currency = "home"; i.Date = "2026-12-01" })) // pre-paid flight typed in ₩
	if err != nil || fmt.Sprint(h.Amount, "|", h.Home, "|", h.Currency) != "200|290000|₩" {
		t.Errorf("home input: %+v %v", h, err)
	}
	for _, d := range []string{"2026-04-09", "2027-04-15", "2027-02-30", ""} {
		if _, err := Clean(c, in(func(i *Input) { i.Amount = 1.0; i.Date = d })); err == nil || !strings.Contains(err.Error(), "날짜") {
			t.Errorf("date %q: %v", d, err)
		}
	}
	z := cfgOf(func(c *tripconfig.Config) { c.CurrencyDecimals = 0 })
	if _, err := Clean(z, in(func(i *Input) { i.Amount = 1.5 })); err == nil || !strings.Contains(err.Error(), "정수") {
		t.Errorf("1.5 with 0 decimals: %v", err)
	}
	if _, err := Clean(z, in(func(i *Input) { i.Amount = 290000.5; i.Currency = "home" })); err == nil || !strings.Contains(err.Error(), "정수") {
		t.Errorf("₩ has no decimals: %v", err)
	}
	usd := cfgOf(func(c *tripconfig.Config) {
		c.CurrencyDecimals = 0
		c.HomeSymbol = "$"
		c.HomeDecimals = 2
		c.Rate = 0.0067
	})
	if g, _ := Clean(usd, in(func(i *Input) { i.Amount = 1500.0 })); g.Home != 10.05 {
		t.Errorf("cents kept: %v", g.Home)
	}
	if _, err := Clean(c, in(func(i *Input) { i.Payer = "모르는 사람" })); err == nil || !strings.Contains(err.Error(), "결제자") {
		t.Errorf("unknown payer: %v", err)
	}
}

func TestCleanAmountEdgesAndItem(t *testing.T) {
	c := cfgOf(nil)
	for _, bad := range []any{0.0, -1.0, "", "abc", "inf", "NaN", "0x10", 1e9, nil, true} {
		if _, err := Clean(c, in(func(i *Input) { i.Amount = bad })); err == nil {
			t.Errorf("amount %v must be rejected", bad)
		}
	}
	if g, err := Clean(c, in(func(i *Input) { i.Amount = " 12.5 " })); err != nil || g.Amount != 12.5 {
		t.Errorf("numeric string: %+v %v", g, err)
	}
	long := strings.Repeat("가", 80)
	if g, _ := Clean(c, in(func(i *Input) { i.Item = "  " + long + "  " })); len([]rune(g.Item)) != 60 {
		t.Errorf("item must be trimmed to 60: %d", len([]rune(g.Item)))
	}
	if g, _ := Clean(c, in(func(i *Input) { i.Item = "   " })); g.Item != "식비" {
		t.Errorf("blank item falls back to the category: %q", g.Item)
	}
	if _, err := Clean(c, in(func(i *Input) { i.Category = "주식" })); err == nil {
		t.Error("unknown category")
	}
}

func TestRowCodecLegacyAndSerials(t *testing.T) {
	c := cfgOf(nil)
	// a 10-column row from the Apps Script version, a formula-looking item stored with its escape apostrophe
	e, ok := fromRow(c, []string{"2027-04-11", "14:05", "식비", "'=SUM(1)", "4.5", "6525", "나", "카드", "id-1", "유로"})
	if !ok || e.Item != "=SUM(1)" || e.Currency != "local" || e.Amount != 4.5 || e.CreatedBy != "" {
		t.Fatalf("%+v %v", e, ok)
	}
	if e, _ := fromRow(c, []string{"2027-04-11", "14:05", "식비", "x", "1", "1450", "나", "카드", "id-2", "₩"}); e.Currency != "home" {
		t.Errorf("home currency mark: %+v", e)
	}
	if _, ok := fromRow(c, []string{"2027-04-11", "14:05", "식비", "x", "1", "1", "나", "카드", ""}); ok {
		t.Error("a row without an ID is not an expense")
	}
	// the sheet turned the typed date/time into numbers (serial day / fraction of a day)
	d, _ := fromRow(c, []string{"46488", "0.6076388888888889", "식비", "x", "1", "1", "나", "카드", "id-3", "유로"})
	if d.Date != "2027-04-11" || d.Time != "14:35" {
		t.Errorf("serials: %q %q", d.Date, d.Time)
	}
}

type memLog struct {
	mu      sync.Mutex
	entries []audit.Entry
	fail    bool
}

func (m *memLog) Record(_ context.Context, e audit.Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("history down")
	}
	m.entries = append(m.entries, e)
	return nil
}

func newService(t *testing.T) (*Service, tripconfig.Config, *memLog, *sheets.MemGrid) {
	t.Helper()
	g := sheets.NewMemGrid(nil)
	c := cfgOf(nil)
	l := &memLog{}
	clock := time.Date(2027, 4, 11, 5, 5, 0, 0, time.UTC) // 14:05 in Tokyo
	s := &Service{Grid: g, Log: l, Now: func() time.Time { clock = clock.Add(time.Second); return clock }}
	if err := s.Ensure(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return s, c, l, g
}

const uid1 = "11111111-1111-4111-8111-111111111111"

func TestAddIsIdempotentAndRecordsAuthor(t *testing.T) {
	s, c, l, _ := newService(t)
	ctx := context.Background()
	list, err := s.Add(ctx, c, "a@x.com", in(func(i *Input) { i.ID = uid1 }))
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	e := list[0]
	if e.ID != uid1 || e.Time != "14:05" || e.CreatedBy != "a@x.com" || e.UpdatedBy != "a@x.com" || e.UpdatedAt == "" {
		t.Errorf("%+v", e)
	}
	again, err := s.Add(ctx, c, "a@x.com", in(func(i *Input) { i.ID = uid1; i.Amount = 99.0 })) // a retried request
	if err != nil || len(again) != 1 || again[0].Amount != 4.5 {
		t.Errorf("retry must not add or change anything: %+v %v", again, err)
	}
	if len(l.entries) != 1 || l.entries[0].Action != "add" || l.entries[0].After == "" || l.entries[0].Before != "" {
		t.Errorf("history: %+v", l.entries)
	}
}

func TestAddWithoutValidClientIDGeneratesOne(t *testing.T) {
	s, c, _, _ := newService(t)
	list, _ := s.Add(context.Background(), c, "a@x.com", in(func(i *Input) { i.ID = "not-a-uuid" }))
	if len(list) != 1 || !IsClientID(list[0].ID) {
		t.Errorf("%+v", list)
	}
	s.Add(context.Background(), c, "a@x.com", in(nil))
	list, _ = s.List(context.Background(), c)
	if len(list) != 2 || list[0].ID == list[1].ID {
		t.Errorf("ids must be unique: %+v", list)
	}
}

func TestUpdateKeepsTimeAndCreatorAndDetectsConflicts(t *testing.T) {
	s, c, l, _ := newService(t)
	ctx := context.Background()
	added, _ := s.Add(ctx, c, "a@x.com", in(func(i *Input) { i.ID = uid1 }))
	seen := added[0].UpdatedAt

	list, err := s.Update(ctx, c, "b@x.com", uid1, in(func(i *Input) { i.Amount = 7.25; i.Item = "소바" }), &seen)
	if err != nil {
		t.Fatal(err)
	}
	e := list[0]
	if e.Amount != 7.25 || e.Item != "소바" || e.Time != "14:05" || e.CreatedBy != "a@x.com" || e.UpdatedBy != "b@x.com" || e.UpdatedAt == seen {
		t.Errorf("%+v", e)
	}
	// a second editor still holding the first version loses: nothing is overwritten
	_, err = s.Update(ctx, c, "a@x.com", uid1, in(func(i *Input) { i.Amount = 1.0 }), &seen)
	var ce *Error
	if !errors.As(err, &ce) || ce.Kind != Conflict {
		t.Fatalf("want conflict, got %v", err)
	}
	if cur, _ := s.List(ctx, c); cur[0].Amount != 7.25 {
		t.Errorf("conflict must not change the row: %+v", cur[0])
	}
	// no base (older client) → last write wins
	if list, err = s.Update(ctx, c, "a@x.com", uid1, in(func(i *Input) { i.Amount = 2.0 }), nil); err != nil || list[0].Amount != 2 {
		t.Errorf("%+v %v", list, err)
	}
	if _, err = s.Update(ctx, c, "a@x.com", "missing", in(nil), nil); !errors.As(err, &ce) || ce.Kind != NotFound {
		t.Errorf("unknown id: %v", err)
	}
	if n := len(l.entries); n != 3 || l.entries[1].Action != "update" || l.entries[1].Before == "" || l.entries[1].After == "" {
		t.Errorf("history %d: %+v", n, l.entries)
	}
}

func TestLegacyRowWithoutUpdatedAtNeedsEmptyBase(t *testing.T) {
	s, c, _, g := newService(t)
	ctx := context.Background()
	_ = g.Append(ctx, "지출", []any{"2027-04-11", "09:00", "식비", "예전 기록", 3.0, 4350.0, "나", "카드", "old-1", "유로"}) // 10 columns
	list, _ := s.List(ctx, c)
	if len(list) != 1 || list[0].UpdatedAt != "" {
		t.Fatalf("%+v", list)
	}
	stale := "2027-01-01T00:00:00.000Z"
	if _, err := s.Update(ctx, c, "a@x.com", "old-1", in(nil), &stale); err == nil {
		t.Error("a base that does not match must conflict")
	}
	empty := ""
	list, err := s.Update(ctx, c, "a@x.com", "old-1", in(nil), &empty)
	if err != nil || list[0].CreatedBy != "a@x.com" || list[0].Time != "09:00" {
		t.Errorf("%+v %v", list, err)
	}
}

func TestDeleteKeepsLastValuesInHistory(t *testing.T) {
	s, c, l, _ := newService(t)
	ctx := context.Background()
	added, _ := s.Add(ctx, c, "a@x.com", in(func(i *Input) { i.ID = uid1; i.Item = "지울 것" }))
	stale := "2000-01-01T00:00:00.000Z"
	var ce *Error
	if _, err := s.Delete(ctx, c, "b@x.com", uid1, &stale); !errors.As(err, &ce) || ce.Kind != Conflict {
		t.Fatalf("stale delete: %v", err)
	}
	list, err := s.Delete(ctx, c, "b@x.com", uid1, &added[0].UpdatedAt)
	if err != nil || len(list) != 0 {
		t.Fatalf("%v %v", list, err)
	}
	last := l.entries[len(l.entries)-1]
	if last.Action != "delete" || !strings.Contains(last.Before, "지울 것") || last.After != "" || last.Email != "b@x.com" {
		t.Errorf("history: %+v", last)
	}
	if _, err := s.Delete(ctx, c, "b@x.com", uid1, nil); !errors.As(err, &ce) || ce.Kind != NotFound {
		t.Errorf("second delete: %v", err)
	}
}

func TestHistoryFailureDoesNotFailTheWrite(t *testing.T) {
	s, c, l, _ := newService(t)
	l.fail = true
	list, err := s.Add(context.Background(), c, "a@x.com", in(nil))
	if err != nil || len(list) != 1 {
		t.Errorf("the entry is saved even if history is down: %v %v", list, err)
	}
}

func TestConcurrentAddsAreAllKept(t *testing.T) {
	s, c, _, _ := newService(t)
	var wg sync.WaitGroup
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Add(context.Background(), c, "a@x.com", in(func(x *Input) { x.ID = fmt.Sprintf("%08d-1111-4111-8111-111111111111", i) }))
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	list, _ := s.List(context.Background(), c)
	if len(list) != 25 {
		t.Errorf("got %d of 25", len(list))
	}
}

func TestMissingExpenseTabIsCreatedOnFirstUse(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	s := &Service{Grid: g}
	c := cfgOf(nil)
	ctx := context.Background()
	if list, err := s.List(ctx, c); err != nil || len(list) != 0 {
		t.Fatalf("%v %v", list, err)
	}
	rows, err := g.Rows(ctx, "지출")
	if err != nil || len(rows) != 1 || rows[0][4] != "금액(유로)" || rows[0][12] != "수정시각" {
		t.Fatalf("header expected: %v %v", rows, err)
	}
	g2 := sheets.NewMemGrid(nil) // adding to a trip nobody has opened yet works too
	if list, err := (&Service{Grid: g2}).Add(ctx, c, "a@x.com", in(nil)); err != nil || len(list) != 1 {
		t.Errorf("%v %v", list, err)
	}
}

// The version a client sends back (updatedAt) must change on EVERY write. With a clock that has not moved (two writes in
// the same millisecond) a timestamp alone would repeat, and a stale edit or delete would slip through unnoticed.
func TestEveryWriteChangesTheVersionEvenWhenTheClockStandsStill(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	frozen := time.Date(2027, 4, 11, 5, 5, 0, 0, time.UTC)
	s := &Service{Grid: g, Now: func() time.Time { return frozen }}
	c := cfgOf(nil)
	ctx := context.Background()
	added, err := s.Add(ctx, c, "a@x.com", in(func(i *Input) { i.ID = uid1 }))
	if err != nil {
		t.Fatal(err)
	}
	v1 := added[0].UpdatedAt
	list, err := s.Update(ctx, c, "b@x.com", uid1, in(func(i *Input) { i.Amount = 2.0 }), &v1)
	if err != nil {
		t.Fatal(err)
	}
	v2 := list[0].UpdatedAt
	if v2 == v1 || v2 < v1 {
		t.Fatalf("the version must move forward even in the same millisecond: %q then %q", v1, v2)
	}
	var ce *Error
	if _, err = s.Delete(ctx, c, "a@x.com", uid1, &v1); !errors.As(err, &ce) || ce.Kind != Conflict {
		t.Fatalf("a delete holding the first version must conflict, got %v", err)
	}
	if _, err = s.Update(ctx, c, "a@x.com", uid1, in(func(i *Input) { i.Amount = 3.0 }), &v1); !errors.As(err, &ce) || ce.Kind != Conflict {
		t.Fatalf("an edit holding the first version must conflict, got %v", err)
	}
	if list, err = s.Update(ctx, c, "a@x.com", uid1, in(func(i *Input) { i.Amount = 3.0 }), &v2); err != nil || list[0].UpdatedAt <= v2 {
		t.Fatalf("holding the current version works and moves it again: %v %v", list, err)
	}
}

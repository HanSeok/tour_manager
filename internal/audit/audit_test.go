package audit

import (
	"context"
	"testing"
	"time"

	"tourmanager/internal/sheets"
)

func TestGridLogCreatesTabOnceAndAppends(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	l := &GridLog{Grid: g}
	ctx := context.Background()
	at := time.Date(2027, 4, 11, 5, 5, 9, 123e6, time.UTC)
	for _, id := range []string{"a", "b"} {
		if err := l.Record(ctx, Entry{At: at, Email: "x@y.com", Tab: "지출", ItemID: id, Action: "add", After: `{"k":1}`}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := g.Rows(ctx, Tab)
	if err != nil || len(rows) != 3 {
		t.Fatalf("header + 2 entries expected: %v %v", rows, err)
	}
	if rows[0][0] != "시각" || rows[0][6] != "이후값" {
		t.Errorf("header: %v", rows[0])
	}
	want := []string{"2027-04-11T05:05:09.123Z", "x@y.com", "지출", "a", "add", "", `{"k":1}`}
	for i, w := range want {
		if rows[1][i] != w {
			t.Errorf("col %d = %q want %q", i, rows[1][i], w)
		}
	}
}

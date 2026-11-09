// Package audit keeps the append-only change history (the "변경이력" tab): who did what, with the values before and
// after, so a deletion or an overwrite can be undone by hand.
package audit

import (
	"context"
	"sync"
	"time"

	"tourmanager/internal/sheets"
)

// Tab is the history tab; Header its first row.
const Tab = "변경이력"

var Header = []string{"시각", "이메일", "탭", "항목ID", "동작", "이전값", "이후값"}

type Entry struct {
	At     time.Time
	Email  string
	Tab    string
	ItemID string
	Action string // add | update | delete
	Before string // JSON, empty for add
	After  string // JSON, empty for delete
}

type Log interface {
	Record(ctx context.Context, e Entry) error
}

// Discard drops everything (tests, or when history is not wanted).
type Discard struct{}

func (Discard) Record(context.Context, Entry) error { return nil }

// GridLog appends to the 변경이력 tab, creating it on first use.
type GridLog struct {
	Grid sheets.Grid

	mu    sync.Mutex
	ready bool
}

func (g *GridLog) Record(ctx context.Context, e Entry) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.ready {
		if _, err := g.Grid.EnsureTab(ctx, Tab, Header); err != nil {
			return err
		}
		g.ready = true
	}
	return g.Grid.Append(ctx, Tab, []any{e.At.UTC().Format("2006-01-02T15:04:05.000Z"), e.Email, e.Tab, e.ItemID, e.Action, e.Before, e.After})
}

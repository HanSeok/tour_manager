package members

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestParseRowsSkipsBadRolesAndDuplicates(t *testing.T) {
	got := ParseRows([][]string{
		{"이메일", "표시이름", "역할"},
		{"A@x.com", "에이", "편집자"},
		{"b@x.com", "", "보기"},
		{"c@x.com", "씨", "관리자"},   // typo'd role must not grant access
		{"a@x.com", "에이2", "소유자"}, // duplicate: first wins
		{"not-an-email", "x", "편집자"},
		{"d@x.com", "디"},
	})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Email != "a@x.com" || got[0].Role != Editor || got[0].Name != "에이" {
		t.Errorf("a: %+v", got[0])
	}
	if got[1].Name != "b" || got[1].Role != Viewer {
		t.Errorf("b should default its name to the local part: %+v", got[1])
	}
}

func TestRolePermissions(t *testing.T) {
	if !Owner.CanWrite() || !Owner.CanManage() || !Editor.CanWrite() || Editor.CanManage() || Viewer.CanWrite() {
		t.Fatal("permission matrix wrong")
	}
}

func TestDirectoryOwnerAlwaysAllowedAndCacheTTL(t *testing.T) {
	calls := 0
	rows := [][]string{{"e@x.com", "이", "편집자"}}
	d := NewDirectory("Owner@X.com", func(context.Context) ([][]string, error) { calls++; return rows, nil }, time.Minute)
	clock := time.Unix(1000, 0)
	d.now = func() time.Time { return clock }
	ctx := context.Background()

	if m, ok := d.Lookup(ctx, "owner@x.com"); !ok || m.Role != Owner {
		t.Fatalf("owner: %+v %v", m, ok)
	}
	if m, ok := d.Lookup(ctx, " E@x.com "); !ok || m.Role != Editor {
		t.Fatalf("editor: %+v %v", m, ok)
	}
	if _, ok := d.Lookup(ctx, "stranger@x.com"); ok {
		t.Fatal("stranger must be denied")
	}
	if calls != 1 {
		t.Errorf("should cache within TTL, calls=%d", calls)
	}
	// removed from the sheet → loses access once the TTL passes
	rows = nil
	clock = clock.Add(61 * time.Second)
	if _, ok := d.Lookup(ctx, "e@x.com"); ok {
		t.Fatal("revoked member must lose access after TTL")
	}
}

func TestDirectoryKeepsLastGoodListWhenSheetFails(t *testing.T) {
	fail := false
	d := NewDirectory("o@x.com", func(context.Context) ([][]string, error) {
		if fail {
			return nil, errors.New("boom")
		}
		return [][]string{{"e@x.com", "이", "편집자"}}, nil
	}, time.Second)
	clock := time.Unix(0, 0)
	d.now = func() time.Time { return clock }
	ctx := context.Background()
	d.Lookup(ctx, "e@x.com")
	fail = true
	clock = clock.Add(time.Hour)
	if _, ok := d.Lookup(ctx, "e@x.com"); !ok {
		t.Error("keep serving the last good list")
	}
	fresh := NewDirectory("o@x.com", func(context.Context) ([][]string, error) { return nil, errors.New("boom") }, time.Second)
	if _, ok := fresh.Lookup(ctx, "e@x.com"); ok {
		t.Error("no cache + sheet down: deny non-owners")
	}
	if _, ok := fresh.Lookup(ctx, "o@x.com"); !ok {
		t.Error("owner always allowed")
	}
}

// Package members decides who may use the server and what they may do.
// The source of truth is the "멤버" tab of the owner's spreadsheet; this package only caches it (short TTL
// so a removed member loses access within about a minute).
package members

import (
	"context"
	"strings"
	"sync"
	"time"
)

// SheetName is the tab that holds the member list (이메일 | 표시이름 | 역할).
const SheetName = "멤버"

type Role string

const (
	Owner  Role = "owner"
	Editor Role = "editor"
	Viewer Role = "viewer"
)

type Member struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  Role   `json:"role"`
}

func (r Role) CanWrite() bool  { return r == Owner || r == Editor }
func (r Role) CanManage() bool { return r == Owner }

func parseRole(s string) (Role, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "소유자", "owner":
		return Owner, true
	case "편집자", "editor":
		return Editor, true
	case "보기", "viewer":
		return Viewer, true
	}
	return "", false
}

func norm(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// ParseRows turns sheet rows into members. Rows with an unknown role or an invalid e-mail are skipped (a typo must
// never grant access); the first row for an e-mail wins.
func ParseRows(rows [][]string) []Member {
	out := []Member{}
	seen := map[string]bool{}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		email := norm(r[0])
		role, ok := parseRole(r[2])
		if !ok || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t") || seen[email] {
			continue
		}
		seen[email] = true
		name := strings.TrimSpace(r[1])
		if name == "" {
			name = email[:strings.Index(email, "@")]
		}
		out = append(out, Member{Email: email, Name: name, Role: role})
	}
	return out
}

// Loader reads the current member rows (nil rows + nil error when the tab does not exist yet).
type Loader func(ctx context.Context) ([][]string, error)

type Directory struct {
	ownerEmail string
	load       Loader
	ttl        time.Duration
	now        func() time.Time

	mu     sync.Mutex
	cached map[string]Member
	at     time.Time
}

// NewDirectory: ownerEmail is always the owner, even before the 멤버 tab exists.
func NewDirectory(ownerEmail string, load Loader, ttl time.Duration) *Directory {
	return &Directory{ownerEmail: norm(ownerEmail), load: load, ttl: ttl, now: time.Now}
}

// Lookup returns the member for an e-mail. When the sheet cannot be read it keeps serving the last good list;
// without one only the owner gets in.
func (d *Directory) Lookup(ctx context.Context, email string) (Member, bool) {
	email = norm(email)
	if email == "" {
		return Member{}, false
	}
	list := d.snapshot(ctx)
	if m, ok := list[email]; ok {
		if email == d.ownerEmail {
			m.Role = Owner
		}
		return m, true
	}
	if email == d.ownerEmail {
		return Member{Email: email, Name: email[:strings.Index(email, "@")], Role: Owner}, true
	}
	return Member{}, false
}

// All lists the members known right now (owner first when it is not in the sheet).
func (d *Directory) All(ctx context.Context) []Member {
	list := d.snapshot(ctx)
	out := []Member{}
	if _, ok := list[d.ownerEmail]; !ok && d.ownerEmail != "" {
		out = append(out, Member{Email: d.ownerEmail, Name: d.ownerEmail[:strings.Index(d.ownerEmail, "@")], Role: Owner})
	}
	for _, m := range list {
		out = append(out, m)
	}
	return out
}

func (d *Directory) snapshot(ctx context.Context) map[string]Member {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cached != nil && d.now().Sub(d.at) < d.ttl {
		return d.cached
	}
	rows, err := d.load(ctx)
	if err != nil {
		if d.cached != nil {
			return d.cached // sheet unreachable: keep the last good list
		}
		return map[string]Member{}
	}
	m := map[string]Member{}
	for _, x := range ParseRows(rows) {
		m[x.Email] = x
	}
	d.cached, d.at = m, d.now()
	return m
}

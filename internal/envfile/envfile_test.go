package envfile

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	body := `# comment
OWNER_EMAIL=me@example.com
export BASE_URL="http://localhost:8080"
GOOGLE_CLIENT_SECRET='s3cr#t value'
TRIP_SHEET_ID=abc123   # trailing comment
EMPTY=
not a line
=novalue
ALREADY=from-file
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"OWNER_EMAIL", "BASE_URL", "GOOGLE_CLIENT_SECRET", "TRIP_SHEET_ID", "EMPTY", "ALREADY"} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
	t.Setenv("ALREADY", "from-env")
	set, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(set)
	if want := []string{"BASE_URL", "EMPTY", "GOOGLE_CLIENT_SECRET", "OWNER_EMAIL", "TRIP_SHEET_ID"}; !reflect.DeepEqual(set, want) {
		t.Errorf("set %v want %v", set, want)
	}
	for k, want := range map[string]string{
		"OWNER_EMAIL": "me@example.com", "BASE_URL": "http://localhost:8080", "GOOGLE_CLIENT_SECRET": "s3cr#t value",
		"TRIP_SHEET_ID": "abc123", "EMPTY": "", "ALREADY": "from-env",
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s=%q want %q", k, got, want)
		}
	}
}

func TestMissingFileIsFine(t *testing.T) {
	if set, err := Load(filepath.Join(t.TempDir(), "nope")); err != nil || set != nil {
		t.Errorf("%v %v", set, err)
	}
}

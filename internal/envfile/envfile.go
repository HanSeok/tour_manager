// Package envfile loads KEY=VALUE lines from a .env file into the process environment, so secrets do not have to be
// typed into a shell command. Variables already set in the environment win over the file.
package envfile

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// Load reads path (a missing file is fine). It returns the names it set.
func Load(path string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var set []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, val, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, val); err != nil {
			return set, err
		}
		set = append(set, key)
	}
	return set, sc.Err()
}

func parseLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	key, val, found := strings.Cut(line, "=")
	key = strings.TrimSpace(key)
	if !found || key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	val = strings.TrimSpace(val)
	if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
		return key, val[1 : len(val)-1], true
	}
	if i := strings.Index(val, " #"); i >= 0 { // trailing comment on an unquoted value
		val = strings.TrimSpace(val[:i])
	}
	return key, val, true
}

package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// TokenStore keeps the owner's refresh token (the one credential that lets the server act on the owner's Google data).
type TokenStore interface {
	Load() (*oauth2.Token, error) // (nil, nil) when nothing is stored yet
	Save(*oauth2.Token) error
}

// FileTokenStore is an interim store: a 0600 file on the data volume (not encrypted). The design calls for an
// encrypted SQLite row once SQLite lands.
type FileTokenStore struct{ Path string }

func (f FileTokenStore) Load() (*oauth2.Token, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var t oauth2.Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (f FileTokenStore) Save(t *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

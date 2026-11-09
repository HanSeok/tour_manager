package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"tourmanager/internal/auth"
)

func signerCookie(t *testing.T, w http.ResponseWriter, email string) {
	t.Helper()
	auth.Signer{Secret: []byte(strings.Repeat("s", 32))}.SetCookie(w, email, time.Hour, false)
}

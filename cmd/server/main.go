// Command server runs the trip dashboard: static front end + JSON API + Google sign-in.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata" // IANA zones inside the binary (the container has no tzdata)

	"tourmanager/internal/envfile"
	"tourmanager/internal/server"
	"tourmanager/prototype"
)

var version = "dev" // -ldflags "-X main.version=..."

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// .env (ENV_FILE to point elsewhere) fills in what the environment does not already set
	if set, err := envfile.Load(env("ENV_FILE", ".env")); err != nil {
		log.Fatalf(".env 를 읽지 못했습니다: %v", err)
	} else if len(set) > 0 {
		log.Printf(".env 에서 %d개 설정을 읽었습니다", len(set))
	}
	baseURL := env("BASE_URL", "http://localhost:8080")
	cfg := server.Config{
		BaseURL:       baseURL,
		OwnerEmail:    os.Getenv("OWNER_EMAIL"),
		SpreadsheetID: os.Getenv("TRIP_SHEET_ID"),
		DataDir:       env("DATA_DIR", "./var"),
		SessionSecret: []byte(os.Getenv("SESSION_SECRET")),
		ClientID:      os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret:  os.Getenv("GOOGLE_CLIENT_SECRET"),
		DevLogin:      os.Getenv("DEV_LOGIN") == "1",
		FixtureDir:    os.Getenv("FIXTURE_DIR"),
		Version:       version,
		Static:        server.StaticFS(os.Getenv("STATIC_DIR"), prototype.Files),
	}
	h, err := server.New(cfg)
	if err != nil {
		log.Fatalf("설정 오류: %v", err)
	}
	base := strings.TrimRight(baseURL, "/")
	if cfg.ClientID != "" {
		log.Printf("Google Cloud 의 OAuth 클라이언트에 승인된 리디렉션 URI 로 등록할 주소 두 개:\n    %s/auth/callback\n    %s/auth/owner/callback", base, base)
		if cfg.FixtureDir == "" {
			if _, err := os.Stat(filepath.Join(cfg.DataDir, "owner_token.json")); err != nil {
				log.Printf("소유자 Google 연결이 아직 없습니다: %s 로 로그인한 뒤 %s/auth/owner 를 한 번 여세요", cfg.OwnerEmail, base)
			}
		}
	}
	if cfg.DevLogin {
		log.Printf("DEV_LOGIN 이 켜져 있습니다 (localhost 전용, 이메일만으로 로그인): %s/auth/dev?email=%s", base, cfg.OwnerEmail)
	}
	srv := &http.Server{
		Addr: ":" + env("PORT", "8080"), Handler: h,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		log.Printf("tour-manager %s listening on %s (%s)", version, srv.Addr, baseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

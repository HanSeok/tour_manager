// Command server runs the trip dashboard: static front end + JSON API + Google sign-in.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata" // IANA zones inside the binary (the container has no tzdata)

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

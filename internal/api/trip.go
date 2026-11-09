package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

// Trip serves the trip-level functions. Only bootstrap exists so far; the others from the contract
// (refresh, expenses, state, calendar, ...) are added as they are ported.
type Trip struct {
	Source  sheets.Source
	Members *members.Directory
	Version string
}

func (t *Trip) Register(rt *Router) {
	rt.Register("bootstrap", PermRead, t.Bootstrap)
}

// LoadConfig reads and validates the 설정 tab. A missing tab is reported as "not configured", not as a failure.
func (t *Trip) LoadConfig(ctx context.Context) (tripconfig.Config, error) {
	rows, err := t.Source.Rows(ctx, tripconfig.SheetName)
	if errors.Is(err, sheets.ErrNoSheet) {
		return tripconfig.Parse(nil, false), nil
	}
	if err != nil {
		return tripconfig.Config{}, err
	}
	return tripconfig.Parse(rows, true), nil
}

func (t *Trip) Bootstrap(ctx context.Context, who Principal, _ []json.RawMessage) (any, error) {
	cfg, err := t.LoadConfig(ctx)
	if err != nil {
		log.Printf("bootstrap: settings: %v", err)
		return nil, Errf(502, "설정 탭을 읽지 못했습니다. 소유자 Google 연결 상태를 확인하세요")
	}
	// Collections are empty until their functions are ported; the shape matches what the dashboard already expects.
	return map[string]any{
		"config":   cfg,
		"version":  t.Version,
		"me":       who,
		"members":  t.Members.All(ctx),
		"events":   []any{},
		"expenses": []any{},
		"state":    map[string]any{},
		"places":   nil,
		"visits":   []any{},
		"photos":   []any{},
	}, nil
}

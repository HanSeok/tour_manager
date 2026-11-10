package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"

	"tourmanager/internal/drive"
	"tourmanager/internal/trips"
)

// TripManager creates trips in the owner's Google Drive and lists the ones this server knows.
type TripManager interface {
	Create(ctx context.Context, ownerEmail, ownerName string, req trips.Request) (drive.Trip, error)
	List() ([]drive.Trip, error)
	Current() (string, error)
	Switch(sheetID string) error
}

// CreateTrip makes a new trip (Drive folder + spreadsheet with every tab) and switches the dashboard to it.
// Owner only, so Google's own error text can be shown as is.
func (t *Trip) CreateTrip(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	if t.Trips == nil {
		return nil, Errf(412, "이 서버에서는 새 여행을 만들 수 없습니다 (개발용 샘플 데이터로 실행 중이거나 Google 연결이 없습니다)")
	}
	req, ok, err := arg[trips.Request](args, 0)
	if err != nil || !ok {
		return nil, Errf(422, "여행 정보가 없습니다")
	}
	trip, err := t.Trips.Create(ctx, who.Email, who.Name, req)
	var in *trips.InputError
	switch {
	case errors.As(err, &in):
		return nil, Errf(422, in.Msg)
	case err != nil:
		log.Printf("createTrip: %v", err)
		return nil, Errf(502, "여행을 만들지 못했습니다: "+err.Error()+hint(err))
	}
	t.mu.Lock()
	t.cfgOK = false // the next read must see the new trip, not the cached settings of the old one
	t.mu.Unlock()
	log.Printf("createTrip: %q (%s) by %s", trip.Title, trip.SheetID, who.Email)
	return map[string]any{"trip": trip}, nil
}

// hint says what to do about the Google errors that are the owner's setup, not a bug.
func hint(err error) string {
	m := err.Error()
	switch {
	case strings.Contains(m, "has not been used") || strings.Contains(m, "is disabled"):
		return " — Google Cloud 의 API 및 서비스 > 라이브러리에서 Google Drive API 와 Google Sheets API 를 사용 설정하세요"
	case strings.Contains(m, "insufficientPermissions") || strings.Contains(m, "insufficient authentication scopes") || strings.Contains(m, "Insufficient Permission"):
		return " — 소유자 연결에 Drive 권한이 없습니다. /auth/owner 를 열어 다시 연결하세요"
	}
	return ""
}

func (t *Trip) ListTrips(context.Context, Principal, []json.RawMessage) (any, error) {
	if t.Trips == nil {
		return map[string]any{"trips": []drive.Trip{}}, nil
	}
	list, err := t.Trips.List()
	if err != nil {
		return nil, sheetErr("listTrips", err)
	}
	if list == nil {
		list = []drive.Trip{}
	}
	return map[string]any{"trips": list}, nil
}

// SwitchTrip makes another trip the one the dashboard shows. The choice is the server's, so it applies to every member:
// owner only.
func (t *Trip) SwitchTrip(_ context.Context, who Principal, args []json.RawMessage) (any, error) {
	if t.Trips == nil {
		return nil, Errf(412, "이 서버에서는 여행을 바꿀 수 없습니다")
	}
	id, ok, err := arg[string](args, 0)
	if err != nil || !ok || id == "" {
		return nil, Errf(422, "여행을 고르세요")
	}
	if err := t.Trips.Switch(id); err != nil {
		if errors.Is(err, trips.ErrUnknownTrip) {
			return nil, Errf(404, err.Error())
		}
		log.Printf("switchTrip: %v", err)
		return nil, Errf(502, "여행을 바꾸지 못했습니다")
	}
	t.mu.Lock()
	t.cfgOK = false // the next read must see the other trip, not the cached settings
	t.mu.Unlock()
	log.Printf("switchTrip: %s by %s", id, who.Email)
	return map[string]any{"current": id}, nil
}

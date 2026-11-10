package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"tourmanager/internal/maplink"
	"tourmanager/internal/places"
)

// PlaceLinks reads the place a Google Maps share link points to.
type PlaceLinks interface {
	Resolve(ctx context.Context, text string) (maplink.Result, error)
}

// resolvePlaceLink(text) → {name, lat, lng}. The text may be a whole shared message; the first link in it is used.
func (t *Trip) ResolvePlaceLink(ctx context.Context, _ Principal, args []json.RawMessage) (any, error) {
	if t.Links == nil {
		return nil, Errf(412, "이 서버에서는 링크로 장소를 읽을 수 없습니다")
	}
	text, ok, err := arg[string](args, 0)
	if err != nil || !ok || text == "" {
		return nil, Errf(422, "링크를 붙여 넣으세요")
	}
	res, err := t.Links.Resolve(ctx, text)
	var in *maplink.InputError
	switch {
	case errors.As(err, &in):
		return nil, Errf(422, in.Msg)
	case err != nil:
		log.Printf("resolvePlaceLink: %v", err)
		return nil, Errf(502, "구글 지도에 연결하지 못했습니다. 잠시 후 다시 시도하세요")
	}
	return res, nil
}

// placeList is the trip's own places (an empty list when the server has none configured).
func (t *Trip) placeList(ctx context.Context) ([]places.Place, error) {
	if t.Places == nil {
		return []places.Place{}, nil
	}
	return t.Places.List(ctx)
}

func (t *Trip) placesReady(ctx context.Context) error {
	if _, err := t.ready(ctx); err != nil {
		return err
	}
	if t.Places == nil {
		return Errf(412, "이 서버에서는 장소를 저장할 수 없습니다")
	}
	return nil
}

// addPlace(place) → the whole place list.
func (t *Trip) AddPlace(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	in, ok, err := arg[places.Input](args, 0)
	if err != nil || !ok {
		return nil, Errf(422, "장소 내용이 없습니다")
	}
	if err := t.placesReady(ctx); err != nil {
		return nil, err
	}
	list, err := t.Places.Add(ctx, who.Email, in)
	if err != nil {
		return nil, sheetErr("addPlace", err)
	}
	return list, nil
}

// updatePlace(id, place, baseUpdatedAt?)
func (t *Trip) UpdatePlace(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	id, okID, err := arg[string](args, 0)
	if err != nil || !okID || id == "" {
		return nil, Errf(422, "장소 ID가 없습니다")
	}
	in, ok, err := arg[places.Input](args, 1)
	if err != nil || !ok {
		return nil, Errf(422, "장소 내용이 없습니다")
	}
	base, hasBase, err := arg[string](args, 2)
	if err != nil {
		return nil, err
	}
	if err := t.placesReady(ctx); err != nil {
		return nil, err
	}
	var bp *string
	if hasBase {
		bp = &base
	}
	list, err := t.Places.Update(ctx, who.Email, id, in, bp)
	if err != nil {
		return nil, sheetErr("updatePlace", err)
	}
	return list, nil
}

// deletePlace(id, baseUpdatedAt?)
func (t *Trip) DeletePlace(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	id, okID, err := arg[string](args, 0)
	if err != nil || !okID || id == "" {
		return nil, Errf(422, "장소 ID가 없습니다")
	}
	base, hasBase, err := arg[string](args, 1)
	if err != nil {
		return nil, err
	}
	if err := t.placesReady(ctx); err != nil {
		return nil, err
	}
	var bp *string
	if hasBase {
		bp = &base
	}
	list, err := t.Places.Delete(ctx, who.Email, id, bp)
	if err != nil {
		return nil, sheetErr("deletePlace", err)
	}
	return list, nil
}

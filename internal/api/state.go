package api

import (
	"context"
	"encoding/json"

	"tourmanager/internal/tripconfig"
)

// sharedState is what companions plan together, keyed like the dashboard's own state. Without a server-side store it is empty.
func (t *Trip) sharedState(ctx context.Context, cfg tripconfig.Config) (map[string]any, error) {
	out := map[string]any{}
	if t.State == nil || !cfg.Configured() {
		return out, nil
	}
	all, err := t.State.All(ctx)
	if err != nil {
		return nil, err
	}
	for k, v := range all {
		out[k] = v
	}
	return out, nil
}

// saveState(key, patch) → the merged value of that key. patch is {sub-key: value | null}; only the changed sub-keys are sent.
func (t *Trip) SaveState(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	key, ok, err := arg[string](args, 0)
	if err != nil || !ok || key == "" {
		return nil, Errf(422, "저장할 항목이 없습니다")
	}
	patch, ok, err := arg[map[string]json.RawMessage](args, 1)
	if err != nil || !ok {
		return nil, Errf(422, "저장 형식이 올바르지 않습니다")
	}
	cfg, err := t.ready(ctx)
	if err != nil {
		return nil, err
	}
	if t.State == nil {
		return nil, Errf(412, "이 서버에서는 동선을 저장할 수 없습니다")
	}
	merged, err := t.State.Save(ctx, who.Email, key, patch, func(d string) bool { return d >= cfg.Start && d <= cfg.End })
	if err != nil {
		return nil, sheetErr("saveState", err)
	}
	return merged, nil
}

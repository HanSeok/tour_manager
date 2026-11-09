package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

// Trip serves the trip-level functions: bootstrap, refresh and the expense calls. The rest of the contract
// (state, calendar, places, visits, photos ...) is added as it is ported.
type Trip struct {
	Grid     sheets.Grid
	Members  *members.Directory
	Expenses *expenses.Service
	Version  string
	CfgTTL   time.Duration // how long a read of the 설정 tab is reused (0 = read every time)

	mu    sync.Mutex
	cfg   tripconfig.Config
	cfgAt time.Time
	cfgOK bool
}

func (t *Trip) Register(rt *Router) {
	rt.Register("bootstrap", PermRead, t.Bootstrap)
	rt.Register("refresh", PermRead, t.Refresh)
	rt.Register("addExpense", PermWrite, t.AddExpense)
	rt.Register("updateExpense", PermWrite, t.UpdateExpense)
	rt.Register("deleteExpense", PermWrite, t.DeleteExpense)
}

// LoadConfig reads and validates the 설정 tab. A missing tab is reported as "not configured", not as a failure.
func (t *Trip) LoadConfig(ctx context.Context) (tripconfig.Config, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfgOK && t.CfgTTL > 0 && time.Since(t.cfgAt) < t.CfgTTL {
		return t.cfg, nil
	}
	rows, err := t.Grid.Rows(ctx, tripconfig.SheetName)
	var cfg tripconfig.Config
	switch {
	case errors.Is(err, sheets.ErrNoSheet):
		cfg = tripconfig.Parse(nil, false)
	case err != nil:
		return tripconfig.Config{}, err
	default:
		cfg = tripconfig.Parse(rows, true)
	}
	t.cfg, t.cfgAt, t.cfgOK = cfg, time.Now(), true
	return cfg, nil
}

// ready returns the settings, or the error to send when the trip is not configured / the sheet cannot be read.
func (t *Trip) ready(ctx context.Context) (tripconfig.Config, error) {
	cfg, err := t.LoadConfig(ctx)
	if err != nil {
		log.Printf("settings: %v", err)
		return cfg, Errf(502, "설정 탭을 읽지 못했습니다. 소유자 Google 연결 상태를 확인하세요")
	}
	if !cfg.Configured() {
		return cfg, Errf(412, "설정이 끝나지 않았습니다. 설정 탭의 [필수] 항목을 채워 주세요")
	}
	return cfg, nil
}

func (t *Trip) Bootstrap(ctx context.Context, who Principal, _ []json.RawMessage) (any, error) {
	cfg, err := t.LoadConfig(ctx)
	if err != nil {
		log.Printf("bootstrap: settings: %v", err)
		return nil, Errf(502, "설정 탭을 읽지 못했습니다. 소유자 Google 연결 상태를 확인하세요")
	}
	list := []expenses.Expense{}
	if cfg.Configured() {
		if err := t.Expenses.Ensure(ctx, cfg); err != nil {
			return nil, sheetErr("bootstrap", err)
		}
		if list, err = t.Expenses.List(ctx, cfg); err != nil {
			return nil, sheetErr("bootstrap", err)
		}
	}
	// Collections not ported yet stay empty; the shape matches what the dashboard already expects.
	return map[string]any{
		"config":   cfg,
		"version":  t.Version,
		"me":       who,
		"members":  t.Members.All(ctx),
		"events":   []any{},
		"expenses": list,
		"state":    map[string]any{},
		"places":   nil,
		"visits":   []any{},
		"photos":   []any{},
	}, nil
}

func (t *Trip) Refresh(ctx context.Context, _ Principal, _ []json.RawMessage) (any, error) {
	cfg, err := t.ready(ctx)
	if err != nil {
		return nil, err
	}
	list, err := t.Expenses.List(ctx, cfg)
	if err != nil {
		return nil, sheetErr("refresh", err)
	}
	return map[string]any{"state": map[string]any{}, "expenses": list, "events": []any{}}, nil
}

// sheetErr turns a storage failure into a safe message (details go to the log, not to the browser).
func sheetErr(op string, err error) error {
	var ve *expenses.ValidationError
	var ee *expenses.Error
	switch {
	case errors.As(err, &ve):
		return Errf(422, ve.Msg)
	case errors.As(err, &ee) && ee.Kind == expenses.NotFound:
		return Errf(404, ee.Msg)
	case errors.As(err, &ee) && ee.Kind == expenses.Conflict:
		return Errf(409, ee.Msg)
	}
	log.Printf("%s: %v", op, err)
	return Errf(502, "Google 시트에 접근하지 못했습니다. 잠시 후 다시 시도하세요")
}

// arg decodes args[i]; a missing or null argument yields the zero value and ok=false.
func arg[T any](args []json.RawMessage, i int) (v T, ok bool, err error) {
	if i >= len(args) || string(args[i]) == "null" {
		return v, false, nil
	}
	if err := json.Unmarshal(args[i], &v); err != nil {
		return v, false, Errf(422, "요청 형식이 올바르지 않습니다")
	}
	return v, true, nil
}

func (t *Trip) AddExpense(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	in, ok, err := arg[expenses.Input](args, 0)
	if err != nil || !ok {
		return nil, Errf(422, "지출 내용이 없습니다")
	}
	cfg, err := t.ready(ctx)
	if err != nil {
		return nil, err
	}
	list, err := t.Expenses.Add(ctx, cfg, who.Email, in)
	if err != nil {
		return nil, sheetErr("addExpense", err)
	}
	return list, nil
}

// updateExpense(id, expense, baseUpdatedAt?) — baseUpdatedAt is the updatedAt the editor saw (conflict check).
func (t *Trip) UpdateExpense(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	id, okID, err := arg[string](args, 0)
	if err != nil || !okID || id == "" {
		return nil, Errf(422, "지출 ID가 없습니다")
	}
	in, ok, err := arg[expenses.Input](args, 1)
	if err != nil || !ok {
		return nil, Errf(422, "지출 내용이 없습니다")
	}
	base, hasBase, err := arg[string](args, 2)
	if err != nil {
		return nil, err
	}
	cfg, err := t.ready(ctx)
	if err != nil {
		return nil, err
	}
	var bp *string
	if hasBase {
		bp = &base
	}
	list, err := t.Expenses.Update(ctx, cfg, who.Email, id, in, bp)
	if err != nil {
		return nil, sheetErr("updateExpense", err)
	}
	return list, nil
}

// deleteExpense(id, baseUpdatedAt?)
func (t *Trip) DeleteExpense(ctx context.Context, who Principal, args []json.RawMessage) (any, error) {
	id, okID, err := arg[string](args, 0)
	if err != nil || !okID || id == "" {
		return nil, Errf(422, "지출 ID가 없습니다")
	}
	base, hasBase, err := arg[string](args, 1)
	if err != nil {
		return nil, err
	}
	cfg, err := t.ready(ctx)
	if err != nil {
		return nil, err
	}
	var bp *string
	if hasBase {
		bp = &base
	}
	list, err := t.Expenses.Delete(ctx, cfg, who.Email, id, bp)
	if err != nil {
		return nil, sheetErr("deleteExpense", err)
	}
	return list, nil
}

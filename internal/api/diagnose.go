package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
	"tourmanager/internal/tripconfig"
)

// Check is one line of the setup screen. OK nil = optional / not applicable (shown as "—").
type Check struct {
	Label  string `json:"label"`
	OK     *bool  `json:"ok"`
	Detail string `json:"detail"`
}

func yes(label, detail string) Check { t := true; return Check{label, &t, detail} }
func no(label, detail string) Check  { f := false; return Check{label, &f, detail} }
func na(label, detail string) Check  { return Check{label, nil, detail} }

// checks inspects the connection step by step and says what to do next. Details can contain Google's own error text,
// so this is only ever sent to the owner.
func (t *Trip) checks(ctx context.Context) []Check {
	var out []Check
	if t.Conn != nil {
		if ok, detail := t.Conn(ctx); ok {
			out = append(out, yes("Google 연결 (소유자)", detail))
		} else {
			return append(out, no("Google 연결 (소유자)", detail))
		}
	}
	rows, err := t.Grid.RawRows(ctx, tripconfig.SheetName)
	var cfg tripconfig.Config
	switch {
	case errors.Is(err, sheets.ErrNotConnected):
		return append(out, no("시트 접근", sheets.ErrNotConnected.Error()))
	case errors.Is(err, sheets.ErrNoSheet):
		out = append(out, yes("시트 접근", "스프레드시트를 열었습니다"))
		cfg = tripconfig.Parse(nil, false)
	case err != nil:
		return append(out, no("시트 접근", err.Error()+" — 시트 ID, Google Sheets API 사용 설정, 소유자 계정의 시트 접근 권한을 확인하세요"))
	default:
		out = append(out, yes("시트 접근", "스프레드시트를 열었습니다"))
		cfg = tripconfig.Parse(rows, true)
	}
	switch {
	case cfg.Configured():
		out = append(out, yes("설정 탭", fmt.Sprintf("%s · %d일", cfg.Title, cfg.Days)))
	case len(cfg.Missing) == 1 && cfg.Missing[0] == "설정 탭":
		out = append(out, no("설정 탭", "'설정' 탭이 없습니다. 아래 버튼으로 자동으로 만들거나, 템플릿 사본을 쓰세요"))
	default:
		var parts []string
		if len(cfg.Missing) > 0 {
			parts = append(parts, fmt.Sprintf("빈 항목 %d개: %s", len(cfg.Missing), strings.Join(cfg.Missing, ", ")))
		}
		if len(cfg.Invalid) > 0 {
			parts = append(parts, fmt.Sprintf("형식 오류 %d개", len(cfg.Invalid)))
		}
		out = append(out, no("설정 탭", strings.Join(parts, " / ")))
	}
	mrows, err := t.Grid.Rows(ctx, members.SheetName)
	switch {
	case errors.Is(err, sheets.ErrNoSheet):
		out = append(out, na("멤버 탭", "없음 — 소유자만 로그인할 수 있습니다. 동행자를 추가하려면 '멤버' 탭(이메일|표시이름|역할)을 만드세요"))
	case err != nil:
		out = append(out, no("멤버 탭", err.Error()))
	default:
		counts := map[members.Role]int{}
		for _, m := range members.ParseRows(mrows) {
			counts[m.Role]++
		}
		out = append(out, yes("멤버 탭", fmt.Sprintf("소유자 %d · 편집자 %d · 보기 %d", counts[members.Owner], counts[members.Editor], counts[members.Viewer])))
	}
	if cfg.Configured() {
		erows, err := t.Grid.RawRows(ctx, cfg.ExpenseSheet)
		switch {
		case errors.Is(err, sheets.ErrNoSheet):
			out = append(out, na("지출 탭", "'"+cfg.ExpenseSheet+"' 탭은 처음 접속할 때 자동으로 만들어집니다"))
		case err != nil:
			out = append(out, no("지출 탭", err.Error()))
		default:
			out = append(out, yes("지출 탭", fmt.Sprintf("'%s' · 지출 %d건", cfg.ExpenseSheet, len(expensesIn(cfg, erows)))))
		}
	}
	return out
}

func expensesIn(cfg tripconfig.Config, rows [][]string) []expenses.Expense {
	return expenses.Parse(cfg, rows)
}

// Diagnose is the owner's "점검" button.
func (t *Trip) Diagnose(ctx context.Context, _ Principal, _ []json.RawMessage) (any, error) {
	return t.checks(ctx), nil
}

// SetupSettings creates the 설정 tab (header + every setting with its default) in a sheet that has none, so a blank
// spreadsheet is enough to start. A tab that already holds settings is never touched.
func (t *Trip) SetupSettings(ctx context.Context, who Principal, _ []json.RawMessage) (any, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows, err := t.Grid.RawRows(ctx, tripconfig.SheetName)
	if err != nil && !errors.Is(err, sheets.ErrNoSheet) {
		return nil, sheetErr("setupSettings", err)
	}
	if len(rows) > 1 {
		return nil, Errf(409, "이미 '설정' 탭이 있습니다. 값을 직접 고쳐 주세요")
	}
	template := tripconfig.TemplateRows()
	if _, err := t.Grid.EnsureTab(ctx, tripconfig.SheetName, template[0]); err != nil {
		return nil, sheetErr("setupSettings", err)
	}
	for _, r := range template[1:] {
		cells := make([]any, len(r))
		for i, c := range r {
			cells[i] = c
		}
		if err := t.Grid.Append(ctx, tripconfig.SheetName, cells); err != nil {
			return nil, sheetErr("setupSettings", err)
		}
	}
	t.cfgOK = false // the next read must see the new tab
	log.Printf("setupSettings: created the 설정 tab (%s)", who.Email)
	return map[string]any{"created": len(template) - 1}, nil
}

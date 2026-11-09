package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"tourmanager/internal/expenses"
	"tourmanager/internal/members"
	"tourmanager/internal/sheets"
)

// failGrid fails every read with err (a Google problem), writes are not reached in these tests.
type failGrid struct {
	*sheets.MemGrid
	err error
}

func (f failGrid) Rows(context.Context, string) ([][]string, error)    { return nil, f.err }
func (f failGrid) RawRows(context.Context, string) ([][]string, error) { return nil, f.err }

func routerFor(role members.Role, g sheets.Grid, conn func(context.Context) (bool, string)) *Router {
	dir := members.NewDirectory("o@x.com", func(context.Context) ([][]string, error) { return nil, nil }, time.Minute)
	tr := &Trip{Grid: g, Members: dir, Expenses: &expenses.Service{Grid: g}, Conn: conn}
	rt := NewRouter(func(*http.Request) (Principal, bool) { return Principal{Email: "w@x.com", Name: "w", Role: role}, true }, "")
	tr.Register(rt)
	return rt
}

func checksOf(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, _ := out["data"].(map[string]any)["checks"].([]any)
	var list []map[string]any
	for _, c := range raw {
		list = append(list, c.(map[string]any))
	}
	return list
}

func TestNotConnectedOwnerSeesConnectLinkEditorSeesPlainError(t *testing.T) {
	g := failGrid{sheets.NewMemGrid(nil), sheets.ErrNotConnected}
	conn := func(context.Context) (bool, string) { return false, "아직 연결되지 않았습니다" }
	code, out := call(routerFor(members.Owner, g, conn), "bootstrap", `{"args":[]}`, nil)
	if code != 200 {
		t.Fatalf("owner: %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["connect"] != "/auth/owner" {
		t.Errorf("owner needs the link to connect Google: %v", data["connect"])
	}
	cs := checksOf(t, out)
	if len(cs) != 1 || cs[0]["ok"] != false || !strings.Contains(cs[0]["detail"].(string), "연결") {
		t.Errorf("checks: %v", cs)
	}
	if missing := data["config"].(map[string]any)["missing"].([]any); len(missing) == 0 {
		t.Error("the page needs `missing` non-empty to show the setup screen")
	}
	code, out = call(routerFor(members.Editor, g, conn), "bootstrap", `{"args":[]}`, nil)
	if code != 503 || strings.Contains(out["error"].(string), "/auth/owner") {
		t.Errorf("editor gets a plain message without internals: %d %v", code, out)
	}
}

func TestGoogleErrorDetailGoesToTheOwnerOnly(t *testing.T) {
	g := failGrid{sheets.NewMemGrid(nil), errors.New("sheets GET /v4: 403 Google Sheets API has not been used in project 123 before or it is disabled")}
	code, out := call(routerFor(members.Owner, g, nil), "bootstrap", `{"args":[]}`, nil)
	cs := checksOf(t, out)
	if code != 200 || len(cs) != 1 || !strings.Contains(cs[0]["detail"].(string), "Sheets API has not been used") || !strings.Contains(cs[0]["detail"].(string), "사용 설정") {
		t.Fatalf("owner must see Google's reason and what to do: %d %v", code, cs)
	}
	code, out = call(routerFor(members.Editor, g, nil), "bootstrap", `{"args":[]}`, nil)
	if code != 502 || strings.Contains(out["error"].(string), "Sheets API") {
		t.Errorf("others must not see Google's text: %d %v", code, out)
	}
}

func TestDiagnoseWalksThroughTheSetupAndIsOwnerOnly(t *testing.T) {
	g := sheets.NewMemGrid(map[string][][]string{
		"설정": {{"항목", "값"}, {"여행 제목", "t"}, {"시작일", "2027-04-10"}},
		"멤버": {{"이메일", "표시이름", "역할"}, {"e@x.com", "에", "편집자"}, {"v@x.com", "브", "보기"}, {"bad", "x", "관리자"}},
	})
	conn := func(context.Context) (bool, string) { return true, "연결됨" }
	_, out := call(routerFor(members.Owner, g, conn), "diagnose", `{"args":[]}`, nil)
	var labels []string
	got := map[string]map[string]any{}
	for _, c := range out["data"].([]any) {
		m := c.(map[string]any)
		labels = append(labels, m["label"].(string))
		got[m["label"].(string)] = m
	}
	if strings.Join(labels, "|") != "Google 연결 (소유자)|시트 접근|설정 탭|멤버 탭" { // no 지출 line until the trip is configured
		t.Fatalf("labels: %v", labels)
	}
	if got["설정 탭"]["ok"] != false || !strings.Contains(got["설정 탭"]["detail"].(string), "빈 항목") {
		t.Errorf("incomplete settings are listed: %v", got["설정 탭"])
	}
	if d := got["멤버 탭"]["detail"].(string); got["멤버 탭"]["ok"] != true || !strings.Contains(d, "편집자 1") || !strings.Contains(d, "보기 1") {
		t.Errorf("member counts ignore the invalid row: %v", got["멤버 탭"])
	}
	if code, _ := call(routerFor(members.Editor, g, conn), "diagnose", `{"args":[]}`, nil); code != 403 {
		t.Errorf("diagnose is for the owner: %d", code)
	}
}

func TestDiagnoseWithoutSettingsOrMembersTabs(t *testing.T) {
	_, out := call(routerFor(members.Owner, sheets.NewMemGrid(nil), nil), "diagnose", `{"args":[]}`, nil)
	var details []string
	for _, c := range out["data"].([]any) {
		m := c.(map[string]any)
		details = append(details, m["label"].(string)+":"+m["detail"].(string))
	}
	text := strings.Join(details, "\n")
	if !strings.Contains(text, "'설정' 탭이 없습니다") || !strings.Contains(text, "소유자만 로그인할 수 있습니다") {
		t.Errorf("%s", text)
	}
}

func TestBootstrapOfAnIncompleteTripIncludesChecksForTheOwnerOnly(t *testing.T) {
	g := sheets.NewMemGrid(map[string][][]string{"설정": {{"항목", "값"}, {"여행 제목", "t"}}})
	_, out := call(routerFor(members.Owner, g, nil), "bootstrap", `{"args":[]}`, nil)
	if len(checksOf(t, out)) == 0 {
		t.Error("owner should get the checklist on the setup screen")
	}
	_, out = call(routerFor(members.Editor, g, nil), "bootstrap", `{"args":[]}`, nil)
	if _, has := out["data"].(map[string]any)["checks"]; has {
		t.Error("editors must not receive the owner's checklist")
	}
}

func TestSetupSettingsCreatesTheTabOnABlankSheetAndNeverTouchesExistingOnes(t *testing.T) {
	g := sheets.NewMemGrid(nil)
	owner := routerFor(members.Owner, g, nil)
	code, out := call(owner, "setupSettings", `{"args":[]}`, nil)
	if code != 200 || out["data"].(map[string]any)["created"] != float64(22) {
		t.Fatalf("%d %v", code, out)
	}
	rows, err := g.Rows(context.Background(), "설정")
	if err != nil || len(rows) != 23 || rows[0][0] != "항목" || rows[1][0] != "여행 제목" || !strings.HasPrefix(rows[1][2], "[필수]") {
		t.Fatalf("%v %v", len(rows), err)
	}
	// it is a real starting point: bootstrap now lists exactly what the owner still has to fill in
	_, boot := call(owner, "bootstrap", `{"args":[]}`, nil)
	missing := boot["data"].(map[string]any)["config"].(map[string]any)["missing"].([]any)
	if len(missing) == 0 || missing[0] == "설정 탭" {
		t.Errorf("after creating the tab the missing list names settings, not the tab: %v", missing)
	}
	// a second call (or a tab that already holds settings) is refused and nothing is overwritten
	if err := g.UpdateRow(context.Background(), "설정", 2, []any{"여행 제목", "내 여행", "[필수] x"}); err != nil {
		t.Fatal(err)
	}
	if code, out = call(owner, "setupSettings", `{"args":[]}`, nil); code != 409 {
		t.Fatalf("existing settings must be protected: %d %v", code, out)
	}
	if rows, _ = g.Rows(context.Background(), "설정"); rows[1][1] != "내 여행" || len(rows) != 23 {
		t.Errorf("settings were modified: %v", rows[1])
	}
	if code, _ = call(routerFor(members.Editor, g, nil), "setupSettings", `{"args":[]}`, nil); code != 403 {
		t.Errorf("owner only: %d", code)
	}
}

func TestSetupSettingsFillsATabThatOnlyHasAHeader(t *testing.T) {
	g := sheets.NewMemGrid(map[string][][]string{"설정": {{"항목", "값", "설명"}}})
	if code, out := call(routerFor(members.Owner, g, nil), "setupSettings", `{"args":[]}`, nil); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	if rows, _ := g.Rows(context.Background(), "설정"); len(rows) != 23 || rows[0][1] != "값" {
		t.Errorf("header kept once, settings appended: %d rows", len(rows))
	}
}

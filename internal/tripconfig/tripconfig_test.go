package tripconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func rows(m map[string]string) [][]string {
	out := [][]string{{"항목", "값", "설명"}}
	for k, v := range m {
		out = append(out, []string{k, v, "desc"})
	}
	return out
}

func base() map[string]string {
	return map[string]string{
		"여행 제목": "t", "시작일": "2027-04-10", "종료일": "2027-04-14", "현지 시간대": "Asia/Tokyo", "목적지 키워드": "나하",
		"My Maps 링크": "https://x/?mid=abc", "현지 통화 이름": "유로", "현지 통화 기호": "€", "환산 통화 기호": "₩", "환율": "1450",
		"결제자": "나", "결제수단": "카드", "지출 시트 이름": "지출", "입국 소요(분)": "", "출국편 공항 도착(분 전)": "", "기본 출발 시각": "",
	}
}

func with(m map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

func TestIsRealDateRejectsRolledOverDates(t *testing.T) {
	for in, want := range map[string]bool{"2027-02-28": true, "2027-02-30": false, "2027-4-1": false, "": false} {
		if got := isRealDate(in); got != want {
			t.Errorf("isRealDate(%q)=%v want %v", in, got, want)
		}
	}
}

func TestInvalidTimezoneEmptyListsReservedSheetBadBuffers(t *testing.T) {
	raw := with(base(), "현지 시간대", "Asia/Tokio", "목적지 키워드", ",", "결제자", " , ", "지출 시트 이름", "설정",
		"입국 소요(분)", "-5", "기본 출발 시각", "25:00")
	cfg := Parse(rows(raw), true)
	text := strings.Join(cfg.Invalid, "\n")
	for _, label := range []string{"현지 시간대", "목적지 키워드", "결제자", "지출 시트 이름", "입국 소요", "기본 출발 시각"} {
		if !strings.Contains(text, label) {
			t.Errorf("expected invalid %q in:\n%s", label, text)
		}
	}
	if strings.Contains(text, "결제수단") {
		t.Errorf("결제수단 should be valid:\n%s", text)
	}
	good := Parse(rows(with(raw, "현지 시간대", "Asia/Tokyo", "목적지 키워드", "나하", "결제자", "나", "지출 시트 이름", "여행_지출", "입국 소요(분)", "60", "기본 출발 시각", "9:00")), true)
	if len(good.Invalid) != 0 {
		t.Fatalf("unexpected invalid: %v", good.Invalid)
	}
	if good.DefaultStart != "09:00" {
		t.Errorf("single-digit hour must be normalized, got %q", good.DefaultStart)
	}
	if good.Offset != "+09:00" {
		t.Errorf("offset=%q want +09:00", good.Offset)
	}
}

func TestNewFieldsAndDefaults(t *testing.T) {
	good := Parse(rows(with(base(), "이동수단", "대중교통", "현지 통화 소수 자릿수", "2",
		"캘린더 ID", "abc@group.calendar.google.com", "지도 타일 주소", "https://{s}.tile.example/{z}/{x}/{y}.png")), true)
	if len(good.Invalid) != 0 {
		t.Fatalf("invalid: %v", good.Invalid)
	}
	if good.TravelMode != "transit" || good.CurrencyDecimals != 2 || good.CalendarID != "abc@group.calendar.google.com" {
		t.Errorf("got %+v", good)
	}
	d := Parse(rows(base()), true)
	if d.TravelMode != "driving" || d.CurrencyDecimals != 0 || d.TileURL != "" || len(d.Invalid) != 0 {
		t.Errorf("defaults wrong: %+v", d)
	}
	if d.ArrivalBufferMin != 60 || d.FlightLeadMin != 90 {
		t.Errorf("blank buffers should fall back to 60/90, got %d/%d", d.ArrivalBufferMin, d.FlightLeadMin)
	}
	bad := Parse(rows(with(base(), "이동수단", "비행기", "현지 통화 소수 자릿수", "3", "캘린더 ID", "a b", "지도 타일 주소", "javascript:alert(1)/{z}/{x}/{y}")), true)
	text := strings.Join(bad.Invalid, "\n")
	for _, label := range []string{"이동수단", "소수 자릿수", "캘린더 ID", "지도 타일 주소"} {
		if !strings.Contains(text, label) {
			t.Errorf("expected invalid %q", label)
		}
	}
}

func TestMissingRequiredAndNoSheet(t *testing.T) {
	cfg := Parse(rows(map[string]string{"여행 제목": "t"}), true)
	if cfg.Configured() || !contains(cfg.Missing, "시작일") || contains(cfg.Missing, "여행 제목") {
		t.Errorf("missing=%v", cfg.Missing)
	}
	// defaults count as present (결제자 etc.) unless the label exists with an empty value
	if contains(cfg.Missing, "결제자") {
		t.Errorf("default 결제자 should satisfy required: %v", cfg.Missing)
	}
	none := Parse(nil, false)
	if len(none.Missing) != 1 || none.Missing[0] != "설정 탭" {
		t.Errorf("no sheet: %v", none.Missing)
	}
}

func TestJSNumberSemantics(t *testing.T) {
	for in, ok := range map[string]bool{"9.3": true, " 1450 ": true, "1e3": true, "inf": false, "NaN": false, "0x10": false, "": true, "9,3": false} {
		if _, got := jsNumber(in); got != ok {
			t.Errorf("jsNumber(%q) ok=%v want %v", in, got, ok)
		}
	}
	if jsRound(-0.5) != 0 || jsRound(2.5) != 3 {
		t.Errorf("jsRound must follow Math.round")
	}
	if got := Parse(rows(with(base(), "환율", "inf")), true); len(got.Invalid) == 0 {
		t.Errorf("rate 'inf' must be invalid")
	}
}

func TestJSONShapeHidesServerOnlyFields(t *testing.T) {
	cfg := Parse(rows(with(base(), "캘린더 ID", "secret@group.calendar.google.com")), true)
	b, _ := json.Marshal(cfg)
	s := string(b)
	for _, leak := range []string{"secret@group", "mid", `"abc"`} {
		if strings.Contains(s, leak) {
			t.Errorf("leaked %q in %s", leak, s)
		}
	}
	if !strings.Contains(s, `"hasMyMaps":true`) || !strings.Contains(s, `"missing":[]`) || !strings.Contains(s, `"invalid":[]`) {
		t.Errorf("shape: %s", s)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestTemplateRowsCoverEverySettingAndRoundTrip(t *testing.T) {
	rows := TemplateRows()
	if len(rows) != len(fields)+1 || rows[0][0] != "항목" {
		t.Fatalf("header + one row per field expected, got %d rows", len(rows))
	}
	for i, f := range fields {
		r := rows[i+1]
		if r[0] != f.label || r[1] != f.def || descriptions[f.key] == "" || !strings.HasPrefix(r[2], map[bool]string{true: "[필수]", false: "[선택]"}[f.required]) {
			t.Errorf("row for %s: %v", f.key, r)
		}
	}
	// the fresh tab parses (every label is recognised): what is still missing is exactly the required settings without a default
	cfg := Parse(rows, true)
	var want []string
	for _, f := range fields {
		if f.required && f.def == "" {
			want = append(want, f.label)
		}
	}
	if strings.Join(cfg.Missing, "|") != strings.Join(want, "|") {
		t.Errorf("missing %v want %v", cfg.Missing, want)
	}
}

func TestDatesAndTimesTheSheetTurnedIntoNumbers(t *testing.T) {
	// 46487 = 2027-04-10, 46491 = 2027-04-14 (days since 1899-12-30); 0.375 = 09:00
	cfg := Parse(rows(with(base(), "시작일", "46487", "종료일", "46491", "기본 출발 시각", "0.375")), true)
	if cfg.Start != "2027-04-10" || cfg.End != "2027-04-14" || cfg.Days != 5 || cfg.DefaultStart != "09:00" || len(cfg.Invalid) != 0 {
		t.Fatalf("%+v", cfg)
	}
	// real text is left alone, and so are numbers that cannot be dates
	text := Parse(rows(with(base(), "시작일", "2027-04-10", "종료일", "2027-04-14", "기본 출발 시각", "9:30")), true)
	if text.Start != "2027-04-10" || text.DefaultStart != "09:30" {
		t.Errorf("%+v", text)
	}
	if bad := Parse(rows(with(base(), "시작일", "12345")), true); len(bad.Invalid) == 0 {
		t.Errorf("a number that is not a plausible date must still be rejected: %+v", bad.Start)
	}
	for _, in := range []string{"", "abc", "0", "19999", "80000", "2027-04-10"} {
		if serialDate(in) != in {
			t.Errorf("serialDate(%q) must not change it, got %q", in, serialDate(in))
		}
	}
	for _, in := range []string{"", "1", "09:00", "x"} {
		if serialTime(in) != in {
			t.Errorf("serialTime(%q) must not change it, got %q", in, serialTime(in))
		}
	}
}

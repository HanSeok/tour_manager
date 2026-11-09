// Package tripconfig reads and validates the trip settings (the "설정" tab: 항목 | 값 | 설명).
// It is a port of apps-script/Config.js: same labels, same validation messages, same defaults.
package tripconfig

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// SheetName is the tab that holds the settings.
	SheetName    = "설정"
	maxTripDays  = 31
	maxBufferMin = 600
)

var (
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timeRe = regexp.MustCompile(`^([01]?\d|2[0-3]):[0-5]\d$`) // "9:00" is fine (typed or sheet-converted)
	// JS Number() accepts decimal/exponent forms only; strconv.ParseFloat also takes "inf", "nan" and hex floats.
	numberRe = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)
	midRe    = regexp.MustCompile(`[?&]mid=([\w-]+)`)
	tileRe   = regexp.MustCompile("^https://[^\\s\"'<>`]+$")
	sepRe    = regexp.MustCompile(`[,，]`)
	digitRe  = regexp.MustCompile(`^[0-2]$`)

	reservedSheets = []string{"설정", "안내", "_이동시간", "_영업시간"}
	travelModes    = map[string]string{"자동차": "driving", "대중교통": "transit", "도보": "walking"}
	travelOrder    = []string{"자동차", "대중교통", "도보"}
)

type field struct {
	key, label string
	required   bool
	def        string
}

// Fields in sheet order. Keep in sync with the template (Config.js CONFIG_FIELDS).
var fields = []field{
	{"title", "여행 제목", true, ""},
	{"start", "시작일", true, ""},
	{"end", "종료일", true, ""},
	{"timezone", "현지 시간대", true, ""},
	{"destKeywords", "목적지 키워드", true, ""},
	{"myMapsUrl", "My Maps 링크", true, ""},
	{"localCurrency", "현지 통화 이름", true, ""},
	{"localSymbol", "현지 통화 기호", true, ""},
	{"homeSymbol", "환산 통화 기호", true, "₩"},
	{"rate", "환율", true, ""},
	{"payers", "결제자", true, "나, 배우자"},
	{"methods", "결제수단", true, "카드, 현금"},
	{"expenseSheet", "지출 시트 이름", true, "여행_지출"},
	{"subtitle", "부제", false, ""},
	{"arrivalBufferMin", "입국 소요(분)", false, "60"},
	{"flightLeadMin", "출국편 공항 도착(분 전)", false, "90"},
	{"defaultStart", "기본 출발 시각", false, "09:00"},
	{"travelMode", "이동수단", false, "자동차"},
	{"currencyDecimals", "현지 통화 소수 자릿수", false, "0"},
	{"homeDecimals", "환산 통화 소수 자릿수", false, "0"},
	{"calendarId", "캘린더 ID", false, ""},
	{"tileUrl", "지도 타일 주소", false, ""},
}

// Config is the validated trip configuration. Fields without a json tag never leave the server.
type Config struct {
	Title            string   `json:"title"`
	Subtitle         string   `json:"subtitle"`
	Start            string   `json:"start"`
	End              string   `json:"end"`
	Days             int      `json:"days"`
	Timezone         string   `json:"timezone"`
	Offset           string   `json:"offset"` // one UTC offset for the whole trip (a DST switch mid-trip shifts times by 1h)
	DestKeywords     []string `json:"destKeywords"`
	LocalCurrency    string   `json:"localCurrency"`
	LocalSymbol      string   `json:"localSymbol"`
	HomeSymbol       string   `json:"homeSymbol"`
	Rate             float64  `json:"rate"`
	Payers           []string `json:"payers"`
	Methods          []string `json:"methods"`
	ExpenseSheet     string   `json:"expenseSheet"`
	ArrivalBufferMin int      `json:"arrivalBufferMin"`
	FlightLeadMin    int      `json:"flightLeadMin"`
	DefaultStart     string   `json:"defaultStart"`
	TravelMode       string   `json:"travelMode"`
	CurrencyDecimals int      `json:"currencyDecimals"`
	HomeDecimals     int      `json:"homeDecimals"`
	TileURL          string   `json:"tileUrl"`
	Missing          []string `json:"missing"`
	Invalid          []string `json:"invalid"`
	HasMyMaps        bool     `json:"hasMyMaps"`

	MapID      string `json:"-"` // My Maps "mid" — server side only
	CalendarID string `json:"-"`
}

// Configured reports whether every required setting is present and valid.
func (c Config) Configured() bool { return len(c.Missing) == 0 && len(c.Invalid) == 0 }

// Parse builds the config from the settings rows ([label, value, ...]). hasSheet=false means the tab does not exist.
func Parse(rows [][]string, hasSheet bool) Config {
	byLabel := map[string]string{}
	for _, r := range rows {
		if len(r) == 0 {
			continue
		}
		v := ""
		if len(r) > 1 {
			v = strings.TrimSpace(r[1])
		}
		byLabel[strings.TrimSpace(r[0])] = v
	}
	raw := map[string]string{}
	for _, f := range fields {
		if v, ok := byLabel[f.label]; ok { // a label that exists with an empty value overrides the default
			raw[f.key] = v
		} else {
			raw[f.key] = f.def
		}
	}
	return validate(raw, hasSheet)
}

func isRealDate(s string) bool {
	if !dateRe.MatchString(s) {
		return false
	}
	t, err := time.Parse("2006-01-02", s) // 2027-02-30 must not roll over
	return err == nil && t.Format("2006-01-02") == s
}

// jsNumber mimics JS Number(s) for the cases we accept: ok=false means NaN.
func jsNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true // Number("") === 0
	}
	if !numberRe.MatchString(s) {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func jsRound(v float64) int { return int(math.Floor(v + 0.5)) } // Math.round rounds .5 up, math.Round away from zero

func toInt(s string, fallback int) int {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	v, ok := jsNumber(s)
	if !ok {
		return fallback
	}
	return jsRound(v)
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range sepRe.Split(s, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normTime(s string) string {
	if timeRe.MatchString(s) && len(s) == 4 {
		return "0" + s
	}
	return s
}

func isReserved(name string) bool {
	for _, r := range reservedSheets {
		if r == name {
			return true
		}
	}
	return false
}

func validate(raw map[string]string, hasSheet bool) Config {
	missing := []string{}
	if !hasSheet {
		missing = append(missing, "설정 탭")
	} else {
		for _, f := range fields {
			if f.required && raw[f.key] == "" {
				missing = append(missing, f.label)
			}
		}
	}
	invalid := []string{}
	bad := func(label, why string) { invalid = append(invalid, label+": "+why) }

	if raw["start"] != "" && !isRealDate(raw["start"]) {
		bad("시작일", "있는 날짜를 YYYY-MM-DD 형식으로 입력하세요")
	}
	if raw["end"] != "" && !isRealDate(raw["end"]) {
		bad("종료일", "있는 날짜를 YYYY-MM-DD 형식으로 입력하세요")
	}
	days := 0
	if isRealDate(raw["start"]) && isRealDate(raw["end"]) {
		s, _ := time.Parse("2006-01-02", raw["start"])
		e, _ := time.Parse("2006-01-02", raw["end"])
		days = int(e.Sub(s).Hours()/24+0.5) + 1
	}
	if raw["start"] != "" && raw["end"] != "" && (days < 1 || days > maxTripDays) {
		bad("종료일", "시작일 이후 "+strconv.Itoa(maxTripDays)+"일 이내여야 합니다")
	}
	rate, rateOK := jsNumber(raw["rate"])
	if raw["rate"] != "" && !(rateOK && rate > 0) {
		bad("환율", "0보다 큰 숫자여야 합니다")
	}
	mid := ""
	if m := midRe.FindStringSubmatch(raw["myMapsUrl"]); m != nil {
		mid = m[1]
	}
	if raw["myMapsUrl"] != "" && mid == "" {
		bad("My Maps 링크", "주소에 'mid=' 가 없습니다")
	}
	offset := "+00:00"
	if raw["timezone"] != "" {
		loc, err := time.LoadLocation(raw["timezone"])
		if err != nil || raw["timezone"] == "Local" {
			bad("현지 시간대", "Asia/Tokyo 같은 IANA 시간대 이름이어야 합니다")
		} else {
			day := "2026-01-01"
			if isRealDate(raw["start"]) {
				day = raw["start"]
			}
			at, _ := time.Parse("2006-01-02T15:04:05Z", day+"T12:00:00Z")
			offset = at.In(loc).Format("-07:00")
		}
	}
	for _, lf := range [][2]string{{"destKeywords", "목적지 키워드"}, {"payers", "결제자"}, {"methods", "결제수단"}} {
		if raw[lf[0]] != "" && len(splitList(raw[lf[0]])) == 0 {
			bad(lf[1], "쉼표로 구분한 값이 하나 이상 있어야 합니다")
		}
	}
	if isReserved(raw["expenseSheet"]) {
		bad("지출 시트 이름", "'"+raw["expenseSheet"]+"'는 대시보드가 쓰는 탭 이름입니다")
	}
	if raw["defaultStart"] != "" && !timeRe.MatchString(raw["defaultStart"]) {
		bad("기본 출발 시각", "HH:MM 형식이어야 합니다 (예: 09:00)")
	}
	for _, bf := range [][2]string{{"arrivalBufferMin", "입국 소요(분)"}, {"flightLeadMin", "출국편 공항 도착(분 전)"}} {
		if strings.TrimSpace(raw[bf[0]]) == "" {
			continue
		}
		if v, ok := jsNumber(raw[bf[0]]); !(ok && v >= 0 && v <= maxBufferMin) {
			bad(bf[1], "0~"+strconv.Itoa(maxBufferMin)+" 사이의 숫자여야 합니다")
		}
	}
	if raw["travelMode"] != "" {
		if _, ok := travelModes[raw["travelMode"]]; !ok {
			bad("이동수단", strings.Join(travelOrder, " / ")+" 중 하나여야 합니다")
		}
	}
	for _, df := range [][2]string{{"currencyDecimals", "현지 통화 소수 자릿수"}, {"homeDecimals", "환산 통화 소수 자릿수"}} {
		if v := strings.TrimSpace(raw[df[0]]); v != "" && !digitRe.MatchString(v) {
			bad(df[1], "0, 1, 2 중 하나여야 합니다")
		}
	}
	decimals := func(key string) int { return min(2, max(0, toInt(raw[key], 0))) }
	if cal := raw["calendarId"]; cal != "" && (strings.ContainsAny(cal, " \t\r\n\f\v") || len([]rune(cal)) > 200) {
		bad("캘린더 ID", "공백 없는 캘린더 ID여야 합니다 (캘린더 설정 → 캘린더 통합)")
	}
	if raw["tileUrl"] != "" && !isTileURL(raw["tileUrl"]) {
		bad("지도 타일 주소", "https:// 로 시작하고 {z} {x} {y} 가 들어 있어야 합니다")
	}

	defaultStart := "09:00"
	if timeRe.MatchString(raw["defaultStart"]) {
		defaultStart = normTime(raw["defaultStart"])
	}
	tz := raw["timezone"]
	if tz == "" {
		tz = "UTC"
	}
	mode := travelModes[raw["travelMode"]]
	if mode == "" {
		mode = "driving"
	}
	tile := ""
	if isTileURL(raw["tileUrl"]) {
		tile = raw["tileUrl"]
	}
	return Config{
		Title: raw["title"], Subtitle: raw["subtitle"], Start: raw["start"], End: raw["end"], Days: days,
		Timezone: tz, Offset: offset, DestKeywords: splitList(raw["destKeywords"]),
		LocalCurrency: raw["localCurrency"], LocalSymbol: raw["localSymbol"], HomeSymbol: raw["homeSymbol"], Rate: rate,
		Payers: splitList(raw["payers"]), Methods: splitList(raw["methods"]), ExpenseSheet: raw["expenseSheet"],
		ArrivalBufferMin: toInt(raw["arrivalBufferMin"], 60), FlightLeadMin: toInt(raw["flightLeadMin"], 90),
		DefaultStart: defaultStart, TravelMode: mode,
		CurrencyDecimals: decimals("currencyDecimals"), HomeDecimals: decimals("homeDecimals"),
		TileURL: tile, Missing: missing, Invalid: invalid, HasMyMaps: mid != "",
		MapID: mid, CalendarID: raw["calendarId"],
	}
}

func isTileURL(u string) bool {
	if !tileRe.MatchString(u) {
		return false
	}
	for _, t := range []string{"{z}", "{x}", "{y}"} {
		if !strings.Contains(u, t) {
			return false
		}
	}
	return true
}

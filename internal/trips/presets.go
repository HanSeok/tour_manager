package trips

// Preset is one destination the new-trip form offers: choosing it fills the time zone, currency and rate.
// Rates are ROUGH hints in won per one unit (not live quotes); the form tells the user to check them before leaving.
type Preset struct {
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
	Symbol   string `json:"symbol"`
	Rate     string `json:"rate"`
}

var presets = []Preset{
	{"일본", "Asia/Tokyo", "엔", "¥", "9.3"}, // first = the default of a blank form (see Defaults)
	{"대만", "Asia/Taipei", "대만 달러", "NT$", "44"},
	{"중국 (베이징·상하이)", "Asia/Shanghai", "위안", "CN¥", "190"},
	{"홍콩", "Asia/Hong_Kong", "홍콩 달러", "HK$", "180"},
	{"태국", "Asia/Bangkok", "바트", "฿", "40"},
	{"베트남", "Asia/Ho_Chi_Minh", "동", "₫", "0.055"},
	{"싱가포르", "Asia/Singapore", "싱가포르 달러", "S$", "1050"},
	{"말레이시아", "Asia/Kuala_Lumpur", "링깃", "RM", "310"},
	{"인도네시아 (발리)", "Asia/Makassar", "루피아", "Rp", "0.085"},
	{"필리핀", "Asia/Manila", "페소", "₱", "25"},
	{"미국 서부", "America/Los_Angeles", "달러", "$", "1400"},
	{"미국 동부", "America/New_York", "달러", "$", "1400"},
	{"하와이", "Pacific/Honolulu", "달러", "$", "1400"},
	{"괌", "Pacific/Guam", "달러", "$", "1400"},
	{"영국", "Europe/London", "파운드", "£", "1800"},
	{"프랑스", "Europe/Paris", "유로", "€", "1500"},
	{"이탈리아", "Europe/Rome", "유로", "€", "1500"},
	{"스페인", "Europe/Madrid", "유로", "€", "1500"},
	{"독일", "Europe/Berlin", "유로", "€", "1500"},
	{"호주 (시드니)", "Australia/Sydney", "호주 달러", "A$", "900"},
	{"한국 (국내)", "Asia/Seoul", "원", "₩", "1"},
}

// Presets returns the destination choices (a copy).
func Presets() []Preset { return append([]Preset(nil), presets...) }

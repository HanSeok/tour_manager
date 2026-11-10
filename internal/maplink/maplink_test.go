package maplink

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func mustParse(t *testing.T, raw string) Result {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := Parse(u)
	if !ok {
		t.Fatalf("no result for %s", raw)
	}
	return r
}

func TestParseReadsNameAndThePinCoordinatesNotTheMapCentre(t *testing.T) {
	// the @ part is where the map is centred; !3d/!4d is the place's own pin
	r := mustParse(t, "https://www.google.com/maps/place/Shuri+Castle/@26.2000,127.7000,17z/data=!3m1!4b1!4m6!3m5!1s0x34e5:0xabc!8m2!3d26.2169!4d127.7196!16s%2Fm%2F0abc")
	if r.Name != "Shuri Castle" || r.Lat != 26.2169 || r.Lng != 127.7196 {
		t.Errorf("%+v", r)
	}
}

func TestParseDecodesKoreanNames(t *testing.T) {
	r := mustParse(t, "https://www.google.com/maps/place/%EC%8A%88%EB%A6%AC%EC%84%B1/@26.2170,127.7195,17z/data=!4m2!3m1!1s0x0:0x1")
	if r.Name != "슈리성" || r.Lat != 26.217 || r.Lng != 127.7195 {
		t.Errorf("%+v", r)
	}
}

func TestParseFallsBackToTheMapCentreWhenThereIsNoPin(t *testing.T) {
	r := mustParse(t, "https://www.google.com/maps/place/Cafe/@35.6586,139.7454,17z")
	if r.Lat != 35.6586 || r.Lng != 139.7454 {
		t.Errorf("%+v", r)
	}
}

func TestParseCoordinateQueries(t *testing.T) {
	for _, raw := range []string{
		"https://www.google.com/maps?q=26.2170,127.7195",
		"https://maps.google.com/?q=26.2170%2C127.7195",
		"https://www.google.com/maps/search/?api=1&query=26.2170,127.7195",
		"https://www.google.com/maps?ll=26.2170,127.7195&z=15",
		"https://www.google.com/maps/@26.2170,127.7195,15z",
	} {
		r := mustParse(t, raw)
		if r.Lat != 26.217 || r.Lng != 127.7195 || r.Name != "" {
			t.Errorf("%s → %+v", raw, r)
		}
	}
}

func TestParseTakesTheNameFromASearchPath(t *testing.T) {
	r := mustParse(t, "https://www.google.com/maps/search/%ED%95%B4%EB%B3%80+%EC%B9%B4%ED%8E%98/@26.4,127.7,14z")
	if r.Name != "해변 카페" || r.Lat != 26.4 {
		t.Errorf("%+v", r)
	}
}

func TestParseRefusesWhatIsNotAPlace(t *testing.T) {
	for _, raw := range []string{
		"https://www.google.com/maps/place/Shuri+Castle",             // no coordinates anywhere
		"https://www.google.com/maps/@91.0,127.0,15z",                // out of range
		"https://www.google.com/maps/@0.0,0.0,15z",                   // the "nothing" point
		"https://www.google.com/search?q=26.2,127.7",                 // not a maps URL
		"https://www.google.com/maps/dir/Naha/Shuri/@26.2,127.7,12z", // directions, not a place
	} {
		u, _ := url.Parse(raw)
		if r, ok := Parse(u); ok {
			t.Errorf("%s → %+v", raw, r)
		}
	}
}

func TestParseIgnoresACoordinateLikeName(t *testing.T) {
	r := mustParse(t, "https://www.google.com/maps/place/26.2170,127.7195/@26.2170,127.7195,17z")
	if r.Name != "" {
		t.Errorf("a pair of numbers is not a name: %+v", r)
	}
}

func TestFirstURLPicksTheLinkOutOfSharedText(t *testing.T) {
	got := firstURL("슈리성\n오키나와 나하시\nhttps://maps.app.goo.gl/AbC123xyz?g_st=ic 에서 확인")
	if got != "https://maps.app.goo.gl/AbC123xyz?g_st=ic" {
		t.Errorf("got %q", got)
	}
	if firstURL("링크 없음") != "" {
		t.Error("no link, no result")
	}
}

func TestConsentPageIsUnwrapped(t *testing.T) {
	long := "https://www.google.com/maps/place/Cafe/@35.6,139.7,17z/data=!3d35.6!4d139.7"
	u, _ := url.Parse("https://consent.google.com/m?continue=" + url.QueryEscape(long) + "&gl=DE")
	if got := unwrapConsent(u).String(); got != long {
		t.Errorf("got %s", got)
	}
}

func TestDefaultHostRuleIsGoogleOnly(t *testing.T) {
	for host, want := range map[string]bool{
		"maps.app.goo.gl": true, "goo.gl": true, "www.google.com": true, "google.com": true, "maps.google.com": true,
		"www.google.co.kr": true, "consent.google.com": true,
		"evil.com": false, "google.com.evil.com": false, "notgoogle.com": false, "127.0.0.1": false, "localhost": false, "goo.gl.evil.com": false,
	} {
		if defaultAllow(host) != want {
			t.Errorf("%s: want %v", host, want)
		}
	}
}

// a fake "google": /s/<id> is a short link that redirects to the long URL
func fake(t *testing.T, hits *int32) (*Resolver, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/s/ok", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		http.Redirect(w, r, "/maps/place/Shuri+Castle/@26.2,127.7,17z/data=!3d26.2169!4d127.7196", http.StatusFound)
	})
	mux.HandleFunc("/s/twohops", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		http.Redirect(w, r, "/s/ok", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/s/loop", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		http.Redirect(w, r, "/s/loop", http.StatusFound)
	})
	mux.HandleFunc("/s/evil", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		http.Redirect(w, r, "https://evil.example/maps/place/x/@1,1,1z", http.StatusFound)
	})
	mux.HandleFunc("/s/page", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		_, _ = w.Write([]byte("<html>no coordinates in here</html>"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	return &Resolver{HTTP: srv.Client(), AllowHTTP: true, AllowHost: func(h string) bool { return h == host }, MaxHops: 4}, srv.URL
}

func TestResolveFollowsAShortLinkToTheCoordinates(t *testing.T) {
	var hits int32
	r, base := fake(t, &hits)
	for _, path := range []string{"/s/ok", "/s/twohops"} {
		got, err := r.Resolve(context.Background(), "여기 "+base+path+" 어때?")
		if err != nil || got.Name != "Shuri Castle" || got.Lat != 26.2169 || got.Lng != 127.7196 {
			t.Errorf("%s: %+v %v", path, got, err)
		}
	}
}

func TestResolveNeedsNoRequestForAFullURL(t *testing.T) {
	var hits int32
	r, base := fake(t, &hits)
	got, err := r.Resolve(context.Background(), base+"/maps/place/Cafe/@35.6,139.7,17z/data=!3d35.61!4d139.71")
	if err != nil || got.Lat != 35.61 || atomic.LoadInt32(&hits) != 0 {
		t.Errorf("%+v %v hits=%d", got, err, hits)
	}
}

func TestResolveStopsAtLoopsForeignHostsAndPagesWithoutCoordinates(t *testing.T) {
	var hits int32
	r, base := fake(t, &hits)
	for _, path := range []string{"/s/loop", "/s/evil", "/s/page"} {
		if _, err := r.Resolve(context.Background(), base+path); err == nil {
			t.Errorf("%s accepted", path)
		} else {
			var in *InputError
			if !errors.As(err, &in) {
				t.Errorf("%s: want InputError, got %T %v", path, err, err)
			}
		}
	}
	if n := atomic.LoadInt32(&hits); n > (4+1)+1+1 { // loop ≤ MaxHops+1 requests, evil 1, page 1
		t.Errorf("too many requests: %d", n)
	}
}

func TestResolveDoesNotCallOutToLinksOutsideTheAllowedHosts(t *testing.T) {
	var hits int32
	r, _ := fake(t, &hits)
	for _, text := range []string{"https://evil.example/s/ok", "http://169.254.169.254/latest/meta-data", "file:///etc/passwd", "그냥 글자"} {
		if _, err := r.Resolve(context.Background(), text); err == nil {
			t.Errorf("%q accepted", text)
		}
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Error("a request was made")
	}
}

func TestResolveRequiresHTTPSUnlessTestingAllowsIt(t *testing.T) {
	var hits int32
	r, base := fake(t, &hits)
	r.AllowHTTP = false
	if _, err := r.Resolve(context.Background(), base+"/s/ok"); err == nil {
		t.Error("plain http accepted")
	}
}

// the long URL a real maps.app.goo.gl share link (a place in Osaka) redirected to on 2026-11-10
func TestParseARealShareLinkDestination(t *testing.T) {
	r := mustParse(t, "https://www.google.com/maps/place/%EC%98%A4%EC%82%AC%EC%B9%B4+%EA%B3%B5%ED%95%AD+(%EC%9D%B4%ED%83%80%EB%AF%B8+%EA%B3%B5%ED%95%AD)/@34.7907912,135.4356809,16z/data=!3m1!4b1!4m6!3m5!1s0x6000f06df58777b9:0x7411a874d9df4cd!8m2!3d34.7861612!4d135.4380483!16zL20vMDFteWxq?entry=tts")
	if r.Name != "오사카 공항 (이타미 공항)" || r.Lat != 34.7861612 || r.Lng != 135.4380483 {
		t.Errorf("%+v", r)
	}
}

// rt plays "the internet": canned answers per URL, and it records every request so a test can prove none was made.
type rt struct {
	calls  []string
	reply  map[string]string // URL → Location of a 302; an absent URL answers 200 with no redirect
	bodies map[string]string // URL → JSON body of a 200
	status map[string]int    // URL → status to answer instead
}

func (r *rt) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	r.calls = append(r.calls, u)
	h := http.Header{}
	code, body := 200, io.NopCloser(strings.NewReader(""))
	if loc, ok := r.reply[u]; ok {
		h.Set("Location", loc)
		code = 302
	}
	if b, ok := r.bodies[u]; ok {
		body = io.NopCloser(strings.NewReader(b))
	}
	if c, ok := r.status[u]; ok {
		code = c
	}
	return &http.Response{StatusCode: code, Header: h, Body: body, Request: req}, nil
}

func prod(r *rt) *Resolver { return &Resolver{HTTP: &http.Client{Transport: r}} } // production rules: no AllowHost, https only

func TestProductionRulesRefuseEveryForeignTargetWithoutAnyRequest(t *testing.T) {
	for _, text := range []string{
		"https://evil.example/maps/place/x/@1,1,1z", "http://maps.app.goo.gl/x", "https://maps.app.goo.gl:8443/x", "https://127.0.0.1/maps",
		"https://google.com.evil.example/maps", "https://169.254.169.254/latest/meta-data", "https://localhost/maps",
	} {
		n := &rt{}
		if _, err := prod(n).Resolve(context.Background(), text); err == nil || len(n.calls) != 0 {
			t.Errorf("%s: err=%v calls=%v", text, err, n.calls)
		}
	}
}

func TestProductionRulesFollowARealLookingShareLink(t *testing.T) {
	long := "https://www.google.com/maps/place/%EC%8A%88%EB%A6%AC%EC%84%B1/@26.2,127.7,17z/data=!3d26.2169!4d127.7196"
	n := &rt{reply: map[string]string{"https://maps.app.goo.gl/abc": long}}
	res, err := prod(n).Resolve(context.Background(), "https://maps.app.goo.gl/abc")
	if err != nil || res.Name != "슈리성" || res.Lat != 26.2169 || len(n.calls) != 1 {
		t.Errorf("%+v %v calls=%v", res, err, n.calls)
	}
}

func TestARedirectToAForeignHostIsNeverFetched(t *testing.T) {
	n := &rt{reply: map[string]string{"https://maps.app.goo.gl/abc": "https://evil.example/steal"}}
	if _, err := prod(n).Resolve(context.Background(), "https://maps.app.goo.gl/abc"); err == nil || len(n.calls) != 1 {
		t.Errorf("err=%v calls=%v (only the first hop may be requested)", err, n.calls)
	}
}

func TestAConsentPageCannotSmuggleInAForeignContinueTarget(t *testing.T) {
	n := &rt{reply: map[string]string{"https://maps.app.goo.gl/abc": "https://consent.google.com/m?continue=" + url.QueryEscape("https://evil.example/maps/place/x/@1,1,1z")}}
	if _, err := prod(n).Resolve(context.Background(), "https://maps.app.goo.gl/abc"); err == nil || len(n.calls) != 1 {
		t.Errorf("err=%v calls=%v", err, n.calls)
	}
}

func TestAConsentPageWithAGoogleContinueTargetIsFollowed(t *testing.T) {
	long := "https://www.google.com/maps/place/Cafe/@35.6,139.7,17z/data=!3d35.61!4d139.71"
	n := &rt{reply: map[string]string{"https://maps.app.goo.gl/abc": "https://consent.google.com/m?continue=" + url.QueryEscape(long)}}
	res, err := prod(n).Resolve(context.Background(), "https://maps.app.goo.gl/abc")
	if err != nil || res.Lat != 35.61 || len(n.calls) != 1 {
		t.Errorf("%+v %v calls=%v", res, err, n.calls)
	}
}

/* ---------- Kakao Map ---------- */

const kakaoPanel = "https://place-api.map.kakao.com/places/panel3/1650036035"

// trimmed from the real answer for place 1650036035 (2026-11-10): only the fields the reader uses
const kakaoBody = `{"summary":{"confirm_id":"1650036035","name":"카페인큐브","point":{"lon":126.57214735519239,"lat":33.452943673157485},
 "category":{"name":"커피전문점","name1":"음식점","name2":"카페","name3":"커피전문점"},
 "address":{"disp":"제주특별자치도 제주시 첨단로 213-4 JDC엘리트빌딩 1층 (영평동)","road":"제주특별자치도 제주시 첨단로 213-4 JDC엘리트빌딩 1층"}},
 "open_hours":{"headline":{"display_text":"영업 중"}}}`

func TestKakaoLinkWithCoordinatesIsReadWithoutAnyRequest(t *testing.T) {
	n := &rt{}
	res, err := prod(n).Resolve(context.Background(), "https://map.kakao.com/link/map/%EC%B9%B4%EC%B9%B4%EC%98%A4%ED%8C%90%EA%B5%90%EC%98%A4%ED%94%BC%EC%8A%A4,37.402056,127.108212")
	if err != nil || res.Name != "카카오판교오피스" || res.Lat != 37.402056 || res.Lng != 127.108212 || len(n.calls) != 0 {
		t.Errorf("%+v %v calls=%v", res, err, n.calls)
	}
	res, err = prod(n).Resolve(context.Background(), "https://map.kakao.com/link/to/Cafe,%2037.5,127.0")
	if err != nil || res.Lat != 37.5 {
		t.Errorf("link/to: %+v %v", res, err)
	}
}

func TestKakaoPlaceIDsAreRecognised(t *testing.T) {
	for raw, want := range map[string]string{
		"https://place.map.kakao.com/1650036035":           "1650036035",
		"https://place.map.kakao.com/m/1650036035":         "1650036035",
		"https://place.map.kakao.com/1650036035?service=x": "1650036035",
		"https://map.kakao.com/?itemId=1650036035":         "1650036035",
		"https://place.map.kakao.com/abc":                  "",
		"https://place.map.kakao.com/12":                   "", // too short to be an id
		"https://map.kakao.com/?q=cafe":                    "",
		"https://www.google.com/maps/1650036035":           "",
	} {
		u, _ := url.Parse(raw)
		if got := kakaoPlaceID(u); got != want {
			t.Errorf("%s: got %q want %q", raw, got, want)
		}
	}
}

func TestKakaoPlacePageGivesNameAddressPositionAndACategorySuggestion(t *testing.T) {
	n := &rt{bodies: map[string]string{kakaoPanel: kakaoBody}}
	res, err := prod(n).Resolve(context.Background(), "카페 https://place.map.kakao.com/1650036035 여기")
	if err != nil || res.Name != "카페인큐브" || res.Lat != 33.452943673157485 || res.Lng != 126.57214735519239 ||
		res.Layer != "식당" || res.Address != "제주특별자치도 제주시 첨단로 213-4 JDC엘리트빌딩 1층" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(n.calls) != 1 || n.calls[0] != kakaoPanel {
		t.Errorf("only Kakao's own place API may be called: %v", n.calls)
	}
}

func TestKakaoShortLinkLeadsToThePlace(t *testing.T) {
	n := &rt{reply: map[string]string{"https://kko.to/AbCdEf": "https://place.map.kakao.com/1650036035"}, bodies: map[string]string{kakaoPanel: kakaoBody}}
	res, err := prod(n).Resolve(context.Background(), "https://kko.to/AbCdEf")
	if err != nil || res.Name != "카페인큐브" || len(n.calls) != 2 {
		t.Errorf("%+v %v calls=%v", res, err, n.calls)
	}
}

func TestKakaoFailuresAreSortedIntoYourLinkAndTheirServer(t *testing.T) {
	var in *InputError
	n := &rt{status: map[string]int{kakaoPanel: 404}}
	if _, err := prod(n).Resolve(context.Background(), "https://place.map.kakao.com/1650036035"); !errors.As(err, &in) {
		t.Errorf("unknown place: want InputError, got %v", err)
	}
	n = &rt{status: map[string]int{kakaoPanel: 500}}
	if _, err := prod(n).Resolve(context.Background(), "https://place.map.kakao.com/1650036035"); err == nil || errors.As(err, &in) {
		t.Errorf("Kakao down: want a plain error, got %v", err)
	}
	for name, body := range map[string]string{"no point": `{"summary":{"name":"x"}}`, "bad coordinates": `{"summary":{"name":"x","point":{"lat":999,"lon":1}}}`, "not json": "<html>"} {
		n = &rt{bodies: map[string]string{kakaoPanel: body}}
		if res, err := prod(n).Resolve(context.Background(), "https://place.map.kakao.com/1650036035"); err == nil {
			t.Errorf("%s accepted: %+v", name, res)
		}
	}
}

func TestOnlyKakaoHostsAreTrusted(t *testing.T) {
	for _, raw := range []string{"https://place.map.kakao.com.evil.example/1650036035", "https://evilkko.to/x", "http://place.map.kakao.com/1650036035", "https://place.map.kakao.com:8443/1650036035"} {
		n := &rt{}
		if _, err := prod(n).Resolve(context.Background(), raw); err == nil || len(n.calls) != 0 {
			t.Errorf("%s: err=%v calls=%v", raw, err, n.calls)
		}
	}
}

func TestKakaoCategoriesMapToTheDashboardLayers(t *testing.T) {
	for _, c := range []struct{ n1, n2, want string }{
		{"음식점", "카페", "식당"}, {"음식점", "한식", "식당"}, {"관광,명소", "자연,공원", "관광"}, {"문화,예술", "박물관", "관광"},
		{"가정,생활", "편의점", "쇼핑"}, {"대형마트", "", "쇼핑"}, {"숙박", "호텔", "숙소"}, {"교통,수송", "공항", "교통"},
		{"교통,수송", "지하철,전철", "교통"}, {"병원", "", ""},
	} {
		if got := layerOfKakao(c.n1, c.n2); got != c.want {
			t.Errorf("%s/%s: got %q want %q", c.n1, c.n2, got, c.want)
		}
	}
}

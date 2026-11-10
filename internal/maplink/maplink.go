// Package maplink reads a place out of a Google Maps or Kakao Map share link: the name and the coordinates. The link is followed
// through its redirects (a share link such as maps.app.goo.gl/... ends at a long URL that carries the pin), and only
// Google's own hosts are ever contacted. No API key and no page content is used — only the URL it leads to.
package maplink

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultHops = 5
	maxName     = 60
	callTimeout = 8 * time.Second
)

// Result is what a link says about the place. Name may be empty (a link that is only coordinates).
type Result struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
	Layer   string  `json:"layer,omitempty"`   // suggested category, when the source says what the place is
	Address string  `json:"address,omitempty"` // street address, when the source gives one
	Link    string  `json:"link,omitempty"`    // the link worth keeping to open the place again (photos, reviews …)
}

// MaxLinkLen is the longest link that is stored with a place.
const MaxLinkLen = 500

// AllowedLink reports whether a link may be stored and shown as a clickable link: https, a Google or Kakao map host,
// no credentials, no port, no whitespace, not absurdly long. Anything else could point somewhere we never vetted.
func AllowedLink(raw string) bool {
	if raw == "" || len(raw) > MaxLinkLen || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Opaque != "" {
		return false
	}
	return defaultAllow(u.Hostname())
}

// withLink attaches the link to keep: Kakao places get their clean address, anything else the link as it was shared.
func withLink(res Result, shared string) Result {
	if u, err := url.Parse(shared); err == nil {
		if id := kakaoPlaceID(u); id != "" {
			shared = "https://place.map.kakao.com/" + id
		}
	}
	if AllowedLink(shared) {
		res.Link = shared
	}
	return res
}

// InputError means the link itself is the problem (not Google being unreachable).
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &InputError{Msg: fmt.Sprintf(format, a...)} }

const num = `-?\d+(?:\.\d+)?`

var (
	pinRe     = regexp.MustCompile(`!3d(` + num + `)!4d(` + num + `)`)
	atRe      = regexp.MustCompile(`@(` + num + `),(` + num + `)`)
	pairRe    = regexp.MustCompile(`^\s*(` + num + `)\s*,\s*(` + num + `)\s*$`)
	urlRe     = regexp.MustCompile(`https?://[^\s<>"']+`)
	googleRe  = regexp.MustCompile(`^(www\.|maps\.|consent\.)?google\.(com|co\.[a-z]{2}|com\.[a-z]{2}|[a-z]{2,3})$`)
	queryKeys = []string{"q", "query", "ll", "center", "destination"}
)

// defaultAllow admits Google's map hosts and its short-link hosts, nothing else (and no explicit ports).
func defaultAllow(host string) bool {
	host = strings.ToLower(host)
	if host == "goo.gl" || host == "maps.app.goo.gl" || kakaoHost(host) {
		return true
	}
	return googleRe.MatchString(host)
}

func firstURL(text string) string {
	m := urlRe.FindString(text)
	return strings.TrimRight(m, ".,;:!?)]}>'\"")
}

// unwrapConsent returns the page a Google consent interstitial (shown to some regions) wants to continue to.
func unwrapConsent(u *url.URL) *url.URL {
	if strings.HasPrefix(strings.ToLower(u.Hostname()), "consent.") {
		if next, err := url.Parse(u.Query().Get("continue")); err == nil && next.Host != "" {
			return next
		}
	}
	return u
}

func coords(lat, lng string) (float64, float64, bool) {
	la, err1 := strconv.ParseFloat(lat, 64)
	lo, err2 := strconv.ParseFloat(lng, 64)
	if err1 != nil || err2 != nil || la < -90 || la > 90 || lo < -180 || lo > 180 || (la == 0 && lo == 0) {
		return 0, 0, false
	}
	return la, lo, true
}

// Parse reads the place from a long Google Maps URL. ok=false: no usable position (e.g. a short link not followed yet).
func Parse(u *url.URL) (Result, bool) {
	isMaps := strings.HasPrefix(u.Path, "/maps") || strings.HasPrefix(strings.ToLower(u.Hostname()), "maps.")
	if !isMaps || strings.HasPrefix(u.Path, "/maps/dir") {
		return Result{}, false
	}
	var lat, lng float64
	found := false
	if m := pinRe.FindStringSubmatch(u.Path + "?" + u.RawQuery); m != nil { // the place's own pin
		lat, lng, found = coords(m[1], m[2])
	}
	if !found {
		q := u.Query()
		for _, k := range queryKeys {
			if m := pairRe.FindStringSubmatch(q.Get(k)); m != nil {
				if lat, lng, found = coords(m[1], m[2]); found {
					break
				}
			}
		}
	}
	if !found { // where the map happens to be centred
		if m := atRe.FindStringSubmatch(u.Path); m != nil {
			lat, lng, found = coords(m[1], m[2])
		}
	}
	if !found {
		return Result{}, false
	}
	return Result{Name: nameOf(u), Lat: lat, Lng: lng}, true
}

// nameOf is the segment after /place/ or /search/ ("+" is a space; a real plus is %2B).
func nameOf(u *url.URL) string {
	segs := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	for i, s := range segs {
		if (s == "place" || s == "search") && i+1 < len(segs) {
			seg := segs[i+1]
			if seg == "" || strings.HasPrefix(seg, "@") || strings.HasPrefix(seg, "data=") {
				return ""
			}
			dec, err := url.PathUnescape(strings.ReplaceAll(seg, "+", " "))
			if err != nil {
				return ""
			}
			dec = strings.TrimSpace(dec)
			if pairRe.MatchString(dec) {
				return ""
			}
			if utf8.RuneCountInString(dec) > maxName {
				dec = string([]rune(dec)[:maxName])
			}
			return dec
		}
	}
	return ""
}

// Resolver follows share links. AllowHost / AllowHTTP exist so tests can talk to a local fake; leave them zero in production.
type Resolver struct {
	HTTP      *http.Client
	AllowHost func(host string) bool // default: Google's map hosts
	AllowHTTP bool
	MaxHops   int
}

func (r *Resolver) allowed(u *url.URL) bool {
	if u.Scheme != "https" && !(r.AllowHTTP && u.Scheme == "http") {
		return false
	}
	if r.AllowHost != nil {
		return r.AllowHost(u.Host)
	}
	return u.Port() == "" && defaultAllow(u.Hostname())
}

// Resolve finds the first link in text (a pasted share message works) and returns the place it points to.
func (r *Resolver) Resolve(ctx context.Context, text string) (Result, error) {
	raw := firstURL(text)
	if raw == "" {
		return Result{}, bad("붙여 넣은 내용에서 링크를 찾지 못했습니다. 구글 지도·카카오맵에서 장소를 열고 '공유' 로 복사한 링크를 붙여 넣으세요")
	}
	cur, err := url.Parse(raw)
	if err != nil {
		return Result{}, bad("링크 형식이 올바르지 않습니다")
	}
	hops := r.MaxHops
	if hops <= 0 {
		hops = defaultHops
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	client := *r.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	for hop := 0; hop <= hops; hop++ {
		if !r.allowed(cur) {
			return Result{}, bad("구글 지도·카카오맵 링크만 읽을 수 있습니다 (maps.app.goo.gl, place.map.kakao.com …)")
		}
		cur = unwrapConsent(cur)
		if !r.allowed(cur) {
			return Result{}, bad("구글 지도·카카오맵 링크만 읽을 수 있습니다 (maps.app.goo.gl, place.map.kakao.com …)")
		}
		if res, ok := Parse(cur); ok {
			return withLink(res, raw), nil
		}
		if res, ok := parseKakaoLink(cur); ok {
			return withLink(res, raw), nil
		}
		if id := kakaoPlaceID(cur); id != "" {
			res, err := kakaoPlace(ctx, &client, id)
			if err != nil {
				return res, err
			}
			return withLink(res, raw), nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur.String(), nil)
		if err != nil {
			return Result{}, bad("링크 형식이 올바르지 않습니다")
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; tour-manager)")
		req.Header.Set("Accept-Language", "ko,en;q=0.8")
		resp, err := client.Do(req)
		if err != nil {
			return Result{}, fmt.Errorf("maplink: %w", err) // Google unreachable / timeout: not the user's mistake
		}
		resp.Body.Close() // only the redirect matters, never the page
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode > 399 || loc == "" {
			return Result{}, bad("이 링크에서 위치를 찾지 못했습니다. 장소를 연 뒤 '공유' 로 복사한 링크(maps.app.goo.gl …)를 붙여 넣어 주세요")
		}
		next, err := cur.Parse(loc)
		if err != nil {
			return Result{}, bad("링크 형식이 올바르지 않습니다")
		}
		cur = next
	}
	return Result{}, bad("링크가 너무 많이 이동합니다. 장소 페이지의 공유 링크를 붙여 넣어 주세요")
}

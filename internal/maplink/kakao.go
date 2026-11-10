package maplink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kakao Map. A place page (place.map.kakao.com/<id>) is a script-driven page with no coordinates in its HTML; the page
// itself loads them from place-api.map.kakao.com. That endpoint is not a documented public API, so it can change without
// notice: a failure there is reported as "could not read", and nothing else depends on it.
const kakaoAPI = "https://place-api.map.kakao.com/places/panel3/"

var (
	kakaoIDRe   = regexp.MustCompile(`^/(?:m/)?(\d{5,})/?$`)
	kakaoItemRe = regexp.MustCompile(`^\d{5,}$`)
)

func kakaoHost(host string) bool {
	switch strings.ToLower(host) {
	case "place.map.kakao.com", "map.kakao.com", "kko.to":
		return true
	}
	return false
}

// kakaoPlaceID is the numeric place id in a Kakao place link, or "".
func kakaoPlaceID(u *url.URL) string {
	switch strings.ToLower(u.Hostname()) {
	case "place.map.kakao.com":
		if m := kakaoIDRe.FindStringSubmatch(u.Path); m != nil {
			return m[1]
		}
	case "map.kakao.com":
		if id := u.Query().Get("itemId"); kakaoItemRe.MatchString(id) {
			return id
		}
	}
	return ""
}

// parseKakaoLink reads Kakao's documented link format: map.kakao.com/link/map/<name>,<lat>,<lng> (also /link/to, /link/from).
func parseKakaoLink(u *url.URL) (Result, bool) {
	if strings.ToLower(u.Hostname()) != "map.kakao.com" {
		return Result{}, false
	}
	var rest string
	for _, p := range []string{"/link/map/", "/link/to/", "/link/from/"} {
		if strings.HasPrefix(u.Path, p) {
			rest = strings.TrimPrefix(u.Path, p)
		}
	}
	parts := strings.Split(rest, ",")
	if len(parts) < 3 {
		return Result{}, false
	}
	lat, lng, ok := coords(strings.TrimSpace(parts[len(parts)-2]), strings.TrimSpace(parts[len(parts)-1]))
	if !ok {
		return Result{}, false
	}
	name := strings.TrimSpace(strings.Join(parts[:len(parts)-2], ","))
	if utf8.RuneCountInString(name) > maxName {
		name = string([]rune(name)[:maxName])
	}
	return Result{Name: name, Lat: lat, Lng: lng}, true
}

// layerOfKakao suggests one of the dashboard's categories from Kakao's category names ("" = no suggestion).
func layerOfKakao(name1, name2 string) string {
	has := func(s string, words ...string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has(name1, "음식점") || has(name2, "카페"):
		return "식당"
	case has(name1, "숙박"):
		return "숙소"
	case has(name1, "교통") || has(name2, "공항", "지하철", "주차장"):
		return "교통"
	case has(name1, "마트", "쇼핑", "유통") || has(name2, "편의점", "백화점", "시장", "쇼핑", "마트"):
		return "쇼핑"
	case has(name1, "관광", "명소", "문화", "예술"):
		return "관광"
	}
	return ""
}

// kakaoPlace reads the name, address, position and category of a place from Kakao's own place API.
func kakaoPlace(ctx context.Context, client *http.Client, id string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kakaoAPI+id, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; tour-manager)")
	req.Header.Set("Referer", "https://place.map.kakao.com/")
	req.Header.Set("pf", "web")
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("maplink: kakao: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Result{}, bad("카카오맵에서 그 장소를 찾지 못했습니다. 링크를 다시 확인하세요")
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("maplink: kakao: status %d", resp.StatusCode)
	}
	var p struct {
		Summary struct {
			Name  string `json:"name"`
			Point struct {
				Lat *float64 `json:"lat"`
				Lon *float64 `json:"lon"`
			} `json:"point"`
			Category struct {
				Name1 string `json:"name1"`
				Name2 string `json:"name2"`
			} `json:"category"`
			Address struct {
				Disp string `json:"disp"`
				Road string `json:"road"`
			} `json:"address"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&p); err != nil {
		return Result{}, fmt.Errorf("maplink: kakao: unreadable answer: %w", err)
	}
	s := p.Summary
	if s.Point.Lat == nil || s.Point.Lon == nil {
		return Result{}, bad("이 카카오맵 장소의 위치를 읽지 못했습니다")
	}
	lat, lng, ok := coords(strconv.FormatFloat(*s.Point.Lat, 'f', -1, 64), strconv.FormatFloat(*s.Point.Lon, 'f', -1, 64))
	if !ok {
		return Result{}, bad("이 카카오맵 장소의 위치를 읽지 못했습니다")
	}
	name := strings.TrimSpace(s.Name)
	if utf8.RuneCountInString(name) > maxName {
		name = string([]rune(name)[:maxName])
	}
	addr := strings.TrimSpace(s.Address.Road)
	if addr == "" {
		addr = strings.TrimSpace(s.Address.Disp)
	}
	return Result{Name: name, Lat: lat, Lng: lng, Layer: layerOfKakao(s.Category.Name1, s.Category.Name2), Address: addr}, nil
}

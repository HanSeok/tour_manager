package places

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tourmanager/internal/audit"
	"tourmanager/internal/maplink"
	"tourmanager/internal/sheets"
)

type recLog struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recLog) Record(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func newSvc() (*Service, *sheets.MemGrid, *recLog) {
	g := sheets.NewMemGrid(nil)
	l := &recLog{}
	return &Service{Grid: g, Log: l}, g, l
}

const id1 = "11111111-1111-4111-8111-111111111111"

func okInput() Input {
	return Input{ID: id1, Name: "나하 공항 근처 라멘", Layer: "식당", Lat: 26.2, Lng: 127.65, Memo: "줄 길다"}
}

func TestAddStoresThePlaceWithItsAuthorAndCreatesTheTab(t *testing.T) {
	s, g, _ := newSvc()
	list, err := s.Add(context.Background(), "a@x.com", okInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	p := list[0]
	if p.ID != id1 || p.Name != "나하 공항 근처 라멘" || p.Layer != "식당" || p.Lat != 26.2 || p.Lng != 127.65 || p.Memo != "줄 길다" ||
		p.CreatedBy != "a@x.com" || p.UpdatedBy != "a@x.com" || p.UpdatedAt == "" {
		t.Errorf("place = %+v", p)
	}
	rows, _ := g.Rows(context.Background(), Tab)
	if len(rows) != 2 || strings.Join(rows[0], "|") != strings.Join(Header, "|") {
		t.Errorf("tab = %v", rows)
	}
}

func TestAddIsIdempotentForTheSameClientID(t *testing.T) {
	s, _, l := newSvc()
	for i := 0; i < 3; i++ {
		if _, err := s.Add(context.Background(), "a@x.com", okInput()); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.List(context.Background())
	if len(list) != 1 || len(l.entries) != 1 {
		t.Errorf("retries must not duplicate: %d places, %d history rows", len(list), len(l.entries))
	}
}

func TestAServerIDIsMadeWhenTheClientSendsNone(t *testing.T) {
	s, _, _ := newSvc()
	in := okInput()
	in.ID = "not-a-uuid"
	list, err := s.Add(context.Background(), "a@x.com", in)
	if err != nil || len(list) != 1 || list[0].ID == "not-a-uuid" || len(list[0].ID) != 36 {
		t.Errorf("%v %+v", err, list)
	}
}

func TestInvalidInputIsRejectedAndNothingIsStored(t *testing.T) {
	cases := map[string]func(*Input){
		"이름 없음":       func(i *Input) { i.Name = "   " },
		"이름 너무 김":     func(i *Input) { i.Name = strings.Repeat("가", 61) },
		"분류":          func(i *Input) { i.Layer = "놀이공원" },
		"위도 범위":       func(i *Input) { i.Lat = 91.0 },
		"경도 범위":       func(i *Input) { i.Lng = -181.0 },
		"위도 문자":       func(i *Input) { i.Lat = "abc" },
		"위도 없음":       func(i *Input) { i.Lat = nil },
		"메모 너무 김":     func(i *Input) { i.Memo = strings.Repeat("a", 201) },
		"제어문자 이름":     func(i *Input) { i.Name = "a\nb" },
		"0,0 좌표(미입력)": func(i *Input) { i.Lat, i.Lng = 0.0, 0.0 },
	}
	for name, mut := range cases {
		s, g, _ := newSvc()
		in := okInput()
		mut(&in)
		_, err := s.Add(context.Background(), "a@x.com", in)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: want ValidationError, got %v", name, err)
		}
		if rows, _ := g.Rows(context.Background(), Tab); len(rows) > 1 {
			t.Errorf("%s: stored anyway: %v", name, rows)
		}
	}
}

func TestCoordinatesMayArriveAsNumericStringsAndTextIsTrimmed(t *testing.T) {
	s, _, _ := newSvc()
	in := okInput()
	in.Lat, in.Lng, in.Name = "26.5", " 127.7 ", "  카페  "
	list, err := s.Add(context.Background(), "a@x.com", in)
	if err != nil || list[0].Lat != 26.5 || list[0].Lng != 127.7 || list[0].Name != "카페" {
		t.Errorf("%v %+v", err, list)
	}
}

func TestFormulaLikeTextIsStoredAsTextNeverEvaluated(t *testing.T) {
	s, g, _ := newSvc()
	in := okInput()
	in.Name, in.Memo = "=HYPERLINK(\"http://evil\")", "+cmd"
	list, err := s.Add(context.Background(), "a@x.com", in)
	if err != nil || list[0].Name != in.Name || list[0].Memo != "+cmd" {
		t.Fatalf("%v %+v", err, list)
	}
	rows, _ := g.Rows(context.Background(), Tab)
	if rows[1][1] != in.Name {
		t.Errorf("cell = %q", rows[1][1])
	}
}

func TestUpdateKeepsTheCreatorAndRecordsTheEditor(t *testing.T) {
	s, _, l := newSvc()
	_, _ = s.Add(context.Background(), "a@x.com", okInput())
	in := okInput()
	in.Name = "새 이름"
	list, err := s.Update(context.Background(), "b@x.com", id1, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := list[0]
	if p.Name != "새 이름" || p.CreatedBy != "a@x.com" || p.UpdatedBy != "b@x.com" {
		t.Errorf("place = %+v", p)
	}
	last := l.entries[len(l.entries)-1]
	if last.Action != "update" || last.Tab != Tab || !strings.Contains(last.Before, "나하 공항 근처 라멘") || !strings.Contains(last.After, "새 이름") {
		t.Errorf("history = %+v", last)
	}
}

func TestStaleEditsAndDeletesAreRefusedWithAConflict(t *testing.T) {
	s, _, _ := newSvc()
	list, _ := s.Add(context.Background(), "a@x.com", okInput())
	seen := list[0].UpdatedAt
	in := okInput()
	in.Name = "b가 먼저 고침"
	if _, err := s.Update(context.Background(), "b@x.com", id1, in, &seen); err != nil {
		t.Fatal(err)
	}
	var e *Error
	if _, err := s.Update(context.Background(), "a@x.com", id1, okInput(), &seen); !errors.As(err, &e) || e.Kind != Conflict {
		t.Errorf("stale update: %v", err)
	}
	if _, err := s.Delete(context.Background(), "a@x.com", id1, &seen); !errors.As(err, &e) || e.Kind != Conflict {
		t.Errorf("stale delete: %v", err)
	}
	if cur, _ := s.List(context.Background()); len(cur) != 1 || cur[0].Name != "b가 먼저 고침" {
		t.Errorf("the other person's edit must survive: %+v", cur)
	}
}

func TestDeleteRemovesTheRowAndKeepsItsLastValuesInTheHistory(t *testing.T) {
	s, _, l := newSvc()
	_, _ = s.Add(context.Background(), "a@x.com", okInput())
	list, err := s.Delete(context.Background(), "b@x.com", id1, nil)
	if err != nil || len(list) != 0 {
		t.Fatalf("%v %+v", err, list)
	}
	last := l.entries[len(l.entries)-1]
	if last.Action != "delete" || last.Email != "b@x.com" || !strings.Contains(last.Before, "나하 공항 근처 라멘") {
		t.Errorf("history = %+v", last)
	}
	var e *Error
	if _, err := s.Delete(context.Background(), "b@x.com", id1, nil); !errors.As(err, &e) || e.Kind != NotFound {
		t.Errorf("second delete: %v", err)
	}
	if _, err := s.Update(context.Background(), "b@x.com", id1, okInput(), nil); !errors.As(err, &e) || e.Kind != NotFound {
		t.Errorf("update of a deleted place: %v", err)
	}
}

func TestTheVersionAlwaysMovesForwardEvenWithAFrozenClock(t *testing.T) {
	s, _, _ := newSvc()
	frozen := time.Date(2027, 4, 10, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return frozen }
	list, _ := s.Add(context.Background(), "a@x.com", okInput())
	v1 := list[0].UpdatedAt
	list, err := s.Update(context.Background(), "a@x.com", id1, okInput(), &v1)
	if err != nil || list[0].UpdatedAt <= v1 {
		t.Errorf("version did not advance: %q -> %q (%v)", v1, list[0].UpdatedAt, err)
	}
}

func TestThereIsALimitOnHowManyPlacesATripHolds(t *testing.T) {
	s, _, _ := newSvc()
	rows := [][]any{}
	for i := 0; i < MaxPlaces; i++ {
		rows = append(rows, []any{"id" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), "p", "관광", 26.0, 127.0, "", "a", "a", "t"})
	}
	_, _ = s.Grid.EnsureTab(context.Background(), Tab, Header)
	if err := s.Grid.(interface {
		PutRows(context.Context, string, int, [][]any) error
	}).PutRows(context.Background(), Tab, 2, rows); err != nil {
		t.Fatal(err)
	}
	in := okInput()
	in.ID = "22222222-2222-4222-8222-222222222222"
	var ve *ValidationError
	if _, err := s.Add(context.Background(), "a@x.com", in); !errors.As(err, &ve) {
		t.Errorf("want ValidationError at the limit, got %v", err)
	}
}

func TestTheSourceLinkIsStoredAndReturned(t *testing.T) {
	s, g, _ := newSvc()
	in := okInput()
	in.Link = "https://maps.app.goo.gl/waTe1ppztvxpCJWXA"
	list, err := s.Add(context.Background(), "a@x.com", in)
	if err != nil || list[0].Link != in.Link {
		t.Fatalf("%v %+v", err, list)
	}
	rows, _ := g.Rows(context.Background(), Tab)
	if len(Header) != 11 || Header[9] != "링크" || Header[10] != "영업시간" || len(rows[1]) != 11 || rows[1][9] != in.Link {
		t.Errorf("header=%v row=%v", Header, rows[1])
	}
	in.Link = "https://place.map.kakao.com/1650036035"
	list, err = s.Update(context.Background(), "b@x.com", id1, in, nil)
	if err != nil || list[0].Link != in.Link {
		t.Errorf("update: %v %+v", err, list)
	}
}

func TestOnlyGoogleAndKakaoLinksAreAccepted(t *testing.T) {
	for _, link := range []string{
		"javascript:alert(1)", "http://maps.app.goo.gl/x", "https://evil.example/x", "https://maps.app.goo.gl.evil.example/x",
		"https://maps.app.goo.gl:8443/x", "//maps.app.goo.gl/x", "data:text/html,x", "https://maps.app.goo.gl/" + strings.Repeat("a", 500),
		"https://maps.app.goo.gl/x y", "https://user:pw@maps.app.goo.gl/x",
	} {
		s, g, _ := newSvc()
		in := okInput()
		in.Link = link
		var ve *ValidationError
		if _, err := s.Add(context.Background(), "a@x.com", in); !errors.As(err, &ve) {
			t.Errorf("%q: want ValidationError, got %v", link, err)
		}
		if rows, _ := g.Rows(context.Background(), Tab); len(rows) > 1 {
			t.Errorf("%q stored", link)
		}
	}
	s, _, _ := newSvc()
	in := okInput()
	for _, ok := range []string{"", "https://maps.app.goo.gl/waTe1ppztvxpCJWXA", "https://www.google.com/maps/place/x/@1,2,3z", "https://place.map.kakao.com/1650036035", "https://kko.to/abc"} {
		in.Link = ok
		in.ID = "" // a new place each time
		if _, err := s.Add(context.Background(), "a@x.com", in); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

func TestPlacesStoredBeforeTheLinkColumnStillReadAndTheHeaderIsCompleted(t *testing.T) {
	old := [][]string{
		{"ID", "이름", "분류", "위도", "경도", "메모", "기록자", "수정자", "수정시각"},
		{id1, "옛 장소", "관광", "26.2", "127.7", "", "a@x.com", "a@x.com", "2027-04-10T00:00:00.000Z"},
	}
	g := sheets.NewMemGrid(map[string][][]string{Tab: old})
	s := &Service{Grid: g}
	list, err := s.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "옛 장소" || list[0].Link != "" {
		t.Fatalf("%v %+v", err, list)
	}
	rows, _ := g.Rows(context.Background(), Tab)
	if len(rows[0]) != len(Header) || rows[0][9] != "링크" {
		t.Errorf("the header was not completed: %v", rows[0])
	}
	in := okInput()
	in.Link = "https://kko.to/abc"
	if list, err = s.Update(context.Background(), "b@x.com", id1, in, nil); err != nil || list[0].Link != in.Link {
		t.Errorf("%v %+v", err, list)
	}
}

func TestOpeningHoursAreStoredAsJSONInTheirOwnColumnAndReturned(t *testing.T) {
	s, g, _ := newSvc()
	in := okInput()
	in.Hours = maplink.Hours{1: {{480, 1080}}, 6: {}}
	list, err := s.Add(context.Background(), "a@x.com", in)
	if err != nil || len(list[0].Hours) != 2 || list[0].Hours[1][0] != [2]int{480, 1080} || list[0].Hours[6] == nil || len(list[0].Hours[6]) != 0 {
		t.Fatalf("%v %+v", err, list)
	}
	rows, _ := g.Rows(context.Background(), Tab)
	if rows[1][10] != `{"1":[[480,1080]],"6":[]}` {
		t.Errorf("the cell is compact JSON: %q", rows[1][10])
	}
	// a place stored without hours has none; an edit may add, change or clear them
	in.ID = "22222222-2222-4222-8222-222222222222"
	in.Hours = nil
	list, _ = s.Add(context.Background(), "a@x.com", in)
	if list[1].Hours != nil {
		t.Errorf("no hours given: %v", list[1].Hours)
	}
	in.Hours = maplink.Hours{2: {{600, 700}}}
	if list, err = s.Update(context.Background(), "b@x.com", in.ID, in, nil); err != nil || list[1].Hours[2][0] != [2]int{600, 700} {
		t.Errorf("update adds hours: %v %+v", err, list)
	}
	in.Hours = nil
	if list, err = s.Update(context.Background(), "b@x.com", in.ID, in, nil); err != nil || list[1].Hours != nil {
		t.Errorf("update clears hours: %v %+v", err, list)
	}
}

func TestNonsenseHoursAreRefusedAndNothingIsStored(t *testing.T) {
	for _, h := range []maplink.Hours{{7: {{1, 2}}}, {1: {{700, 600}}}, {1: {{0, 3000}}}, {1: {{1, 2}, {3, 4}, {5, 6}, {7, 8}, {9, 10}}}} {
		s, g, _ := newSvc()
		in := okInput()
		in.Hours = h
		var ve *ValidationError
		if _, err := s.Add(context.Background(), "a@x.com", in); !errors.As(err, &ve) {
			t.Errorf("%v: want ValidationError, got %v", h, err)
		}
		if rows, _ := g.Rows(context.Background(), Tab); len(rows) > 1 {
			t.Errorf("%v stored", h)
		}
	}
}

func TestAHandEditedOrBrokenHoursCellNeverBreaksTheList(t *testing.T) {
	row := func(cell string) []string {
		return []string{id1, "장소", "식당", "26.2", "127.7", "", "a@x.com", "a@x.com", "2027-04-10T00:00:00.000Z", "", cell}
	}
	for _, cell := range []string{"", "not json", `{"9":[[1,2]]}`, `{"1":[[900,100]]}`, `[1,2]`, `{"1":"x"}`} {
		g := sheets.NewMemGrid(map[string][][]string{Tab: {Header, row(cell)}})
		list, err := (&Service{Grid: g}).List(context.Background())
		if err != nil || len(list) != 1 || list[0].Hours != nil {
			t.Errorf("%q: %v %+v", cell, err, list)
		}
	}
}

func TestPlacesStoredBeforeTheHoursColumnStillReadAndTheHeaderIsCompleted(t *testing.T) {
	old := [][]string{
		{"ID", "이름", "분류", "위도", "경도", "메모", "기록자", "수정자", "수정시각", "링크"},
		{id1, "옛 장소", "관광", "26.2", "127.7", "", "a@x.com", "a@x.com", "2027-04-10T00:00:00.000Z", "https://kko.to/abc"},
	}
	g := sheets.NewMemGrid(map[string][][]string{Tab: old})
	list, err := (&Service{Grid: g}).List(context.Background())
	if err != nil || list[0].Link != "https://kko.to/abc" || list[0].Hours != nil {
		t.Fatalf("%v %+v", err, list)
	}
	if rows, _ := g.Rows(context.Background(), Tab); rows[0][10] != "영업시간" {
		t.Errorf("header = %v", rows[0])
	}
}

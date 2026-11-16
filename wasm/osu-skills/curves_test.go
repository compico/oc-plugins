package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const testFile = `165089 senzawa - padowo padowo (PotatoDew)\senzawa - padowo padowo (PotatoDew) [padowo].osu`

// testMap — минимальная карта: серия нажатий с интервалом 150 мс (стрим) и пауза.
func testMap() string {
	var b strings.Builder
	b.WriteString("osu file format v14\n\n[General]\nMode: 0\n\n[Metadata]\nTitle:t\nArtist:a\nCreator:c\nVersion:v\nBeatmapID:1\nBeatmapSetID:1\n\n")
	b.WriteString("[Difficulty]\nHPDrainRate:5\nCircleSize:4\nOverallDifficulty:8\nApproachRate:9\nSliderMultiplier:1.4\nSliderTickRate:1\n\n")
	b.WriteString("[TimingPoints]\n0,333.33,4,2,0,60,1,0\n\n[HitObjects]\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "%d,%d,%d,1,0,0:0:0:0:\n", 100+(i%2)*200, 100+(i%3)*50, 1000+i*150)
	}
	return b.String()
}

type fakeHost struct {
	ids  []string
	urls []string
	fail bool
	now  time.Time
}

func newTestService() (*Service, *fakeHost) {
	h := &fakeHost{now: time.Unix(1_700_000_000, 0)}
	s := NewService(DefaultTosu, func(id, url string) error {
		if h.fail {
			return fmt.Errorf("denied")
		}
		h.ids, h.urls = append(h.ids, id), append(h.urls, url)
		return nil
	}, func() time.Time { return h.now })
	return s, h
}

// answer отвечает на последний запрос к tosu.
func (h *fakeHost) answer(s *Service, status int, body, err string) {
	s.OnFetched(h.ids[len(h.ids)-1], status, body, err)
}

func TestCurvesFetchesFromTosuThenAnswers(t *testing.T) {
	s, h := newTestService()
	if r := s.Curves(Request{File: testFile}); r.Status != http.StatusAccepted {
		t.Fatalf("первый запрос: %d, ждали 202", r.Status)
	}
	want := "http://127.0.0.1:24050/files/beatmap/165089%20senzawa%20-%20padowo%20padowo%20%28PotatoDew%29/senzawa%20-%20padowo%20padowo%20%28PotatoDew%29%20%5Bpadowo%5D.osu"
	if len(h.urls) != 1 || h.urls[0] != want {
		t.Fatalf("адрес файла: %v\nждали %s", h.urls, want)
	}
	if len(h.ids[0]) > 64 {
		t.Fatalf("id запроса длиннее 64 символов: %q", h.ids[0])
	}
	// Пока файл загружается, повторы не плодят запросов.
	if r := s.Curves(Request{File: testFile}); r.Status != http.StatusAccepted || len(h.urls) != 1 {
		t.Fatalf("повтор: %d, запросов %d", r.Status, len(h.urls))
	}
	h.answer(s, 200, testMap(), "")
	r := s.Curves(Request{File: testFile})
	if r.Status != http.StatusOK {
		t.Fatalf("после загрузки: %d %v", r.Status, r.Body)
	}
	resp := r.Body.(Response)
	if resp.Window != defaultWindow || len(resp.Stamina) == 0 || len(resp.Agility) == 0 {
		t.Fatalf("ответ: %+v", resp)
	}
	if len(resp.Memory) != 0 {
		t.Fatal("память без FL должна быть пустым списком")
	}
}

func TestCurvesAcceptsSlashesAndCustomTosuHost(t *testing.T) {
	s, h := newTestService()
	s.Tosu = "localhost:9999"
	s.Curves(Request{File: "1 a/b c.OSU"})
	if len(h.urls) != 1 || h.urls[0] != "http://localhost:9999/files/beatmap/1%20a/b%20c.OSU" {
		t.Fatalf("%v", h.urls)
	}
}

func TestCurvesModsAndCache(t *testing.T) {
	s, h := newTestService()
	s.Curves(Request{File: testFile})
	h.answer(s, 200, testMap(), "")
	nm := s.Curves(Request{File: testFile}).Body.(Response)
	dt := s.Curves(Request{File: testFile, Mods: "dt"}).Body.(Response)
	if dt.Speed != 1.5 || nm.Speed != 1 {
		t.Fatalf("скорость: nm %v dt %v", nm.Speed, dt.Speed)
	}
	fl := s.Curves(Request{File: testFile, Mods: "FL"}).Body.(Response)
	if len(fl.Memory) == 0 {
		t.Fatal("с FL память должна считаться")
	}
	if len(h.urls) != 1 {
		t.Fatalf("смена модов не должна качать файл заново: %v", h.urls)
	}
}

func TestChecksumKeepsCacheFresh(t *testing.T) {
	s, h := newTestService()
	a, b := strings.Repeat("a", 32), strings.Repeat("b", 32)
	s.Curves(Request{File: testFile, Checksum: a})
	h.answer(s, 200, testMap(), "")
	if r := s.Curves(Request{File: testFile, Checksum: a}); r.Status != http.StatusOK {
		t.Fatalf("та же сумма: %d", r.Status)
	}
	// Карту поправили в редакторе: путь тот же, сумма другая — файл берётся заново.
	if r := s.Curves(Request{File: testFile, Checksum: b}); r.Status != http.StatusAccepted || len(h.urls) != 2 {
		t.Fatalf("новая сумма: %d, запросов %d", r.Status, len(h.urls))
	}
}

func TestCurvesRejectsBadInput(t *testing.T) {
	s, h := newTestService()
	for _, req := range []Request{
		{}, {File: "x.txt"}, {File: ".osu"}, {File: "../secret.osu"}, {File: "a/../../json/v2.osu"}, {File: `a\..\b.osu`}, {File: "/abs/x.osu"},
		{File: `\\srv\x.osu`}, {File: "C:\\Songs\\x.osu"}, {File: "a/./b.osu"}, {File: "a\x00b.osu"}, {File: strings.Repeat("a", 600) + ".osu"},
		{File: testFile, Mods: "XX"}, {File: testFile, Mods: "H"}, {File: testFile, Window: -1}, {File: testFile, Window: 70000},
		{File: testFile, Checksum: "zz"}, {File: testFile, Checksum: strings.Repeat("a", 31)},
	} {
		if r := s.Curves(req); r.Status != http.StatusBadRequest {
			t.Errorf("%+v: %d, ждали 400", req, r.Status)
		}
	}
	if len(h.urls) != 0 {
		t.Fatal("неверный запрос не должен ходить в сеть")
	}
}

func TestDotsInNamesAreFine(t *testing.T) {
	s, h := newTestService()
	if r := s.Curves(Request{File: "1 song... [hard]/artist - song... (x) [hard].osu"}); r.Status != http.StatusAccepted || len(h.urls) != 1 {
		t.Fatalf("многоточие в названии: %d", r.Status)
	}
}

func TestFetchFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		err    string
	}{
		{"нет файла", 404, "", ""},
		{"сбой tosu", 503, "", ""},
		{"tosu не запущен", 0, "", "dial"},
		{"слишком большой", 200, strings.Repeat("x", maxMapBytes), ""},
		{"не .osu", 200, "<html>nope</html>", ""},
	}
	for _, c := range cases {
		s, h := newTestService()
		s.Curves(Request{File: testFile})
		h.answer(s, c.status, c.body, c.err)
		if r := s.Curves(Request{File: testFile}); r.Status != http.StatusBadGateway {
			t.Errorf("%s: %d, ждали 502", c.name, r.Status)
		}
		if len(h.urls) != 1 {
			t.Errorf("%s: повторная загрузка раньше срока", c.name)
		}
		// После паузы пробуем снова (tosu могли запустить).
		h.now = h.now.Add(failTTL + time.Second)
		if r := s.Curves(Request{File: testFile}); r.Status != http.StatusAccepted || len(h.urls) != 2 {
			t.Errorf("%s: после паузы %d, запросов %d", c.name, r.Status, len(h.urls))
		}
	}
}

func TestFetchDenied(t *testing.T) {
	s, h := newTestService()
	h.fail = true
	if r := s.Curves(Request{File: testFile}); r.Status != http.StatusBadGateway {
		t.Fatalf("без права net.local: %d", r.Status)
	}
}

func TestStrayAnswerIsIgnored(t *testing.T) {
	s, _ := newTestService()
	s.OnFetched("map:unknown", 200, testMap(), "") // ответ на запрос, которого нет (экземпляр пересоздан)
}

func TestMapCacheEvicts(t *testing.T) {
	s, h := newTestService()
	for i := 0; i <= mapsCap; i++ {
		f := fmt.Sprintf("%d x/m.osu", i)
		s.Curves(Request{File: f})
		h.answer(s, 200, testMap(), "")
	}
	if _, ok := s.maps.get(mapKey(Request{File: "0 x/m.osu"})); ok {
		t.Fatal("самая старая карта должна вытесниться")
	}
	if _, ok := s.maps.get(mapKey(Request{File: fmt.Sprintf("%d x/m.osu", mapsCap)})); !ok {
		t.Fatal("последняя карта должна остаться")
	}
}

func TestValidTosu(t *testing.T) {
	for in, want := range map[string]bool{
		"127.0.0.1:24050": true, "localhost:8080": true, "[::1]:80": true,
		"192.168.1.5:80": false, "example.com:80": false, "127.0.0.1": false, "127.0.0.1:24050/x": false, "": false,
	} {
		if ValidTosu(in) != want {
			t.Errorf("%q: ждали %v", in, want)
		}
	}
}

func TestSmallWindowAndAutoCoarsening(t *testing.T) {
	s, h := newTestService()
	s.Curves(Request{File: testFile})
	h.answer(s, 200, testMap(), "")
	fine := s.Curves(Request{File: testFile, Window: 200}).Body.(Response)
	if fine.Window != 200 || len(fine.Stamina) < 100 {
		t.Fatalf("мелкое окно: window %d, точек %d", fine.Window, len(fine.Stamina))
	}
	for _, w := range []int{-1, 10, 49, maxWindow + 1} {
		if r := s.Curves(Request{File: testFile, Window: w}); r.Status != http.StatusBadRequest {
			t.Errorf("window %d: %d, ждали 400", w, r.Status)
		}
	}
	// Ответ не помещается — окно укрупняется, пока точек не станет достаточно мало.
	old := maxPoints
	maxPoints = 300
	defer func() { maxPoints = old }()
	coarse := s.Curves(Request{File: testFile, Window: 100}).Body.(Response)
	if coarse.Window <= 100 || coarse.points() > maxPoints {
		t.Fatalf("укрупнение: window %d, точек %d", coarse.Window, coarse.points())
	}
}

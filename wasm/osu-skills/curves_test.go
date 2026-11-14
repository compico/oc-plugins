package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
	fetched []string
	fail    bool
	now     time.Time
}

func newTestService() (*Service, *fakeHost) {
	h := &fakeHost{now: time.Unix(1_700_000_000, 0)}
	s := NewService(DefaultSource, func(id, url string) error {
		if h.fail {
			return fmt.Errorf("denied")
		}
		h.fetched = append(h.fetched, id+" "+url)
		return nil
	}, func() time.Time { return h.now })
	return s, h
}

func TestCurvesDownloadsThenAnswers(t *testing.T) {
	s, h := newTestService()
	if r := s.Curves(Request{ID: 75}); r.Status != http.StatusAccepted {
		t.Fatalf("первый запрос: %d, ждали 202", r.Status)
	}
	if len(h.fetched) != 1 || h.fetched[0] != "map:75 https://osu.ppy.sh/osu/75" {
		t.Fatalf("запрос к источнику: %v", h.fetched)
	}
	// Пока карта скачивается, повторы не плодят запросов.
	if r := s.Curves(Request{ID: 75}); r.Status != http.StatusAccepted || len(h.fetched) != 1 {
		t.Fatalf("повтор: %d, запросов %d", r.Status, len(h.fetched))
	}
	s.OnFetched(75, 200, testMap(), "")
	r := s.Curves(Request{ID: 75})
	if r.Status != http.StatusOK {
		t.Fatalf("после загрузки: %d %v", r.Status, r.Body)
	}
	resp := r.Body.(Response)
	if resp.BeatmapID != 75 || resp.Window != defaultWindow || len(resp.Stamina) == 0 || len(resp.Agility) == 0 {
		t.Fatalf("ответ: %+v", resp)
	}
	if len(resp.Memory) != 0 {
		t.Fatal("память без FL должна быть пустым списком")
	}
}

func TestCurvesModsAndCache(t *testing.T) {
	s, h := newTestService()
	s.Curves(Request{ID: 5})
	s.OnFetched(5, 200, testMap(), "")
	nm := s.Curves(Request{ID: 5}).Body.(Response)
	dt := s.Curves(Request{ID: 5, Mods: "dt"}).Body.(Response)
	if dt.Speed != 1.5 || nm.Speed != 1 {
		t.Fatalf("скорость: nm %v dt %v", nm.Speed, dt.Speed)
	}
	fl := s.Curves(Request{ID: 5, Mods: "FL"}).Body.(Response)
	if len(fl.Memory) == 0 {
		t.Fatal("с FL память должна считаться")
	}
	if len(h.fetched) != 1 {
		t.Fatalf("смена модов не должна качать карту заново: %v", h.fetched)
	}
}

func TestCurvesRejectsBadInput(t *testing.T) {
	s, h := newTestService()
	for _, req := range []Request{{ID: 0}, {ID: -3}, {ID: 1 << 40}, {ID: 1, Mods: "XX"}, {ID: 1, Mods: "H"}, {ID: 1, Window: -1}, {ID: 1, Window: 70000}} {
		if r := s.Curves(req); r.Status != http.StatusBadRequest {
			t.Errorf("%+v: %d, ждали 400", req, r.Status)
		}
	}
	if len(h.fetched) != 0 {
		t.Fatal("неверный запрос не должен ходить в сеть")
	}
}

func TestFetchFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		err    string
	}{
		{"нет карты", 404, "", ""},
		{"сбой источника", 503, "", ""},
		{"ошибка сети", 0, "", "dial"},
		{"слишком большой", 200, strings.Repeat("x", maxMapBytes), ""},
		{"не .osu", 200, "<html>captcha</html>", ""},
	}
	for _, c := range cases {
		s, h := newTestService()
		s.Curves(Request{ID: 9})
		s.OnFetched(9, c.status, c.body, c.err)
		if r := s.Curves(Request{ID: 9}); r.Status != http.StatusBadGateway {
			t.Errorf("%s: %d, ждали 502", c.name, r.Status)
		}
		if len(h.fetched) != 1 {
			t.Errorf("%s: повторная загрузка раньше срока", c.name)
		}
		// После паузы пробуем снова.
		h.now = h.now.Add(failTTL + time.Second)
		if r := s.Curves(Request{ID: 9}); r.Status != http.StatusAccepted || len(h.fetched) != 2 {
			t.Errorf("%s: после паузы %d, запросов %d", c.name, r.Status, len(h.fetched))
		}
	}
}

func TestFetchDenied(t *testing.T) {
	s, h := newTestService()
	h.fail = true
	if r := s.Curves(Request{ID: 2}); r.Status != http.StatusBadGateway {
		t.Fatalf("без права net.http: %d", r.Status)
	}
}

func TestMapCacheEvicts(t *testing.T) {
	s, _ := newTestService()
	for id := int64(1); id <= mapsCap+1; id++ {
		s.Curves(Request{ID: id})
		s.OnFetched(id, 200, testMap(), "")
	}
	if _, ok := s.maps.get(1); ok {
		t.Fatal("самая старая карта должна вытесниться")
	}
	if _, ok := s.maps.get(mapsCap + 1); !ok {
		t.Fatal("последняя карта должна остаться")
	}
}

func TestValidSource(t *testing.T) {
	for src, want := range map[string]bool{
		"https://osu.ppy.sh/osu/{id}": true, "https://m.example/api/osu/{id}": true,
		"http://osu.ppy.sh/osu/{id}": false, "https://x/{id}/{id}": false, "https://x/none": false, "https://x/ {id}": false, "": false,
	} {
		if ValidSource(src) != want {
			t.Errorf("%q: ждали %v", src, want)
		}
	}
}

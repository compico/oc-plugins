package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newP() (*Player, *time.Time) {
	t := time.Unix(5000, 0)
	return &Player{Now: func() time.Time { return t }}, &t
}

func set(t *testing.T, p *Player, js string) Response {
	t.Helper()
	return p.Set(json.RawMessage(js))
}

func TestNowPlayingRoundTripAndAdvancesPosition(t *testing.T) {
	p, clock := newP()
	if p.Get() != nil {
		t.Fatal("до первого POST ничего не играет")
	}
	r := set(t, p, `{"trackId":"55","title":"Song","artists":["A","B"],"cover":"avatars.yandex.net/get-music-content/1/abc/%%","durationMs":200000,"positionMs":10000,"playing":true}`)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d %v", r.Status, r.Body)
	}
	*clock = clock.Add(4 * time.Second)
	n := p.Get()
	if n == nil || n.Title != "Song" || n.PositionMs != 14000 || len(n.Artists) != 2 || n.Cover == "" {
		t.Fatalf("%+v", n)
	}
}

func TestNowPlayingPausedDoesNotAdvance(t *testing.T) {
	p, clock := newP()
	set(t, p, `{"trackId":"1","durationMs":100000,"positionMs":5000,"playing":false}`)
	*clock = clock.Add(10 * time.Second)
	if n := p.Get(); n == nil || n.PositionMs != 5000 {
		t.Fatalf("%+v", n)
	}
}

func TestNowPlayingPositionCappedAtDuration(t *testing.T) {
	p, clock := newP()
	set(t, p, `{"trackId":"1","durationMs":10000,"positionMs":9000,"playing":true}`)
	*clock = clock.Add(15 * time.Second)
	if n := p.Get(); n.PositionMs != 10000 {
		t.Fatalf("%+v", n)
	}
	set(t, p, `{"trackId":"1","durationMs":10000,"positionMs":99999,"playing":true}`)
	if n := p.Get(); n.PositionMs != 10000 {
		t.Fatalf("позиция сверх длительности должна обрезаться: %+v", n)
	}
	set(t, p, `{"trackId":"1","durationMs":10000,"positionMs":-5,"playing":false}`)
	if n := p.Get(); n.PositionMs != 0 {
		t.Fatalf("%+v", n)
	}
}

func TestNowPlayingGoesStaleAndClears(t *testing.T) {
	p, clock := newP()
	set(t, p, `{"trackId":"1","playing":true}`)
	*clock = clock.Add(staleAfter + time.Second)
	if p.Get() != nil {
		t.Fatal("давно молчащий аддон — ничего не играет")
	}
	set(t, p, `{"trackId":"1","playing":true}`)
	if p.Get() == nil {
		t.Fatal("новое состояние оживляет")
	}
	set(t, p, `{"trackId":""}`)
	if p.Get() != nil {
		t.Fatal("пустой trackId сбрасывает состояние")
	}
}

func TestNowPlayingRejectsBadInput(t *testing.T) {
	p, _ := newP()
	for _, js := range []string{``, `nope`, `[]`, `{"trackId":"12ab"}`, `{"trackId":"../x"}`, `{"trackId":"123456789012345678901"}`, `{"trackId":"1","durationMs":"x"}`} {
		if r := set(t, p, js); r.Status != http.StatusBadRequest {
			t.Errorf("%q: ждали 400, получили %d", js, r.Status)
		}
	}
	if p.Get() != nil {
		t.Fatal("отклонённый ввод не должен менять состояние")
	}
}

func TestNowPlayingSanitizes(t *testing.T) {
	p, _ := newP()
	long := strings.Repeat("я", 500)
	set(t, p, `{"trackId":"1","title":"`+long+`","artists":["","a","b","c","d","e","f","`+long+`"],"cover":"https://evil.example/x.png","durationMs":999999999999,"playing":true}`)
	n := p.Get()
	if n.Cover != "" {
		t.Errorf("чужая обложка должна отбрасываться: %q", n.Cover)
	}
	if len([]rune(n.Title)) != maxTitle {
		t.Errorf("заголовок не обрезан: %d", len([]rune(n.Title)))
	}
	if len(n.Artists) != maxArtists || n.Artists[0] != "a" {
		t.Errorf("исполнители: %v", n.Artists)
	}
	if n.DurationMs != 0 {
		t.Errorf("нереальная длительность отбрасывается: %d", n.DurationMs)
	}
	for _, c := range []string{"avatars.yandex.net.evil.com/x", "avatars.yandex.net/a b", "avatars.yandex.net/<script>", "//avatars.yandex.net/x", "http://avatars.yandex.net/x"} {
		set(t, p, `{"trackId":"1","cover":"`+c+`"}`)
		if p.Get().Cover != "" {
			t.Errorf("обложка %q прошла проверку", c)
		}
	}
}

func TestWithNowAddsFieldOnlyToSuccess(t *testing.T) {
	q, _ := newQ()
	r := withNow(q.List(), nil)
	m := r.Body.(map[string]any)
	if v, ok := m["now"]; !ok || v.(*NowPlaying) != nil {
		t.Fatalf("now должен быть null: %v", m)
	}
	if _, ok := m["items"]; !ok {
		t.Fatal("items на месте")
	}
	q.Env.(*fakeEnv).loadErr = errors.New("boom")
	e := withNow(q.List(), &NowPlaying{})
	if _, ok := e.Body.(map[string]string); !ok || e.Status != http.StatusInternalServerError {
		t.Fatalf("ошибка не должна обрастать полем now: %+v", e)
	}
}

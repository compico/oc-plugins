package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

// Действия Deck — это команды пульта: каждое тело должно проходить ту же проверку, что и запрос виджета.
func TestDeckActionsAreValidControlCommands(t *testing.T) {
	acts := deckActions()
	if len(acts) != 6 {
		t.Fatalf("действий %d", len(acts))
	}
	seen, bodies := map[string]bool{}, map[string]bool{}
	for _, a := range acts {
		if a.ID == "" || a.Title == "" || seen[a.ID] {
			t.Fatalf("плохое действие: %+v", a)
		}
		seen[a.ID] = true
		if a.Path != controlPath || a.Method != "" {
			t.Errorf("%s: маршрут %s %s", a.ID, a.Method, a.Path)
		}
		var b struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(a.Body, &b); err != nil || !validActions[b.Action] {
			t.Errorf("%s: тело %s не команда пульта", a.ID, a.Body)
		}
		bodies[b.Action] = true
		// Принимается самим обработчиком (кроме skip_ban: ему нужен играющий трек).
		if b.Action != actSkipBan {
			c := &Control{Now: func() time.Time { return time.Unix(1000, 0) }}
			if r := c.Request(a.Body, &Queue{}, nil); r.Status != 200 {
				t.Errorf("%s: пульт ответил %d", a.ID, r.Status)
			}
		}
	}
	for _, want := range []string{actPlay, actPause, actToggle, actSkip, actSkipBan} {
		if !bodies[want] {
			t.Errorf("нет действия для %s", want)
		}
	}
	// только пропуск с баном просит второго касания
	for _, a := range acts {
		if a.Danger != (a.ID == "skip-ban") {
			t.Errorf("%s: danger=%v", a.ID, a.Danger)
		}
	}
}

// Подсветка плиток: топик «now» хранит {"now":{"playing":…}}, поэтому условие смотрит в now.playing.
func TestDeckActionsHighlightByPlayerState(t *testing.T) {
	want := map[string]string{"now": "true", "toggle": "true", "play": "true", "pause": "false"}
	for _, a := range deckActions() {
		w := want[a.ID]
		if w == "" {
			if a.ActiveWhen != nil {
				t.Errorf("%s: подсветки быть не должно", a.ID)
			}
			continue
		}
		if a.ActiveWhen == nil || a.ActiveWhen.Topic != topicNow || a.ActiveWhen.Field != "now.playing" || string(a.ActiveWhen.Equals) != w {
			t.Errorf("%s: %+v", a.ID, a.ActiveWhen)
		}
	}
	// условие должно быть выполнимо на реальном кадре, который публикует плагин
	raw, _ := json.Marshal(map[string]any{"now": &NowPlaying{Playing: true}, "at": 1})
	var m struct {
		Now struct {
			Playing bool `json:"playing"`
		} `json:"now"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || !m.Now.Playing {
		t.Fatalf("кадр now не содержит now.playing: %s", raw)
	}
}

// Плитка «Сейчас играет» берёт текст из тех же полей, что публикует плагин в топик now.
func TestNowTileDisplaysTitleAndArtists(t *testing.T) {
	var now *ocplugin.Action
	for _, a := range deckActions() {
		if a.Display != nil {
			if now != nil {
				t.Fatal("текст должен быть только у плитки «Сейчас играет»")
			}
			a := a
			now = &a
		}
	}
	if now == nil || now.ID != "now" || now.Display.Topic != topicNow {
		t.Fatalf("%+v", now)
	}
	raw, _ := json.Marshal(map[string]any{"now": &NowPlaying{Title: "Song", Artists: []string{"A", "B"}}, "at": 1})
	var ev struct {
		Now struct {
			Title   string   `json:"title"`
			Artists []string `json:"artists"`
		} `json:"now"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil || ev.Now.Title != "Song" || len(ev.Now.Artists) != 2 {
		t.Fatalf("кадр now не содержит now.title/now.artists: %s", raw)
	}
	if got := now.Display.Fields; len(got) != 2 || got[0] != "now.title" || got[1] != "now.artists" {
		t.Fatalf("поля %v", got)
	}
}

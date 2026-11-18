package main

import (
	"encoding/json"
	"testing"
	"time"
)

// Действия Deck — это команды пульта: каждое тело должно проходить ту же проверку, что и запрос виджета.
func TestDeckActionsAreValidControlCommands(t *testing.T) {
	acts := deckActions()
	if len(acts) != 5 {
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

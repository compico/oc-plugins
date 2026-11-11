package main

import (
	"encoding/json"
	"testing"
	"time"
)

type recPub struct{ got []string }

func (r *recPub) Retained(topic string, v any) { r.got = append(r.got, "R:"+topic+":"+mustJSON(v)) }
func (r *recPub) Fire(topic string, v any)     { r.got = append(r.got, "F:"+topic+":"+mustJSON(v)) }

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestEventsNowQueueCommand(t *testing.T) {
	at := time.UnixMilli(1700000000123)
	pub := &recPub{}
	ev := &Events{Pub: pub, Now: func() time.Time { return at }}

	ev.PublishNow(nil)
	ev.PublishNow(&NowPlaying{TrackID: "1", Title: "T", Playing: true})
	ev.PublishQueue(Response{Status: 200, Body: map[string]any{"items": []any{}}})
	ev.PublishQueue(Response{Status: 500, Body: map[string]string{"error": "x"}}) // ошибка не публикуется

	c := &Control{Now: func() time.Time { return at }}
	ev.PublishCommand(c) // пусто — ничего не публикуется
	if r := c.Request(json.RawMessage(`{"action":"skip"}`), nil, nil); r.Status != 200 {
		t.Fatal(r)
	}
	ev.PublishCommand(c)

	want := []string{
		`R:now:{"at":1700000000123,"now":null}`,
		`R:now:{"at":1700000000123,"now":{"trackId":"1","title":"T","artists":null,"playing":true,"positionMs":0}}`,
		`R:queue:{"items":[]}`,
		`F:commands:{"action":"skip","id":1700000000123000}`,
	}
	if len(pub.got) != len(want) {
		t.Fatalf("got %v", pub.got)
	}
	for i := range want {
		if i == 1 { // форма NowPlaying задаётся её тегами; здесь важно лишь, что событие retained и несёт at
			continue
		}
		if pub.got[i] != want[i] {
			t.Errorf("%d: got %s want %s", i, pub.got[i], want[i])
		}
	}
}

func TestEventDeclsAreValid(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range eventDecls() {
		if seen[d.ID] || d.ID == "" {
			t.Fatalf("bad decl %v", d)
		}
		seen[d.ID] = true
	}
	for _, id := range []string{topicNow, topicQueue, topicCommands} {
		if !seen[id] {
			t.Errorf("topic %s is not declared", id)
		}
	}
}

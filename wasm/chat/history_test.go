package main

import (
	"fmt"
	"testing"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

func msgEv(id, login string) ocplugin.ChatEvent {
	return ocplugin.ChatEvent{Event: "message", Channel: "c", Message: &ocplugin.ChatMessage{ID: id, Login: login, Text: "hi " + id}}
}

func ids(h *History) string {
	s := ""
	for _, m := range h.Messages() {
		s += m.ID + ","
	}
	return s
}

func TestHistoryKeepsLastMessagesInOrder(t *testing.T) {
	h := NewHistory(3)
	for i := 1; i <= 5; i++ {
		if !h.Apply(msgEv(fmt.Sprint("m", i), "v")) {
			t.Fatal("сообщение нужно раздать виджетам")
		}
	}
	if got := ids(h); got != "m3,m4,m5," {
		t.Fatalf("история: %s", got)
	}
	h.SetMax(2)
	if got := ids(h); got != "m4,m5," {
		t.Fatalf("после SetMax: %s", got)
	}
}

func TestHistoryDeleteByIDAndByLogin(t *testing.T) {
	h := NewHistory(10)
	h.Apply(msgEv("a", "alice"))
	h.Apply(msgEv("b", "Bob"))
	h.Apply(msgEv("c", "alice"))
	if !h.Apply(ocplugin.ChatEvent{Event: "delete", TargetID: "b"}) || ids(h) != "a,c," {
		t.Fatalf("delete по id: %s", ids(h))
	}
	if !h.Apply(ocplugin.ChatEvent{Event: "delete", Login: "ALICE"}) || ids(h) != "" {
		t.Fatalf("delete по логину без учёта регистра: %s", ids(h))
	}
	if h.Apply(ocplugin.ChatEvent{Event: "delete"}) {
		t.Fatal("delete без id и логина раздавать нечего")
	}
}

func TestHistoryClear(t *testing.T) {
	h := NewHistory(10)
	h.Apply(msgEv("a", "alice"))
	h.Apply(msgEv("b", "bob"))
	h.Apply(ocplugin.ChatEvent{Event: "clear", Login: "bob"})
	if ids(h) != "a," {
		t.Fatalf("clear пользователя: %s", ids(h))
	}
	h.Apply(ocplugin.ChatEvent{Event: "clear"})
	if ids(h) != "" || h.Messages() == nil {
		t.Fatalf("clear чата: %q, список должен быть не nil", ids(h))
	}
}

func TestHistoryStatusAndUnknownEvents(t *testing.T) {
	h := NewHistory(10)
	off := false
	if !h.Apply(ocplugin.ChatEvent{Event: "status", Connected: &off, Error: "no account"}) {
		t.Fatal("статус нужно раздать")
	}
	if s := h.Status(); s.Connected == nil || *s.Connected || s.Error != "no account" {
		t.Fatalf("статус: %+v", s)
	}
	if h.Apply(ocplugin.ChatEvent{Event: "weird"}) || h.Apply(ocplugin.ChatEvent{Event: "message"}) {
		t.Fatal("неизвестное событие и сообщение без тела не раздаются")
	}
}

func TestHistoryMessagesIsACopy(t *testing.T) {
	h := NewHistory(10)
	h.Apply(msgEv("a", "alice"))
	got := h.Messages()
	got[0].ID = "changed"
	if ids(h) != "a," {
		t.Fatal("Messages отдаёт копию")
	}
}

func TestParseHistorySize(t *testing.T) {
	for in, want := range map[string]int{"": DefaultHistory, "abc": DefaultHistory, "0": DefaultHistory, "-5": DefaultHistory, " 50 ": 50, "5000": MaxHistory, "1000": 1000} {
		if got := ParseHistorySize(in); got != want {
			t.Errorf("ParseHistorySize(%q) = %d, want %d", in, got, want)
		}
	}
}

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDuplicateTrackIsRejectedWithRefundAndChatReply(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(Redemption{ID: "r1", RewardID: "rw", User: "alice", Input: link(1)})
	q.OnRedemption(Redemption{ID: "r2", RewardID: "rw", User: "bob", Input: link(1)})
	q.OnRedemption(Redemption{ID: "r3", RewardID: "rw", User: "carol", Input: link(2)})
	s, _ := e.Load()
	if s.Items[0].Status != statusQueued || s.Items[1].Status != statusRejected || s.Items[2].Status != statusQueued {
		t.Fatalf("статусы: %v %v %v", s.Items[0].Status, s.Items[1].Status, s.Items[2].Status)
	}
	if s.Items[1].Error != errDuplicate {
		t.Fatalf("причина: %q", s.Items[1].Error)
	}
	if len(e.updates) != 1 || e.updates[0] != "r2:CANCELED" {
		t.Fatalf("баллы возвращаются только за дубль: %v", e.updates)
	}
	if len(e.said) != 1 || !strings.HasPrefix(e.said[0], "@bob ") || !strings.Contains(e.said[0], "уже есть в очереди") || !strings.Contains(e.said[0], "баллы возвращены") {
		t.Fatalf("ответ в чат: %v", e.said)
	}
	// отклонённый дубль не занимает место в очереди: аддон получит заявки 1 и 3
	if it, _ := next(t, q); it == nil || it.User != "alice" {
		t.Fatalf("первая заявка: %+v", it)
	}
}

func TestDuplicateAppliesWhileSentAndPlayingButNotAfterPlayed(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	it, _ := next(t, q) // sent
	q.OnRedemption(red("r2", link(1)))
	if status(t, e, 2) != statusRejected {
		t.Fatal("трек отдан аддону, но ещё не играет: дубль отклоняется")
	}
	if r := q.Started(body(it.ID)); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	q.OnRedemption(red("r3", link(1)))
	if status(t, e, 3) != statusRejected {
		t.Fatal("трек играет: дубль отклоняется")
	}
	if r := q.Played(body(it.ID)); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	q.OnRedemption(red("r4", link(1)))
	if status(t, e, 4) != statusQueued {
		t.Fatal("после того как трек сыграл, его можно заказать снова")
	}
	if len(e.said) != 2 {
		t.Fatalf("ответов в чат два (r2 и r3): %v", e.said)
	}
}

func TestFailedTrackCanBeRequestedAgain(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	it, _ := next(t, q)
	if r := q.Failed(json.RawMessage(fmt.Sprintf(`{"id":%d,"reason":"x"}`, it.ID))); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	q.OnRedemption(red("r2", link(1)))
	if status(t, e, 2) != statusQueued {
		t.Fatal("отменённая заявка не мешает заказать трек снова")
	}
}

func TestOtherRejectionsDoNotWriteToChat(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", "not a link"))
	q.Ban("10", "x")
	q.OnRedemption(red("r2", link(1)))
	if len(e.said) != 0 {
		t.Fatalf("в чат пишем только про дубли: %v", e.said)
	}
}

func TestDuplicateMessageSanitizesNick(t *testing.T) {
	for user, want := range map[string]string{
		"alice":                 "@alice этот трек",
		"Боб_1":                 "@Боб_1 этот трек",
		"/ban x\nevil":          "@banxevil этот трек",
		"":                      "Этот трек",
		"!!!":                   "Этот трек",
		strings.Repeat("a", 90): "@" + strings.Repeat("a", 40) + " этот трек",
	} {
		if got := duplicateMessage(user); !strings.HasPrefix(got, want) {
			t.Errorf("%q: %q", user, got)
		}
	}
}

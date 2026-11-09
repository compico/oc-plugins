package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func status(t *testing.T, e *fakeEnv, id int) string {
	t.Helper()
	s, _ := e.Load()
	for _, it := range s.Items {
		if it.ID == id {
			return it.Status
		}
	}
	t.Fatalf("заявки %d нет", id)
	return ""
}

func TestSkippedTrackRefundsPoints(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	next(t, q)
	q.Started(body(1))
	if status(t, e, 1) != statusPlaying || len(e.updates) != 0 {
		t.Fatal("после старта заявка играет, награда не тронута")
	}
	if r := q.Failed(json.RawMessage(`{"id":1,"reason":"трек пропущен"}`)); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	if status(t, e, 1) != statusFailed || len(e.updates) != 1 || e.updates[0] != "r1:CANCELED" {
		t.Fatalf("пропуск возвращает баллы: %v", e.updates)
	}
	if r := q.Played(body(1)); r.Status != 409 {
		t.Fatalf("после возврата «отыграно» невозможно: %d", r.Status)
	}
}

func TestPlayedRequiresStartedOrSent(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	if r := q.Played(body(1)); r.Status != 409 {
		t.Fatalf("ещё не отданная заявка не может быть отыграна: %d", r.Status)
	}
	next(t, q) // sent
	if r := q.Played(body(1)); r.Status != 200 || status(t, e, 1) != statusPlayed {
		t.Fatalf("потерянное подтверждение старта не должно мешать: %+v", r)
	}
}

func TestPlayingItemsAreListedAndNotResent(t *testing.T) {
	q, _ := newQ()
	q.OnRedemption(red("r1", link(1)))
	q.OnRedemption(red("r2", link(2)))
	next(t, q)
	q.Started(body(1))
	items := q.Playing().Body.(map[string]any)["items"].([]Item)
	if len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("%+v", items)
	}
	// пока первая играет, аддон получает следующую (она ставится в очередь плеера заранее), играющая не отдаётся повторно
	if it, resend := next(t, q); it == nil || it.ID != 2 || resend {
		t.Fatalf("%+v", it)
	}
	if empty := q.Playing(); len(empty.Body.(map[string]any)["items"].([]Item)) != 1 {
		t.Fatal("отданная, но не стартовавшая заявка не «играет»")
	}
}

func TestPlayingSurvivesPruning(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r0", link(1)))
	next(t, q)
	q.Started(body(1))
	for i := 2; i < maxKept+30; i++ {
		q.OnRedemption(red(fmt.Sprint("r", i), "bad"))
	}
	if status(t, e, 1) != statusPlaying {
		t.Fatal("играющая заявка не должна вытесняться историей")
	}
}

func TestBanRejectsFutureRequestsAndRefunds(t *testing.T) {
	q, e := newQ()
	if err := q.Ban("10", "Song"); err != nil {
		t.Fatal(err)
	}
	q.OnRedemption(red("r1", link(1))) // trackId 10
	q.OnRedemption(red("r2", link(2))) // trackId 20
	if status(t, e, 1) != statusRejected || len(e.updates) != 1 || e.updates[0] != "r1:CANCELED" {
		t.Fatalf("заявка на забаненный трек отклоняется, баллы возвращаются: %v", e.updates)
	}
	if status(t, e, 2) != statusQueued {
		t.Fatal("остальные заявки не затронуты")
	}
	s, _ := e.Load()
	if !strings.Contains(s.Items[0].Error, "banned") {
		t.Fatalf("причина: %q", s.Items[0].Error)
	}
}

func TestBanListOps(t *testing.T) {
	q, e := newQ()
	q.Ban("10", strings.Repeat("я", 500))
	q.Ban("10", "again") // повтор не дублирует
	if len(e.bans) != 1 || len([]rune(e.bans[0].Title)) != maxBanName || e.bans[0].At != 1000 {
		t.Fatalf("%+v", e.bans)
	}
	for _, b := range []string{``, `x`, `{}`, `{"trackId":"1a"}`} {
		if r := q.Unban(json.RawMessage(b)); r.Status != http.StatusBadRequest {
			t.Errorf("%q: %d", b, r.Status)
		}
	}
	if r := q.Unban(json.RawMessage(`{"trackId":"99"}`)); r.Status != http.StatusNotFound {
		t.Fatalf("%d", r.Status)
	}
	if r := q.Unban(json.RawMessage(`{"trackId":"10"}`)); r.Status != 200 || len(e.bans) != 0 {
		t.Fatalf("%+v %v", r, e.bans)
	}
	if got := q.Bans().Body.(map[string]any)["items"].([]Ban); got == nil || len(got) != 0 {
		t.Fatal("пустой список — [], не null")
	}
}

func TestBanListIsBounded(t *testing.T) {
	q, e := newQ()
	for i := 1; i <= maxBans+20; i++ {
		q.Ban(fmt.Sprint(i), "t")
	}
	if len(e.bans) != maxBans || e.bans[0].TrackID != "21" {
		t.Fatalf("%d, первый %s: вытесняются самые старые", len(e.bans), e.bans[0].TrackID)
	}
}

func TestBanStorageFailures(t *testing.T) {
	q, e := newQ()
	e.bansErr = errors.New("storage down")
	if err := q.Ban("10", ""); err == nil {
		t.Fatal("ошибка хранилища должна дойти до вызывающего")
	}
	if r := q.Bans(); r.Status != 500 || strings.Contains(fmt.Sprint(r.Body), "down") {
		t.Fatalf("%+v", r)
	}
	if r := q.Unban(json.RawMessage(`{"trackId":"10"}`)); r.Status != 500 {
		t.Fatalf("%d", r.Status)
	}
	// бан-лист недоступен — заявки всё равно принимаются (иначе сбой хранилища банов убил бы всю очередь)
	q.OnRedemption(red("r1", link(1)))
	if status(t, e, 1) != statusQueued {
		t.Fatal("сбой бан-листа не должен блокировать заявки")
	}
}

// --- управление ---

func newCtl() (*Control, *time.Time) {
	t := time.Unix(7000, 0)
	return &Control{Now: func() time.Time { return t }}, &t
}

func req(c *Control, q *Queue, js string, now *NowPlaying) Response {
	return c.Request(json.RawMessage(js), q, now)
}

func TestControlQueuesAndAcknowledges(t *testing.T) {
	c, _ := newCtl()
	q, _ := newQ()
	for _, a := range []string{"play", "pause", "toggle", "skip"} {
		if r := req(c, q, `{"action":"`+a+`"}`, nil); r.Status != 200 {
			t.Fatalf("%s: %+v", a, r)
		}
	}
	cmds := c.Pending().Body.(map[string]any)["commands"].([]Command)
	if len(cmds) != 4 || cmds[0].Action != "play" || cmds[3].Action != "skip" {
		t.Fatalf("%+v", cmds)
	}
	for i := 1; i < len(cmds); i++ {
		if cmds[i].ID <= cmds[i-1].ID {
			t.Fatal("идентификаторы должны строго расти")
		}
	}
	if r := c.Done(json.RawMessage(fmt.Sprintf(`{"id":%d}`, cmds[1].ID))); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	if r := c.Done(json.RawMessage(fmt.Sprintf(`{"id":%d}`, cmds[1].ID))); r.Status != 404 {
		t.Fatal("повторное подтверждение: команда уже снята")
	}
	if left := c.Pending().Body.(map[string]any)["commands"].([]Command); len(left) != 3 {
		t.Fatalf("%+v", left)
	}
}

func TestControlRejectsBadInput(t *testing.T) {
	c, _ := newCtl()
	q, _ := newQ()
	for _, js := range []string{``, `x`, `{}`, `{"action":"format_disk"}`, `{"action":""}`, `{"action":5}`} {
		if r := req(c, q, js, nil); r.Status != 400 {
			t.Errorf("%q: %d", js, r.Status)
		}
	}
	for _, js := range []string{``, `{}`, `{"id":0}`, `{"id":"a"}`} {
		if r := c.Done(json.RawMessage(js)); r.Status != 400 {
			t.Errorf("done %q: %d", js, r.Status)
		}
	}
	if len(c.Pending().Body.(map[string]any)["commands"].([]Command)) != 0 {
		t.Fatal("мусор не должен ставить команды")
	}
}

func TestControlCommandsExpireAndAreCapped(t *testing.T) {
	c, clock := newCtl()
	q, _ := newQ()
	req(c, q, `{"action":"skip"}`, nil)
	*clock = clock.Add(commandTTL + time.Second)
	if len(c.Pending().Body.(map[string]any)["commands"].([]Command)) != 0 {
		t.Fatal("устаревшая команда не должна исполняться")
	}
	for i := 0; i < maxCommands; i++ {
		if r := req(c, q, `{"action":"pause"}`, nil); r.Status != 200 {
			t.Fatal(r)
		}
	}
	if r := req(c, q, `{"action":"pause"}`, nil); r.Status != http.StatusTooManyRequests {
		t.Fatalf("очередь команд ограничена: %d", r.Status)
	}
}

func TestSkipBanBansCurrentTrackFirst(t *testing.T) {
	c, _ := newCtl()
	q, e := newQ()
	if r := req(c, q, `{"action":"skip_ban"}`, nil); r.Status != http.StatusConflict || len(e.bans) != 0 {
		t.Fatalf("без играющего трека банить нечего: %+v", r)
	}
	r := req(c, q, `{"action":"skip_ban"}`, &NowPlaying{TrackID: "55", Title: "Bad song"})
	if r.Status != 200 || r.Body.(map[string]any)["banned"] != "55" || len(e.bans) != 1 || e.bans[0].Title != "Bad song" {
		t.Fatalf("%+v %v", r, e.bans)
	}
	if cmds := c.Pending().Body.(map[string]any)["commands"].([]Command); len(cmds) != 1 || cmds[0].Action != "skip_ban" {
		t.Fatalf("%+v", cmds)
	}
}

func TestSkipBanFailsClosedWhenBanCannotBeSaved(t *testing.T) {
	c, _ := newCtl()
	q, e := newQ()
	e.bansErr = errors.New("nope")
	r := req(c, q, `{"action":"skip_ban"}`, &NowPlaying{TrackID: "55"})
	if r.Status != 500 {
		t.Fatalf("%+v", r)
	}
	if len(c.Pending().Body.(map[string]any)["commands"].([]Command)) != 0 {
		t.Fatal("если бан не записался, трек не пропускаем молча")
	}
}

func TestControlIDsStayMonotonicAfterClockGoesBack(t *testing.T) {
	c, clock := newCtl()
	q, _ := newQ()
	a := req(c, q, `{"action":"skip"}`, nil).Body.(map[string]any)["id"].(int64)
	*clock = clock.Add(-time.Hour)
	b := req(c, q, `{"action":"skip"}`, nil).Body.(map[string]any)["id"].(int64)
	if b <= a {
		t.Fatalf("%d <= %d", b, a)
	}
}

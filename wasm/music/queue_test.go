package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeEnv struct {
	bans      []Ban
	bansErr   error
	st        state
	loadErr   error
	saveErr   error
	updateErr error
	updates   []string
	logs      []string
	said      []string
}

func (f *fakeEnv) Load() (state, error) {
	if f.loadErr != nil {
		return state{}, f.loadErr
	}
	// как настоящее хранилище: каждый раз новая копия
	b, _ := json.Marshal(f.st)
	var s state
	_ = json.Unmarshal(b, &s)
	return s, nil
}
func (f *fakeEnv) Save(s state) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if b, _ := json.Marshal(s); len(b) > 256<<10 { // лимит значения в хранилище OC
		return errors.New("value must be valid JSON up to 256 KB")
	}
	f.st = s
	return nil
}
func (f *fakeEnv) LoadBans() ([]Ban, error) {
	if f.bansErr != nil {
		return nil, f.bansErr
	}
	return append([]Ban(nil), f.bans...), nil
}
func (f *fakeEnv) SaveBans(b []Ban) error {
	if f.bansErr != nil {
		return f.bansErr
	}
	f.bans = append([]Ban(nil), b...)
	return nil
}
func (f *fakeEnv) Complete(_, id string, ok bool) error {
	st := "CANCELED"
	if ok {
		st = "FULFILLED"
	}
	f.updates = append(f.updates, id+":"+st)
	return f.updateErr
}
func (f *fakeEnv) Say(text string)                 { f.said = append(f.said, text) }
func (f *fakeEnv) Log(level, msg string, _ ...any) { f.logs = append(f.logs, level+": "+msg) }

func newQ() (*Queue, *fakeEnv) {
	e := &fakeEnv{}
	return &Queue{Env: e, Cfg: Config{RewardID: "rw"}, Now: func() time.Time { return time.Unix(1000, 0) }}, e
}

func red(id, in string) Redemption { return Redemption{ID: id, RewardID: "rw", User: "v", Input: in} }
func link(n int) string            { return fmt.Sprintf("https://music.yandex.ru/album/%d/track/%d", n, n*10) }
func body(id int) json.RawMessage  { return json.RawMessage(fmt.Sprintf(`{"id":%d}`, id)) }

// next разбирает ответ Next: заявка (nil — очередь пуста) и признак resend.
func next(t *testing.T, q *Queue) (it *Item, resend bool) {
	t.Helper()
	r := q.Next()
	if r.Status != 200 {
		t.Fatalf("next: %+v", r)
	}
	m := r.Body.(map[string]any)
	if m["item"] == nil {
		return nil, false
	}
	x := m["item"].(Item)
	return &x, m["resend"] == true
}

func TestFIFOPollingAndConfirmedStart(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	q.OnRedemption(red("r2", link(2)))
	it, resend := next(t, q)
	if it.ID != 1 || resend || it.TrackID != "10" || it.URL != "https://music.yandex.ru/album/1/track/10" || it.SentAt != 1000 {
		t.Fatalf("первой должна уйти первая: %+v", it)
	}
	// повторный опрос идемпотентен: та же заявка, с resend, очередь не продвигается
	if it, resend = next(t, q); it.ID != 1 || !resend {
		t.Fatalf("%+v resend=%v", it, resend)
	}
	if r := q.Started(body(1)); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	if it, resend = next(t, q); it.ID != 2 || resend {
		t.Fatalf("после старта первой — вторая: %+v", it)
	}
	if len(e.updates) != 0 {
		t.Fatalf("старт трека ещё не повод выполнять награду: %v", e.updates)
	}
	if it, _ = next(t, q); it == nil || it.ID != 2 {
		t.Fatal("вторая всё ещё отдана и не подтверждена")
	}
	if r := q.Played(body(1)); r.Status != 200 || len(e.updates) != 1 || e.updates[0] != "r1:FULFILLED" {
		t.Fatalf("после отыгрыша награда выполняется: %+v %v", r, e.updates)
	}
	q.Started(body(2))
	q.Played(body(2))
	if it, _ = next(t, q); it != nil {
		t.Fatalf("очередь пуста: %+v", it)
	}
	if r := q.List(); len(r.Body.(map[string]any)["items"].([]Item)) != 2 {
		t.Fatalf("история: %+v", r)
	}
}

func TestDuplicateForeignAndRejected(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	q.OnRedemption(red("r1", link(1))) // повторная доставка
	foreign := red("r2", link(2))
	foreign.RewardID = "other"
	q.OnRedemption(foreign) // чужая награда: не наша
	q.OnRedemption(red("r3", "https://evil.example/x"))
	q.OnRedemption(red("r3", "https://evil.example/x")) // повтор отклонённого: баллы второй раз не возвращаем
	s, _ := e.Load()
	if len(s.Items) != 2 || s.Items[1].Status != statusRejected || s.Items[1].Error == "" {
		t.Fatalf("%+v", s.Items)
	}
	if len(e.updates) != 1 || e.updates[0] != "r3:CANCELED" {
		t.Fatalf("%v", e.updates)
	}
}

func TestSadPaths(t *testing.T) {
	q, _ := newQ()
	q.OnRedemption(red("r1", link(1)))
	if r := q.Started(body(1)); r.Status != 409 {
		t.Fatalf("старт неотданной заявки: %d", r.Status)
	}
	if r := q.Started(body(99)); r.Status != 404 {
		t.Fatalf("неизвестная заявка: %d", r.Status)
	}
	for _, b := range []string{``, `not json`, `{}`, `{"id":0}`, `{"id":-3}`} {
		if r := q.Started(json.RawMessage(b)); r.Status != 400 {
			t.Errorf("тело %q: %d", b, r.Status)
		}
	}
	next(t, q)
	q.Started(body(1))
	if r := q.Started(body(1)); r.Status != 200 {
		t.Fatalf("повтор подтверждения старта безопасен: %d", r.Status)
	}
	if r := q.Played(body(1)); r.Status != 200 {
		t.Fatalf("%d", r.Status)
	}
	if r := q.Played(body(1)); r.Status != 409 {
		t.Fatalf("повторный «отыграно»: %d", r.Status)
	}
	if r := q.Started(body(1)); r.Status != 409 {
		t.Fatalf("старт после отыгрыша: %d", r.Status)
	}
	if r := q.Failed(body(1)); r.Status != 409 {
		t.Fatalf("ошибка после успеха: %d", r.Status)
	}
}

func TestFailedCancelsAndMovesOn(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	q.OnRedemption(red("r2", link(2)))
	next(t, q)
	long := strings.Repeat("я", maxReason+50)
	if r := q.Failed(json.RawMessage(fmt.Sprintf(`{"id":1,"reason":%q}`, long))); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
	if e.updates[0] != "r1:CANCELED" {
		t.Fatalf("%v", e.updates)
	}
	if s, _ := e.Load(); s.Items[0].Status != statusFailed || len([]rune(s.Items[0].Error)) != maxReason {
		t.Fatalf("причина должна быть обрезана: %+v", s.Items[0])
	}
	if it, _ := next(t, q); it == nil || it.ID != 2 {
		t.Fatalf("очередь не должна вставать: %+v", it)
	}
	// заявку, которая ещё не отдавалась, тоже можно отменить (аддон мог получить её и упасть до подтверждения)
	q.OnRedemption(red("r3", link(3)))
	if r := q.Failed(body(3)); r.Status != 200 {
		t.Fatalf("%+v", r)
	}
}

func TestQueueLimitAndPruning(t *testing.T) {
	q, e := newQ()
	for i := 1; i <= maxQueued+1; i++ {
		q.OnRedemption(red(fmt.Sprintf("r%d", i), link(i)))
	}
	s, _ := e.Load()
	if last := s.Items[len(s.Items)-1]; last.Status != statusRejected || last.Error != "the queue is full" {
		t.Fatalf("%+v", last)
	}
	if len(e.updates) != 1 {
		t.Fatalf("%v", e.updates)
	}
	// история не растёт бесконечно: завершённые срезаются, живые остаются
	q2, e2 := newQ()
	q2.OnRedemption(red("live", link(1)))
	for i := 1; i <= maxKept+50; i++ {
		q2.OnRedemption(red(fmt.Sprintf("x%d", i), "bad"))
	}
	s2, _ := e2.Load()
	if len(s2.Items) > maxKept {
		t.Fatalf("история %d > %d", len(s2.Items), maxKept)
	}
	if s2.Items[0].RedemptionID != "live" || s2.Items[0].Status != statusQueued {
		t.Fatalf("живая заявка не должна срезаться: %+v", s2.Items[0])
	}
}

// Регрессия: «тяжёлые» заявки (символы, которые JSON экранирует в 6 байт) не должны упереть состояние в лимит хранилища.
func TestHeavyHistoryNeverBlocksTheQueue(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("live", link(1)))
	heavy := strings.Repeat("<", 2000) // ввод зрителя до 2 КБ и больше
	for i := 0; i < 400; i++ {
		q.OnRedemption(red(fmt.Sprintf("h%d", i), heavy))
	}
	s, _ := e.Load()
	if b, _ := json.Marshal(s); len(b) > maxBytes {
		t.Fatalf("состояние %d байт > бюджета %d", len(b), maxBytes)
	}
	if s.Items[0].RedemptionID != "live" {
		t.Fatal("живая заявка не должна срезаться")
	}
	for _, it := range s.Items {
		if len([]rune(it.Input)) > maxInput {
			t.Fatalf("ввод не обрезан: %d", len([]rune(it.Input)))
		}
	}
	// и очередь продолжает работать: новая заявка принимается и выдаётся
	q.OnRedemption(red("after", link(2)))
	if it, _ := next(t, q); it == nil || it.RedemptionID != "live" {
		t.Fatalf("%+v", it)
	}
	q.Started(body(1))
	if it, _ := next(t, q); it == nil || it.RedemptionID != "after" {
		t.Fatalf("очередь встала: %+v", it)
	}
}

func TestLongInputIsTruncated(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", strings.Repeat("я", maxInput+100)))
	if s, _ := e.Load(); len([]rune(s.Items[0].Input)) != maxInput {
		t.Fatalf("ввод зрителя должен обрезаться до %d символов", maxInput)
	}
}

func TestStateSurvivesNewInstance(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	next(t, q)
	// «перезапуск плагина»: новый экземпляр над тем же хранилищем
	q2 := &Queue{Env: e, Cfg: q.Cfg, Now: q.Now}
	if it, resend := next(t, q2); it == nil || !resend {
		t.Fatalf("активная заявка должна уйти повторно: %+v %v", it, resend)
	}
}

func TestRewardNotConfigured(t *testing.T) {
	q, e := newQ()
	q.Cfg.RewardID = ""
	q.OnRedemption(red("r1", link(1)))
	if s, _ := e.Load(); len(s.Items) != 0 || len(e.updates) != 0 {
		t.Fatal("без выбранной награды погашения игнорируются: ничего не принимаем и ничего не отменяем")
	}
	if len(e.logs) != 1 || !strings.HasPrefix(e.logs[0], "warn:") {
		t.Fatalf("пользователь должен увидеть предупреждение в логе: %v", e.logs)
	}
}

func TestStorageFailures(t *testing.T) {
	boom := errors.New("permission denied: storage")
	// хранилище недоступно: заявка не принимается, но и награду не трогаем
	q, e := newQ()
	e.loadErr = boom
	q.OnRedemption(red("r1", link(1)))
	if len(e.updates) != 0 || !strings.HasPrefix(e.logs[len(e.logs)-1], "error:") {
		t.Fatalf("%v %v", e.updates, e.logs)
	}
	for name, r := range map[string]Response{"next": q.Next(), "list": q.List(), "started": q.Started(body(1))} {
		if r.Status != 500 || strings.Contains(fmt.Sprint(r.Body), "denied") {
			t.Errorf("%s: должен быть общий 500 без подробностей: %+v", name, r)
		}
	}
	// запись не удалась: заявка не считается принятой, награду не отменяем
	q2, e2 := newQ()
	e2.saveErr = boom
	q2.OnRedemption(red("r1", link(1)))
	q2.OnRedemption(red("r2", "bad"))
	if len(e2.updates) != 0 {
		t.Fatalf("при сбое записи награды не трогаем: %v", e2.updates)
	}
	// запись не удалась при подтверждении: награду не выполняем, аддон повторит
	q3, e3 := newQ()
	q3.OnRedemption(red("r1", link(1)))
	next(t, q3)
	q3.Started(body(1))
	e3.saveErr = boom
	if r := q3.Played(body(1)); r.Status != 500 || len(e3.updates) != 0 {
		t.Fatalf("%+v %v", r, e3.updates)
	}
	e3.saveErr = nil
	if r := q3.Played(body(1)); r.Status != 200 || len(e3.updates) != 1 {
		t.Fatalf("повтор подтверждения должен пройти: %+v %v", r, e3.updates)
	}
}

func TestTwitchErrorDoesNotBreakQueue(t *testing.T) {
	q, e := newQ()
	e.updateErr = errors.New("helix: 403")
	q.OnRedemption(red("r1", link(1)))
	next(t, q)
	q.Started(body(1))
	if r := q.Played(body(1)); r.Status != 200 {
		t.Fatalf("ошибка Twitch не должна ломать подтверждение: %+v", r)
	}
	if s, _ := e.Load(); s.Items[0].Status != statusPlayed {
		t.Fatalf("%+v", s.Items[0])
	}
	if !strings.HasPrefix(e.logs[len(e.logs)-1], "warn:") {
		t.Fatalf("ошибка Twitch должна попасть в лог: %v", e.logs)
	}
}

func TestClearQueuedCancelsOnlyQueuedAndRefunds(t *testing.T) {
	q, e := newQ()
	for i := 1; i <= 3; i++ {
		q.OnRedemption(red(fmt.Sprintf("r%d", i), link(i)))
	}
	q.Next() // первая заявка отдана плееру (sent): её не трогаем
	r := q.ClearQueued(nil)
	if r.Status != 200 || r.Body.(map[string]any)["cleared"] != 2 {
		t.Fatalf("clear: %+v", r)
	}
	got := map[string]int{}
	for _, it := range e.st.Items {
		got[it.Status]++
	}
	if got[statusSent] != 1 || got[statusFailed] != 2 {
		t.Fatalf("statuses: %v", got)
	}
	if strings.Join(e.updates, ",") != "r2:CANCELED,r3:CANCELED" {
		t.Fatalf("refunds: %v", e.updates)
	}
	if r := q.ClearQueued(nil); r.Body.(map[string]any)["cleared"] != 0 || len(e.updates) != 2 {
		t.Fatalf("повторная очистка ничего не отменяет: %+v %v", r, e.updates)
	}
}

func TestClearQueuedStoreErrorTouchesNothing(t *testing.T) {
	q, e := newQ()
	q.OnRedemption(red("r1", link(1)))
	e.saveErr = errors.New("disk")
	if r := q.ClearQueued(nil); r.Status != 500 || len(e.updates) != 0 {
		t.Fatalf("при ошибке записи награды не трогаем: %+v %v", r, e.updates)
	}
}

func TestBanTrackByIDAndLink(t *testing.T) {
	q, e := newQ()
	for _, b := range []string{`{"trackId":"123","title":"Song"}`, `{"link":"https://music.yandex.ru/album/5/track/456"}`, `{"trackId":"123"}`} {
		if r := q.BanTrack(json.RawMessage(b)); r.Status != 200 {
			t.Fatalf("%s: %+v", b, r)
		}
	}
	if len(e.bans) != 2 || e.bans[0].Title != "Song" || e.bans[1].TrackID != "456" || e.bans[1].Title != "трек 456" {
		t.Fatalf("bans: %+v", e.bans)
	}
	for _, bad := range []string{`{}`, `{"trackId":"12a"}`, `{"link":"https://evil.example/album/1/track/2"}`, `{"link":"http://music.yandex.ru/album/1/track/2"}`, `nope`} {
		if r := q.BanTrack(json.RawMessage(bad)); r.Status != 400 {
			t.Errorf("%s должен давать 400: %+v", bad, r)
		}
	}
	e.bansErr = errors.New("x")
	if r := q.BanTrack(json.RawMessage(`{"trackId":"9"}`)); r.Status != 500 {
		t.Fatalf("ошибка хранилища: %+v", r)
	}
}

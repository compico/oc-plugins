package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Вся логика очереди здесь, без привязки к ABI и SDK: проверяется обычными тестами, а main.go только подключает её к хосту.
//
// Модель: плагин пассивен. Хост доставляет twitch.redemption, аддон плеера опрашивает маршруты (GET /next, POST /started,
// POST /failed); обработчики отвечают сразу и ничего не ждут. Вся очередь лежит в storage хоста, поэтому перезапуск плагина ничего не теряет.

// Статусы заявки.
const (
	statusQueued   = "queued"   // принята, ждёт своей очереди
	statusSent     = "sent"     // отдана аддону, подтверждения старта ещё нет
	statusPlaying  = "playing"  // трек начал играть; награда ещё не выполнена: ждём, пока он будет отыгран
	statusPlayed   = "played"   // трек отыгран (не пропущен), награда выполнена
	statusFailed   = "failed"   // не запустился или пропущен, награда отменена (баллы возвращены)
	statusRejected = "rejected" // ссылка не подошла или очередь полна, награда отменена
)

const (
	maxQueued  = 100       // заявок в ожидании: дальше новые отклоняются
	maxKept    = 200       // сколько заявок помним (дедупликация повторных доставок + история)
	maxInput   = 200       // сколько символов ввода зрителя храним (для показа; ссылка разбирается из полного ввода)
	maxBytes   = 200 << 10 // бюджет состояния: у значения в хранилище OC лимит 256 КБ, держим запас
	maxReason  = 200
	maxBans    = 300 // сколько забаненных треков помним (отдельный ключ хранилища, не делит бюджет с очередью)
	maxBanName = 80
	bansKey    = "bans"
	stateKey   = "state"
	storeError = "storage error"
)

// Config — настройки плагина (задаются пользователем).
type Config struct {
	// RewardID — награда Twitch, погашения которой считаются заявками на трек.
	RewardID string `json:"rewardId"`
}

// Redemption — погашение награды (то, что плагин берёт из события хоста).
type Redemption struct {
	ID       string
	RewardID string
	User     string
	Input    string
}

// Item — заявка на трек.
type Item struct {
	ID           int    `json:"id"`
	RedemptionID string `json:"redemptionId"`
	RewardID     string `json:"rewardId"`
	User         string `json:"user"`
	Input        string `json:"input"`
	AlbumID      string `json:"albumId,omitempty"`
	TrackID      string `json:"trackId,omitempty"`
	URL          string `json:"url,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	SentAt       int64  `json:"sentAt,omitempty"`
}

type state struct {
	NextID int    `json:"nextId"`
	Items  []Item `json:"items"`
}

// Ban — забаненный трек: заявки на него отклоняются, а награда возвращается зрителю.
type Ban struct {
	TrackID string `json:"trackId"`
	Title   string `json:"title,omitempty"`
	At      int64  `json:"at"`
}

// Env — всё, что очереди нужно от хоста. В плагине его реализует main.go поверх SDK, в тестах — фейк.
type Env interface {
	Load() (state, error)
	Save(state) error
	LoadBans() ([]Ban, error)
	SaveBans([]Ban) error
	// Complete меняет статус погашения на Twitch: ok — FULFILLED, иначе CANCELED (баллы возвращаются зрителю).
	Complete(rewardID, redemptionID string, ok bool) error
	Log(level, msg string, kv ...any)
}

// Response — ответ маршрута: статус и JSON-тело.
type Response struct {
	Status int
	Body   any
}

func errResp(code int, msg string) Response { return Response{code, map[string]string{"error": msg}} }

// Queue — очередь заявок.
type Queue struct {
	Env Env
	Cfg Config
	Now func() time.Time
}

func size(s state) int {
	b, _ := json.Marshal(s)
	return len(b)
}

// prune держит состояние в пределах maxKept заявок и maxBytes байт: срезаются самые старые завершённые, живые заявки
// (ожидающие и отданные) не трогаются. Без ограничения по байтам история из «тяжёлых» заявок (JSON экранирует «<» как
// \u003c — 6 байт на символ) упёрлась бы в лимит хранилища, запись начала бы падать, а срезание идёт только при записи —
// очередь встала бы насовсем.
func prune(s state) state {
	live := func(it Item) bool {
		return it.Status == statusQueued || it.Status == statusSent || it.Status == statusPlaying
	}
	dropOldestDone := func() bool {
		for i, it := range s.Items {
			if !live(it) {
				s.Items = append(s.Items[:i:i], s.Items[i+1:]...)
				return true
			}
		}
		return false
	}
	for len(s.Items) > maxKept && dropOldestDone() {
	}
	for size(s) > maxBytes && dropOldestDone() {
	}
	return s
}

func (q *Queue) save(s state) error { return q.Env.Save(prune(s)) }

func (q *Queue) complete(it Item, ok bool) {
	if err := q.Env.Complete(it.RewardID, it.RedemptionID, ok); err != nil {
		// Не повод ломать очередь: заявка уже в нужном статусе, а награду пользователь может закрыть вручную на Twitch.
		q.Env.Log("warn", "не удалось обновить статус награды на Twitch", "redemption", it.RedemptionID, "err", err.Error())
	}
}

// OnRedemption — событие twitch.redemption.
func (q *Queue) OnRedemption(r Redemption) {
	if q.Cfg.RewardID == "" {
		q.Env.Log("warn", "награда для заявок не выбрана в настройках плагина (rewardId): погашения игнорируются")
		return
	}
	if r.RewardID != q.Cfg.RewardID {
		return
	}
	s, err := q.Env.Load()
	if err != nil {
		q.Env.Log("error", "очередь недоступна, заявка потеряна (награда осталась невыполненной на Twitch)", "redemption", r.ID, "err", err.Error())
		return
	}
	for _, it := range s.Items {
		if it.RedemptionID == r.ID {
			return // повторная доставка того же погашения
		}
	}
	input := r.Input
	if rs := []rune(input); len(rs) > maxInput {
		input = string(rs[:maxInput])
	}
	s.NextID++
	it := Item{ID: s.NextID, RedemptionID: r.ID, RewardID: r.RewardID, User: r.User, Input: input, Status: statusQueued}
	queued := 0
	for _, x := range s.Items {
		if x.Status == statusQueued {
			queued++
		}
	}
	if tr, ok := parseYandexTrack(r.Input); !ok {
		it.Status, it.Error = statusRejected, "not a Yandex Music track link"
	} else if queued >= maxQueued {
		it.Status, it.Error = statusRejected, "the queue is full"
	} else if q.isBanned(tr.TrackID) {
		it.Status, it.Error = statusRejected, "the track is banned"
	} else {
		it.AlbumID, it.TrackID, it.URL = tr.AlbumID, tr.TrackID, tr.URL
	}
	s.Items = append(s.Items, it)
	if err := q.save(s); err != nil {
		q.Env.Log("error", "не удалось сохранить заявку", "redemption", r.ID, "err", err.Error())
		return
	}
	if it.Status == statusRejected {
		q.complete(it, false) // баллы возвращаются
		q.Env.Log("info", "заявка отклонена", "user", it.User, "reason", it.Error)
		return
	}
	q.Env.Log("info", "заявка принята", "id", it.ID, "user", it.User, "track", it.TrackID)
}

// Next отдаёт аддону заявку: уже отданную без подтверждения — повторно (resend), иначе самую раннюю из очереди.
// Опрос идемпотентен: повторный GET не продвигает очередь, пока аддон не подтвердит старт или ошибку.
func (q *Queue) Next() Response {
	s, err := q.Env.Load()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	now := q.Now().Unix()
	for i := range s.Items {
		if s.Items[i].Status == statusSent {
			it := s.Items[i]
			s.Items[i].SentAt = now
			if err := q.save(s); err != nil {
				return errResp(http.StatusInternalServerError, storeError)
			}
			return Response{http.StatusOK, map[string]any{"item": it, "resend": true}}
		}
	}
	for i := range s.Items {
		if s.Items[i].Status == statusQueued {
			s.Items[i].Status, s.Items[i].SentAt = statusSent, now
			if err := q.save(s); err != nil {
				return errResp(http.StatusInternalServerError, storeError)
			}
			return Response{http.StatusOK, map[string]any{"item": s.Items[i], "resend": false}}
		}
	}
	return Response{http.StatusOK, map[string]any{"item": nil}}
}

type finishBody struct {
	ID     int    `json:"id"`
	Reason string `json:"reason"`
}

// Жизненный цикл: queued → sent (GET /next) → playing (POST /started: трек начал играть) → played (POST /played: отыгран,
// награда выполняется). POST /failed из queued/sent/playing отменяет награду — баллы возвращаются зрителю (не запустился, пропущен).
type action int

const (
	actStarted action = iota
	actPlayed
	actFailed
)

// Started — аддон подтверждает, что трек начал играть. Награда пока не выполняется. Повтор безопасен.
func (q *Queue) Started(body json.RawMessage) Response { return q.finish(body, actStarted) }

// Played — трек отыгран до конца (с перемотками, но не пропущен): награда выполняется.
func (q *Queue) Played(body json.RawMessage) Response { return q.finish(body, actPlayed) }

// Failed — запустить не удалось или трек пропущен: награда отменяется.
func (q *Queue) Failed(body json.RawMessage) Response { return q.finish(body, actFailed) }

func (q *Queue) finish(body json.RawMessage, act action) Response {
	var b finishBody
	if err := json.Unmarshal(body, &b); err != nil || b.ID < 1 {
		return errResp(http.StatusBadRequest, `body must be {"id": N}`)
	}
	s, err := q.Env.Load()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	for i := range s.Items {
		it := &s.Items[i]
		if it.ID != b.ID {
			continue
		}
		switch act {
		case actStarted:
			if it.Status == statusPlaying {
				return Response{http.StatusOK, map[string]any{"ok": true}} // повтор подтверждения
			}
			if it.Status != statusSent {
				return errResp(http.StatusConflict, "request was not sent to the player") // подтверждать можно только отданную
			}
			it.Status = statusPlaying
			if err := q.save(s); err != nil {
				return errResp(http.StatusInternalServerError, storeError)
			}
			return Response{http.StatusOK, map[string]any{"ok": true}}
		case actPlayed:
			if it.Status != statusPlaying && it.Status != statusSent {
				return errResp(http.StatusConflict, "request is "+it.Status+", not playing")
			}
			it.Status = statusPlayed
		default:
			if it.Status != statusQueued && it.Status != statusSent && it.Status != statusPlaying {
				return errResp(http.StatusConflict, "request is already "+it.Status)
			}
			reason := []rune(b.Reason)
			if len(reason) > maxReason {
				reason = reason[:maxReason]
			}
			it.Status, it.Error = statusFailed, string(reason)
		}
		done := *it
		if err := q.save(s); err != nil {
			return errResp(http.StatusInternalServerError, storeError)
		}
		q.complete(done, act == actPlayed) // после сохранения: если запись не удалась, награду не трогаем и аддон повторит подтверждение
		return Response{http.StatusOK, map[string]any{"ok": true}}
	}
	return errResp(http.StatusNotFound, "unknown request")
}

// Playing — заявки, которые аддон начал играть, но не довёл до конца. Аддон спрашивает их после перезапуска,
// чтобы продолжить следить за треком или вернуть баллы, если трек уже не играет.
func (q *Queue) Playing() Response {
	s, err := q.Env.Load()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	items := []Item{}
	for _, it := range s.Items {
		if it.Status == statusPlaying {
			items = append(items, it)
		}
	}
	return Response{http.StatusOK, map[string]any{"items": items}}
}

// --- бан-лист ---

func (q *Queue) isBanned(trackID string) bool {
	bans, err := q.Env.LoadBans()
	if err != nil {
		q.Env.Log("warn", "бан-лист недоступен, проверка пропущена", "err", err.Error())
		return false
	}
	for _, b := range bans {
		if b.TrackID == trackID {
			return true
		}
	}
	return false
}

// Ban добавляет трек в бан-лист (повтор не дублирует). Самые старые записи вытесняются при переполнении.
func (q *Queue) Ban(trackID, title string) error {
	bans, err := q.Env.LoadBans()
	if err != nil {
		return err
	}
	for _, b := range bans {
		if b.TrackID == trackID {
			return nil
		}
	}
	if rs := []rune(title); len(rs) > maxBanName {
		title = string(rs[:maxBanName])
	}
	bans = append(bans, Ban{TrackID: trackID, Title: title, At: q.Now().Unix()})
	if len(bans) > maxBans {
		bans = bans[len(bans)-maxBans:]
	}
	return q.Env.SaveBans(bans)
}

// Unban убирает трек из бан-листа.
func (q *Queue) Unban(body json.RawMessage) Response {
	var b struct {
		TrackID string `json:"trackId"`
	}
	if err := json.Unmarshal(body, &b); err != nil || !trackIDRe.MatchString(b.TrackID) {
		return errResp(http.StatusBadRequest, `body must be {"trackId": "digits"}`)
	}
	bans, err := q.Env.LoadBans()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	for i, x := range bans {
		if x.TrackID == b.TrackID {
			if err := q.Env.SaveBans(append(bans[:i:i], bans[i+1:]...)); err != nil {
				return errResp(http.StatusInternalServerError, storeError)
			}
			return Response{http.StatusOK, map[string]any{"ok": true}}
		}
	}
	return errResp(http.StatusNotFound, "track is not banned")
}

// Bans — бан-лист.
func (q *Queue) Bans() Response {
	bans, err := q.Env.LoadBans()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	if bans == nil {
		bans = []Ban{}
	}
	return Response{http.StatusOK, map[string]any{"items": bans}}
}

// List — вся очередь с историей (для интерфейса).
func (q *Queue) List() Response {
	s, err := q.Env.Load()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	if s.Items == nil {
		s.Items = []Item{}
	}
	return Response{http.StatusOK, map[string]any{"items": s.Items}}
}

// ClearQueued отменяет все заявки, которые ещё не отданы плееру (queued): баллы возвращаются. Играющие и отданные не трогаем:
// ими занят аддон, их можно отменить по одной или пропустить.
func (q *Queue) ClearQueued(body json.RawMessage) Response {
	var b struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &b) // тело необязательно
	reason := []rune(b.Reason)
	if len(reason) == 0 {
		reason = []rune("очередь очищена стримером")
	}
	if len(reason) > maxReason {
		reason = reason[:maxReason]
	}
	s, err := q.Env.Load()
	if err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	var done []Item
	for i := range s.Items {
		if it := &s.Items[i]; it.Status == statusQueued {
			it.Status, it.Error = statusFailed, string(reason)
			done = append(done, *it)
		}
	}
	if len(done) == 0 {
		return Response{http.StatusOK, map[string]any{"ok": true, "cleared": 0}}
	}
	if err := q.save(s); err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	for _, it := range done {
		q.complete(it, false) // после сохранения, как в finish
	}
	return Response{http.StatusOK, map[string]any{"ok": true, "cleared": len(done)}}
}

// BanTrack — ручной бан по номеру трека или ссылке Яндекс Музыки (со страницы настроек).
func (q *Queue) BanTrack(body json.RawMessage) Response {
	var b struct {
		TrackID string `json:"trackId"`
		Link    string `json:"link"`
		Title   string `json:"title"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return errResp(http.StatusBadRequest, `body must be {"trackId": "digits"} or {"link": "https://music.yandex.ru/album/N/track/N"}`)
	}
	id, title := b.TrackID, b.Title
	if id == "" && b.Link != "" {
		t, ok := parseYandexTrack(b.Link)
		if !ok {
			return errResp(http.StatusBadRequest, "not a Yandex Music track link")
		}
		id = t.TrackID
	}
	if !trackIDRe.MatchString(id) {
		return errResp(http.StatusBadRequest, "trackId must be digits")
	}
	if strings.TrimSpace(title) == "" {
		title = "трек " + id
	}
	if err := q.Ban(id, title); err != nil {
		return errResp(http.StatusInternalServerError, storeError)
	}
	return Response{http.StatusOK, map[string]any{"ok": true}}
}

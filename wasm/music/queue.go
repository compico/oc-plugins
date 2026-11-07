package main

import (
	"encoding/json"
	"net/http"
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
	statusPlayed   = "played"   // аддон подтвердил запуск, награда выполнена
	statusFailed   = "failed"   // аддон не смог запустить, награда отменена
	statusRejected = "rejected" // ссылка не подошла или очередь полна, награда отменена
)

const (
	maxQueued  = 100 // заявок в ожидании: дальше новые отклоняются
	maxKept    = 200 // сколько заявок помним (дедупликация повторных доставок + история)
	maxInput   = 500 // сколько символов ввода зрителя храним
	maxReason  = 200
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

// Env — всё, что очереди нужно от хоста. В плагине его реализует main.go поверх SDK, в тестах — фейк.
type Env interface {
	Load() (state, error)
	Save(state) error
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

func prune(s state) state {
	if len(s.Items) <= maxKept {
		return s
	}
	drop := len(s.Items) - maxKept
	kept := make([]Item, 0, maxKept)
	for _, it := range s.Items { // срезаем самые старые завершённые; живые заявки не трогаем
		if drop > 0 && it.Status != statusQueued && it.Status != statusSent {
			drop--
			continue
		}
		kept = append(kept, it)
	}
	s.Items = kept
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

// Started — аддон подтверждает, что трек запущен.
func (q *Queue) Started(body json.RawMessage) Response { return q.finish(body, true) }

// Failed — аддон сообщает, что запустить трек не удалось.
func (q *Queue) Failed(body json.RawMessage) Response { return q.finish(body, false) }

func (q *Queue) finish(body json.RawMessage, ok bool) Response {
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
		if ok && it.Status != statusSent {
			return errResp(http.StatusConflict, "request was not sent to the player") // подтверждать можно только отданную
		}
		if it.Status != statusSent && it.Status != statusQueued {
			return errResp(http.StatusConflict, "request is already "+it.Status)
		}
		if ok {
			it.Status = statusPlayed
		} else {
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
		q.complete(done, ok) // после сохранения: если запись не удалась, награду не трогаем и аддон повторит подтверждение
		return Response{http.StatusOK, map[string]any{"ok": true}}
	}
	return errResp(http.StatusNotFound, "unknown request")
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

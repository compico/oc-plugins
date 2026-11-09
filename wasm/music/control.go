package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// Управление плеером. Плагин пассивен и до PulseSync не дотягивается, поэтому команда ставится в короткую очередь, а аддон
// опрашивает её (GET /commands) и подтверждает (POST /commands/done) ДО исполнения: команда выполняется не более одного раза
// (двойной пропуск хуже потерянного). Устаревшая команда не должна сработать «когда-нибудь потом», поэтому живёт недолго.

const (
	commandTTL  = 10 * time.Second
	maxCommands = 10
)

// Допустимые действия.
const (
	actPlay    = "play"
	actPause   = "pause"
	actToggle  = "toggle"
	actSkip    = "skip"
	actSkipBan = "skip_ban" // пропустить и забанить трек (заявки на него больше не принимаются)
)

var validActions = map[string]bool{actPlay: true, actPause: true, actToggle: true, actSkip: true, actSkipBan: true}

// Command — команда плееру.
type Command struct {
	ID     int64  `json:"id"`
	Action string `json:"action"`
	at     time.Time
}

// Control — очередь команд в памяти.
type Control struct {
	Now  func() time.Time
	cmds []Command
	last int64
}

func (c *Control) expire() {
	now := c.Now()
	kept := c.cmds[:0]
	for _, x := range c.cmds {
		if now.Sub(x.at) <= commandTTL {
			kept = append(kept, x)
		}
	}
	c.cmds = kept
}

// Request принимает команду пользователя. Для skip_ban трек берётся из «сейчас играет» и банится ДО постановки команды:
// если бан не записался, трек не пропускается молча без бана.
func (c *Control) Request(raw json.RawMessage, q *Queue, now *NowPlaying) Response {
	var b struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || !validActions[b.Action] {
		return errResp(http.StatusBadRequest, "action must be one of: play, pause, toggle, skip, skip_ban")
	}
	c.expire()
	if len(c.cmds) >= maxCommands {
		return errResp(http.StatusTooManyRequests, "too many pending commands")
	}
	out := map[string]any{}
	if b.Action == actSkipBan {
		if now == nil {
			return errResp(http.StatusConflict, "nothing is playing")
		}
		if err := q.Ban(now.TrackID, now.Title); err != nil {
			return errResp(http.StatusInternalServerError, storeError)
		}
		out["banned"] = now.TrackID
	}
	t := c.Now()
	id := t.UnixMilli() * 1000
	if id <= c.last {
		id = c.last + 1 // идентификаторы строго растут, в том числе после перезапуска плагина
	}
	c.last = id
	c.cmds = append(c.cmds, Command{ID: id, Action: b.Action, at: t})
	out["id"] = id
	return Response{http.StatusOK, out}
}

// Pending — команды, которые аддон ещё не подтвердил.
func (c *Control) Pending() Response {
	c.expire()
	list := make([]Command, len(c.cmds))
	copy(list, c.cmds)
	return Response{http.StatusOK, map[string]any{"commands": list}}
}

// Done — аддон принял команду к исполнению: она снимается с очереди.
func (c *Control) Done(raw json.RawMessage) Response {
	var b struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || b.ID < 1 {
		return errResp(http.StatusBadRequest, `body must be {"id": N}`)
	}
	for i, x := range c.cmds {
		if x.ID == b.ID {
			c.cmds = append(c.cmds[:i:i], c.cmds[i+1:]...)
			return Response{http.StatusOK, map[string]any{"ok": true}}
		}
	}
	return errResp(http.StatusNotFound, "unknown or expired command")
}

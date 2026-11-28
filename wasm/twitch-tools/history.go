package main

import (
	"strconv"
	"strings"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

const (
	// DefaultHistory — сколько последних сообщений плагин помнит, если в настройках не задано иное.
	DefaultHistory = 200
	// MaxHistory — верхняя граница настройки: история лежит в памяти плагина и целиком уходит виджету при открытии.
	MaxHistory = 1000
)

// ClampHistory приводит размер истории к допустимому: неположительное значение даёт умолчание, слишком большое — MaxHistory.
func ClampHistory(n int) int {
	if n <= 0 {
		return DefaultHistory
	}
	if n > MaxHistory {
		return MaxHistory
	}
	return n
}

// ParseHistorySize читает размер истории из настройки плагина (строка); пусто или мусор — умолчание.
func ParseHistorySize(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return DefaultHistory
	}
	return ClampHistory(n)
}

// Status — последнее известное состояние связи OC с чатом Twitch.
type Status struct {
	Connected *bool  `json:"connected,omitempty"`
	Error     string `json:"error,omitempty"`
}

// History — последние сообщения чата в порядке прихода. Это единственное состояние плагина: в OBS и в Dashboard виджет
// при открытии берёт историю отсюда, а новые события получает по подписке.
type History struct {
	max    int
	msgs   []ocplugin.ChatMessage
	status Status
}

func NewHistory(max int) *History { return &History{max: ClampHistory(max)} }

// SetMax меняет размер истории и сразу отбрасывает лишнее самое старое.
func (h *History) SetMax(n int) {
	h.max = ClampHistory(n)
	h.trim()
}

func (h *History) trim() {
	if over := len(h.msgs) - h.max; over > 0 {
		h.msgs = append([]ocplugin.ChatMessage(nil), h.msgs[over:]...)
	}
}

// Apply применяет событие чата к истории. Возвращает true, если событие нужно раздать виджетам.
func (h *History) Apply(ev ocplugin.ChatEvent) bool {
	switch ev.Event {
	case "message":
		if ev.Message == nil {
			return false
		}
		h.msgs = append(h.msgs, *ev.Message)
		h.trim()
		return true
	case "delete":
		switch {
		case ev.TargetID != "":
			h.remove(func(m ocplugin.ChatMessage) bool { return m.ID == ev.TargetID })
		case ev.Login != "":
			h.removeLogin(ev.Login)
		default:
			return false
		}
		return true
	case "clear":
		if ev.Login == "" {
			h.msgs = nil
		} else {
			h.removeLogin(ev.Login)
		}
		return true
	case "status":
		h.status = Status{Connected: ev.Connected, Error: ev.Error}
		return true
	}
	return false
}

func (h *History) removeLogin(login string) {
	login = strings.ToLower(login)
	h.remove(func(m ocplugin.ChatMessage) bool { return strings.ToLower(m.Login) == login })
}

func (h *History) remove(drop func(ocplugin.ChatMessage) bool) {
	kept := h.msgs[:0:0]
	for _, m := range h.msgs {
		if !drop(m) {
			kept = append(kept, m)
		}
	}
	h.msgs = kept
}

// Messages — копия истории (не nil, чтобы в JSON был пустой список).
func (h *History) Messages() []ocplugin.ChatMessage {
	return append([]ocplugin.ChatMessage{}, h.msgs...)
}

// Status — последнее известное состояние связи с чатом.
func (h *History) Status() Status { return h.status }

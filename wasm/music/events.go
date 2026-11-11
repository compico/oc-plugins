package main

import (
	"time"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

// События для подписчиков (виджеты, страница настроек, аддон, deck): они избавляют их от опроса, но не заменяют HTTP —
// маршруты остаются рабочими, а подписчик при подключении всё равно может прочитать состояние обычным GET.
//
//	now      — «сейчас играет» (retained): {"now": {...}|null, "at": unixMs}; позицию клиент досчитывает от at.
//	queue    — очередь (retained): тело GET /queue без поля now (оно приходит в топике now).
//	commands — новая команда плееру (НЕ retained): {"id", "action"}. Повторная доставка устаревшей команды опасна
//	           (двойной пропуск), поэтому состояние очереди команд не хранится — при подключении её читают GET /commands.
const (
	topicNow      = "now"
	topicQueue    = "queue"
	topicCommands = "commands"
)

// publisher — отправка события; в тестах подменяется.
type publisher interface {
	Retained(topic string, v any)
	Fire(topic string, v any)
}

type sdkPublisher struct{}

// Ошибки публикации не должны ломать основную операцию (заявка уже принята): событие — удобство, а не источник истины.
func (sdkPublisher) Retained(topic string, v any) {
	if err := ocplugin.PublishRetained(topic, v); err != nil {
		ocplugin.Warn("publish", "topic", topic, "err", err.Error())
	}
}

func (sdkPublisher) Fire(topic string, v any) {
	if err := ocplugin.Publish(topic, v); err != nil {
		ocplugin.Warn("publish", "topic", topic, "err", err.Error())
	}
}

func eventDecls() []ocplugin.EventDecl {
	return []ocplugin.EventDecl{
		{ID: topicNow, Name: "Сейчас играет", Description: "Состояние плеера: трек, позиция, играет ли."},
		{ID: topicQueue, Name: "Очередь", Description: "Очередь заявок (как GET /queue)."},
		{ID: topicCommands, Name: "Команды плееру", Description: "Новая команда пульта (play, pause, skip…). Не хранится."},
	}
}

// Events связывает состояние плагина с публикацией.
type Events struct {
	Pub publisher
	Now func() time.Time
}

func (e *Events) PublishNow(now *NowPlaying) {
	e.Pub.Retained(topicNow, map[string]any{"now": now, "at": e.Now().UnixMilli()})
}

func (e *Events) PublishQueue(r Response) {
	if r.Status == 200 {
		e.Pub.Retained(topicQueue, r.Body)
	}
}

// PublishCommand объявляет только что поставленную команду: её id и действие берутся из очереди команд, а не из тела запроса.
func (e *Events) PublishCommand(c *Control) {
	if n := len(c.cmds); n > 0 {
		last := c.cmds[n-1]
		e.Pub.Fire(topicCommands, map[string]any{"id": last.ID, "action": last.Action})
	}
}

// Плагин «chat» — чат Twitch как автономный wasm-плагин: хранит последние сообщения на сервере и показывает их виджетом
// (в OBS и в Dashboard одинаково). Заменяет встроенный HTML-виджет twitch-chat.
//
// Чат читает сам OC (одно соединение с каналом по умолчанию из настроек OC) и присылает плагину события chat.message: message,
// delete, clear и status. Сообщения приходят с готовыми смайлами (parts: текст или смайл со ссылкой на картинку), поэтому плагин
// и виджет не считают позиции и не строят ссылки. Плагин ведёт историю и раздаёт события виджетам топиком chat; историю виджет
// берёт маршрутом GET /messages. Все настройки отображения — настройки виджета (у каждого источника свои), фильтры применяет виджет.
//
// Сборка: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o chat.wasm .
package main

import (
	"fmt"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

const version = "0.1.0"

// topicChat — топик событий для виджетов: ChatEvent как его прислал OC.
const topicChat = "chat"

// Config — настройки плагина.
type Config struct {
	HistorySize string `json:"historySize"`
}

var hist = NewHistory(DefaultHistory)

func init() {
	p := ocplugin.New(ocplugin.Meta{
		ID: "chat", Name: "Twitch chat", Version: version, Author: "compico",
		Description: "Чат Twitch: ники, смайлы, шрифты, цвета и фон настраиваются в редакторе. Чат читается на сервере и хранится в плагине, поэтому виден сразу после открытия.",
		Permissions: []string{ocplugin.PermChatRead},
		Events:      []ocplugin.EventDecl{{ID: topicChat, Name: "Chat events", Description: "Сообщения чата и их удаление: message, delete, clear, status"}},
		Widgets:     []ocplugin.Widget{{ID: "chat", Name: "Twitch chat", Entry: "index.html", Width: 480, Height: 600, Settings: widgetSettings()}},
		ConfigSchema: map[string]any{
			"historySize": map[string]any{
				"type": "string", "title": "Сколько сообщений помнить",
				"description": fmt.Sprintf("Число от 1 до %d, по умолчанию %d. Столько последних сообщений виджет получает при открытии.", MaxHistory, DefaultHistory),
			},
		},
	})

	p.OnChatMessage(func(ev ocplugin.ChatEvent) {
		if hist.Apply(ev) {
			if err := ocplugin.Publish(topicChat, ev); err != nil {
				ocplugin.Warn("не удалось раздать событие чата: " + err.Error())
			}
		}
	})

	p.OnInit(func() error {
		var cfg Config
		if err := p.Config(&cfg); err != nil {
			return err
		}
		hist.SetMax(ParseHistorySize(cfg.HistorySize))
		return nil
	})

	// История и последнее состояние связи: виджет берёт их при открытии, затем живёт по подписке на topicChat.
	p.GET("/messages", func(c *ocplugin.Ctx) error {
		return c.JSON(200, map[string]any{"messages": hist.Messages(), "status": hist.Status()})
	})
}

func main() {} // обязателен для сборки reactor, остаётся пустым

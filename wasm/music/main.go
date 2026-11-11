// Плагин «music» — заявки на треки из Яндекс Музыки за баллы канала Twitch.
//
// Зритель погашает награду и вставляет ссылку на трек; плагин ставит заявку в очередь, аддон плеера (PulseSync) забирает
// её маршрутом GET /next, запускает трек и подтверждает POST /started (или POST /failed). Награда на Twitch выполняется
// или отменяется (баллы возвращаются) автоматически.
//
// Сборка: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o music.wasm .
package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

const version = "0.6.0"

// sdkEnv подключает очередь к хосту через SDK.
type sdkEnv struct{}

func (sdkEnv) Load() (state, error) {
	s, _, err := ocplugin.Load[state](stateKey)
	return s, err
}

func (sdkEnv) Save(s state) error { return ocplugin.Store(stateKey, s) }

func (sdkEnv) LoadBans() ([]Ban, error) {
	b, _, err := ocplugin.Load[[]Ban](bansKey)
	return b, err
}

func (sdkEnv) SaveBans(b []Ban) error { return ocplugin.Store(bansKey, b) }

func (sdkEnv) Complete(rewardID, redemptionID string, ok bool) error {
	if ok {
		return ocplugin.FulfillRedemption(rewardID, redemptionID)
	}
	return ocplugin.CancelRedemption(rewardID, redemptionID)
}

func (sdkEnv) Log(level, msg string, kv ...any) {
	switch level {
	case "error":
		ocplugin.Error(msg, kv...)
	case "warn":
		ocplugin.Warn(msg, kv...)
	default:
		ocplugin.Info(msg, kv...)
	}
}

// rawBody отдаёт тело запроса как есть; пустое тело — nil (обработчики очереди ответят 400).
func rawBody(c *ocplugin.Ctx) json.RawMessage {
	var raw json.RawMessage
	if err := c.Bind(&raw); err != nil {
		return nil
	}
	return raw
}

func reply(c *ocplugin.Ctx, r Response) error {
	if r.Status == 0 {
		r.Status = http.StatusOK
	}
	return c.JSON(r.Status, r.Body)
}

func init() {
	p := ocplugin.New(ocplugin.Meta{
		ID: "music", Name: "Заявки на треки", Version: version, Author: "compico",
		Description: "Заявки на треки Яндекс Музыки за баллы канала: очередь и выдача плееру. Награда выполняется или отменяется сама.",
		Permissions: []string{ocplugin.PermStorage, ocplugin.PermRedeem, ocplugin.PermPublish},
		Events:      eventDecls(),
		// Интерфейс лежит в ui/ и упаковывается в .wasm при сборке (ocpack).
		// Страница настроек (окно Plugins → Settings): очередь, пульт и бан-лист для ручного управления в редких случаях.
		Settings: "settings.html",
		Widgets: []ocplugin.Widget{
			{ID: "queue", Name: "Очередь треков", Entry: "index.html", Width: 420, Height: 360},
			{ID: "player", Name: "Плеер с заявками", Entry: "player.html", Width: 460, Height: 400},
			{ID: "control", Name: "Пульт плеера", Entry: "control.html", Width: 360, Height: 230},
		},
		ConfigSchema: map[string]any{
			"rewardId": map[string]any{
				"type": "string", "format": "twitch.reward", "title": "Награда для заявок",
				"description": "Погашения этой награды считаются заявками на трек. Награда должна быть создана приложением OC, иначе Twitch не даст менять её статус.",
			},
		},
	})
	q := &Queue{Env: sdkEnv{}, Now: time.Now}
	pl := &Player{Now: time.Now}
	ctl := &Control{Now: time.Now}
	ev := &Events{Pub: sdkPublisher{}, Now: time.Now}
	// После изменения очереди подписчики получают её целиком; ответ маршрута от этого не меняется.
	changed := func(r Response) Response {
		if r.Status == http.StatusOK {
			ev.PublishQueue(q.List())
		}
		return r
	}
	p.OnInit(func() error {
		var cfg Config
		if err := p.Config(&cfg); err != nil {
			return err
		}
		q.Cfg = cfg
		return nil
	})
	p.OnRedemption(func(r ocplugin.Redemption) {
		user := r.UserName
		if user == "" {
			user = r.UserLogin
		}
		q.OnRedemption(Redemption{ID: r.ID, RewardID: r.RewardID, User: user, Input: r.Input})
		ev.PublishQueue(q.List())
	})
	p.GET("/next", func(c *ocplugin.Ctx) error { return reply(c, changed(q.Next())) })
	p.GET("/queue", func(c *ocplugin.Ctx) error { return reply(c, withNow(q.List(), pl.Get())) })
	p.POST("/nowplaying", func(c *ocplugin.Ctx) error {
		r := pl.Set(rawBody(c))
		if r.Status == http.StatusOK {
			ev.PublishNow(pl.Get())
		}
		return reply(c, r)
	})
	p.POST("/started", func(c *ocplugin.Ctx) error { return reply(c, changed(q.Started(rawBody(c)))) })
	p.POST("/played", func(c *ocplugin.Ctx) error { return reply(c, changed(q.Played(rawBody(c)))) })
	p.POST("/failed", func(c *ocplugin.Ctx) error { return reply(c, changed(q.Failed(rawBody(c)))) })
	p.GET("/playing", func(c *ocplugin.Ctx) error { return reply(c, q.Playing()) })
	// Управление плеером: пульт (виджет) ставит команду, аддон забирает.
	p.POST("/control", func(c *ocplugin.Ctx) error {
		raw := rawBody(c)
		r := ctl.Request(raw, q, pl.Get())
		if r.Status == http.StatusOK {
			ev.PublishCommand(ctl)
		}
		return reply(c, r)
	})
	p.GET("/commands", func(c *ocplugin.Ctx) error { return reply(c, ctl.Pending()) })
	p.POST("/commands/done", func(c *ocplugin.Ctx) error { return reply(c, ctl.Done(rawBody(c))) })
	p.GET("/bans", func(c *ocplugin.Ctx) error { return reply(c, q.Bans()) })
	p.POST("/ban", func(c *ocplugin.Ctx) error { return reply(c, q.BanTrack(rawBody(c))) })
	p.POST("/clear", func(c *ocplugin.Ctx) error { return reply(c, changed(q.ClearQueued(rawBody(c)))) })
	p.POST("/unban", func(c *ocplugin.Ctx) error { return reply(c, q.Unban(rawBody(c))) })
}

func main() {}

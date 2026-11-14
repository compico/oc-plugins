// Плагин «osu-skills» — график сложности навыков текущей карты osu! (stamina, tenacity, agility, precision, reading, memory,
// accuracy, reaction). Объединяет tosu-оверлей osuskillsgraph и расчёт навыков go-osu: поднимать go-osu не нужно.
//
// Виджет слушает tosu (websocket v2), узнаёт карту и моды и просит у плагина кривые (POST /curves). Плагин скачивает файл
// .osu (Fetch хоста), считает кривые библиотекой github.com/compico/go-osu/pkg и отдаёт их в формате ручки go-osu /strains.
//
// Сборка: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o osu-skills.wasm .
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

const version = "0.1.0"

// Config — настройки пользователя.
type Config struct {
	Source string `json:"source"`
}

// svc — сервис плагина; пакетная переменная нужна обработчику ответа Fetch, который создаётся раньше присваивания.
var svc *Service

func init() {
	p := ocplugin.New(ocplugin.Meta{
		ID: "osu-skills", Name: "osu! skills graph", Version: version, Author: "compico",
		Description: "График сложности навыков текущей карты osu! (данные из tosu, расчёт go-osu). Файл карты скачивается по её ID.",
		Permissions: []string{ocplugin.PermNetHTTP},
		// Интерфейс лежит в ui/ и упаковывается в .wasm при сборке (ocpack).
		Widgets: []ocplugin.Widget{{ID: "graph", Name: "osu! skills graph", Entry: "index.html", Width: 520, Height: 220}},
		ConfigSchema: map[string]any{
			"source": map[string]any{
				"type": "string", "title": "Источник файлов карт",
				"description": "Ссылка на .osu, {id} заменяется ID сложности (beatmap_id). Только https. По умолчанию " + DefaultSource,
			},
		},
	})

	svc = NewService(DefaultSource, func(id, url string) error {
		// Ответ придёт в OnHTTPResponse: обработчик привязан к id запроса, а id — к карте.
		p.OnHTTPResponse(id, func(r ocplugin.HTTPResponse) {
			n, err := strconv.ParseInt(strings.TrimPrefix(r.ID, "map:"), 10, 64)
			if err != nil {
				return
			}
			svc.OnFetched(n, r.Status, r.Body, r.Error)
		})
		return ocplugin.Fetch(id, ocplugin.FetchRequest{URL: url})
	}, time.Now)

	p.OnInit(func() error {
		var cfg Config
		if err := p.Config(&cfg); err != nil {
			return err
		}
		svc.Source = DefaultSource
		if cfg.Source != "" {
			if ValidSource(cfg.Source) {
				svc.Source = cfg.Source
			} else {
				ocplugin.Warn(fmt.Sprintf("источник карт %q не подходит (нужен https и один {id}), используется стандартный", cfg.Source))
			}
		}
		return nil
	})

	p.POST("/curves", func(c *ocplugin.Ctx) error {
		var req Request
		if err := c.Bind(&req); err != nil {
			return c.Error(400, "invalid request")
		}
		r := svc.Curves(req)
		return c.JSON(r.Status, r.Body)
	})
}

func main() {} // обязателен для сборки reactor, остаётся пустым

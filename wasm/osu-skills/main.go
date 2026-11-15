// Плагин «osu-skills» — график сложности навыков текущей карты osu! (stamina, tenacity, agility, precision, reading, memory,
// accuracy, reaction). Объединяет tosu-оверлей osuskillsgraph и расчёт навыков go-osu: поднимать go-osu не нужно.
//
// Состояние (файл карты, моды, время) хост ретранслирует из websocket tosu событием live (net.local streams): из sandbox-iframe
// tosu websocket не пускает (Origin null), а опрос HTTP из плагина заметно грузит процессор. Виджет подписан на live и просит кривые (POST /curves). Плагин берёт файл
// .osu у того же tosu (Fetch хоста, разрешение net.local), считает кривые библиотекой github.com/compico/go-osu/pkg и отдаёт
// их в формате ручки go-osu /strains.
//
// Сборка: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o osu-skills.wasm .
package main

import (
	"fmt"
	"time"

	"github.com/compico/overlaycomposer/pkg/ocplugin"
)

const version = "0.6.0"

// Config — настройки пользователя.
type Config struct {
	TosuHost string `json:"tosuHost"`
}

// svc — сервис плагина; пакетная переменная нужна обработчику ответа Fetch, который создаётся раньше присваивания.
var svc *Service

func init() {
	p := ocplugin.New(ocplugin.Meta{
		ID: "osu-skills", Name: "osu! skills graph", Version: version, Author: "compico",
		Description: "График сложности навыков текущей карты osu! (данные и файл карты — из tosu, расчёт — go-osu).",
		Permissions: []string{ocplugin.PermNetLocal},
		// Хост пускает плагин только на tosu и только в раздел с файлами карт.
		NetLocal: []ocplugin.NetLocalTarget{{Target: DefaultTosu, Setting: "tosuHost", Paths: []string{MapsPath},
			// Состояние tosu хост ретранслирует сам: держит websocket к tosu (без Origin) и публикует выбранные поля событием live,
			// пока на него подписан виджет. wasm при этом не вызывается.
			Streams: []ocplugin.NetLocalStream{{
				Path: "/websocket/v2", Topic: "live", MinIntervalMs: 250,
				Fields: map[string]string{
					"file": "directPath.beatmapFile", "checksum": "beatmap.checksum",
					"mods": "play.mods.name", "time": "beatmap.time.live",
				},
			}},
		}},
		Events: []ocplugin.EventDecl{{ID: "live", Name: "Current map", Description: "Файл карты, контрольная сумма, моды и время в карте из tosu"}},
		// Интерфейс лежит в ui/ и упаковывается в .wasm при сборке (ocpack).
		Widgets: []ocplugin.Widget{{ID: "graph", Name: "osu! skills graph", Entry: "index.html", Width: 520, Height: 220,
			// Визуальные настройки лежат в самом виджете: у каждого источника на сцене они свои. Умолчания должны совпадать с кодом ui/main.js.
			Settings: []ocplugin.WidgetSetting{
				{Key: "style", Label: "Стиль", Type: "select", Default: "ribbon", Options: []ocplugin.SettingOption{
					{Value: "ribbon", Label: "Ribbon: гора, цвет по ведущему навыку"},
					{Value: "silhouette", Label: "Silhouette: силуэт навыков, ведущий закрашен"},
					{Value: "lines", Label: "Lines: линия на навык"},
					{Value: "spectrum", Label: "Spectrum: столбики, текущее значение навыков"},
				}},
				{Key: "window", Label: "Окно точки, мс", Type: "number", Default: 5000, Min: ptr(500), Max: ptr(60000), Step: ptr(500),
					Help: "Сколько миллисекунд карты в одной точке графика. На длинных картах больше значение — плавнее график (не влияет на spectrum)."},
			},
		}},
		ConfigSchema: map[string]any{
			"tosuHost": map[string]any{
				"type": "string", "title": "Адрес tosu",
				"description": "host:port, где слушает tosu. По умолчанию " + DefaultTosu + ". ",
			},
		},
	})

	svc = NewService(DefaultTosu, func(id, url string) error {
		// Ответ придёт в OnHTTPResponse: обработчик привязан к id запроса, а id — к карте (или к опросу состояния).
		p.OnHTTPResponse(id, func(r ocplugin.HTTPResponse) { svc.OnFetched(r.ID, r.Status, r.Body, r.Error) })
		return ocplugin.Fetch(id, ocplugin.FetchRequest{URL: url})
	}, time.Now)

	p.OnInit(func() error {
		var cfg Config
		if err := p.Config(&cfg); err != nil {
			return err
		}
		svc.Tosu = DefaultTosu
		if cfg.TosuHost != "" {
			if ValidTosu(cfg.TosuHost) {
				svc.Tosu = cfg.TosuHost
			} else {
				ocplugin.Warn(fmt.Sprintf("адрес tosu %q не подходит (нужен host:port на этом компьютере), используется %s", cfg.TosuHost, DefaultTosu))
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

func ptr(v float64) *float64 { return &v }

func main() {} // обязателен для сборки reactor, остаётся пустым

# oc-plugins

Плагины для [Overlay Composer](https://github.com/compico/overlaycomposer) (OC). Каждый плагин — своя папка `plugins/<id>/`
с `plugin.json`, `index.html` и своим `README.md`.

## Плагины

| Плагин | Что делает | Разрешения |
|---|---|---|
| [`osu-np`](plugins/osu-np) | на `!np` отвечает в чате ссылкой на текущую карту osu! (данные из tosu) | `chat.read`, `chat.send`, `net.local` |
| [`fps-meter`](plugins/fps-meter) | FPS и время кадра страницы оверлея (avg/p95/max, пропущенные кадры, график) — чтобы найти виджет, «просаживающий» оверлей в OBS | — |

### Автономные плагины (standalone, wasm)

Работают на сервере OC, без окна и iframe: отвечают на HTTP-запросы, получают события (например, награды Twitch), хранят данные.
Один `.wasm`-файл, исходники — в [`wasm/<id>/`](wasm), готовые файлы — в [Releases](https://github.com/compico/oc-plugins/releases) (в репозитории бинарников нет).

| Плагин | Что делает | Разрешения |
|---|---|---|
| [`music`](wasm/music) | заявки на треки Яндекс Музыки за баллы канала: очередь, выдача аддону плеера, выполнение/отмена награды; виджет очереди | `storage`, `twitch.redemption` |

Установка: скачайте `<id>.wasm` из релиза `<id>-v<версия>`, положите в папку плагинов Overlay Composer (рядом с обычными плагинами; её открывает «Open folder» в «Standalone plugins…»), нажмите «Rescan» и выдайте плагину разрешения в «Plugin store… → Standalone plugins…».
Устройство плагинов — [`docs/WASM_PLUGINS.md`](https://github.com/compico/overlaycomposer/blob/main/docs/WASM_PLUGINS.md); SDK для Go — [`pkg/ocplugin`](https://github.com/compico/overlaycomposer/tree/main/pkg/ocplugin).

## Установка HTML-плагина

1. В OC: Add Source → **Open plugins folder**.
2. Скопируйте в неё папку нужного плагина из `plugins/` (целиком, вместе с `plugin.json`).
3. Add Source → выберите плагин, подтвердите разрешения, настройте в свойствах слоя.

Плагин — это чужой код, который работает внутри оверлея: ставьте только те, которым доверяете. Разрешения (`chat.read`, `chat.send`, `storage`, `net.local`)
показываются при добавлении виджета, их можно отозвать в свойствах слоя.

## Как написать свой плагин

- API плагинов и все типы — один файл [`oc.d.ts`](https://github.com/compico/overlaycomposer/blob/main/web/sdk/oc.d.ts)
  (запущенный OC отдаёт его по `/plugin-sdk/oc.d.ts`); описание — [`docs/PLUGINS.md`](https://github.com/compico/overlaycomposer/blob/main/docs/PLUGINS.md).
  Этот файл можно отдать ИИ-помощнику вместе с задачей.
- Структура плагина: `plugins/<id>/plugin.json` (id = имя папки), `index.html` (точка входа), остальные файлы — что нужно виджету, `README.md`.
- Журнал изменений — `plugins/<id>/changelog.json`: `[{ "version": "1.2.0", "date": "2026-10-27", "changes": ["что нового"], "migration": "что сделать после обновления" }]`. При выходе новой версии Overlay Composer покажет пользователю записи новее его версии (в центре уведомлений). В файле должна быть запись для текущей `version`; `migration` пишите, только если после обновления нужны действия (новое поле, право, переименованная настройка).
- Логику, не связанную с DOM и сетью, выносите в отдельный файл и тестируйте (пример: `plugins/osu-np/logic.js` и `test/osu-np.test.mjs`).
- Автономный wasm-плагин: папка `wasm/<id>/` со своим `go.mod` (id = имя папки = `id` в `oc_meta`), см. [`wasm/music`](wasm/music). Если есть папка `ui/` — это интерфейс виджетов (объявляются в `Meta.Widgets`): при сборке `ocpack` упаковывает его в сам `.wasm` (при первой сборке ставится из overlaycomposer@`OCPACK_REF`, нужен доступ к сети; или задайте `OCPACK=<путь к ocpack>`). Проверка и сборка: `node scripts/wasm.mjs build <id>` (нужны Go 1.24+ и Node.js). Новая версия — поднять `version` в коде: после merge в `main` CI выпустит релиз `<id>-v<версия>`; если менялся код, а версия прежняя, CI на PR подскажет.
- Перед коммитом: `npm test` — проверяет манифесты (`scripts/validate.mjs`) и запускает тесты (нужен только Node.js, зависимостей нет).

## Лицензия

[MIT](LICENSE)

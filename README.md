# oc-plugins

Плагины для [Overlay Composer](https://github.com/compico/overlaycomposer) (OC). Каждый плагин — своя папка `plugins/<id>/`
с `plugin.json`, `index.html` и своим `README.md`.

## Плагины

| Плагин | Что делает | Разрешения |
|---|---|---|
| [`fps-meter`](plugins/fps-meter) | FPS и время кадра страницы оверлея (avg/p95/max, пропущенные кадры, график) — чтобы найти виджет, «просаживающий» оверлей в OBS | — |

### Автономные плагины (standalone, wasm)

Работают на сервере OC, без окна и iframe: отвечают на HTTP-запросы, получают события (например, награды Twitch), хранят данные.
Один `.wasm`-файл, исходники — в [`wasm/<id>/`](wasm), готовые файлы — в [Releases](https://github.com/compico/oc-plugins/releases) (в репозитории бинарников нет). В релизе `<id>-v<версия>` четыре файла: `<id>.wasm`, `<id>.wasm.sha256`, `<id>.json` (версия, sha256, размер и метаданные плагина — по ним Overlay Composer показывает каталог) и `<id>.changelog.json` (журнал изменений). К релизам, вышедшим раньше, файл `.json` докладывает CI из опубликованного `.wasm` (сверив его с `.sha256`), без пересборки, а `.changelog.json` — из `wasm/<id>/changelog.json` (записи не новее версии релиза).

Журнал изменений обязателен: `wasm/<id>/changelog.json` в том же формате, что у виджетов (`[{ "version": "1.2.0", "date"?, "changes"?: ["…"], "migration"?: "что сделать после обновления" }]`), в нём должна быть запись для текущей `version` (иначе `build` падает). Пишите в нём всю историю: Overlay Composer при обновлении показывает записи всех пропущенных версий, а `migration` — только если после обновления нужны действия (новое разрешение, переименованная настройка).

| Плагин | Что делает | Разрешения |
|---|---|---|
| [`music`](wasm/music) | заявки на треки Яндекс Музыки за баллы канала: очередь, выдача аддону плеера, выполнение/отмена награды; виджет очереди | `storage`, `twitch.redemption` |
| [`osu-tools`](wasm/osu-tools) | график навыков текущей карты osu! (tosu + расчёт go-osu), виджет, и команда чата `!np` (заменяет `osu-skills` и `osu-np`) | `net.local`, `chat.command.read`, `chat.send` |
| [`chat`](wasm/chat) | чат Twitch: история хранится на сервере, смайлы приходят готовыми картинками; виджет для OBS (заменяет встроенный `twitch-chat`) | `chat.read` |

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
- Логику, не связанную с DOM и сетью, выносите в отдельный файл и тестируйте (пример: `plugins/fps-meter/logic.js` и `test/fps-meter.test.mjs`).
- Автономный wasm-плагин: папка `wasm/<id>/` со своим `go.mod` (id = имя папки = `id` в `oc_meta`), см. [`wasm/music`](wasm/music). Если есть папка `ui/` — это интерфейс виджетов (объявляются в `Meta.Widgets`): при сборке `ocpack` упаковывает его в сам `.wasm` (при первой сборке ставится из overlaycomposer@`OCPACK_REF`, нужен доступ к сети; или задайте `OCPACK=<путь к ocpack>`). Проверка и сборка: `node scripts/wasm.mjs build <id>` (нужны Go 1.24+ и Node.js). Версия API плагинов (`apiVersion` в `oc_meta`) — какая нужна плагину; Go SDK выставляет её сам. OC принимает плагины с версией от 1 до своей (она в настройках: «Plugin API»), более новый не ставится, магазин просит обновить приложение. Новая версия — поднять `version` в коде: после merge в `main` CI выпустит релиз `<id>-v<версия>`; если менялся код, а версия прежняя, CI на PR подскажет.
- Устаревший плагин не удаляют молча, а объявляют в `deprecated.json` в корне репозитория: `[{ "id": "osu-np", "kind": "widget", "since": "2026-11-17", "reason": "почему и что делать", "replacedBy": { "id": "osu-tools", "kind": "wasm", "repo": "compico/oc-plugins", "url": "https://…" } }]`.
  `kind` — `widget` (`plugins/<id>`) или `wasm` (`wasm/<id>`); `since` и `replacedBy` необязательны; `repo` по умолчанию этот же репозиторий, `url` (https) — куда вести за wasm-плагином (OC их не ставит, это страница релизов). Overlay Composer читает файл вместе с каталогом источника
  и показывает пользователю «устарел» и кнопку перехода на замену — и у тех, кто плагин уже установил. Запись остаётся и после удаления папки плагина. Формат проверяется в `npm test` (`scripts/deprecated.mjs`).
- Перед коммитом: `npm test` — проверяет манифесты (`scripts/validate.mjs`) и запускает тесты (нужен только Node.js, зависимостей нет).

## Лицензия

[MIT](LICENSE)

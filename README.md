# oc-plugins

Плагины для [Overlay Composer](https://github.com/compico/overlaycomposer) (OC). Каждый плагин — своя папка `plugins/<id>/`
с `plugin.json`, `index.html` и своим `README.md`.

## Плагины

| Плагин | Что делает | Разрешения |
|---|---|---|
| [`osu-np`](plugins/osu-np) | на `!np` отвечает в чате ссылкой на текущую карту osu! (данные из tosu) | `chat.read`, `chat.send`, `net.local` |
| [`fps-meter`](plugins/fps-meter) | FPS и время кадра страницы оверлея (avg/p95/max, пропущенные кадры, график) — чтобы найти виджет, «просаживающий» оверлей в OBS | — |

## Установка плагина

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
- Перед коммитом: `npm test` — проверяет манифесты (`scripts/validate.mjs`) и запускает тесты (нужен только Node.js, зависимостей нет).

## Лицензия

[MIT](LICENSE)

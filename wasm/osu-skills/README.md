# osu-skills — график навыков текущей карты osu!

Автономный (standalone) wasm-плагин для Overlay Composer. Виджет рисует, как по ходу карты меняется сложность каждого навыка
(stamina, tenacity, agility, precision, reading, memory, accuracy, reaction), и отмечает текущий момент. Это [osuskillsgraph](https://github.com/compico/osuskillsgraph)
и расчёт [go-osu](https://github.com/compico/go-osu) в одном виджете: поднимать go-osu больше не нужно.

## Как работает

1. Хост Overlay Composer сам держит websocket к tosu (`ws://127.0.0.1:24050/websocket/v2`, ретранслятор `net.local streams`) и публикует событие `live`:
   путь к файлу карты (`directPath.beatmapFile`), контрольную сумму, моды (HD, DT, HR, FL…) и время в карте, не чаще 4 раз в секунду. Хост подключается
   только пока виджет подписан на событие, wasm-плагин при этом не вызывается (нагрузка на процессор ничтожна). Напрямую из виджета websocket не работает:
   он в sandbox-iframe с `Origin: null`, и tosu отвечает 403; опрос HTTP из плагина давал 3–4 % CPU.
2. Виджет, получив событие, просит у плагина кривые (`POST /plugins/osu-skills/api/curves`). Плагин забирает файл `.osu` у самого tosu (`http://127.0.0.1:24050/files/beatmap/…`,
   разрешение `net.local`), считает кривые библиотекой [`github.com/compico/go-osu/pkg`](https://github.com/compico/go-osu/tree/master/pkg) и отдаёт их в формате ручки go-osu `/strains`.
3. Виджет рисует график. Пока файл загружается, плагин отвечает 202, и виджет повторяет запрос; карты и результаты кэшируются в памяти плагина.

Разрешения: `net.local` (только адрес tosu: websocket `/websocket/v2` и файлы карт `/files/beatmap/`). В интернет плагин не ходит, ID карты не нужен: работают и локальные карты.

## Установка

1. Скачайте `osu-skills.wasm` из [релизов](https://github.com/compico/oc-plugins/releases) (теги `osu-skills-v<версия>`), положите в папку плагинов OC и нажмите «Rescan».
2. В «Plugin store… → Standalone plugins…» выдайте разрешение `net.local`.
3. Add Source → Plugins → «osu! skills graph» (520×220, прозрачный фон). tosu должен быть запущен, папка Songs указана в настройках tosu.

Если tosu слушает не на `127.0.0.1:24050`, укажите адрес в настройке плагина `tosuHost`; ширину окна точек задаёт параметр `?window=мс` в адресе виджета (по умолчанию 5000).

## Настройки

- **Адрес tosu** (`tosuHost`): `host:port`, только loopback (`127.0.0.1`, `localhost`, `[::1]`). По умолчанию `127.0.0.1:24050`.

## Ограничения

- Файл карты больше 8 МиБ плагин не принимает. Кривые в ответе ограничены 1 МиБ.
- Размер плагина около 5 МБ (рантайм Go и расчёт навыков).
- Требуется версия Overlay Composer с `net.local streams` (overlaycomposer#148).

Навыки считает go-osu: значения совпадают с карточкой карты в go-osu. Кривые на одной шкале (максимум по карте = 100 % высоты).

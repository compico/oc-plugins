// Чистая логика плагина osu-np (без DOM и сети), чтобы её можно было проверять тестами в Node.
// В браузере доступна как глобальный объект NP, в Node — через require().
(function (root, factory) {
  if (typeof module === "object" && module.exports) module.exports = factory();
  else root.NP = factory();
})(typeof self !== "undefined" ? self : this, function () {
  "use strict";

  var MAX_LEN = 450; // лимит чата Twitch — 500; оставляем запас на имя и служебные символы
  var DEDUP_SUFFIX = " \u{E0000}"; // невидимый символ: Twitch не режет повтор того же текста подряд (как делает Chatterino)
  var DEDUP_WINDOW_MS = 35000;

  // Команда — первое слово сообщения (без учёта регистра): «!np», «!NP», «!np что играет?».
  function isCommand(text, command) {
    var cmd = String(command || "").trim().toLowerCase();
    if (!cmd) return false;
    var first = String(text || "").trim().split(/\s+/, 1)[0].toLowerCase();
    return first === cmd;
  }

  // Кто может пользоваться командой. Каждая следующая роль включает предыдущие; стример и модераторы — всегда.
  function allowed(msg, who) {
    var top = msg.isBroadcaster || msg.isMod;
    switch (who) {
      case "mod": return !!top;
      case "vip": return !!(top || msg.isVip);
      case "sub": return !!(top || msg.isVip || msg.isSub);
      default: return true;
    }
  }

  // «{name}» → значение; неизвестные подстановки превращаются в пустую строку.
  function render(template, vars) {
    return String(template).replace(/\{(\w+)\}/g, function (_, k) {
      return Object.prototype.hasOwnProperty.call(vars, k) && vars[k] != null ? String(vars[k]) : "";
    }).replace(/\s+/g, " ").trim();
  }

  // Данные tosu (websocket/v2) → переменные подстановок или признак, что ссылки нет.
  // Возвращает {kind: "ok"|"unsubmitted"|"none", vars}.
  function describe(tosu) {
    var b = tosu && tosu.beatmap;
    if (!b) return { kind: "none", vars: {} };
    var stars = b.stats && b.stats.stars && b.stats.stars.total;
    var vars = {
      artist: b.artist || "", title: b.title || "", version: b.version || "", mapper: b.mapper || "",
      stars: typeof stars === "number" && stars > 0 ? stars.toFixed(2) : "",
      id: b.id > 0 ? b.id : "", set: b.set > 0 ? b.set : "",
      url: b.id > 0 ? "https://osu.ppy.sh/beatmaps/" + b.id : "",
    };
    // id = 0 у карт, которых нет на сайте (локальные, не загруженные)
    return { kind: b.id > 0 ? "ok" : (b.title || b.artist ? "unsubmitted" : "none"), vars: vars };
  }

  // Текст ответа на команду. Возвращает строку (может быть пустой — тогда отвечать нечем).
  function reply(tosu, cfg, user) {
    if (tosu == null) return cfg.offlineText;
    var d = describe(tosu);
    if (d.kind === "none") return cfg.offlineText;
    if (d.kind === "unsubmitted") return render(cfg.unsubmittedText, Object.assign({ user: user }, d.vars));
    var text = render(cfg.template, Object.assign({ user: user }, d.vars));
    return text.length > MAX_LEN ? text.slice(0, MAX_LEN - 1) + "…" : text;
  }

  // Общая пауза между ответами.
  function Cooldown(seconds, now) {
    this.ms = Math.max(0, seconds) * 1000;
    this.now = now || Date.now;
    this.last = -Infinity;
  }
  Cooldown.prototype.ready = function () { return this.now() - this.last >= this.ms; };
  Cooldown.prototype.hit = function () { this.last = this.now(); };

  // Если тот же текст уходит повторно в пределах окна дедупликации Twitch — добавляем невидимый символ.
  function dedupe(text, last, now) {
    if (last && last.text.replace(DEDUP_SUFFIX, "") === text && now - last.at < DEDUP_WINDOW_MS) {
      return last.text.indexOf(DEDUP_SUFFIX) >= 0 ? text : text + DEDUP_SUFFIX;
    }
    return text;
  }

  var HOST_RE = /^[A-Za-z0-9.\-]+(:\d{1,5})?$/;
  function validHost(h) { return HOST_RE.test(String(h)); }

  return { isCommand: isCommand, allowed: allowed, render: render, describe: describe, reply: reply, Cooldown: Cooldown, dedupe: dedupe, validHost: validHost, DEDUP_SUFFIX: DEDUP_SUFFIX };
});

(function () {
  "use strict";
  // Значения по умолчанию совпадают с settings.go (это проверяет тест); редактор передаёт только изменённые настройки в query.
  var Q = new URLSearchParams(location.search);
  function str(k, d) { return Q.has(k) ? Q.get(k) : d; }
  function num(k, d) { var v = parseFloat(Q.get(k)); return isFinite(v) ? v : d; }
  function bool(k, d) { return Q.has(k) ? Q.get(k) === "true" : d; }

  var S = {
    maxMessages: Math.max(1, num("maxMessages", 20)), fadeSeconds: Math.max(0, num("fadeSeconds", 0)),
    direction: str("direction", "bottom"), gap: num("gap", 6),
    hideCommands: bool("hideCommands", true), showEmotes: bool("showEmotes", true),
    ignore: str("ignoreUsers", "nightbot,streamelements,moobot").toLowerCase().split(",").map(function (s) { return s.trim(); }).filter(Boolean),
    msgFont: str("msgFont", "Segoe UI"), msgSize: num("msgSize", 28), msgColor: str("msgColor", "#ffffff"), msgBold: bool("msgBold", false), shadow: bool("shadow", true),
    nickFont: str("nickFont", "Segoe UI"), nickSize: num("nickSize", 28), nickBold: bool("nickBold", true),
    useTwitchColor: bool("useTwitchColor", true), nickColor: str("nickColor", "#9146ff"), separator: str("separator", ": "),
    bgColor: str("bgColor", "#000000"), bgOpacity: num("bgOpacity", 0), padding: num("padding", 6), radius: num("radius", 8),
    googleFonts: bool("googleFonts", false)
  };

  var chat = document.getElementById("chat"), hint = document.getElementById("hint");
  chat.style.justifyContent = S.direction === "top" ? "flex-start" : "flex-end";

  function showHint(t) { if (t) { hint.textContent = t; hint.hidden = false; } else hint.hidden = true; }

  function rgba(hex, a) {
    var h = hex.replace("#", "");
    if (h.length === 3) h = h.split("").map(function (c) { return c + c; }).join("");
    var n = parseInt(h.slice(0, 6), 16);
    return "rgba(" + (n >> 16 & 255) + "," + (n >> 8 & 255) + "," + (n & 255) + "," + a + ")";
  }
  function fontStack(name) { return '"' + name.replace(/["\\]/g, "") + '", system-ui, sans-serif'; }

  if (S.googleFonts) {
    [S.msgFont, S.nickFont].forEach(function (f) {
      var l = document.createElement("link");
      l.rel = "stylesheet";
      l.href = "https://fonts.googleapis.com/css2?family=" + encodeURIComponent(f).replace(/%20/g, "+") + ":wght@400;700&display=swap";
      document.head.appendChild(l);
    });
  }

  var PALETTE = ["#ff4a4a", "#ff8a1f", "#e6c200", "#3ecf5b", "#1fc7c7", "#4aa3ff", "#a56bff", "#ff5fb0"];
  function fallbackColor(name) {
    var h = 0;
    for (var i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
    return PALETTE[h % PALETTE.length];
  }

  function hasMessage(id) {
    return !!id && !!chat.querySelector('[data-id="' + String(id).replace(/["\\]/g, "") + '"]');
  }

  // Сообщение приходит из плагина уже нарезанным: parts — текст или смайл со ссылкой на картинку. Позиции считать не нужно.
  // Смайлы выводятся только картинками по ссылке из parts; всё остальное — текстом (textContent), HTML из сообщений не строится.
  function accept(m, replay) {
    var login = String(m.login || "").toLowerCase();
    if (S.ignore.indexOf(login) >= 0) return;
    if (S.hideCommands && String(m.text || "")[0] === "!") return;
    if (hasMessage(m.id)) return;
    // История открывается задним числом: сообщения старше времени жизни показывать уже не нужно.
    if (replay && S.fadeSeconds > 0 && m.timestamp && Date.now() - m.timestamp > S.fadeSeconds * 1000) return;
    addMessage(m, login);
  }
  function removeById(id) {
    var m = chat.querySelector('[data-id="' + String(id || "").replace(/["\\]/g, "") + '"]'); if (m) m.remove();
  }
  function clearChat(login) {
    if (!login) chat.replaceChildren();
    else chat.querySelectorAll(".msg").forEach(function (e) { if (e.dataset.login === login.toLowerCase()) e.remove(); });
  }

  function safeURL(u) { return /^https:\/\/static-cdn\.jtvnw\.net\/emoticons\/v2\/[A-Za-z0-9_]+\/[a-z]+\/[a-z]+\/[0-9.]+$/.test(String(u || "")); }

  function addMessage(m, login) {
    var el = document.createElement("div");
    el.className = "msg" + (m.action ? " action" : "");
    if (m.id) el.dataset.id = m.id;
    el.dataset.login = login;
    el.style.marginTop = S.direction === "bottom" ? S.gap + "px" : "0";
    el.style.marginBottom = S.direction === "top" ? S.gap + "px" : "0";
    el.style.padding = S.padding + "px";
    el.style.borderRadius = S.radius + "px";
    el.style.background = S.bgOpacity > 0 ? rgba(S.bgColor, S.bgOpacity / 100) : "transparent";
    el.style.fontFamily = fontStack(S.msgFont);
    el.style.fontSize = S.msgSize + "px";
    el.style.color = S.msgColor;
    el.style.fontWeight = S.msgBold ? "700" : "400";
    if (S.shadow) el.style.textShadow = "0 1px 3px rgba(0,0,0,.85), 0 0 2px rgba(0,0,0,.8)";

    var n = document.createElement("span");
    n.className = "nick";
    n.textContent = (m.displayName || login) + S.separator;
    n.style.fontFamily = fontStack(S.nickFont);
    n.style.fontSize = S.nickSize + "px";
    n.style.fontWeight = S.nickBold ? "700" : "400";
    n.style.color = S.useTwitchColor ? (/^#[0-9a-f]{6}$/i.test(m.color || "") ? m.color : fallbackColor(login)) : S.nickColor;

    var t = document.createElement("span");
    t.className = "text";
    var parts = m.parts && m.parts.length ? m.parts : [{ text: m.text || "" }];
    parts.forEach(function (p) {
      if (p.emote) {
        if (S.showEmotes && safeURL(p.url)) {
          var img = document.createElement("img");
          img.className = "emote"; img.alt = p.name || ""; img.title = p.name || "";
          img.src = p.url;
          t.appendChild(img);
        } else t.appendChild(document.createTextNode(p.name || ""));
      } else t.appendChild(document.createTextNode(p.text || ""));
    });
    if (m.action) t.style.color = n.style.color;

    el.append(n, t);
    if (S.direction === "top") chat.insertBefore(el, chat.firstChild); else chat.appendChild(el);
    trim();
    if (S.fadeSeconds > 0) {
      setTimeout(function () { el.classList.add("fade"); setTimeout(function () { el.remove(); }, 700); }, S.fadeSeconds * 1000);
    }
  }

  // Убираем самые старые сообщения: по количеству и пока всё не помещается по высоте.
  function trim() {
    var old = function () { return S.direction === "top" ? chat.lastElementChild : chat.firstElementChild; };
    while (chat.children.length > S.maxMessages) old().remove();
    while (chat.scrollHeight > chat.clientHeight + 1 && chat.children.length > 1) old().remove();
  }

  function status(s) {
    if (s && s.connected === false) showHint("Chat is not connected" + (s.error ? ": " + s.error : ". Connect the Streamer account in Overlay Composer"));
    else showHint("");
  }

  // События плагина: {event: message|delete|clear|status, ...} — тот же формат, что присылает OC.
  function apply(ev, replay) {
    if (!ev) return;
    if (ev.event === "message" && ev.message) accept(ev.message, replay);
    else if (ev.event === "delete") { if (ev.targetId) removeById(ev.targetId); else if (ev.login) clearChat(ev.login); }
    else if (ev.event === "clear") clearChat(ev.login);
    else if (ev.event === "status") status(ev);
  }

  if (typeof oc === "undefined" || !oc.hosted) { showHint("Open this widget through Overlay Composer"); return; }

  // Сначала подписка, потом история: события, пришедшие пока история загружается, ждут в очереди и не теряются; повторы отсекает hasMessage.
  var ready = false, queue = [];
  oc.ready.then(function (info) {
    // В окне Dashboard вид свой: читаемо и без затухания. Явно заданные настройки (query) не перебиваются.
    if (info && info.surface === "dashboard") {
      var dash = { maxMessages: 200, fadeSeconds: 0, msgSize: 16, nickSize: 16, padding: 4, gap: 4, msgColor: "#e8e8ec", shadow: false };
      Object.keys(dash).forEach(function (k) { if (!Q.has(k)) S[k] = dash[k]; });
    }
    return oc.plugin.subscribe("chat", function (ev) { if (ready) apply(ev, false); else queue.push(ev); });
  }).then(function () {
    return oc.plugin.get("/messages");
  }).then(function (r) {
    var body = r.body || {};
    (body.messages || []).forEach(function (m) { accept(m, true); });
    status(body.status);
    ready = true;
    queue.forEach(function (ev) { apply(ev, false); });
    queue = [];
  }).catch(function (e) {
    showHint("Chat plugin error: " + (e && e.message ? e.message : e));
  });
})();

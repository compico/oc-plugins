import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { classifyChanges, onlyExisting, checkMeta, checkWidgets, listUI, tagName } from "../scripts/wasm.mjs";

test("classifyChanges: какие плагины затронуты и менялся ли код", () => {
  const r = classifyChanges(["wasm/music/main.go", "wasm/music/README.md", "wasm/other/queue_test.go", "plugins/osu-np/index.html", "README.md", "wasm/.keep"]);
  assert.deepEqual(r.changed, ["music", "other"]);
  assert.deepEqual(r.code, ["music"]); // other: только тест — выпускать новую версию не нужно
});

test("classifyChanges: go.mod и go.sum — это код", () => {
  assert.deepEqual(classifyChanges(["wasm/music/go.sum"]).code, ["music"]);
});

test("onlyExisting: удалённый или переименованный плагин не собирается", () => {
  const r = classifyChanges(["wasm/osu-skills/main.go", "wasm/osu-tools/main.go", "wasm/osu-tools/README.md"]);
  assert.deepEqual(r.code, ["osu-skills", "osu-tools"]);
  assert.deepEqual(onlyExisting(r, (id) => id === "osu-tools"), { changed: ["osu-tools"], code: ["osu-tools"] });
});

test("tagName", () => assert.equal(tagName("music", "0.1.0"), "music-v0.1.0"));

const good = { id: "music", name: "Music", version: "0.1.0", apiVersion: 1, permissions: ["storage", "twitch.redemption"], subscriptions: ["twitch.redemption"] };

test("checkMeta: корректные метаданные", () => assert.deepEqual(checkMeta("music", good), []));

test("checkMeta: разрешение net.local известно", () => assert.deepEqual(checkMeta("music", { ...good, permissions: [...good.permissions, "net.local", "chat.command.read"] }), []));

test("checkMeta: команды чата", () => {
  const cmd = { ...good, permissions: ["chat.command.read"], subscriptions: ["chat.command"], commands: ["np"] };
  assert.deepEqual(checkMeta("music", cmd), []);
  assert.match(checkMeta("music", { ...cmd, permissions: [] }).join("|"), /требует разрешения chat.command.read/);
  assert.match(checkMeta("music", { ...cmd, commands: [] }).join("|"), /непустого commands/);
});

test("checkMeta: действия Deck", () => {
  const withActions = { ...good, routes: [{ method: "POST", path: "/control", handler: "h" }], actions: [{ id: "toggle", title: "Play", path: "/control", body: { action: "toggle" } }] };
  assert.deepEqual(checkMeta("music", withActions), []);
  const bad = (a) => checkMeta("music", { ...withActions, actions: [{ ...withActions.actions[0], ...a }] }).join("|");
  assert.match(bad({ id: "Bad" }), /id a-z0-9/);
  assert.match(bad({ title: " " }), /title/);
  assert.match(bad({ path: "/nope" }), /не объявлен в routes/);
  assert.match(bad({ method: "GET" }), /не объявлен в routes/);
  assert.match(bad({ body: "x".repeat(1100) }), /больше 1 КиБ/);
  assert.match(checkMeta("music", { ...withActions, actions: [withActions.actions[0], withActions.actions[0]] }).join("|"), /уникален/);
});

test("checkMeta: ошибки (sad path)", () => {
  const bad = (patch, re) => assert.match(checkMeta("music", { ...good, ...patch }).join("|"), re);
  bad({ id: "other" }, /не совпадает с именем папки/);
  bad({ id: "Music" }, /не совпадает|a-z/);
  bad({ version: "1.0" }, /semver/);
  bad({ apiVersion: 2 }, /apiVersion/);
  bad({ name: " " }, /name обязателен/);
  bad({ permissions: ["root"] }, /неизвестное разрешение/);
  bad({ subscriptions: ["x"] }, /неизвестная подписка/);
  bad({ permissions: ["storage"] }, /требует разрешения/);
});

test("classifyChanges: правка интерфейса (ui/) — это код, нужна новая версия", () => {
  assert.deepEqual(classifyChanges(["wasm/music/ui/index.html"]).code, ["music"]);
});

test("listUI: вложенные файлы через «/», скрытые пропускаются, нет папки — null", () => {
  const dir = mkdtempSync(join(tmpdir(), "ui-"));
  mkdirSync(join(dir, "js"));
  mkdirSync(join(dir, ".git"));
  writeFileSync(join(dir, "index.html"), "x");
  writeFileSync(join(dir, "js", "app.js"), "x");
  writeFileSync(join(dir, ".DS_Store"), "x");
  writeFileSync(join(dir, ".git", "config"), "x");
  assert.deepEqual([...listUI(dir)].sort(), ["index.html", "js/app.js"]);
  assert.equal(listUI(join(dir, "missing")), null);
});

test("checkWidgets: согласованность виджетов и папки ui", () => {
  const w = { widgets: [{ id: "queue", name: "Q", entry: "index.html" }] };
  assert.deepEqual(checkWidgets(w, new Set(["index.html"])), []);
  assert.deepEqual(checkWidgets({}, null), []); // плагин без интерфейса
  assert.match(checkWidgets(w, null).join("|"), /папки ui\/ нет/);
  assert.match(checkWidgets({}, new Set(["index.html"])).join("|"), /виджеты в oc_meta не объявлены/);
  assert.match(checkWidgets(w, new Set(["other.html"])).join("|"), /ui\/index\.html нет/);
});

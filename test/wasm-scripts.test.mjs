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

test("checkMeta: minHost — версия релиза OC", () => {
  assert.deepEqual(checkMeta("music", { ...good, minHost: "v2026.11.25.7" }), []);
  for (const bad of ["2026.11.25.7", "v2026.11", "latest", ""]) assert.match(checkMeta("music", { ...good, minHost: bad }).join("|"), /minHost/);
});

test("checkMeta: команды чата", () => {
  const cmd = { ...good, permissions: ["chat.command.read"], subscriptions: ["chat.command"], commands: ["np"] };
  assert.deepEqual(checkMeta("music", cmd), []);
  assert.match(checkMeta("music", { ...cmd, permissions: [] }).join("|"), /требует разрешения chat.command.read/);
  assert.match(checkMeta("music", { ...cmd, commands: [] }).join("|"), /непустого commands/);
});

test("checkMeta: весь чат", () => {
  const all = { ...good, permissions: ["chat.read"], subscriptions: ["chat.message"] };
  assert.deepEqual(checkMeta("music", all), []);
  assert.match(checkMeta("music", { ...all, permissions: [] }).join("|"), /требует разрешения chat.read/);
  // chat.read и chat.command.read — независимые права
  assert.match(checkMeta("music", { ...all, permissions: ["chat.command.read"] }).join("|"), /требует разрешения chat.read/);
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
  const withEvents = { ...withActions, events: [{ id: "now" }] };
  const aw = (w) => checkMeta("music", { ...withEvents, actions: [{ ...withActions.actions[0], activeWhen: w }] }).join("|");
  assert.equal(aw({ topic: "now", field: "now.playing", equals: true }), "");
  assert.match(aw({ topic: "other" }), /activeWhen\.topic/);
  assert.match(aw({ topic: "now", field: "a b" }), /activeWhen\.field/);
  assert.match(aw({ topic: "now", equals: { a: 1 } }), /activeWhen\.equals/);
  const dp = (d) => checkMeta("music", { ...withEvents, actions: [{ ...withActions.actions[0], display: d }] }).join("|");
  assert.equal(dp({ topic: "now", fields: ["now.title", "now.artists"] }), "");
  assert.match(dp({ topic: "other", fields: ["a"] }), /display\.topic/);
  assert.match(dp({ topic: "now", fields: [] }), /display\.fields/);
  assert.match(dp({ topic: "now", fields: ["a", "b", "c", "d", "e"] }), /display\.fields/);
  assert.match(dp({ topic: "now", fields: ["a b"] }), /display\.fields/);
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

test("infoFromPublished: метаданные опубликованного релиза и проверки совпадения", async () => {
  const { infoFromPublished } = await import("../scripts/wasm.mjs");
  const { createHash } = await import("node:crypto");
  const bytes = Buffer.from("wasm-bytes");
  const sha = createHash("sha256").update(bytes).digest("hex");
  const meta = { id: "music", version: "0.8.1", name: "Music", minHost: "v2026.11.25.7" }; // minHost доезжает до <id>.json
  const info = infoFromPublished({ id: "music", version: "0.8.1", bytes, meta, publishedSha: sha });
  assert.deepEqual(info, { id: "music", version: "0.8.1", sha256: sha, size: bytes.length, meta });
  assert.throws(() => infoFromPublished({ id: "music", version: "0.8.1", bytes, meta, publishedSha: "0".repeat(64) }), /не совпадает с опубликованным/);
  assert.throws(() => infoFromPublished({ id: "music", version: "0.9.0", bytes, meta, publishedSha: sha }), /версия «0\.8\.1»/);
  assert.throws(() => infoFromPublished({ id: "other", version: "0.8.1", bytes, meta, publishedSha: sha }), /id «music»/);
});

test("classifyChanges: changelog.json не считается изменением кода", () => {
  const r = classifyChanges(["wasm/music/changelog.json", "wasm/osu-tools/changelog.json", "wasm/osu-tools/main.go"]);
  assert.deepEqual(r.changed, ["music", "osu-tools"]);
  assert.deepEqual(r.code, ["osu-tools"]);
});

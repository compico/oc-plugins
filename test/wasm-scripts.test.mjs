import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { classifyChanges, checkMeta, checkWidgets, listUI, tagName } from "../scripts/wasm.mjs";

test("classifyChanges: какие плагины затронуты и менялся ли код", () => {
  const r = classifyChanges(["wasm/music/main.go", "wasm/music/README.md", "wasm/other/queue_test.go", "plugins/osu-np/index.html", "README.md", "wasm/.keep"]);
  assert.deepEqual(r.changed, ["music", "other"]);
  assert.deepEqual(r.code, ["music"]); // other: только тест — выпускать новую версию не нужно
});

test("classifyChanges: go.mod и go.sum — это код", () => {
  assert.deepEqual(classifyChanges(["wasm/music/go.sum"]).code, ["music"]);
});

test("tagName", () => assert.equal(tagName("music", "0.1.0"), "music-v0.1.0"));

const good = { id: "music", name: "Music", version: "0.1.0", apiVersion: 1, permissions: ["storage", "twitch.redemption"], subscriptions: ["twitch.redemption"] };

test("checkMeta: корректные метаданные", () => assert.deepEqual(checkMeta("music", good), []));

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

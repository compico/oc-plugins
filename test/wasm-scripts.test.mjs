import { test } from "node:test";
import assert from "node:assert/strict";
import { classifyChanges, checkMeta, tagName } from "../scripts/wasm.mjs";

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

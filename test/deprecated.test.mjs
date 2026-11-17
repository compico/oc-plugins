import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { checkDeprecated } from "../scripts/deprecated.mjs";

const have = new Set(["widget/osu-np", "wasm/osu-tools"]);
const exists = (kind, id) => have.has(`${kind}/${id}`);
const good = { id: "osu-np", kind: "widget", since: "2026-11-17", reason: "Заменён.", replacedBy: { id: "osu-tools", kind: "wasm" } };

test("корректная запись и запись без замены", () => {
  assert.deepEqual(checkDeprecated([good, { id: "gone", kind: "wasm", reason: "Больше не нужен." }], exists), []);
  assert.deepEqual(checkDeprecated([], exists), []);
});

test("удалённый плагин остаётся в списке", () => {
  assert.deepEqual(checkDeprecated([{ ...good, id: "removed-long-ago" }], exists), []);
});

test("ошибки формата", () => {
  const bad = (patch) => checkDeprecated([{ ...good, ...patch }], exists).join("|");
  assert.match(checkDeprecated({}, exists).join("|"), /массивом/);
  assert.match(bad({ id: "a b" }), /id:/);
  assert.match(bad({ kind: "plugin" }), /kind:/);
  assert.match(bad({ reason: " " }), /нужна reason/);
  assert.match(bad({ reason: "я".repeat(501) }), /длиннее 500/);
  assert.match(bad({ since: "17.11.2026" }), /since/);
  assert.match(bad({ since: "2026-13-45" }), /since/);
  assert.match(bad({ kind: "wasm" }), /в репозитории — widget, а не wasm/);
  assert.match(bad({ replacedBy: "osu-tools" }), /объектом/);
  assert.match(bad({ replacedBy: { id: "osu-tools", kind: "x" } }), /replacedBy: kind/);
  assert.match(bad({ replacedBy: { id: "osu-tools", kind: "wasm", repo: "no-slash" } }), /owner\/repo/);
  assert.match(bad({ replacedBy: { id: "osu-tools", kind: "wasm", url: "http://x.test/" } }), /https/);
  assert.match(bad({ replacedBy: { id: "osu-tools", kind: "wasm", url: "не url" } }), /url/);
  assert.match(bad({ replacedBy: { id: "osu-np", kind: "widget" } }), /сам себя/);
  assert.match(bad({ replacedBy: { id: "nowhere", kind: "wasm" } }), /нет wasm-плагина «nowhere»/);
});

test("замена из другого репозитория не проверяется на месте", () => {
  assert.deepEqual(checkDeprecated([{ ...good, replacedBy: { id: "nowhere", kind: "wasm", repo: "someone/else" } }], exists), []);
  assert.deepEqual(checkDeprecated([{ ...good, replacedBy: { id: "osu-tools", kind: "wasm", repo: "compico/oc-plugins" } }], exists), []);
});

test("дубли", () => {
  assert.match(checkDeprecated([good, good], exists).join("|"), /повторяется/);
});

test("deprecated.json репозитория: osu-np и osu-skills заменены osu-tools", () => {
  const list = JSON.parse(readFileSync(new URL("../deprecated.json", import.meta.url), "utf8"));
  const by = Object.fromEntries(list.map((d) => [d.id, d]));
  assert.equal(by["osu-np"].replacedBy.id, "osu-tools");
  assert.equal(by["osu-skills"].replacedBy.id, "osu-tools");
});

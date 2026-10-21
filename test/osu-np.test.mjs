import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const NP = createRequire(import.meta.url)("../plugins/osu-np/logic.js");

const cfg = {
  template: "{artist} - {title} [{version}] {url}",
  unsubmittedText: "not on osu ({user})",
  offlineText: "tosu is off",
};
const map = (over = {}) => ({
  state: { number: 2, name: "Play" },
  beatmap: { id: 1234567, set: 555, artist: "Camellia", title: "Exit This Earth's Atomosphere", version: "Evolution", mapper: "Mat", stats: { stars: { total: 7.456 } }, ...over },
});

test("command matches the first word, case-insensitive", () => {
  assert.equal(NP.isCommand("!np", "!np"), true);
  assert.equal(NP.isCommand("  !NP  what is it?", "!np"), true);
  assert.equal(NP.isCommand("!npx", "!np"), false);
  assert.equal(NP.isCommand("hello !np", "!np"), false);
  assert.equal(NP.isCommand("anything", ""), false);
});

test("who can use the command", () => {
  const viewer = {}, sub = { isSub: true }, vip = { isVip: true }, mod = { isMod: true }, owner = { isBroadcaster: true };
  for (const m of [viewer, sub, vip, mod, owner]) assert.equal(NP.allowed(m, "everyone"), true);
  assert.deepEqual([viewer, sub, vip, mod, owner].map((m) => NP.allowed(m, "sub")), [false, true, true, true, true]);
  assert.deepEqual([viewer, sub, vip, mod, owner].map((m) => NP.allowed(m, "vip")), [false, false, true, true, true]);
  assert.deepEqual([viewer, sub, vip, mod, owner].map((m) => NP.allowed(m, "mod")), [false, false, false, true, true]);
});

test("reply contains the short beatmap link", () => {
  assert.equal(NP.reply(map(), cfg, "Viewer"), "Camellia - Exit This Earth's Atomosphere [Evolution] https://osu.ppy.sh/beatmaps/1234567");
});

test("template placeholders, unknown ones are dropped", () => {
  const t = NP.reply(map(), { ...cfg, template: "@{user}: {mapper} {stars}★ set {set} {nope} {url}" }, "Viewer");
  assert.equal(t, "@Viewer: Mat 7.46★ set 555 https://osu.ppy.sh/beatmaps/1234567");
});

test("unsubmitted / no map / tosu offline", () => {
  assert.equal(NP.reply(map({ id: 0, set: -1 }), cfg, "Bob"), "not on osu (Bob)");
  assert.equal(NP.reply({ beatmap: { id: 0, set: -1, artist: "", title: "" } }, cfg, "Bob"), "tosu is off");
  assert.equal(NP.reply(null, cfg, "Bob"), "tosu is off");
  assert.equal(NP.reply({}, cfg, "Bob"), "tosu is off");
});

test("very long titles are cut to fit the chat limit", () => {
  const t = NP.reply(map({ title: "x".repeat(1000) }), cfg, "u");
  assert.ok(t.length <= 450);
  assert.ok(t.endsWith("…"));
});

test("cooldown", () => {
  let now = 1000;
  const c = new NP.Cooldown(10, () => now);
  assert.equal(c.ready(), true);
  c.hit();
  now += 9999;
  assert.equal(c.ready(), false);
  now += 1;
  assert.equal(c.ready(), true);
  assert.equal(new NP.Cooldown(0, () => now).ready(), true);
});

test("a repeated identical text gets an invisible suffix only inside Twitch's duplicate window", () => {
  assert.equal(NP.dedupe("a", null, 0), "a");
  const first = { text: "a", at: 1000 };
  assert.equal(NP.dedupe("a", first, 2000), "a" + NP.DEDUP_SUFFIX);
  assert.equal(NP.dedupe("b", first, 2000), "b");
  assert.equal(NP.dedupe("a", first, 1000 + 36000), "a"); // окно прошло
  // третий повтор подряд снова чистый текст (чередуем, чтобы не совпасть с предыдущим)
  const second = { text: "a" + NP.DEDUP_SUFFIX, at: 2000 };
  assert.equal(NP.dedupe("a", second, 3000), "a");
});

test("host validation", () => {
  assert.equal(NP.validHost("127.0.0.1:24050"), true);
  assert.equal(NP.validHost("localhost:24050"), true);
  assert.equal(NP.validHost("pc.lan"), true);
  assert.equal(NP.validHost("evil.example/path?x=1"), false);
  assert.equal(NP.validHost("a b"), false);
  assert.equal(NP.validHost(""), false);
});

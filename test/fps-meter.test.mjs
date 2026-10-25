import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const FM = createRequire(import.meta.url)("../plugins/fps-meter/logic.js");

function run(frames, deltaOf, windowMs = 5000) {
  const f = new FM.Frames(windowMs);
  let t = 1000;
  f.push(t);
  for (let i = 0; i < frames; i++) { t += deltaOf(i); f.push(t); }
  return { f, now: t };
}

test("steady 60 fps", () => {
  const { f, now } = run(300, () => 1000 / 60);
  const s = f.snapshot(now, 0);
  assert.equal(s.target, 60);
  assert.ok(Math.abs(s.fps - 60) < 1.5, `fps ${s.fps}`);
  assert.ok(Math.abs(s.avg - 16.667) < 0.01);
  assert.equal(s.slow, 0);
  assert.equal(s.missed, 0);
  assert.equal(s.droppedPct, 0);
});

test("spikes are counted as slow frames and missed frames", () => {
  // каждый 10-й кадр длится 50 мс (≈ 3 кадра при 60 fps → 2 пропущено)
  const { f, now } = run(200, (i) => (i % 10 === 9 ? 50 : 1000 / 60 - 0));
  const s = f.snapshot(now, 60);
  assert.equal(s.slow, 20);
  assert.equal(s.missed, 20 * 2);
  assert.ok(s.max >= 49.9 && s.max <= 50.1);
  assert.ok(s.p99 >= 49.9);
  assert.ok(s.p95 < 20 || s.p95 >= 49.9); // 10% кадров медленные: p95 попадает в «хвост»
  assert.ok(s.droppedPct > 10 && s.droppedPct < 20, `dropped ${s.droppedPct}`);
});

test("auto target picks the nearest standard rate (30 fps source, 144 Hz)", () => {
  assert.equal(FM.detectTarget(1000 / 30), 30);
  assert.equal(FM.detectTarget(1000 / 143), 144);
  assert.equal(FM.detectTarget(1000 / 59), 60);
  assert.equal(FM.detectTarget(0), 60);
  const { f, now } = run(120, () => 1000 / 30);
  assert.equal(f.snapshot(now, 0).target, 30);
  assert.equal(f.snapshot(now, 0).slow, 0);
});

test("window drops old frames", () => {
  const f = new FM.Frames(1000);
  for (let t = 0; t <= 5000; t += 20) f.push(t);
  const s = f.snapshot(5000, 60);
  assert.ok(s.n <= 51 && s.n >= 49, `n ${s.n}`);
});

test("empty and single-frame snapshots do not throw", () => {
  const f = new FM.Frames(1000);
  assert.equal(f.snapshot(0, 0).n, 0);
  f.push(10);
  assert.equal(f.snapshot(10, 0).n, 0);
  assert.equal(f.snapshot(10, 144).target, 144);
});

test("resetGap ignores the pause when the page was hidden", () => {
  const f = new FM.Frames(60000);
  f.push(0); f.push(16); f.resetGap(); f.push(10000); f.push(10016);
  const s = f.snapshot(10016, 60);
  assert.equal(s.n, 2);
  assert.ok(s.max < 20);
});

test("bins: the worst frame of each interval, oldest on the left", () => {
  const f = new FM.Frames(1000);
  for (let t = 0; t <= 1000; t += 10) f.push(t);
  f.push(1100); // 100 мс кадр
  const b = f.bins(1100, 10);
  assert.equal(b.length, 10);
  assert.ok(b[9] >= 99.9, JSON.stringify(b));
  assert.ok(b.slice(0, 8).every((x) => x === 0 || x <= 10.1));
});

test("lag: max lateness of the timer in the window", () => {
  const l = new FM.Lag(1000, 50);
  l.tick(0); l.tick(50); l.tick(100); l.tick(190); l.tick(240);
  assert.equal(l.max(240), 40);
  assert.equal(l.max(5000), 0);
});

test("percentile and level", () => {
  assert.equal(FM.percentile([], 95), 0);
  assert.equal(FM.percentile([1, 2, 3, 4], 50), 2);
  assert.equal(FM.percentile([1, 2, 3, 4], 100), 4);
  assert.equal(FM.level(16.7, 60), "ok");
  assert.equal(FM.level(25, 60), "warn");
  assert.equal(FM.level(40, 60), "bad");
});

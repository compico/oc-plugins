// Чистая логика fps-meter (без DOM), чтобы её можно было проверять тестами в Node.
// В браузере доступна как глобальный объект FM, в Node — через require().
(function (root, factory) {
  if (typeof module === "object" && module.exports) module.exports = factory();
  else root.FM = factory();
})(typeof self !== "undefined" ? self : this, function () {
  "use strict";

  var STANDARD_FPS = [24, 30, 48, 50, 60, 72, 75, 90, 100, 120, 144, 165, 240];

  // Значения с отметкой времени за последние windowMs миллисекунд.
  function Series(windowMs) {
    this.windowMs = windowMs;
    this.t = [];
    this.v = [];
  }
  Series.prototype.push = function (t, v) {
    this.t.push(t);
    this.v.push(v);
    this.prune(t);
  };
  Series.prototype.prune = function (now) {
    var cut = now - this.windowMs, i = 0;
    while (i < this.t.length && this.t[i] < cut) i++;
    if (i > 0) { this.t.splice(0, i); this.v.splice(0, i); }
  };
  Series.prototype.size = function () { return this.v.length; };

  function percentile(sorted, p) {
    if (!sorted.length) return 0;
    var i = Math.min(sorted.length - 1, Math.max(0, Math.ceil((p / 100) * sorted.length) - 1));
    return sorted[i];
  }

  // Ближайшая «стандартная» частота кадров к измеренной медиане (для режима target = auto).
  function detectTarget(medianMs) {
    if (!(medianMs > 0)) return 60;
    var fps = 1000 / medianMs, best = STANDARD_FPS[0];
    for (var i = 0; i < STANDARD_FPS.length; i++) {
      if (Math.abs(STANDARD_FPS[i] - fps) < Math.abs(best - fps)) best = STANDARD_FPS[i];
    }
    return best;
  }

  // Время кадров из requestAnimationFrame: now — время кадра (performance.now()).
  function Frames(windowMs) {
    this.deltas = new Series(windowMs);
    this.last = null;
  }
  Frames.prototype.push = function (now) {
    if (this.last !== null) this.deltas.push(now, now - this.last);
    this.last = now;
  };
  // Страница была скрыта/остановлена: не считаем паузу «провалом».
  Frames.prototype.resetGap = function () { this.last = null; };

  // targetFps: 0/"auto" — определить по медиане окна. Возвращает сводку для показа.
  Frames.prototype.snapshot = function (now, targetFps) {
    this.deltas.prune(now);
    var d = this.deltas.v, n = d.length;
    if (n === 0) return { n: 0, fps: 0, avg: 0, p95: 0, p99: 0, max: 0, slow: 0, missed: 0, droppedPct: 0, target: targetFps > 0 ? targetFps : 60 };
    var sorted = d.slice().sort(function (a, b) { return a - b; });
    var sum = 0, i;
    for (i = 0; i < n; i++) sum += d[i];
    var target = targetFps > 0 ? targetFps : detectTarget(percentile(sorted, 50));
    var T = 1000 / target, slow = 0, missed = 0;
    for (i = 0; i < n; i++) {
      if (d[i] > T * 1.5) slow++;
      var m = Math.round(d[i] / T) - 1;
      if (m > 0) missed += m;
    }
    // fps — по кадрам за последнюю секунду
    var cut = now - 1000, k = this.deltas.t.length - 1, cnt = 0;
    while (k >= 0 && this.deltas.t[k] >= cut) { cnt++; k--; }
    var span = Math.min(1000, now - this.deltas.t[0]) || 1000;
    return {
      n: n,
      fps: cnt * (1000 / span),
      avg: sum / n,
      p95: percentile(sorted, 95),
      p99: percentile(sorted, 99),
      max: sorted[n - 1],
      slow: slow,
      missed: missed,
      droppedPct: (missed / (n + missed)) * 100,
      target: target,
    };
  };
  // Для графика: на каждый из bins столбцов — наибольшее время кадра в его интервале (0, если кадров не было).
  Frames.prototype.bins = function (now, bins) {
    this.deltas.prune(now);
    var out = new Array(bins), w = this.deltas.windowMs, i;
    for (i = 0; i < bins; i++) out[i] = 0;
    var t = this.deltas.t, v = this.deltas.v;
    for (i = 0; i < t.length; i++) {
      var pos = Math.floor(((t[i] - (now - w)) / w) * bins);
      if (pos === bins) pos = bins - 1; // кадр ровно в момент now
      if (pos >= 0 && pos < bins && v[i] > out[pos]) out[pos] = v[i];
    }
    return out;
  };

  // Задержка таймера (setInterval): насколько он опоздал; отражает занятость потока самого плагина.
  function Lag(windowMs, periodMs) {
    this.s = new Series(windowMs);
    this.period = periodMs;
    this.last = null;
  }
  Lag.prototype.tick = function (now) {
    if (this.last !== null) this.s.push(now, Math.max(0, now - this.last - this.period));
    this.last = now;
  };
  Lag.prototype.max = function (now) {
    this.s.prune(now);
    var m = 0;
    for (var i = 0; i < this.s.v.length; i++) if (this.s.v[i] > m) m = this.s.v[i];
    return m;
  };

  // Цвет по времени кадра относительно цели: до 1.2×T — зелёный, до 2×T — жёлтый, дальше — красный.
  function level(ms, targetFps) {
    var T = 1000 / (targetFps > 0 ? targetFps : 60);
    return ms <= T * 1.2 ? "ok" : ms <= T * 2 ? "warn" : "bad";
  }

  return { Series: Series, Frames: Frames, Lag: Lag, percentile: percentile, detectTarget: detectTarget, level: level };
});

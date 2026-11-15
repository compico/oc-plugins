// Чистая логика графика (без DOM): подготовка кривых, сглаживание, прозрачность. Обычный скрипт, не модуль: из sandbox-iframe
// виджета модули не загрузить (запрос идёт с Origin: null). Те же функции подключаются в тестах через require.
(function (root) {
// Pure logic of the widget: no DOM, no network, so it can be tested with
// `node --test`.

/** Skills in drawing order with their colours. */
const SKILLS = [
  { key: 'stamina', label: 'Stamina', color: '#ef5350' },
  { key: 'tenacity', label: 'Tenacity', color: '#ff9800' },
  { key: 'agility', label: 'Agility', color: '#fdd835' },
  { key: 'precision', label: 'Precision', color: '#9ccc65' },
  { key: 'reading', label: 'Reading', color: '#26c6da' },
  { key: 'memory', label: 'Memory', color: '#7986cb', cumulative: true },
  { key: 'accuracy', label: 'Accuracy', color: '#ab47bc' },
  { key: 'reaction', label: 'Reaction', color: '#b0bec5' },
]

/** Opacity of a line that is far below its peak / of a cumulative line. */
const ALPHA_MIN = 0.18
const ALPHA_CUMULATIVE = 0.5

// Mods go-osu's parser knows (pkg/osu/mods).
const KNOWN_MODS = new Set(['NF', 'EZ', 'HD', 'HR', 'SD', 'DT', 'RL', 'HT', 'FL', 'AU', 'SO', 'AP'])
// tosu names some mods differently from go-osu: Nightcore is a DT, Perfect is a SD.
const MOD_ALIASES = { NC: 'DT', PF: 'SD' }

/**
 * Turns the mods string tosu reports ("HDDT", "NC", "NM", "HDCL") into the
 * string go-osu accepts: aliases resolved, mods go-osu does not know (they do
 * not change the difficulty) dropped, duplicates removed. "" means no mods.
 */
function normalizeMods(name) {
  if (typeof name !== 'string') return ''

  const s = name.trim().toUpperCase()
  const out = []
  for (let i = 0; i + 1 < s.length; i += 2) {
    let code = s.slice(i, i + 2)
    code = MOD_ALIASES[code] ?? code
    if (KNOWN_MODS.has(code) && !out.includes(code)) out.push(code)
  }
  return out.join('')
}

/**
 * Prepares a /strains response for drawing: every skill's points on the
 * beatmap's own timeline (time × speed, which is the timeline tosu reports).
 *
 * The height is shared by all skills: 1 is the highest value any skill reaches
 * on this map (accuracy at 1000 while the others peak lower: 1000 is the full
 * height for all of them), so the lines stay comparable and a skill that is
 * hardly there stays a low line. `top` is a skill's own peak on that scale (1
 * for the strongest skill), which is what its line fades against. Skills
 * without a curve (or with a curve of zeros) are left out.
 */
function prepareSkills(response) {
  const speed = response?.speed > 0 ? response.speed : 1

  const found = []
  let highest = 0
  for (const skill of SKILLS) {
    const raw = response?.[skill.key]
    if (!Array.isArray(raw) || raw.length === 0) continue

    let peak = 0
    for (const p of raw) peak = Math.max(peak, p.value)
    if (!(peak > 0)) continue

    highest = Math.max(highest, peak)
    found.push({ skill, raw, peak })
  }

  return found.map(({ skill, raw, peak }) => ({
    ...skill,
    peak,
    top: peak / highest,
    points: raw.map((p) => ({ time: p.time * speed, value: p.value / highest })),
  }))
}

/** End of the drawn time axis: the last point of any curve. */
function endTime(skills) {
  let end = 0
  for (const s of skills) end = Math.max(end, s.points[s.points.length - 1].time)
  return end
}

/**
 * Value of a curve at `time`: that of the point nearest in time. The curves
 * are on a uniform grid with a point in the middle of every window, so this is
 * the value of the window the time is in. Null for an empty curve.
 */
function valueNear(points, time) {
  if (points.length === 0) return null

  let lo = 0
  let hi = points.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (points[mid].time < time) lo = mid + 1
    else hi = mid
  }
  // lo is the first point at or after the time; the one before it may be nearer.
  if (lo > 0 && time - points[lo - 1].time <= points[lo].time - time) lo--
  return points[lo].value
}

/**
 * Opacity of a skill's line at the current time: a line whose current value is
 * below the skill's own peak (`top`) fades with the square of how far below.
 * The lines that are hard at this very moment stand out, the rest do not blur
 * the graph. A cumulative curve (memory) has no meaningful "below its peak", so
 * it has a constant, medium opacity.
 */
function lineAlpha(skill, time) {
  if (skill.cumulative) return ALPHA_CUMULATIVE

  const v = valueNear(skill.points, time)
  if (v === null) return ALPHA_MIN
  const ratio = skill.top > 0 ? Math.min(1, Math.max(0, v / skill.top)) : 0
  return ALPHA_MIN + (1 - ALPHA_MIN) * ratio * ratio
}

/**
 * Bézier segments of a smooth curve through the points (a monotone cubic
 * Hermite spline, Fritsch–Carlson). Between two points the curve never goes
 * above the higher or below the lower one, and at a peak or a dip it is level:
 * round like a sine wave, but without overshoot, so a peak stays exactly the
 * value of its point (the peaks are what the skills are made of) and a curve
 * of non-negative values never dips below zero.
 *
 * Returns one segment per pair of neighbouring points, each the two control
 * points and the end point of a cubic Bézier that starts at the previous point:
 * { c1: {time, value}, c2: {time, value}, to: {time, value} }.
 * Points must be in increasing time; fewer than two points give no segments.
 */
function smoothSegments(points) {
  const n = points.length
  if (n < 2) return []

  const dx = []
  const slope = []
  for (let i = 0; i < n - 1; i++) {
    dx.push(points[i + 1].time - points[i].time)
    slope.push(dx[i] > 0 ? (points[i + 1].value - points[i].value) / dx[i] : 0)
  }

  // Tangent at every point: the average of the neighbouring slopes, 0 at a peak
  // or a dip, the one slope at the ends.
  const tangent = new Array(n)
  tangent[0] = slope[0]
  tangent[n - 1] = slope[n - 2]
  for (let i = 1; i < n - 1; i++) {
    tangent[i] = slope[i - 1] * slope[i] <= 0 ? 0 : (slope[i - 1] + slope[i]) / 2
  }

  // Keep every tangent within three times the slopes around it (and a flat
  // stretch flat): that is what rules out overshoot.
  for (let i = 0; i < n - 1; i++) {
    if (slope[i] === 0) {
      tangent[i] = 0
      tangent[i + 1] = 0
      continue
    }
    const a = tangent[i] / slope[i]
    const b = tangent[i + 1] / slope[i]
    const size = Math.hypot(a, b)
    if (size > 3) {
      tangent[i] = (3 * a * slope[i]) / size
      tangent[i + 1] = (3 * b * slope[i]) / size
    }
  }

  const segments = []
  for (let i = 0; i < n - 1; i++) {
    const third = dx[i] / 3
    segments.push({
      c1: { time: points[i].time + third, value: points[i].value + tangent[i] * third },
      c2: { time: points[i + 1].time - third, value: points[i + 1].value - tangent[i + 1] * third },
      to: points[i + 1],
    })
  }
  return segments
}

/**
 * Height of the smooth curve of smoothSegments at `time` (clamped to the first
 * and last point), so a marker can sit exactly on the line it belongs to.
 * Null for an empty curve.
 */
function smoothValueAt(points, time) {
  if (points.length === 0) return null
  if (points.length === 1 || time <= points[0].time) return points[0].value
  if (time >= points[points.length - 1].time) return points[points.length - 1].value

  // The segment the time is in.
  let lo = 0
  let hi = points.length - 1
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1
    if (points[mid].time <= time) lo = mid
    else hi = mid
  }
  const s = smoothSegments(points)[lo]

  // The curve's time grows with the Bézier parameter: find the parameter by bisection.
  const from = points[lo]
  const at = (u, a, b, c, d) => (1 - u) ** 3 * a + 3 * (1 - u) ** 2 * u * b + 3 * (1 - u) * u ** 2 * c + u ** 3 * d
  let a = 0
  let b = 1
  for (let i = 0; i < 30; i++) {
    const u = (a + b) / 2
    if (at(u, from.time, s.c1.time, s.c2.time, s.to.time) < time) a = u
    else b = u
  }
  const u = (a + b) / 2
  return at(u, from.value, s.c1.value, s.c2.value, s.to.value)
}

/** Drawing styles of the widget. */
const STYLES = ['ribbon', 'silhouette', 'lines', 'spectrum']
const DEFAULT_STYLE = 'ribbon'

/** An unknown or empty style name falls back to the default one. */
function normalizeStyle(name) {
  const s = typeof name === 'string' ? name.trim().toLowerCase() : ''
  return STYLES.includes(s) ? s : DEFAULT_STYLE
}

// A challenger takes the lead only if it is this much above the current leader (no flicker between close skills).
const LEAD_MARGIN = 1.15
// A leader that holds for fewer windows than this is absorbed by its neighbour.
const LEAD_MIN_RUN = 3

/**
 * Which skill dominates where, calm enough to colour a graph with.
 *
 * The cumulative curve (memory) does not take part: it has no "moment" of its own. For every point the values are blurred
 * with the neighbouring windows, the leader changes only when a challenger is LEAD_MARGIN times above it (hysteresis),
 * and a leader that holds for less than LEAD_MIN_RUN windows is given to its neighbour. The result:
 *   times     — the points' times (those of the first skill),
 *   envelope  — the highest value of any skill at every point ({time, value}), the silhouette of the map,
 *   leaders   — the leading skill's key at every point,
 *   runs      — the same as {key, from, to} (indexes of points, inclusive),
 *   weights   — per skill key: 0..1 at every point, 1 where it leads, with soft edges (for colour gradients).
 * Empty when there is no skill that can lead.
 */
function dominance(skills) {
  const cand = skills.filter((s) => !s.cumulative && s.points.length > 0)
  if (cand.length === 0) return { times: [], envelope: [], leaders: [], runs: [], weights: {} }

  const times = cand[0].points.map((p) => p.time)
  const n = times.length
  const raw = cand.map((s) => times.map((t) => valueNear(s.points, t)))
  const blur = (arr) => arr.map((v, i) => 0.25 * arr[Math.max(0, i - 1)] + 0.5 * v + 0.25 * arr[Math.min(n - 1, i + 1)])
  const soft = raw.map(blur)

  const best = (i) => {
    let k = 0
    for (let j = 1; j < cand.length; j++) if (soft[j][i] > soft[k][i]) k = j
    return k
  }
  const lead = new Array(n)
  let cur = best(0)
  for (let i = 0; i < n; i++) {
    const b = best(i)
    if (b !== cur && soft[b][i] > soft[cur][i] * LEAD_MARGIN) cur = b
    lead[i] = cur
  }

  // Absorb runs that are too short: the shortest first, into the stronger neighbour.
  const toRuns = () => {
    const r = []
    for (let i = 0; i < n; i++) {
      if (r.length && r[r.length - 1].k === lead[i]) r[r.length - 1].to = i
      else r.push({ k: lead[i], from: i, to: i })
    }
    return r
  }
  for (let guard = 0; guard < n; guard++) {
    const r = toRuns()
    if (r.length < 2) break
    let s = -1
    for (let i = 0; i < r.length; i++) if (r[i].to - r[i].from + 1 < LEAD_MIN_RUN && (s < 0 || r[i].to - r[i].from < r[s].to - r[s].from)) s = i
    if (s < 0) break
    const mean = (run) => {
      let sum = 0
      for (let i = run.from; i <= run.to; i++) sum += soft[run.k][i]
      return sum / (run.to - run.from + 1)
    }
    const prev = r[s - 1]
    const next = r[s + 1]
    const into = !prev ? next : !next ? prev : mean(prev) >= mean(next) ? prev : next
    for (let i = r[s].from; i <= r[s].to; i++) lead[i] = into.k
  }

  const runs = toRuns().map((r) => ({ key: cand[r.k].key, from: r.from, to: r.to }))
  const leaders = lead.map((k) => cand[k].key)
  const envelope = times.map((time, i) => ({ time, value: Math.max(...raw.map((r) => r[i])) }))

  const weights = {}
  for (const s of cand) {
    let w = leaders.map((k) => (k === s.key ? 1 : 0))
    if (w.some((v) => v > 0)) {
      w = blur(blur(w))
      weights[s.key] = w
    }
  }
  return { times, envelope, leaders, runs, weights }
}

/** The leading skill's key at `time` (the nearest point); null when nobody leads. */
function leaderAt(dom, time) {
  if (dom.times.length === 0) return null
  let lo = 0
  let hi = dom.times.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (dom.times[mid] < time) lo = mid + 1
    else hi = mid
  }
  if (lo > 0 && time - dom.times[lo - 1] <= dom.times[lo] - time) lo--
  return dom.leaders[lo]
}

// ── spectrum: столбики с плавным движением ─────────────────────────────────

const ATTACK_MS = 70 // как быстро столбик растёт к новому значению (постоянная времени)
const RELEASE_MS = 280 // как быстро опускается
const PEAK_HOLD_MS = 500 // сколько метка пика стоит на месте
const PEAK_FALL = 0.55 // потом падает: доли высоты в секунду

/**
 * Один шаг анимации столбика за dt мс: уровень догоняет цель (вверх быстрее, чем вниз), метка пика держится и падает.
 * bar = { level, peak, hold } меняется на месте; возвращает true, если что-то сдвинулось (тогда нужна перерисовка).
 */
function stepBar(bar, target, dt) {
  const before = bar.level + bar.peak * 7
  const tau = target > bar.level ? ATTACK_MS : RELEASE_MS
  bar.level += (target - bar.level) * (1 - Math.exp(-Math.max(dt, 0) / tau))
  if (Math.abs(target - bar.level) < 0.001) bar.level = target

  if (bar.level >= bar.peak) {
    bar.peak = bar.level
    bar.hold = PEAK_HOLD_MS
  } else if (bar.hold > 0) {
    bar.hold -= dt
  } else {
    bar.peak = Math.max(bar.level, bar.peak - (PEAK_FALL * dt) / 1000)
  }
  return bar.level + bar.peak * 7 !== before
}

  const api = { SKILLS, STYLES, DEFAULT_STYLE, ALPHA_MIN, ALPHA_CUMULATIVE, normalizeMods, prepareSkills, endTime, valueNear, lineAlpha, smoothSegments, smoothValueAt, normalizeStyle, dominance, leaderAt, stepBar }
  if (typeof module === 'object' && module.exports) module.exports = api
  else root.OSUGraph = api
})(globalThis)

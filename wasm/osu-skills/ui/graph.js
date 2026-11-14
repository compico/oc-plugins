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

  const api = { SKILLS, ALPHA_MIN, ALPHA_CUMULATIVE, normalizeMods, prepareSkills, endTime, valueNear, lineAlpha, smoothSegments, smoothValueAt }
  if (typeof module === 'object' && module.exports) module.exports = api
  else root.OSUGraph = api
})(globalThis)

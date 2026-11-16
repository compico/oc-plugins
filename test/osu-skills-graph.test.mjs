import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { test } from 'node:test'
const {
  ALPHA_CUMULATIVE,
  ALPHA_MIN,
  endTime,
  lineAlpha,
  normalizeMods,
  prepareSkills,
  smoothSegments,
  smoothValueAt,
  valueNear,
} = createRequire(import.meta.url)('../wasm/osu-skills/ui/graph.js')

const pts = (...pairs) => pairs.map(([time, value]) => ({ time, value }))

test('normalizeMods resolves tosu names to what go-osu accepts', () => {
  assert.equal(normalizeMods('NM'), '')
  assert.equal(normalizeMods(''), '')
  assert.equal(normalizeMods(undefined), '')
  assert.equal(normalizeMods('hddt'), 'HDDT')
  assert.equal(normalizeMods('HDNC'), 'HDDT') // nightcore is a double time
  assert.equal(normalizeMods('PF'), 'SD')
  assert.equal(normalizeMods('HDCLTD'), 'HD') // mods go-osu does not know are dropped
  assert.equal(normalizeMods('DTNC'), 'DT') // no duplicates
  assert.equal(normalizeMods('HDHR'), 'HDHR')
})

test('all skills share one height: 1 is the highest value any of them reaches', () => {
  const skills = prepareSkills({
    speed: 1,
    stamina: pts([0, 100], [1000, 250], [2000, 50]),
    accuracy: pts([0, 1000], [1000, 500]),
    reading: [],
    reaction: pts([0, 0], [10, 0]), // all zeros: nothing to draw
  })

  assert.deepEqual(skills.map((s) => s.key), ['stamina', 'accuracy'])
  assert.deepEqual(skills[0].points.map((p) => p.value), [0.1, 0.25, 0.05])
  assert.deepEqual(skills[1].points.map((p) => p.value), [1, 0.5])
  // Each skill's own peak on that scale: 1 for the strongest.
  assert.equal(skills[0].top, 0.25)
  assert.equal(skills[1].top, 1)
  assert.equal(skills[0].peak, 250)
})

test('a skill that is hardly there stays low', () => {
  // Reading is 0-11 on most maps while the others are in the hundreds.
  const [stamina, reading] = prepareSkills({
    speed: 1,
    reading: pts([0, 2], [1000, 11]),
    stamina: pts([0, 150], [1000, 1000]),
  })

  assert.equal(Math.max(...stamina.points.map((p) => p.value)), 1)
  assert.ok(Math.max(...reading.points.map((p) => p.value)) <= 0.011)
  assert.equal(reading.top, 0.011)
})

test('prepareSkills puts the time on the beatmap timeline', () => {
  const [stamina] = prepareSkills({ speed: 1.5, stamina: pts([1000, 1], [2000, 2]) })
  assert.deepEqual(stamina.points.map((p) => p.time), [1500, 3000])

  // A missing or bad speed does not break the times.
  const [bad] = prepareSkills({ speed: 0, stamina: pts([1000, 1]) })
  assert.equal(bad.points[0].time, 1000)
  assert.deepEqual(prepareSkills(null), [])
})

test('endTime is the last point of any curve', () => {
  const skills = prepareSkills({ speed: 1, stamina: pts([0, 1], [5000, 2]), agility: pts([0, 1], [8000, 1]) })
  assert.equal(endTime(skills), 8000)
})

test('valueNear is the value of the nearest point', () => {
  const curve = pts([2500, 0.1], [7500, 0.8], [12500, 0.5])
  assert.equal(valueNear(curve, 0), 0.1) // before the first point
  assert.equal(valueNear(curve, 2500), 0.1)
  assert.equal(valueNear(curve, 4999), 0.1)
  assert.equal(valueNear(curve, 5000), 0.1) // halfway: the earlier one
  assert.equal(valueNear(curve, 5001), 0.8)
  assert.equal(valueNear(curve, 9999), 0.8)
  assert.equal(valueNear(curve, 10001), 0.5)
  assert.equal(valueNear(curve, 99999), 0.5) // after the last
  assert.equal(valueNear([], 0), null)
  assert.equal(valueNear(pts([100, 3]), 5), 3)
})

test('lineAlpha fades against the skill\'s own peak, not the highest skill', () => {
  // A skill that peaks at 0.4 of the full height is at its peak when it reaches 0.4.
  const skill = { top: 0.4, points: pts([0, 0.4], [1000, 0.2], [2000, 0.04]) }

  assert.equal(lineAlpha(skill, 0), 1) // at its own peak
  const half = lineAlpha(skill, 1000)
  const low = lineAlpha(skill, 2000)
  assert.ok(low < half && half < 1, `${low} < ${half} < 1`)
  assert.ok(low > ALPHA_MIN)
  // The fade is the square of the ratio: half its peak is a quarter of the way up.
  assert.ok(Math.abs(half - (ALPHA_MIN + (1 - ALPHA_MIN) * 0.25)) < 1e-12)
  assert.equal(lineAlpha({ top: 1, points: pts([0, 0]) }, 0), ALPHA_MIN) // nothing there at all
  assert.equal(lineAlpha({ top: 1, points: [] }, 0), ALPHA_MIN)
})

test('lineAlpha: a cumulative line has a constant opacity', () => {
  const memory = { cumulative: true, top: 1, points: pts([0, 0.1], [1000, 1]) }
  assert.equal(lineAlpha(memory, 0), ALPHA_CUMULATIVE)
  assert.equal(lineAlpha(memory, 99999), ALPHA_CUMULATIVE)
})

// Evaluates the cubic Bézier of a segment that starts at `from`.
const bezier = (from, s, u) => {
  const mix = (a, b, c, d) => (1 - u) ** 3 * a + 3 * (1 - u) ** 2 * u * b + 3 * (1 - u) * u ** 2 * c + u ** 3 * d
  return {
    time: mix(from.time, s.c1.time, s.c2.time, s.to.time),
    value: mix(from.value, s.c1.value, s.c2.value, s.to.value),
  }
}

test('smoothSegments goes through every point and never overshoots', () => {
  // Peaks, dips, a flat stretch, a sharp rise, a drop to zero.
  const curve = pts([0, 0], [1000, 5], [2000, 1], [3000, 1], [4000, 1], [5000, 9], [6000, 8.5], [7000, 0], [8000, 0], [9000, 3])
  const segments = smoothSegments(curve)

  assert.equal(segments.length, curve.length - 1)
  segments.forEach((s, i) => {
    assert.deepEqual(s.to, curve[i + 1]) // through every point
    const low = Math.min(curve[i].value, curve[i + 1].value)
    const high = Math.max(curve[i].value, curve[i + 1].value)
    for (let u = 0; u <= 1; u += 0.05) {
      const p = bezier(curve[i], s, u)
      assert.ok(p.value >= low - 1e-9 && p.value <= high + 1e-9, `segment ${i} at ${u}: ${p.value} outside [${low}, ${high}]`)
      assert.ok(p.time >= curve[i].time - 1e-9 && p.time <= curve[i + 1].time + 1e-9)
    }
  })
})

test('smoothSegments is level at a peak and at a dip, and flat where the data is flat', () => {
  const curve = pts([0, 0], [1000, 5], [2000, 1], [3000, 1], [4000, 4])
  const segments = smoothSegments(curve)

  // At the peak (point 1) the control points on both sides are at its height.
  assert.equal(segments[0].c2.value, 5)
  assert.equal(segments[1].c1.value, 5)
  // The dip at 2000 touches the flat stretch: level there too, and the stretch stays flat.
  assert.equal(segments[1].c2.value, 1)
  assert.deepEqual([segments[2].c1.value, segments[2].c2.value], [1, 1])
})

test('smoothSegments keeps a curve that is a straight line straight', () => {
  const segments = smoothSegments(pts([0, 0], [1000, 1], [2000, 2], [3000, 3]))
  for (const s of segments) {
    assert.ok(Math.abs(s.c1.value - s.c1.time / 1000) < 1e-9)
    assert.ok(Math.abs(s.c2.value - s.c2.time / 1000) < 1e-9)
  }
})

test('smoothSegments handles tiny and degenerate input', () => {
  assert.deepEqual(smoothSegments([]), [])
  assert.deepEqual(smoothSegments(pts([0, 1])), [])
  assert.equal(smoothSegments(pts([0, 1], [1000, 3])).length, 1)
  // Points at the same time do not produce NaN.
  for (const s of smoothSegments(pts([0, 1], [0, 3], [1000, 2]))) {
    for (const v of [s.c1.time, s.c1.value, s.c2.time, s.c2.value]) assert.ok(Number.isFinite(v))
  }
})

test('smoothValueAt is on the curve: the points\' values at their times, the curve between', () => {
  const curve = pts([0, 0], [1000, 5], [2000, 1], [3000, 1], [4000, 4])
  for (const p of curve) assert.ok(Math.abs(smoothValueAt(curve, p.time) - p.value) < 1e-6, `at ${p.time}`)

  // Compare with points sampled from the Bézier segments themselves.
  const segments = smoothSegments(curve)
  segments.forEach((s, i) => {
    for (let u = 0.1; u < 1; u += 0.2) {
      const p = bezier(curve[i], s, u)
      assert.ok(Math.abs(smoothValueAt(curve, p.time) - p.value) < 1e-6)
    }
  })

  // Before the first and after the last point it holds the end values; empty is null.
  assert.equal(smoothValueAt(curve, -500), 0)
  assert.equal(smoothValueAt(curve, 99999), 4)
  assert.equal(smoothValueAt([], 0), null)
  assert.equal(smoothValueAt(pts([10, 7]), 500), 7)
})

// ── стили и доминирование ────────────────────────────────────────────────

const { dominance, leaderAt, normalizeStyle, STYLES, DEFAULT_STYLE } = createRequire(import.meta.url)('../wasm/osu-skills/ui/graph.js')

const curve = (key, values, extra = {}) => ({ key, ...extra, points: values.map((value, i) => ({ time: i * 1000, value })) })

test('normalizeStyle: known names, anything else is the default', () => {
  for (const s of STYLES) assert.equal(normalizeStyle(s), s)
  assert.equal(normalizeStyle(' Silhouette '), 'silhouette')
  for (const bad of ['', 'x', undefined, null, 5]) assert.equal(normalizeStyle(bad), DEFAULT_STYLE)
})

test('dominance: the leader follows the highest skill, runs cover every point', () => {
  const a = curve('stamina', [9, 9, 9, 9, 9, 1, 1, 1, 1, 1])
  const b = curve('agility', [1, 1, 1, 1, 1, 9, 9, 9, 9, 9])
  const d = dominance([a, b])
  assert.deepEqual(d.leaders, ['stamina', 'stamina', 'stamina', 'stamina', 'stamina', 'agility', 'agility', 'agility', 'agility', 'agility'])
  assert.deepEqual(d.runs, [{ key: 'stamina', from: 0, to: 4 }, { key: 'agility', from: 5, to: 9 }])
  assert.deepEqual(d.envelope.map((p) => p.value), [9, 9, 9, 9, 9, 9, 9, 9, 9, 9])
  assert.equal(d.weights.stamina[0], 1)
  assert.equal(d.weights.agility[9], 1)
  assert.ok(d.weights.stamina[5] > 0 && d.weights.stamina[5] < 1, 'edges are soft')
})

test('dominance: skills that keep crossing do not flicker', () => {
  // Two skills within a few percent of each other, swapping every point.
  const a = curve('stamina', [10, 10.4, 10, 10.4, 10, 10.4, 10, 10.4, 10, 10.4])
  const b = curve('agility', [10.4, 10, 10.4, 10, 10.4, 10, 10.4, 10, 10.4, 10])
  const d = dominance([a, b])
  assert.equal(d.runs.length, 1)
})

test('dominance: a short blip does not take the colour, a clear change does', () => {
  const a = curve('stamina', [5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5])
  const b = curve('agility', [1, 1, 1, 1, 9, 1, 1, 1, 1, 1, 1, 1]) // one tall window
  assert.equal(dominance([a, b]).runs.length, 1)
  const c = curve('agility', [1, 1, 1, 1, 9, 9, 9, 9, 9, 9, 9, 9])
  assert.deepEqual(dominance([a, c]).runs.map((r) => r.key), ['stamina', 'agility'])
})

test('dominance: the cumulative curve never leads; no skills — nothing to colour', () => {
  const mem = curve('memory', [100, 100, 100, 100], { cumulative: true })
  const a = curve('stamina', [1, 1, 1, 1])
  assert.deepEqual(dominance([mem, a]).leaders, ['stamina', 'stamina', 'stamina', 'stamina'])
  const none = dominance([mem])
  assert.deepEqual(none.runs, [])
  assert.equal(leaderAt(none, 0), null)
})

test('leaderAt: the leader of the window the time is in', () => {
  const d = dominance([curve('stamina', [9, 9, 9, 9, 1, 1, 1, 1]), curve('agility', [1, 1, 1, 1, 9, 9, 9, 9])])
  assert.equal(leaderAt(d, 500), 'stamina')
  assert.equal(leaderAt(d, 6000), 'agility')
  assert.equal(leaderAt(d, 999999), 'agility')
  assert.equal(leaderAt(d, -5), 'stamina')
})

// ── spectrum ─────────────────────────────────────────────────────────────

const { stepBar } = createRequire(import.meta.url)('../wasm/osu-skills/ui/graph.js')

test('stepBar: rises faster than it falls, settles exactly on the target', () => {
  const up = { level: 0, peak: 0, hold: 0 }
  stepBar(up, 1, 16)
  const down = { level: 1, peak: 1, hold: 0 }
  stepBar(down, 0, 16)
  assert.ok(up.level > 1 - down.level, 'attack is faster than release')

  const bar = { level: 0, peak: 0, hold: 0 }
  let moved = true
  for (let i = 0; i < 400 && moved; i++) moved = stepBar(bar, 0.6, 16)
  assert.equal(bar.level, 0.6)
  assert.equal(stepBar(bar, 0.6, 16), false, 'nothing moves: no need to redraw')
})

test('stepBar: the peak holds, then falls, and never drops below the level', () => {
  const bar = { level: 0, peak: 0, hold: 0 }
  for (let i = 0; i < 100; i++) stepBar(bar, 1, 16)
  assert.equal(bar.peak, 1)
  stepBar(bar, 0, 16) // the bar falls, the peak stays for a while
  assert.equal(bar.peak, 1)
  for (let i = 0; i < 40; i++) stepBar(bar, 0, 16) // ~640 ms: the hold is over
  assert.ok(bar.peak < 1 && bar.peak >= bar.level)
  for (let i = 0; i < 400; i++) stepBar(bar, 0, 16)
  assert.equal(bar.peak, bar.level)
})

test('valueLerp: linear between points, ends are held, empty curve is null', () => {
  const { valueLerp } = createRequire(import.meta.url)('../wasm/osu-skills/ui/graph.js')
  const pts = [{ time: 0, value: 0 }, { time: 1000, value: 100 }, { time: 2000, value: 50 }]
  assert.equal(valueLerp(pts, 500), 50)
  assert.equal(valueLerp(pts, 1500), 75)
  assert.equal(valueLerp(pts, 1000), 100)
  assert.equal(valueLerp(pts, -10), 0)
  assert.equal(valueLerp(pts, 9999), 50)
  assert.equal(valueLerp([], 5), null)
})

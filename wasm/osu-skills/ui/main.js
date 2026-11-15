// osu! skills graph: tosu говорит, какая карта и какие моды сейчас, плагин osu-skills считает кривые навыков, виджет рисует.
// Обычный скрипт (не модуль): из sandbox-iframe модули не загрузить, поэтому функции графика берутся из graph.js (OSUGraph).
const { SKILLS, ALPHA_MIN, dominance, endTime, leaderAt, lineAlpha, normalizeMods, normalizeStyle, prepareSkills, smoothSegments, smoothValueAt } = OSUGraph

const query = new URLSearchParams(location.search)
const cfg = {
  // Настройки плагина (style, window) подгружаются в начале (loadSettings); параметры адреса виджета главнее: ?style=…&window=…
  window: Number(query.get('window')) || 5000, // мс карты на одну точку
  style: normalizeStyle(query.get('style')),
}

const canvas = document.getElementById('graph')
const statusEl = document.getElementById('status')
const mainCtx = canvas.getContext('2d')
let ctx = mainCtx // функции рисования пишут в ctx; fadeLayer на время подменяет его слоем
// Слой для заливок, которые к основанию графика растворяются (стили ribbon и silhouette).
const layer = document.createElement('canvas')
const layerCtx = layer.getContext('2d')

// Что tosu говорит сейчас, и что загружено.
const live = { file: '', checksum: '', mods: '', time: 0 }

let skills = []
let dom = dominance([])
let loadedKey = ''
let generation = 0 // номер последней загрузки: устаревшие ответы отбрасываются
let retryTimer = null
let dirty = true

// ── tosu (ретранслятор хоста) ─────────────────────────────────────────────

// tosu не пускает websocket из sandbox-iframe (Origin null, ответ 403), а опрос HTTP из плагина грузит процессор. Поэтому
// хост OC сам держит websocket к tosu, пока виджет подписан на событие live, и присылает только нужные поля (не чаще 4 раз в секунду).
// Между событиями время карты дорисовывается: пока оно идёт, считаем, что идёт и дальше.
const clock = { time: 0, at: 0, moving: false }
let gotState = false

function applyState(data) {
  const now = performance.now()
  clock.moving = data.time !== clock.time && now - clock.at < 1500 && data.time > clock.time
  clock.time = data.time
  clock.at = now

  const file = data.file || ''
  const checksum = /^[0-9a-f]{32}$/i.test(data.checksum || '') ? data.checksum : ''
  const mods = normalizeMods(data.mods)
  if (file !== live.file || checksum !== live.checksum || mods !== live.mods) {
    live.file = file
    live.checksum = checksum
    live.mods = mods
    load()
  }
}

function currentTime() {
  const since = performance.now() - clock.at
  // Событий нет дольше 1,5 с — пауза (tosu присылает время только пока оно меняется): не убегаем вперёд.
  return clock.moving && since < 1500 ? clock.time + since : clock.time
}

function subscribe() {
  // SDK зовёт обработчик как (data, meta): данные события — первый аргумент.
  oc.plugin.subscribe('live', (data) => {
    if (!data || typeof data !== 'object') return
    gotState = true
    if (statusEl.dataset.src === 'state') showStatus('')
    applyState(data)
  })
  // tosu не запущен (или хост не подключился): событий нет вовсе.
  setTimeout(() => {
    if (!gotState) showStatus('Waiting for tosu…', 'state')
  }, 4000)
}

// ── плагин osu-skills ─────────────────────────────────────────────────────

// Плагин берёт файл карты у tosu (разрешение net.local) и считает кривые: пока он занят, отвечает 202, и запрос повторяется.
async function load() {
  clearTimeout(retryTimer)
  const key = `${live.file}|${live.checksum}|${live.mods}`
  if (key === loadedKey) return
  const mine = ++generation
  if (!live.file) {
    skills = []
    loadedKey = ''
    showStatus('')
    dirty = true
    return
  }

  showStatus('Calculating…')
  const again = (ms) => { retryTimer = setTimeout(load, ms) }
  try {
    const res = await oc.plugin.post('/curves', { file: live.file, checksum: live.checksum, mods: live.mods, window: cfg.window })
    if (mine !== generation) return
    if (res.status === 202) return again(700)
    if (res.status === 200) {
      skills = prepareSkills(res.body)
      dom = dominance(skills)
      loadedKey = key
      showStatus('')
    } else {
      skills = []
      loadedKey = ''
      showStatus(res.body?.error || `Plugin error (HTTP ${res.status})`)
      return again(5000)
    }
  } catch {
    if (mine !== generation) return
    skills = []
    showStatus('The osu-skills plugin is not responding. Open the widget through Overlay Composer.')
    return again(5000)
  }
  dirty = true
}

function showStatus(text, src = 'load') {
  statusEl.dataset.src = src
  statusEl.textContent = text
  statusEl.hidden = text === ''
}

// ── drawing ───────────────────────────────────────────────────────────────

const PAD = { left: 8, right: 8, top: 22, bottom: 8 }

function resize() {
  const ratio = window.devicePixelRatio || 1
  const { width, height } = canvas.getBoundingClientRect()
  canvas.width = Math.round(width * ratio)
  canvas.height = Math.round(height * ratio)
  mainCtx.setTransform(ratio, 0, 0, ratio, 0, 0)
  layer.width = canvas.width
  layer.height = canvas.height
  layerCtx.setTransform(ratio, 0, 0, ratio, 0, 0)
  dirty = true
}

function draw() {
  const { width, height } = canvas.getBoundingClientRect()
  ctx.clearRect(0, 0, width, height)
  if (skills.length === 0) return

  const end = Math.max(endTime(skills), 1)
  const plot = {
    width,
    height,
    end,
    x: (ms) => PAD.left + (ms / end) * (width - PAD.left - PAD.right),
    y: (v) => height - PAD.bottom - v * (height - PAD.top - PAD.bottom),
    base: height - PAD.bottom,
  }

  if (cfg.style === 'lines') drawLines(plot)
  else if (cfg.style === 'silhouette') drawSilhouette(plot)
  else drawRibbon(plot)
  ctx.globalAlpha = 1
}

const colorOf = (key) => SKILLS.find((s) => s.key === key)?.color ?? '#fff'

function rgba(hex, alpha) {
  const n = parseInt(hex.slice(1), 16)
  return `rgba(${n >> 16}, ${(n >> 8) & 255}, ${n & 255}, ${alpha})`
}

/**
 * Draws with `fn` onto a separate layer and puts the layer on the graph with its lower part faded away: the colour is
 * strongest at the tops (the peaks) and thins towards the base line, which keeps overlapping areas readable.
 */
function fadeLayer(plot, fn) {
  layerCtx.clearRect(0, 0, plot.width, plot.height)
  ctx = layerCtx
  fn()
  const fade = layerCtx.createLinearGradient(0, PAD.top, 0, plot.base)
  fade.addColorStop(0, 'rgba(0, 0, 0, 0)')
  fade.addColorStop(1, 'rgba(0, 0, 0, 0.8)')
  layerCtx.globalCompositeOperation = 'destination-out'
  layerCtx.globalAlpha = 1
  layerCtx.fillStyle = fade
  layerCtx.fillRect(0, 0, plot.width, plot.height)
  layerCtx.globalCompositeOperation = 'source-over'
  ctx = mainCtx
  mainCtx.drawImage(layer, 0, 0, plot.width, plot.height)
}

/** The path of a smooth curve's outline (no closing). */
function tracePath(plot, points) {
  ctx.moveTo(plot.x(points[0].time), plot.y(points[0].value))
  for (const s of smoothSegments(points)) {
    ctx.bezierCurveTo(plot.x(s.c1.time), plot.y(s.c1.value), plot.x(s.c2.time), plot.y(s.c2.value), plot.x(s.to.time), plot.y(s.to.value))
  }
}

/** The area under a smooth curve down to the base line. */
function traceArea(plot, points) {
  ctx.beginPath()
  ctx.moveTo(plot.x(points[0].time), plot.base)
  ctx.lineTo(plot.x(points[0].time), plot.y(points[0].value))
  for (const s of smoothSegments(points)) {
    ctx.bezierCurveTo(plot.x(s.c1.time), plot.y(s.c1.value), plot.x(s.c2.time), plot.y(s.c2.value), plot.x(s.to.time), plot.y(s.to.value))
  }
  ctx.lineTo(plot.x(points[points.length - 1].time), plot.base)
  ctx.closePath()
}

/** A horizontal gradient: solid colour of the leader inside its run, a short blend at the borders. */
function leaderGradient(plot) {
  const g = ctx.createLinearGradient(plot.x(0), 0, plot.x(plot.end), 0)
  const pos = (i) => Math.min(1, Math.max(0, dom.times[i] / plot.end))
  const step = dom.times.length > 1 ? (dom.times[1] - dom.times[0]) / plot.end : 0
  for (const run of dom.runs) {
    const c = colorOf(run.key)
    const from = pos(run.from)
    const to = pos(run.to)
    const blend = Math.min(step, (to - from) / 2)
    g.addColorStop(Math.min(1, from + blend), c)
    g.addColorStop(Math.max(0, to - blend), c)
  }
  return g
}

/** A horizontal gradient of one skill's colour with the opacity of its leadership (0..1 at every point). */
function weightGradient(plot, key, maxAlpha) {
  const g = ctx.createLinearGradient(plot.x(0), 0, plot.x(plot.end), 0)
  const w = dom.weights[key]
  for (let i = 0; i < dom.times.length; i++) {
    g.addColorStop(Math.min(1, Math.max(0, dom.times[i] / plot.end)), rgba(colorOf(key), w[i] * maxAlpha))
  }
  return g
}

/** The mark of the current moment: a vertical line and a dot on the height `value` (null — no dot). */
function drawNow(plot, value, color) {
  const now = Math.min(Math.max(live.time, 0), plot.end)
  ctx.globalAlpha = 0.9
  ctx.strokeStyle = '#fff'
  ctx.lineWidth = 1
  ctx.beginPath()
  ctx.moveTo(plot.x(now), PAD.top - 4)
  ctx.lineTo(plot.x(now), plot.base)
  ctx.stroke()
  if (value !== null) {
    ctx.fillStyle = color
    ctx.beginPath()
    ctx.arc(plot.x(now), plot.y(value), 3, 0, Math.PI * 2)
    ctx.fill()
  }
  ctx.globalAlpha = 1
}

// ── ribbon: one silhouette of the map, coloured by who leads ──────────────

function drawRibbon(plot) {
  if (dom.envelope.length < 2) return drawLines(plot)

  const gradient = leaderGradient(plot)
  fadeLayer(plot, () => {
    traceArea(plot, dom.envelope)
    ctx.fillStyle = gradient
    ctx.fill()
  })

  ctx.globalAlpha = 1
  ctx.lineWidth = 2
  ctx.lineJoin = 'round'
  ctx.strokeStyle = gradient
  ctx.beginPath()
  tracePath(plot, dom.envelope)
  ctx.stroke()

  // A thin band below the graph: who leads where, at a glance.
  ctx.fillStyle = gradient
  ctx.fillRect(plot.x(0), plot.base + 3, plot.x(plot.end) - plot.x(0), 3)

  const lead = leaderAt(dom, live.time)
  drawNow(plot, smoothValueAt(dom.envelope, live.time), colorOf(lead))
  drawLegend(lead)
}

// ── silhouette: every skill dimly, the leader over it in its own colour ───

function drawSilhouette(plot) {
  if (dom.envelope.length < 2) return drawLines(plot)

  // The silhouette of all skills: where several overlap it is brighter.
  // Over it, every skill only where it leads, with soft edges.
  fadeLayer(plot, () => {
    ctx.fillStyle = 'rgba(255, 255, 255, 0.10)'
    for (const s of skills) {
      if (s.cumulative || s.points.length < 2) continue
      traceArea(plot, s.points)
      ctx.fill()
    }
    for (const s of skills) {
      if (!dom.weights[s.key] || s.points.length < 2) continue
      traceArea(plot, s.points)
      ctx.fillStyle = weightGradient(plot, s.key, 0.9)
      ctx.fill()
    }
  })
  for (const s of skills) {
    if (!dom.weights[s.key] || s.points.length < 2) continue
    ctx.lineWidth = 1.8
    ctx.lineJoin = 'round'
    ctx.strokeStyle = weightGradient(plot, s.key, 1)
    ctx.beginPath()
    tracePath(plot, s.points)
    ctx.stroke()
  }

  const lead = leaderAt(dom, live.time)
  drawNow(plot, smoothValueAt(dom.envelope, live.time), colorOf(lead))
  drawLegend(lead)
}

// ── lines: one line per skill, the ones hard right now are brighter ───────

function drawLines(plot) {
  // The faintest lines first, so the ones that matter are on top.
  const ordered = skills
    .map((skill) => ({ skill, alpha: lineAlpha(skill, live.time) }))
    .sort((a, b) => a.alpha - b.alpha)

  ctx.lineWidth = 1.6
  ctx.lineJoin = 'round'
  ctx.lineCap = 'round'

  for (const { skill, alpha } of ordered) {
    ctx.strokeStyle = skill.color
    ctx.globalAlpha = alpha
    ctx.setLineDash(skill.cumulative ? [4, 3] : [])
    ctx.beginPath()
    tracePath(plot, skill.points)
    ctx.stroke()
  }
  ctx.setLineDash([])

  const now = Math.min(Math.max(live.time, 0), plot.end)
  drawNow(plot, null, '#fff')

  // A dot on every skill at the moment, as bright as its line.
  for (const { skill, alpha } of ordered) {
    const v = smoothValueAt(skill.points, live.time)
    if (v === null) continue
    ctx.globalAlpha = Math.max(alpha, ALPHA_MIN)
    ctx.fillStyle = skill.color
    ctx.beginPath()
    ctx.arc(plot.x(now), plot.y(v), 2.6, 0, Math.PI * 2)
    ctx.fill()
  }
  ctx.globalAlpha = 1
  drawLegend(null, new Map(ordered.map(({ skill, alpha }) => [skill.key, alpha])))
}

/**
 * The legend. With a leader: only the skills that lead somewhere, the current one bright and bold; without
 * (the lines style): every skill as bright as its line.
 */
function drawLegend(leader, alphaOf) {
  ctx.font = '600 10px "Segoe UI", system-ui, sans-serif'
  ctx.textBaseline = 'middle'

  let cursor = PAD.left
  for (const meta of SKILLS) {
    let alpha
    if (alphaOf) {
      if (!alphaOf.has(meta.key)) continue
      alpha = Math.max(alphaOf.get(meta.key), 0.35)
    } else {
      if (!dom.weights[meta.key]) continue
      alpha = meta.key === leader ? 1 : 0.4
    }
    ctx.globalAlpha = alpha
    ctx.fillStyle = meta.color
    ctx.fillRect(cursor, 7, 7, 7)
    ctx.font = `${meta.key === leader ? 700 : 600} 10px "Segoe UI", system-ui, sans-serif`
    ctx.fillText(meta.label, cursor + 10, 11)
    cursor += 10 + ctx.measureText(meta.label).width + 10
  }
  ctx.globalAlpha = 1
}

function frame() {
  const time = currentTime()
  if (time !== live.time) {
    live.time = time
    dirty = true
  }
  if (dirty) {
    dirty = false
    draw()
  }
  requestAnimationFrame(frame)
}

// Настройки плагина: стиль и окно точек. Параметры адреса виджета главнее. Не ответил плагин — остаются значения по умолчанию.
async function loadSettings() {
  try {
    const res = await oc.plugin.post('/settings', {})
    if (res.status !== 200 || !res.body) return
    if (!query.has('style')) cfg.style = normalizeStyle(res.body.style)
    if (!query.has('window') && Number(res.body.window) > 0) cfg.window = Number(res.body.window)
  } catch {
    // значения по умолчанию
  }
}

oc.ready.then(async () => {
  window.addEventListener('resize', resize)
  resize()
  await loadSettings()
  subscribe()
  requestAnimationFrame(frame)
}, () => showStatus('Open the widget through Overlay Composer'))

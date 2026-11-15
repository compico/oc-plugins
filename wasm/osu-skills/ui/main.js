// osu! skills graph: tosu говорит, какая карта и какие моды сейчас, плагин osu-skills считает кривые навыков, виджет рисует.
// Обычный скрипт (не модуль): из sandbox-iframe модули не загрузить, поэтому функции графика берутся из graph.js (OSUGraph).
const { SKILLS, ALPHA_MIN, endTime, lineAlpha, normalizeMods, prepareSkills, smoothSegments, smoothValueAt } = OSUGraph

const query = new URLSearchParams(location.search)
const cfg = {
  window: Number(query.get('window')) || 5000, // мс карты на одну точку
}

const canvas = document.getElementById('graph')
const statusEl = document.getElementById('status')
const ctx = canvas.getContext('2d')

// Что tosu говорит сейчас, и что загружено.
const live = { file: '', checksum: '', mods: '', time: 0 }

let skills = []
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
  oc.plugin.subscribe('live', (e) => {
    if (!e?.data) return
    gotState = true
    if (statusEl.dataset.src === 'state') showStatus('')
    applyState(e.data)
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
  ctx.setTransform(ratio, 0, 0, ratio, 0, 0)
  dirty = true
}

function draw() {
  const { width, height } = canvas.getBoundingClientRect()
  ctx.clearRect(0, 0, width, height)
  if (skills.length === 0) return

  const end = Math.max(endTime(skills), 1)
  const x = (ms) => PAD.left + (ms / end) * (width - PAD.left - PAD.right)
  const y = (v) => height - PAD.bottom - v * (height - PAD.top - PAD.bottom)

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
    const first = skill.points[0]
    ctx.moveTo(x(first.time), y(first.value))
    for (const s of smoothSegments(skill.points)) {
      ctx.bezierCurveTo(x(s.c1.time), y(s.c1.value), x(s.c2.time), y(s.c2.value), x(s.to.time), y(s.to.value))
    }
    ctx.stroke()
  }
  ctx.setLineDash([])

  // The current moment.
  const now = Math.min(Math.max(live.time, 0), end)
  ctx.globalAlpha = 0.9
  ctx.strokeStyle = '#fff'
  ctx.lineWidth = 1
  ctx.beginPath()
  ctx.moveTo(x(now), PAD.top - 4)
  ctx.lineTo(x(now), height - PAD.bottom)
  ctx.stroke()

  // A dot on every skill at the moment, as bright as its line.
  for (const { skill, alpha } of ordered) {
    // On the line itself.
    const v = smoothValueAt(skill.points, live.time)
    if (v === null) continue
    ctx.globalAlpha = Math.max(alpha, ALPHA_MIN)
    ctx.fillStyle = skill.color
    ctx.beginPath()
    ctx.arc(x(now), y(v), 2.6, 0, Math.PI * 2)
    ctx.fill()
  }

  drawLegend(ordered)
  ctx.globalAlpha = 1
}

function drawLegend(ordered) {
  const alphaOf = new Map(ordered.map(({ skill, alpha }) => [skill.key, alpha]))
  ctx.font = '600 10px "Segoe UI", system-ui, sans-serif'
  ctx.textBaseline = 'middle'

  let cursor = PAD.left
  for (const meta of SKILLS) {
    if (!alphaOf.has(meta.key)) continue
    ctx.globalAlpha = Math.max(alphaOf.get(meta.key), 0.35)
    ctx.fillStyle = meta.color
    ctx.fillRect(cursor, 7, 7, 7)
    ctx.fillText(meta.label, cursor + 10, 11)
    cursor += 10 + ctx.measureText(meta.label).width + 10
  }
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

oc.ready.then(() => {
  window.addEventListener('resize', resize)
  resize()
  subscribe()
  requestAnimationFrame(frame)
}, () => showStatus('Open the widget through Overlay Composer'))

// Проверка плагинов репозитория: node scripts/validate.mjs
// Правила повторяют проверки Overlay Composer (internal/plugins), чтобы ошибка находилась до установки.
import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join } from "node:path";

const ROOT = new URL("../plugins/", import.meta.url).pathname;
const TYPES = new Set(["string", "text", "number", "range", "bool", "color", "font", "select"]);
const PERMS = new Set(["chat.read", "chat.send", "storage", "net.local"]);
const errors = [];
const err = (plugin, msg) => errors.push(`${plugin}: ${msg}`);

// changelog.json (необязателен): [{ "version", "date"?, "changes"?: [строки], "migration"?: "что сделать после обновления" }].
// OC показывает записи новее установленной версии в центре уведомлений при выходе обновления.
function checkChangelog(dir, m) {
  const f = join(ROOT, dir, "changelog.json");
  if (!existsSync(f)) return;
  if (statSync(f).size > 64 * 1024) { err(dir, "changelog.json больше 64 КБ — OC его проигнорирует"); return; }
  let log;
  try { log = JSON.parse(readFileSync(f, "utf8")); } catch (e) { err(dir, "changelog.json: " + e.message); return; }
  if (!Array.isArray(log)) { err(dir, "changelog.json должен быть массивом"); return; }
  const seen = new Set();
  log.forEach((c, i) => {
    const where = `changelog[${i}]`;
    if (!/^\d+\.\d+\.\d+/.test(c?.version ?? "")) { err(dir, `${where}: version вида 1.2.3`); return; }
    if (seen.has(c.version)) err(dir, `${where}: версия ${c.version} повторяется`);
    seen.add(c.version);
    if (c.changes !== undefined && (!Array.isArray(c.changes) || c.changes.some((x) => typeof x !== "string" || !x.trim()))) err(dir, `${where}: changes — массив непустых строк`);
    if (c.migration !== undefined && (typeof c.migration !== "string" || !c.migration.trim())) err(dir, `${where}: migration — непустая строка`);
    if (!(c.changes?.length) && !c.migration) err(dir, `${where}: нужен changes или migration`);
  });
  if (!seen.has(m.version)) err(dir, `в changelog.json нет записи для текущей версии ${m.version}`);
}

for (const dir of readdirSync(ROOT).filter((d) => statSync(join(ROOT, d)).isDirectory()).sort()) {
  const mf = join(ROOT, dir, "plugin.json");
  if (!existsSync(mf)) { err(dir, "нет plugin.json"); continue; }
  let m;
  try { m = JSON.parse(readFileSync(mf, "utf8")); } catch (e) { err(dir, "plugin.json: " + e.message); continue; }

  if (m.id !== dir) err(dir, `id "${m.id}" должен совпадать с именем папки`);
  if (!/^[A-Za-z0-9_-]{1,64}$/.test(m.id ?? "")) err(dir, "некорректный id (A-Za-z0-9_-, до 64)");
  if (!String(m.name ?? "").trim()) err(dir, "нужно name");
  if (!/^\d+\.\d+\.\d+/.test(m.version ?? "")) err(dir, "version должна быть вида 1.2.3");
  if (!String(m.description ?? "").trim()) err(dir, "нужно description");
  const entry = m.entry ?? "index.html";
  if (!existsSync(join(ROOT, dir, entry))) err(dir, `нет файла entry: ${entry}`);
  if (!existsSync(join(ROOT, dir, "README.md"))) err(dir, "нет README.md");
  for (const p of m.permissions ?? []) if (!PERMS.has(p)) err(dir, `неизвестное разрешение "${p}"`);
  if (new Set(m.permissions ?? []).size !== (m.permissions ?? []).length) err(dir, "разрешения повторяются");

  checkChangelog(dir, m);

  const seen = new Set();
  for (const s of m.settings ?? []) {
    const where = `settings[${s.key}]`;
    if (!/^[a-zA-Z][a-zA-Z0-9_]{0,31}$/.test(s.key ?? "")) err(dir, `${where}: некорректный key`);
    if (seen.has(s.key)) err(dir, `${where}: key повторяется`);
    seen.add(s.key);
    if (!String(s.label ?? "").trim()) err(dir, `${where}: нужен label`);
    if (!TYPES.has(s.type)) err(dir, `${where}: неизвестный type "${s.type}"`);
    if (s.type === "select" && !(s.options?.length)) err(dir, `${where}: select без options`);
    if (s.type === "select" && s.default !== undefined && !s.options?.some((o) => o.value === s.default)) err(dir, `${where}: default не из options`);
    if ((s.type === "number" || s.type === "range") && s.default !== undefined && typeof s.default !== "number") err(dir, `${where}: default должен быть числом`);
    if (s.type === "bool" && s.default !== undefined && typeof s.default !== "boolean") err(dir, `${where}: default должен быть boolean`);
  }
}

if (errors.length) {
  console.error(errors.map((e) => "✗ " + e).join("\n"));
  process.exit(1);
}
console.log("OK: " + readdirSync(ROOT).filter((d) => statSync(join(ROOT, d)).isDirectory()).join(", "));

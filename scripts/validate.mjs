// Проверка плагинов репозитория: node scripts/validate.mjs
// Правила повторяют проверки Overlay Composer (internal/plugins), чтобы ошибка находилась до установки.
import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join } from "node:path";

const ROOT = new URL("../plugins/", import.meta.url).pathname;
const TYPES = new Set(["string", "text", "number", "range", "bool", "color", "font", "select"]);
const PERMS = new Set(["chat.read", "chat.send", "storage", "net.local"]);
const errors = [];
const err = (plugin, msg) => errors.push(`${plugin}: ${msg}`);

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

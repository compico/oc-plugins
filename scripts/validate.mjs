// Проверка плагинов репозитория: node scripts/validate.mjs
// Правила повторяют проверки Overlay Composer (internal/plugins), чтобы ошибка находилась до установки.
import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join } from "node:path";
import { checkDeprecated } from "./deprecated.mjs";
import { checkChangelog } from "./changelog.mjs";

const ROOT = new URL("../plugins/", import.meta.url).pathname;
const TYPES = new Set(["string", "text", "number", "range", "bool", "color", "font", "select"]);
const PERMS = new Set(["chat.read", "chat.send", "storage", "net.local"]);
const errors = [];
const err = (plugin, msg) => errors.push(`${plugin}: ${msg}`);

// netLocal: какие локальные программы и пути доступны плагину (разрешение net.local); правила как в OC (internal/localnet).
function checkNetLocal(dir, m) {
  const rules = m.netLocal;
  if (rules === undefined) return;
  if (!Array.isArray(rules) || rules.length > 8) { err(dir, "netLocal — массив до 8 правил"); return; }
  if (!(m.permissions ?? []).includes("net.local")) err(dir, "netLocal требует разрешение net.local");
  rules.forEach((r, i) => {
    const where = `netLocal[${i}]`;
    if (Boolean(r?.target) === Boolean(r?.setting)) err(dir, `${where}: нужно ровно одно из target / setting`);
    if (r?.target && !/^(127\.0\.0\.1|localhost|\[::1\]):\d{1,5}$/i.test(r.target)) err(dir, `${where}: target — loopback host:port`);
    if (r?.setting) {
      const s = (m.settings ?? []).find((x) => x.key === r.setting);
      if (!s || s.type !== "string") err(dir, `${where}: setting "${r.setting}" должна быть строковой настройкой плагина`);
    }
    if (!Array.isArray(r?.paths) || r.paths.length < 1 || r.paths.length > 16) { err(dir, `${where}: paths — 1–16 префиксов`); return; }
    for (const p of r.paths) {
      let dec = p;
      try { dec = decodeURIComponent(String(p)); } catch { err(dir, `${where}: путь ${p} не раскодируется`); continue; }
      if (typeof p !== "string" || !p.startsWith("/") || p.startsWith("//") || /[\s\\]/.test(p) || dec.split("/").includes("..")) err(dir, `${where}: недопустимый путь ${JSON.stringify(p)}`);
    }
  });
}

// changelog.json (необязателен): формат и проверка — в scripts/changelog.mjs. OC показывает записи новее установленной версии.
function checkLog(dir, m) {
  for (const e of checkChangelog(join(ROOT, dir, "changelog.json"), m.version)) err(dir, e);
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

  checkNetLocal(dir, m);
  checkLog(dir, m);

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

// deprecated.json в корне репозитория (необязателен): устаревшие плагины и их замена.
{
  const f = new URL("../deprecated.json", import.meta.url).pathname;
  if (existsSync(f)) {
    let list;
    try { list = JSON.parse(readFileSync(f, "utf8")); } catch (e) { errors.push("deprecated.json: " + e.message); }
    if (list !== undefined) {
      const WASM = new URL("../wasm/", import.meta.url).pathname;
      const exists = (kind, id) => existsSync(join(kind === "widget" ? ROOT : WASM, id, kind === "widget" ? "plugin.json" : "go.mod"));
      errors.push(...checkDeprecated(list, exists));
    }
  }
}

if (errors.length) {
  console.error(errors.map((e) => "✗ " + e).join("\n"));
  process.exit(1);
}
console.log("OK: " + readdirSync(ROOT).filter((d) => statSync(join(ROOT, d)).isDirectory()).join(", "));

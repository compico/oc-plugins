// Проверка и выборка changelog.json: [{ "version", "date"?, "changes"?: [строки], "migration"?: "что сделать после обновления" }].
// Общий код для scripts/validate.mjs (виджеты, файл необязателен) и scripts/wasm.mjs (wasm, файл обязателен).
import { existsSync, readFileSync, statSync } from "node:fs";

export const MAX_CHANGELOG = 64 * 1024; // больше OC игнорирует

// Ошибки файла (массив строк); пустой массив — всё хорошо. version — текущая версия плагина: запись для неё обязательна.
export function checkChangelog(file, version, { required = false } = {}) {
  if (!existsSync(file)) return required ? ["нет changelog.json"] : [];
  if (statSync(file).size > MAX_CHANGELOG) return ["changelog.json больше 64 КБ — OC его проигнорирует"];
  let log;
  try { log = JSON.parse(readFileSync(file, "utf8")); } catch (e) { return ["changelog.json: " + e.message]; }
  if (!Array.isArray(log)) return ["changelog.json должен быть массивом"];
  const errs = [];
  const seen = new Set();
  log.forEach((c, i) => {
    const where = `changelog[${i}]`;
    if (!/^\d+\.\d+\.\d+$/.test(c?.version ?? "")) { errs.push(`${where}: version вида 1.2.3`); return; }
    if (seen.has(c.version)) errs.push(`${where}: версия ${c.version} повторяется`);
    seen.add(c.version);
    if (c.changes !== undefined && (!Array.isArray(c.changes) || c.changes.some((x) => typeof x !== "string" || !x.trim()))) errs.push(`${where}: changes — массив непустых строк`);
    if (c.migration !== undefined && (typeof c.migration !== "string" || !c.migration.trim())) errs.push(`${where}: migration — непустая строка`);
    if (!(c.changes?.length) && !c.migration) errs.push(`${where}: нужен changes или migration`);
  });
  if (!seen.has(version)) errs.push(`в changelog.json нет записи для текущей версии ${version}`);
  return errs;
}

export function compareVersions(a, b) {
  const x = a.split(".").map(Number), y = b.split(".").map(Number);
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] - y[i];
  return 0;
}

// Записи не новее version — для релиза старой версии (в нём не должно быть «будущего»).
export function upTo(file, version) {
  const log = JSON.parse(readFileSync(file, "utf8"));
  return log.filter((c) => compareVersions(c.version, version) <= 0);
}

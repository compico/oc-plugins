// Проверка deprecated.json — списка устаревших плагинов репозитория (читает Overlay Composer, см. README).
// Чистая функция: список записей и функция «есть ли плагин такого вида» → массив ошибок.

export const KINDS = new Set(["widget", "wasm"]);
const ID_RE = /^[A-Za-z0-9_-]{1,64}$/;
const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const DATE_RE = /^\d{4}-\d{2}-\d{2}$/;
export const MAX_REASON = 500;

/**
 * @param {unknown} list содержимое deprecated.json
 * @param {(kind: string, id: string) => boolean} exists есть ли в этом репозитории плагин (widget — plugins/<id>, wasm — wasm/<id>)
 * @param {string} [self] «owner/repo» этого репозитория: replacedBy.repo с ним же проверяется как локальный
 */
export function checkDeprecated(list, exists, self = "compico/oc-plugins") {
  const errors = [];
  const err = (where, msg) => errors.push(`deprecated.json ${where}: ${msg}`);
  if (!Array.isArray(list)) return ["deprecated.json: должен быть массивом записей"];
  if (list.length > 200) err("", "больше 200 записей");
  const seen = new Set();
  list.forEach((d, i) => {
    const where = `[${i}]`;
    if (typeof d !== "object" || d === null || Array.isArray(d)) return err(where, "запись должна быть объектом");
    if (!ID_RE.test(d.id ?? "")) err(where, "id: A-Za-z0-9_-, до 64");
    if (!KINDS.has(d.kind)) err(where, `kind: ${[...KINDS].join(" | ")}`);
    const key = `${d.kind}/${d.id}`;
    if (seen.has(key)) err(where, `${key} повторяется`);
    seen.add(key);
    if (typeof d.reason !== "string" || !d.reason.trim()) err(where, "нужна reason — почему плагин устарел и что делать");
    else if ([...d.reason].length > MAX_REASON) err(where, `reason длиннее ${MAX_REASON} символов`);
    if (d.since !== undefined && (!DATE_RE.test(d.since) || Number.isNaN(Date.parse(d.since)))) err(where, "since: дата ГГГГ-ММ-ДД");
    // Папка устаревшего плагина может отсутствовать (его удалили — запись остаётся), но если она есть, вид должен совпадать.
    if (KINDS.has(d.kind) && ID_RE.test(d.id ?? "")) {
      const other = d.kind === "widget" ? "wasm" : "widget";
      if (!exists(d.kind, d.id) && exists(other, d.id)) err(where, `${d.id} в репозитории — ${other}, а не ${d.kind}`);
    }
    if (d.replacedBy === undefined) return;
    const r = d.replacedBy;
    const rw = `${where}.replacedBy`;
    if (typeof r !== "object" || r === null || Array.isArray(r)) return err(rw, "должен быть объектом");
    if (!ID_RE.test(r.id ?? "")) err(rw, "id: A-Za-z0-9_-, до 64");
    if (!KINDS.has(r.kind)) err(rw, `kind: ${[...KINDS].join(" | ")}`);
    if (r.repo !== undefined && !REPO_RE.test(r.repo)) err(rw, "repo вида owner/repo");
    if (r.url !== undefined) {
      let u;
      try { u = new URL(r.url); } catch { err(rw, "url не разбирается"); }
      if (u && (u.protocol !== "https:" || r.url.length > 300)) err(rw, "url — https, до 300 символов");
    }
    if (r.kind === d.kind && r.id === d.id && (r.repo === undefined || r.repo.toLowerCase() === self.toLowerCase())) err(rw, "плагин не может заменять сам себя");
    // Замена из этого же репозитория обязана существовать: иначе пользователя пошлют в никуда.
    const local = r.repo === undefined || r.repo.toLowerCase() === self.toLowerCase();
    if (local && ID_RE.test(r.id ?? "") && KINDS.has(r.kind) && !exists(r.kind, r.id)) err(rw, `в репозитории нет ${r.kind}-плагина «${r.id}»`);
  });
  return errors;
}

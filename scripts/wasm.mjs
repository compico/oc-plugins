// Сборка и проверка wasm-плагинов (wasm/<id>/): node scripts/wasm.mjs <команда>
//   ids                    — id всех плагинов (папки wasm/* с go.mod)
//   changed <base-ref>     — id изменённых относительно base плагинов (для PR); «code» — менялся ли код, а не только README/тесты
//   build <id>...          — go vet + go test + сборка → dist/<id>.wasm, dist/<id>.json (метаданные из самого wasm, sha256, размер), dist/<id>.changelog.json (из wasm/<id>/changelog.json, обязателен)
//   changelog <id> <ver>   — wasm/<id>/changelog.json без записей новее <ver> (для релиза старой версии)
//   check-bump <id>        — после build: если версия из wasm уже выпущена (есть тег), а код менялся — ошибка «поднимите version»
//   meta <file.wasm>       — вывести метаданные, прочитанные из oc_meta
// Зависимостей нет: метаданные читает Node (node:wasi), как их читает OC (oc_meta на одноразовом экземпляре).
import { readFileSync, readdirSync, existsSync, mkdirSync, writeFileSync, statSync, copyFileSync } from "node:fs";
import { join, basename } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { WASI } from "node:wasi";
import { checkChangelog, upTo } from "./changelog.mjs";

const ROOT = fileURLToPath(new URL("..", import.meta.url));
const WASM = join(ROOT, "wasm");
const DIST = join(ROOT, "dist");
const PERMS = new Set(["storage", "net.http", "net.local", "twitch.redemption", "timer", "events.publish", "chat.send", "chat.command.read", "chat.read"]);
const EVENTS = new Set(["twitch.redemption", "chat.command", "chat.message", "timer", "http.response"]);
// Версия API плагинов (internal/wasmplugins/meta.go: APIVersion): максимальная, которую понимает OC, и с какой версии доступны право и событие.
const SURFACES = new Set(["overlay", "dashboard", "deck"]);
const MAX_API = 2;
const PERM_API = { "chat.read": 2 };
const EVENT_API = { "chat.message": 2 };

export function pluginIds() {
  if (!existsSync(WASM)) return [];
  return readdirSync(WASM).filter((d) => statSync(join(WASM, d)).isDirectory() && existsSync(join(WASM, d, "go.mod"))).sort();
}

/** Тег релиза плагина: music-v0.1.0. */
export const tagName = (id, version) => `${id}-v${version}`;

/** По списку изменённых файлов: какие плагины затронуты и менялся ли их код (не README, не changelog.json и не тесты). */
export function classifyChanges(files) {
  const changed = new Set();
  const code = new Set();
  for (const f of files) {
    const m = /^wasm\/([^/]+)\/(.+)$/.exec(f);
    if (!m) continue;
    changed.add(m[1]);
    if (!/\.md$/i.test(m[2]) && m[2] !== "changelog.json" && !/_test\.go$/.test(m[2])) code.add(m[1]);
  }
  return { changed: [...changed].sort(), code: [...code].sort() };
}

/** Убирает из результата classifyChanges плагины, которых больше нет (удалены или переименованы): собирать и выпускать нечего. */
export function onlyExisting(r, exists) {
  return { changed: r.changed.filter(exists), code: r.code.filter(exists) };
}

/** Читает метаданные из oc_meta: чистая функция, host_call недоступен (возвращает «пусто»). */
export async function readMeta(file) {
  const bytes = readFileSync(file);
  const wasi = new WASI({ version: "preview1" });
  const dec = new TextDecoder();
  let inst;
  const mod = await WebAssembly.compile(bytes);
  inst = await WebAssembly.instantiate(mod, { wasi_snapshot_preview1: wasi.wasiImport, oc: { host_call: () => 0n } });
  wasi.initialize(inst);
  const v = inst.exports.oc_meta();
  if (!v) throw new Error("oc_meta вернула пустой ответ");
  const ptr = Number(v >> 32n), len = Number(v & 0xffffffffn);
  const raw = new Uint8Array(inst.exports.memory.buffer, ptr, len).slice();
  inst.exports.dealloc(ptr);
  return JSON.parse(dec.decode(raw));
}

/**
 * <id>.json для опубликованного файла: то же, что пишет build, но метаданные берутся из самого .wasm, а не из пересборки.
 * Сходиться должно всё: sha256 файла с опубликованным, id и версия в oc_meta с тегом релиза.
 */
export function infoFromPublished({ id, version, bytes, meta, publishedSha }) {
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  if (publishedSha && sha256 !== publishedSha) throw new Error(`${id} ${version}: sha256 файла ${sha256} не совпадает с опубликованным ${publishedSha}`);
  if (meta.id !== id) throw new Error(`${id} ${version}: в oc_meta id «${meta.id}»`);
  if (meta.version !== version) throw new Error(`${id} ${version}: в oc_meta версия «${meta.version}»`);
  return { id, version, sha256, size: bytes.length, meta };
}

/** Лёгкая проверка метаданных (полную делает OC при загрузке): чтобы ошибка находилась до релиза. */
export function checkMeta(id, m) {
  const e = [];
  if (m.id !== id) e.push(`id в oc_meta («${m.id}») не совпадает с именем папки («${id}»)`);
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(m.id ?? "")) e.push("id: a-z, 0-9 и «-», до 64 символов");
  if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/.test(m.version ?? "")) e.push(`version «${m.version}»: нужен semver`);
  if (!Number.isInteger(m.apiVersion) || m.apiVersion < 1 || m.apiVersion > MAX_API) e.push(`apiVersion ${m.apiVersion}: нужно целое от 1 до ${MAX_API}`);
  if (!String(m.name ?? "").trim()) e.push("name обязателен");
  for (const p of m.permissions ?? []) {
    if (!PERMS.has(p)) e.push(`неизвестное разрешение «${p}»`);
    else if (m.apiVersion < (PERM_API[p] ?? 1)) e.push(`разрешение ${p} требует apiVersion не ниже ${PERM_API[p]}`);
  }
  for (const s of m.subscriptions ?? []) {
    if (!EVENTS.has(s)) e.push(`неизвестная подписка «${s}»`);
    else if (m.apiVersion < (EVENT_API[s] ?? 1)) e.push(`подписка ${s} требует apiVersion не ниже ${EVENT_API[s]}`);
  }
  if ((m.subscriptions ?? []).includes("twitch.redemption") && !(m.permissions ?? []).includes("twitch.redemption")) e.push("подписка twitch.redemption требует разрешения twitch.redemption");
  // Действия для панели Deck: правила те же, что у OC (internal/wasmplugins/meta.go): иначе плагин не запустится.
  const actions = m.actions ?? [];
  if (actions.length > 32) e.push("actions: не больше 32");
  const actionIds = new Set();
  for (const a of actions) {
    const where = `action «${a?.id}»`;
    if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(a?.id ?? "") || actionIds.has(a.id)) e.push(`${where}: id a-z0-9_- до 32 символов, уникален`);
    actionIds.add(a?.id);
    if (typeof a?.title !== "string" || !a.title.trim() || [...a.title].length > 40 || /[\r\n\t]/.test(a.title)) e.push(`${where}: нужен title до 40 символов в одну строку`);
    const method = a?.method || "POST";
    if (!(m.routes ?? []).some((r) => r.method === method && r.path === a?.path)) e.push(`${where}: маршрут ${method} ${a?.path} не объявлен в routes`);
    if (a?.body !== undefined && JSON.stringify(a.body).length > 1024) e.push(`${where}: body больше 1 КиБ`);
    if (a?.activeWhen) {
      const w = a.activeWhen;
      if (!(m.events ?? []).some((ev) => ev.id === w.topic)) e.push(`${where}: activeWhen.topic «${w.topic}» не объявлен в events`);
      if (w.field !== undefined && !/^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+){0,3}$/.test(w.field)) e.push(`${where}: activeWhen.field — путь вида a.b (до 4 частей)`);
      if (w.equals !== undefined && !["string", "number", "boolean"].includes(typeof w.equals)) e.push(`${where}: activeWhen.equals — строка, число или boolean`);
    }
  }
  for (const a of actions) {
    const d = a?.display;
    if (!d) continue;
    const where = `action «${a.id}»`;
    if (!(m.events ?? []).some((ev) => ev.id === d.topic)) e.push(`${where}: display.topic «${d.topic}» не объявлен в events`);
    if (!Array.isArray(d.fields) || d.fields.length < 1 || d.fields.length > 4) e.push(`${where}: display.fields — от 1 до 4 путей`);
    else for (const f of d.fields) if (!/^[A-Za-z0-9_]+(\.[A-Za-z0-9_]+){0,3}$/.test(f)) e.push(`${where}: display.fields — пути вида a.b (до 4 частей)`);
  }
  if ((m.subscriptions ?? []).includes("chat.command")) {
    if (!(m.permissions ?? []).includes("chat.command.read")) e.push("подписка chat.command требует разрешения chat.command.read");
    if (!(m.commands ?? []).length) e.push("подписка chat.command требует непустого commands");
  }
  if ((m.subscriptions ?? []).includes("chat.message") && !(m.permissions ?? []).includes("chat.read")) {
    e.push("подписка chat.message требует разрешения chat.read");
  }
  return e;
}

function run(cmd, args, opts) {
  const r = spawnSync(cmd, args, { stdio: "inherit", ...opts });
  if (r.error?.code === "ENOENT") throw new Error(`${cmd}: программа не найдена`);
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(" ")}: код ${r.status}`);
}

// ---- статика (виджеты): папка wasm/<id>/ui упаковывается в сам .wasm утилитой ocpack из Overlay Composer ----

/** Ревизия overlaycomposer, из которой берётся ocpack. Менять осознанно: формат секции oc.assets версионируется ревизией. */
export const OCPACK_REF = "b15977a627d5b2dbdefa12896863f61f72cd5400";
const TOOLS = join(ROOT, ".tools", "bin");

/** Файлы папки ui относительно неё (через «/»), без скрытых; null — папки нет. */
export function listUI(dir) {
  if (!existsSync(dir)) return null;
  const out = new Set();
  const walk = (d, rel) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      if (e.name.startsWith(".")) continue;
      if (e.isDirectory()) walk(join(d, e.name), `${rel}${e.name}/`);
      else out.add(`${rel}${e.name}`);
    }
  };
  walk(dir, "");
  return out;
}

/** Согласованность виджетов в метаданных и папки ui. files — результат listUI (null, если папки нет). */
export function checkWidgets(meta, files) {
  const e = [];
  const widgets = meta.widgets ?? [];
  if (widgets.length && !files) e.push("виджеты объявлены, а папки ui/ нет");
  if (files && !widgets.length) e.push("папка ui/ есть, но виджеты в oc_meta не объявлены (Meta.Widgets)");
  for (const w of widgets) if (files && !files.has(w.entry)) e.push(`виджет «${w.id}»: файла ui/${w.entry} нет`);
  // Области виджета (internal/wasmplugins/meta.go: Surface*); пусто — только сцена.
  for (const w of widgets) for (const s of w.surfaces ?? []) if (!SURFACES.has(s)) e.push(`виджет «${w.id}»: неизвестная область «${s}» (overlay, dashboard, deck)`);
  return e;
}

/** Путь к ocpack: переменная OCPACK, затем PATH, затем .tools/bin (установка из OCPACK_REF при первой надобности). */
function ocpackPath() {
  if (process.env.OCPACK) return process.env.OCPACK;
  const exe = process.platform === "win32" ? "ocpack.exe" : "ocpack";
  if (spawnSync("ocpack", ["list"], { stdio: "ignore" }).error?.code !== "ENOENT") return "ocpack";
  const local = join(TOOLS, exe);
  if (!existsSync(local)) {
    console.log(`ocpack не найден — ставлю из overlaycomposer@${OCPACK_REF.slice(0, 12)} в .tools/bin`);
    run("go", ["install", "-trimpath", `github.com/compico/overlaycomposer/cmd/ocpack@${OCPACK_REF}`], {
      env: { ...process.env, GOBIN: TOOLS, CGO_ENABLED: "0" },
    });
  }
  return local;
}

export async function build(id) {
  const dir = join(WASM, id);
  if (!existsSync(join(dir, "go.mod"))) throw new Error(`${id}: нет wasm/${id}/go.mod`);
  run("go", ["vet", "./..."], { cwd: dir });
  run("go", ["test", "-race", "-count=1", "./..."], { cwd: dir });
  mkdirSync(DIST, { recursive: true });
  const out = join(DIST, `${id}.wasm`);
  run("go", ["build", "-trimpath", "-ldflags=-s -w", "-buildmode=c-shared", "-o", out, "."], {
    cwd: dir,
    env: { ...process.env, GOOS: "wasip1", GOARCH: "wasm", CGO_ENABLED: "0" },
  });
  const ui = listUI(join(dir, "ui"));
  if (ui) run(ocpackPath(), ["pack", out, join(dir, "ui")]); // до метаданных и sha256: хеш считается от готового файла
  const meta = await readMeta(out);
  const errs = [...checkMeta(id, meta), ...checkWidgets(meta, ui)];
  const log = join(dir, "changelog.json");
  errs.push(...checkChangelog(log, meta.version, { required: true }));
  if (errs.length) throw new Error(`${id}: ${errs.join("; ")}`);
  copyFileSync(log, join(DIST, `${id}.changelog.json`));
  const data = readFileSync(out);
  const info = { id, version: meta.version, sha256: createHash("sha256").update(data).digest("hex"), size: data.length, meta };
  writeFileSync(join(DIST, `${id}.json`), JSON.stringify(info, null, 2) + "\n");
  writeFileSync(join(DIST, `${id}.wasm.sha256`), `${info.sha256}  ${id}.wasm\n`);
  console.log(`${id} ${meta.version}: ${(data.length / 1048576).toFixed(1)} МБ, sha256 ${info.sha256}`);
  return info;
}

async function main(argv) {
  const [cmd, ...args] = argv;
  switch (cmd) {
    case "ids":
      console.log(pluginIds().join("\n"));
      return;
    case "changed": {
      const base = args[0];
      if (!base) throw new Error("changed <base-ref>");
      const files = execFileSync("git", ["diff", "--name-only", `${base}...HEAD`], { cwd: ROOT, encoding: "utf8" }).split("\n").filter(Boolean);
      console.log(JSON.stringify(onlyExisting(classifyChanges(files), (id) => existsSync(join(ROOT, "wasm", id, "go.mod")))));
      return;
    }
    case "build":
      if (!args.length) throw new Error("build <id>...");
      for (const id of args) await build(id);
      return;
    case "check-bump": {
      const id = args[0];
      const info = JSON.parse(readFileSync(join(DIST, `${id}.json`), "utf8"));
      const tag = tagName(id, info.version);
      const exists = spawnSync("git", ["rev-parse", "-q", "--verify", `refs/tags/${tag}`], { cwd: ROOT }).status === 0;
      if (exists) throw new Error(`${id}: код изменился, а версия ${info.version} уже выпущена (тег ${tag}). Поднимите version в main.go.`);
      console.log(`${id}: версия ${info.version} ещё не выпускалась — ок`);
      return;
    }
    case "describe": {
      // describe <id> <version> <file.wasm> <file.wasm.sha256> — JSON метаданных опубликованного релиза (в stdout)
      const [id, version, file, shaFile] = args;
      if (!id || !version || !file || !shaFile) throw new Error("describe <id> <version> <file.wasm> <file.wasm.sha256>");
      const publishedSha = readFileSync(shaFile, "utf8").trim().split(/\s+/)[0];
      const info = infoFromPublished({ id, version, bytes: readFileSync(file), meta: await readMeta(file), publishedSha });
      console.log(JSON.stringify(info, null, 2));
      return;
    }
    case "changelog": {
      // changelog <id> <version> — changelog.json плагина без записей новее version (для релиза старой версии), в stdout
      const [id, version] = args;
      if (!id || !version) throw new Error("changelog <id> <version>");
      console.log(JSON.stringify(upTo(join(WASM, id, "changelog.json"), version), null, 2));
      return;
    }
    case "meta":
      console.log(JSON.stringify(await readMeta(args[0]), null, 2));
      return;
    default:
      throw new Error("команды: ids | changed <base> | build <id>... | check-bump <id> | meta <file.wasm> | describe <id> <version> <file> <sha-file>");
  }
}

if (process.argv[1] && basename(process.argv[1]) === "wasm.mjs") {
  main(process.argv.slice(2)).catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
}

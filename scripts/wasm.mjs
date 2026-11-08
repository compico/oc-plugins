// Сборка и проверка wasm-плагинов (wasm/<id>/): node scripts/wasm.mjs <команда>
//   ids                    — id всех плагинов (папки wasm/* с go.mod)
//   changed <base-ref>     — id изменённых относительно base плагинов (для PR); «code» — менялся ли код, а не только README/тесты
//   build <id>...          — go vet + go test + сборка → dist/<id>.wasm, dist/<id>.json (метаданные из самого wasm, sha256, размер)
//   check-bump <id>        — после build: если версия из wasm уже выпущена (есть тег), а код менялся — ошибка «поднимите version»
//   meta <file.wasm>       — вывести метаданные, прочитанные из oc_meta
// Зависимостей нет: метаданные читает Node (node:wasi), как их читает OC (oc_meta на одноразовом экземпляре).
import { readFileSync, readdirSync, existsSync, mkdirSync, writeFileSync, statSync } from "node:fs";
import { join, basename } from "node:path";
import { fileURLToPath } from "node:url";
import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import { WASI } from "node:wasi";

const ROOT = fileURLToPath(new URL("..", import.meta.url));
const WASM = join(ROOT, "wasm");
const DIST = join(ROOT, "dist");
const PERMS = new Set(["storage", "net.http", "twitch.redemption", "timer"]);
const EVENTS = new Set(["twitch.redemption", "timer", "http.response"]);

export function pluginIds() {
  if (!existsSync(WASM)) return [];
  return readdirSync(WASM).filter((d) => statSync(join(WASM, d)).isDirectory() && existsSync(join(WASM, d, "go.mod"))).sort();
}

/** Тег релиза плагина: music-v0.1.0. */
export const tagName = (id, version) => `${id}-v${version}`;

/** По списку изменённых файлов: какие плагины затронуты и менялся ли их код (не README и не тесты). */
export function classifyChanges(files) {
  const changed = new Set();
  const code = new Set();
  for (const f of files) {
    const m = /^wasm\/([^/]+)\/(.+)$/.exec(f);
    if (!m) continue;
    changed.add(m[1]);
    if (!/\.md$/i.test(m[2]) && !/_test\.go$/.test(m[2])) code.add(m[1]);
  }
  return { changed: [...changed].sort(), code: [...code].sort() };
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

/** Лёгкая проверка метаданных (полную делает OC при загрузке): чтобы ошибка находилась до релиза. */
export function checkMeta(id, m) {
  const e = [];
  if (m.id !== id) e.push(`id в oc_meta («${m.id}») не совпадает с именем папки («${id}»)`);
  if (!/^[a-z0-9][a-z0-9-]{0,63}$/.test(m.id ?? "")) e.push("id: a-z, 0-9 и «-», до 64 символов");
  if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$/.test(m.version ?? "")) e.push(`version «${m.version}»: нужен semver`);
  if (m.apiVersion !== 1) e.push(`apiVersion ${m.apiVersion}: хост понимает 1`);
  if (!String(m.name ?? "").trim()) e.push("name обязателен");
  for (const p of m.permissions ?? []) if (!PERMS.has(p)) e.push(`неизвестное разрешение «${p}»`);
  for (const s of m.subscriptions ?? []) if (!EVENTS.has(s)) e.push(`неизвестная подписка «${s}»`);
  if ((m.subscriptions ?? []).includes("twitch.redemption") && !(m.permissions ?? []).includes("twitch.redemption")) e.push("подписка twitch.redemption требует разрешения twitch.redemption");
  return e;
}

function run(cmd, args, opts) {
  const r = spawnSync(cmd, args, { stdio: "inherit", ...opts });
  if (r.error?.code === "ENOENT") throw new Error(`${cmd}: программа не найдена`);
  if (r.status !== 0) throw new Error(`${cmd} ${args.join(" ")}: код ${r.status}`);
}

// ---- статика (виджеты): папка wasm/<id>/ui упаковывается в сам .wasm утилитой ocpack из Overlay Composer ----

/** Ревизия overlaycomposer, из которой берётся ocpack. Менять осознанно: формат секции oc.assets версионируется ревизией. */
export const OCPACK_REF = "4eb6541b0d2b7dcc2b464205f5870496ce37b853";
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
  if (errs.length) throw new Error(`${id}: ${errs.join("; ")}`);
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
      console.log(JSON.stringify(classifyChanges(files)));
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
    case "meta":
      console.log(JSON.stringify(await readMeta(args[0]), null, 2));
      return;
    default:
      throw new Error("команды: ids | changed <base> | build <id>... | check-bump <id> | meta <file.wasm>");
  }
}

if (process.argv[1] && basename(process.argv[1]) === "wasm.mjs") {
  main(process.argv.slice(2)).catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
}

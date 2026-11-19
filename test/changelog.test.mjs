import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readdirSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkChangelog, upTo, compareVersions } from "../scripts/changelog.mjs";
import { pluginIds } from "../scripts/wasm.mjs";

const tmp = mkdtempSync(join(tmpdir(), "chlog-"));
function write(name, data) {
  const f = join(tmp, name);
  writeFileSync(f, typeof data === "string" ? data : JSON.stringify(data));
  return f;
}
const ok = [{ version: "1.0.0", changes: ["a"] }, { version: "1.1.0", migration: "сделайте б" }];

test("корректный файл", () => assert.deepEqual(checkChangelog(write("ok.json", ok), "1.1.0"), []));
test("нет файла: необязателен / обязателен", () => {
  assert.deepEqual(checkChangelog(join(tmp, "none.json"), "1.0.0"), []);
  assert.equal(checkChangelog(join(tmp, "none.json"), "1.0.0", { required: true }).length, 1);
});
test("нет записи для текущей версии", () => {
  assert.match(checkChangelog(write("a.json", ok), "1.2.0")[0], /нет записи для текущей версии 1\.2\.0/);
});
test("плохие записи", () => {
  const errs = checkChangelog(write("bad.json", [{ version: "1.0", changes: ["a"] }, { version: "2.0.0" }, { version: "2.0.0", changes: [""] }]), "2.0.0");
  assert.ok(errs.some((e) => e.includes("version вида")));
  assert.ok(errs.some((e) => e.includes("нужен changes или migration")));
  assert.ok(errs.some((e) => e.includes("повторяется")));
  assert.ok(errs.some((e) => e.includes("changes — массив")));
});
test("не JSON и не массив", () => {
  assert.match(checkChangelog(write("x.json", "{"), "1.0.0")[0], /changelog\.json:/);
  assert.match(checkChangelog(write("y.json", {}), "1.0.0")[0], /массивом/);
});
test("compareVersions и upTo", () => {
  assert.ok(compareVersions("0.10.0", "0.9.0") > 0);
  assert.deepEqual(upTo(write("u.json", ok), "1.0.5").map((c) => c.version), ["1.0.0"]);
});
test("у каждого wasm-плагина репозитория changelog.json верный", async () => {
  for (const id of pluginIds()) {
    const f = join(import.meta.dirname, "..", "wasm", id, "changelog.json");
    assert.ok(existsSync(f), `${id}: нет changelog.json`);
    const src = (await import("node:fs")).readFileSync(join(import.meta.dirname, "..", "wasm", id, "main.go"), "utf8");
    const v = /version\s*=\s*"(\d+\.\d+\.\d+)"/.exec(src)?.[1];
    assert.ok(v, `${id}: версия не найдена в main.go`);
    assert.deepEqual(checkChangelog(f, v, { required: true }), [], id);
  }
});

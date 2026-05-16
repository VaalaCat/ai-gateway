import { test } from "node:test";
import assert from "node:assert/strict";
import { flattenKeys } from "./check-i18n.mjs";

test("flattenKeys: 嵌套对象返回点分隔的叶子路径", () => {
  const input = { a: { b: "x" }, c: "y" };
  const result = flattenKeys(input);
  assert.deepEqual(result.sort(), ["a.b", "c"]);
});

test("flattenKeys: 空对象返回空数组", () => {
  assert.deepEqual(flattenKeys({}), []);
});

test("flattenKeys: 深度嵌套", () => {
  const input = { a: { b: { c: { d: "leaf" } } } };
  assert.deepEqual(flattenKeys(input), ["a.b.c.d"]);
});

test("flattenKeys: 数组当作 leaf 处理（不深入）", () => {
  const input = { a: ["x", "y"] };
  assert.deepEqual(flattenKeys(input), ["a"]);
});

import { findPlaceholderValues } from "./check-i18n.mjs";

test("findPlaceholderValues: 正常 value 不报", () => {
  const result = findPlaceholderValues({ a: "Apple", b: { c: "Cherry" } });
  assert.deepEqual(result, []);
});

test("findPlaceholderValues: 空字符串报 empty", () => {
  const result = findPlaceholderValues({ a: "" });
  assert.deepEqual(result, [{ key: "a", value: "", reason: "empty" }]);
});

test("findPlaceholderValues: 仅空白也报 empty", () => {
  const result = findPlaceholderValues({ a: "  " });
  assert.deepEqual(result, [{ key: "a", value: "  ", reason: "empty" }]);
});

test("findPlaceholderValues: value 与 key 末段完全相同报 echo", () => {
  const result = findPlaceholderValues({ a: { b: "b" } });
  assert.deepEqual(result, [{ key: "a.b", value: "b", reason: "echo" }]);
});

test("findPlaceholderValues: 首字大写不报（合理翻译）", () => {
  const result = findPlaceholderValues({ cancel: "Cancel" });
  assert.deepEqual(result, []);
});

test("findPlaceholderValues: 嵌套深度多个违规", () => {
  const result = findPlaceholderValues({
    a: { b: "" },
    c: { d: "d" },
    e: "valid",
  });
  assert.deepEqual(result.sort((x, y) => x.key.localeCompare(y.key)), [
    { key: "a.b", value: "", reason: "empty" },
    { key: "c.d", value: "d", reason: "echo" },
  ]);
});

import { extractCodeReferences } from "./check-i18n.mjs";
import { mkdtempSync, writeFileSync, rmSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function mkFixture(files) {
  const dir = mkdtempSync(join(tmpdir(), "i18n-test-"));
  for (const [path, content] of Object.entries(files)) {
    const full = join(dir, path);
    mkdirSync(join(full, ".."), { recursive: true });
    writeFileSync(full, content);
  }
  return dir;
}

test("extractCodeReferences: 单 namespace + 字面量 t()", () => {
  const dir = mkFixture({
    "a.tsx": `
const t = useTranslations("agentRoutes");
return <div>{t("title")}</div>;
`,
  });
  try {
    const { static: refs, dynamic } = extractCodeReferences(dir);
    assert.equal(refs.length, 1);
    assert.equal(refs[0].key, "agentRoutes.title");
    assert.equal(dynamic.length, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 多 namespace 各走各的变量", () => {
  const dir = mkFixture({
    "b.tsx": `
const t = useTranslations("agentRoutes");
const tc = useTranslations("common");
return <>{t("title")} {tc("cancel")}</>;
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    const keys = refs.map((r) => r.key).sort();
    assert.deepEqual(keys, ["agentRoutes.title", "common.cancel"]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 模板字符串进 dynamic", () => {
  const dir = mkFixture({
    "c.tsx": `
const t = useTranslations("agentRoutes");
return t(\`status.\${x}\`);
`,
  });
  try {
    const { static: refs, dynamic } = extractCodeReferences(dir);
    assert.equal(refs.length, 0);
    assert.equal(dynamic.length, 1);
    assert.match(dynamic[0].snippet, /status/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 变量传入进 dynamic", () => {
  const dir = mkFixture({
    "d.tsx": `
const t = useTranslations("agentRoutes");
const k = "title";
return t(k);
`,
  });
  try {
    const { static: refs, dynamic } = extractCodeReferences(dir);
    assert.equal(refs.length, 0);
    assert.equal(dynamic.length, 1);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 行尾 i18n-skip 跳过该行", () => {
  const dir = mkFixture({
    "e.tsx": `
const t = useTranslations("agentRoutes");
const x = t("dynamicKey"); // i18n-skip
const y = t("realKey");
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    const keys = refs.map((r) => r.key);
    assert.ok(!keys.includes("agentRoutes.dynamicKey"));
    assert.ok(keys.includes("agentRoutes.realKey"));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 文件级 i18n-skip-file 跳过整个文件", () => {
  const dir = mkFixture({
    "f.tsx": `/* i18n-skip-file */
const t = useTranslations("agentRoutes");
return t("anything");
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    assert.equal(refs.length, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: getTranslations 同样识别", () => {
  const dir = mkFixture({
    "g.ts": `
const t = await getTranslations("dashboard");
return t("welcome");
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    assert.equal(refs.length, 1);
    assert.equal(refs[0].key, "dashboard.welcome");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 嵌套子目录递归扫", () => {
  const dir = mkFixture({
    "components/inner/h.tsx": `
const t = useTranslations("ns");
return t("nested");
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    assert.equal(refs.length, 1);
    assert.equal(refs[0].key, "ns.nested");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 非翻译变量调用被忽略", () => {
  const dir = mkFixture({
    "i.tsx": `
import { someUtil } from "./util";
const result = someUtil("foo.bar");
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    assert.equal(refs.length, 0);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("extractCodeReferences: 同名变量在不同函数作用域用不同 ns", () => {
  const dir = mkFixture({
    "j.tsx": `
export function StatusBadge() {
  const t = useTranslations("common");
  return t("enabled");
}

export function RoleBadge() {
  const t = useTranslations("users");
  return t("roleAdmin");
}

export function OnlineBadge() {
  const t = useTranslations("agents");
  return t("online");
}
`,
  });
  try {
    const { static: refs } = extractCodeReferences(dir);
    const keys = refs.map((r) => r.key).sort();
    assert.deepEqual(keys, ["agents.online", "common.enabled", "users.roleAdmin"]);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

import {
  checkKeysAligned,
  checkPlaceholderValues,
  checkCodeReferencesExist,
} from "./check-i18n.mjs";

test("checkKeysAligned: 完全对齐返回空", () => {
  assert.deepEqual(checkKeysAligned(["a", "b.c"], ["a", "b.c"]), []);
});

test("checkKeysAligned: zh 多的 key 报缺失于 en", () => {
  const v = checkKeysAligned(["a", "b"], ["a"]);
  assert.deepEqual(v, [{ type: "missing", key: "b", side: "en" }]);
});

test("checkKeysAligned: en 多的 key 报缺失于 zh", () => {
  const v = checkKeysAligned(["a"], ["a", "b"]);
  assert.deepEqual(v, [{ type: "missing", key: "b", side: "zh" }]);
});

test("checkKeysAligned: 深度不同也能识别", () => {
  const v = checkKeysAligned(["a.b.c"], ["a.b"]);
  assert.equal(v.length, 2);
  assert.ok(v.some((x) => x.key === "a.b.c" && x.side === "en"));
  assert.ok(v.some((x) => x.key === "a.b" && x.side === "zh"));
});

test("checkPlaceholderValues: 双语 violation 都收集且标 side", () => {
  const v = checkPlaceholderValues({ a: "" }, { a: "a" });
  assert.equal(v.length, 2);
  assert.ok(v.some((x) => x.side === "zh" && x.reason === "empty"));
  assert.ok(v.some((x) => x.side === "en" && x.reason === "echo"));
});

test("checkPlaceholderValues: 全部合法返回空", () => {
  const v = checkPlaceholderValues({ a: "中文" }, { a: "English" });
  assert.deepEqual(v, []);
});

test("checkCodeReferencesExist: 全部命中返回空", () => {
  const refs = {
    static: [{ key: "ns.foo", file: "x.tsx", line: 1 }],
    dynamic: [],
  };
  assert.deepEqual(checkCodeReferencesExist(refs, ["ns.foo", "ns.bar"]), []);
});

test("checkCodeReferencesExist: 缺失 key 报 violation 含 file/line", () => {
  const refs = {
    static: [
      { key: "ns.exists", file: "a.tsx", line: 1 },
      { key: "ns.missing", file: "b.tsx", line: 5 },
    ],
    dynamic: [],
  };
  const v = checkCodeReferencesExist(refs, ["ns.exists"]);
  assert.equal(v.length, 1);
  assert.deepEqual(v[0], { type: "missing-ref", key: "ns.missing", file: "b.tsx", line: 5 });
});

import { formatReport } from "./check-i18n.mjs";

test("formatReport: 空 violations 返回空串", () => {
  assert.equal(formatReport([]), "");
});

test("formatReport: 3 类 violation 都格式化", () => {
  const report = formatReport([
    { type: "missing", key: "a.b", side: "en" },
    { key: "c", value: "", reason: "empty", side: "zh" },
    { type: "missing-ref", key: "ns.foo", file: "x.tsx", line: 1 },
  ]);
  assert.match(report, /Keys not aligned/);
  assert.match(report, /Placeholder/);
  assert.match(report, /Code references/);
  assert.match(report, /3 violations/);
});

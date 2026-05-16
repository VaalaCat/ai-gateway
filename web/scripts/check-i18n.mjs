/**
 * 把嵌套对象拍平成点分隔的 key 路径数组。
 * 数组、null、字符串、数字都当 leaf 处理（不深入）。
 */
export function flattenKeys(obj, prefix = "") {
  const result = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      result.push(...flattenKeys(v, path));
    } else {
      result.push(path);
    }
  }
  return result;
}

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, extname } from "node:path";

function walkFiles(dir, exts) {
  const result = [];
  for (const name of readdirSync(dir)) {
    if (name === "node_modules" || name.startsWith(".")) continue;
    const full = join(dir, name);
    const stat = statSync(full);
    if (stat.isDirectory()) {
      result.push(...walkFiles(full, exts));
    } else if (exts.includes(extname(name))) {
      result.push(full);
    }
  }
  return result;
}

/**
 * 扫源码收集 i18n key 引用：
 *   - 解析 `const VAR = useTranslations("NS")` 收集所有声明（含 line 号）
 *   - 对每个 VAR("literal") 调用，向上回溯找最近的同名声明 → 该作用域的 ns
 *   - 模板字符串、变量参数 → dynamic（不参与对比）
 *   - 支持 `// i18n-skip` 行内、`/* i18n-skip-file *\/` 文件级
 */
export function extractCodeReferences(rootDir) {
  const staticRefs = [];
  const dynamic = [];
  const files = walkFiles(rootDir, [".ts", ".tsx"]);

  for (const file of files) {
    const text = readFileSync(file, "utf8");
    if (text.includes("/* i18n-skip-file */")) continue;

    const lines = text.split("\n");

    // 收集所有声明：{line, varName, ns}
    const declarations = [];
    for (let i = 0; i < lines.length; i++) {
      const m = lines[i].match(
        /(?:const|let|var)\s+(\w+)\s*=\s*(?:await\s+)?(?:useTranslations|getTranslations)\s*\(\s*["']([^"']+)["']\s*\)/,
      );
      if (m) {
        declarations.push({ line: i + 1, varName: m[1], ns: m[2] });
      }
    }
    if (declarations.length === 0) continue;

    // 知道所有声明过的翻译变量名
    const varNames = new Set(declarations.map((d) => d.varName));
    const varAlt = [...varNames]
      .map((v) => v.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))
      .join("|");
    const literalRe = new RegExp(
      `\\b(${varAlt})\\(\\s*["']([^"']+)["']\\s*\\)`,
      "g",
    );
    const callRe = new RegExp(`\\b(${varAlt})\\(\\s*([^"'])`, "g");

    // 给定调用行号 + 变量名，找最近的、行号 ≤ 调用行号的同名声明
    function nsForCall(varName, callLine) {
      let best = null;
      for (const d of declarations) {
        if (d.varName === varName && d.line <= callLine) {
          if (!best || d.line > best.line) best = d;
        }
      }
      return best?.ns;
    }

    for (let i = 0; i < lines.length; i++) {
      const line = lines[i];
      if (line.includes("// i18n-skip")) continue;

      literalRe.lastIndex = 0;
      let lm;
      while ((lm = literalRe.exec(line)) !== null) {
        const ns = nsForCall(lm[1], i + 1);
        if (!ns) continue;
        staticRefs.push({ key: `${ns}.${lm[2]}`, file, line: i + 1 });
      }

      callRe.lastIndex = 0;
      while (callRe.exec(line) !== null) {
        dynamic.push({ file, line: i + 1, snippet: line.trim() });
      }
    }
  }

  return { static: staticRefs, dynamic };
}

/**
 * 找出疑似未填写的占位值：
 * - 空字符串或仅空白
 * - value 与 key 末段完全相同（严格大小写）
 */
export function findPlaceholderValues(obj, prefix = "") {
  const result = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v !== null && typeof v === "object" && !Array.isArray(v)) {
      result.push(...findPlaceholderValues(v, path));
    } else if (typeof v === "string") {
      if (v.trim() === "") {
        result.push({ key: path, value: v, reason: "empty" });
      } else if (v === k) {
        result.push({ key: path, value: v, reason: "echo" });
      }
    }
  }
  return result;
}

/**
 * 对比中英 key 集合，输出缺失项。
 */
export function checkKeysAligned(zhKeys, enKeys) {
  const zh = new Set(zhKeys);
  const en = new Set(enKeys);
  const violations = [];
  for (const k of zh) {
    if (!en.has(k)) violations.push({ type: "missing", key: k, side: "en" });
  }
  for (const k of en) {
    if (!zh.has(k)) violations.push({ type: "missing", key: k, side: "zh" });
  }
  return violations;
}

/**
 * 在中英两份 JSON 上跑 findPlaceholderValues 并标注语言。
 */
export function checkPlaceholderValues(zhObj, enObj) {
  return [
    ...findPlaceholderValues(zhObj).map((v) => ({ ...v, side: "zh" })),
    ...findPlaceholderValues(enObj).map((v) => ({ ...v, side: "en" })),
  ];
}

/**
 * 代码引用的 key 必须存在于 zh.json，否则报 violation。
 */
export function checkCodeReferencesExist(codeRefs, zhKeys) {
  const zhSet = new Set(zhKeys);
  const violations = [];
  for (const ref of codeRefs.static) {
    if (!zhSet.has(ref.key)) {
      violations.push({ type: "missing-ref", key: ref.key, file: ref.file, line: ref.line });
    }
  }
  return violations;
}

import { fileURLToPath } from "node:url";

const ZH_PATH = "src/i18n/zh.json";
const EN_PATH = "src/i18n/en.json";
const CODE_ROOT = "src";

export function formatReport(violations) {
  if (violations.length === 0) return "";
  const lines = ["✗ i18n check failed", ""];
  const aligned = violations.filter((v) => v.type === "missing");
  const placeholder = violations.filter((v) => v.reason);
  const missingRef = violations.filter((v) => v.type === "missing-ref");

  if (aligned.length > 0) {
    lines.push(`[1] Keys not aligned between zh.json / en.json (${aligned.length}):`);
    for (const v of aligned) {
      lines.push(`  - missing in ${v.side}.json: ${v.key}`);
    }
    lines.push("");
  }
  if (placeholder.length > 0) {
    lines.push(`[2] Placeholder values (${placeholder.length}):`);
    for (const v of placeholder) {
      lines.push(`  - ${v.side}.json: ${v.key} = ${JSON.stringify(v.value)} (${v.reason})`);
    }
    lines.push("");
  }
  if (missingRef.length > 0) {
    lines.push(`[3] Code references not found in JSON (${missingRef.length}):`);
    for (const v of missingRef) {
      lines.push(`  - ${v.file}:${v.line} → ${v.key}`);
    }
    lines.push("");
  }
  lines.push(`${violations.length} violations.`);
  return lines.join("\n");
}

export async function main() {
  const zhObj = JSON.parse(readFileSync(ZH_PATH, "utf8"));
  const enObj = JSON.parse(readFileSync(EN_PATH, "utf8"));
  const zhKeys = flattenKeys(zhObj);
  const enKeys = flattenKeys(enObj);
  const codeRefs = extractCodeReferences(CODE_ROOT);

  const violations = [
    ...checkKeysAligned(zhKeys, enKeys),
    ...checkPlaceholderValues(zhObj, enObj),
    ...checkCodeReferencesExist(codeRefs, zhKeys),
  ];

  if (violations.length === 0) {
    console.log("✓ i18n check passed");
    console.log(`  - zh.json: ${zhKeys.length} keys`);
    console.log(`  - en.json: ${enKeys.length} keys`);
    console.log(
      `  - code references: ${codeRefs.static.length} static, ${codeRefs.dynamic.length} dynamic (skipped)`,
    );
    return 0;
  }

  console.error(formatReport(violations));
  return 1;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  main().then((code) => process.exit(code));
}

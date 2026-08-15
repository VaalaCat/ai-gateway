import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { createTranslator } from "next-intl";

type Messages = Record<string, unknown>;

const localeFiles = {
  en: resolve("src/i18n/en.json"),
  zh: resolve("src/i18n/zh.json"),
} as const;

const sampleValues: Record<string, string | number | Date> = {
  count: 2,
  size: 20,
  max: 5,
  input: "$1.00",
  output: "$2.00",
  cacheRead: "$0.50",
  cacheWrite: "$0.75",
  value: "99.90%",
  bytes: 1024,
  name: "Example Token",
  weight: 1,
  priority: 0,
  available: 2,
  total: 3,
  window: "24h",
  offer: "OpenAI Direct",
  position: 1,
  subject: "Example item",
};

function objectAtPath(messages: Messages, path: string) {
  return path.split(".").reduce<unknown>((value, segment) => {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      return undefined;
    }
    return (value as Messages)[segment];
  }, messages);
}

function flattenStringMessages(value: unknown, prefix = ""): Map<string, string> {
  const result = new Map<string, string>();
  if (typeof value === "string") {
    result.set(prefix, value);
    return result;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return result;
  }
  for (const [key, child] of Object.entries(value)) {
    const childPrefix = prefix ? `${prefix}.${key}` : key;
    for (const [path, message] of flattenStringMessages(child, childPrefix)) {
      result.set(path, message);
    }
  }
  return result;
}

function verifyLocale(locale: keyof typeof localeFiles, namespace: string) {
  const messages = JSON.parse(readFileSync(localeFiles[locale], "utf8")) as Messages;
  const namespaceMessages = objectAtPath(messages, namespace);
  if (typeof namespaceMessages !== "object" || namespaceMessages === null || Array.isArray(namespaceMessages)) {
    throw new Error(`${locale}: namespace ${namespace} is missing or is not an object`);
  }

  const leaves = flattenStringMessages(namespaceMessages);
  if (leaves.size === 0) throw new Error(`${locale}: namespace ${namespace} has no messages`);
  const failures: string[] = [];
  const runtimeErrors: string[] = [];
  const translate = createTranslator({
    locale,
    messages,
    namespace,
    onError(error) {
      runtimeErrors.push(error.message);
    },
  });
  const translateRuntime = translate as unknown as (
    key: string,
    values: Record<string, string | number | Date>,
  ) => string;

  for (const [key, message] of leaves) {
    if (message.trim().length === 0) {
      failures.push(`${key}: empty message`);
      continue;
    }
    runtimeErrors.length = 0;
    let formatted: string;
    try {
      formatted = translateRuntime(key, sampleValues);
    } catch (error) {
      failures.push(`${key}: ${error instanceof Error ? error.message : String(error)}`);
      continue;
    }
    if (runtimeErrors.length > 0) failures.push(`${key}: ${runtimeErrors.join("; ")}`);
    if (typeof formatted !== "string" || formatted.trim().length === 0) {
      failures.push(`${key}: formatted to an empty value`);
    }
  }
  if (failures.length > 0) {
    throw new Error(`${locale}: runtime verification failed\n  - ${failures.join("\n  - ")}`);
  }
  return { messages, leaves };
}

function main() {
  const namespace = process.argv[2]?.trim();
  if (!namespace) {
    throw new Error("usage: pnpm exec tsx scripts/verify-i18n-keys.ts <namespace>");
  }
  const en = verifyLocale("en", namespace);
  const zh = verifyLocale("zh", namespace);
  const enKeys = new Set(en.leaves.keys());
  const zhKeys = new Set(zh.leaves.keys());
  const missingInZh = [...enKeys].filter((key) => !zhKeys.has(key));
  const missingInEn = [...zhKeys].filter((key) => !enKeys.has(key));
  if (missingInZh.length > 0 || missingInEn.length > 0) {
    throw new Error([
      `namespace ${namespace} is not aligned`,
      ...missingInZh.map((key) => `missing in zh: ${key}`),
      ...missingInEn.map((key) => `missing in en: ${key}`),
    ].join("\n  - "));
  }
  console.log(`✓ ${namespace}: ${enKeys.size} aligned, non-empty, runtime-formatted keys in zh/en`);
}

main();

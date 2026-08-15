import { createTranslator } from "next-intl";
import { describe, expect, it } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";

const catalogScopeKeys = [
  "selectTokenForCatalog",
  "adminAllAPIs",
  "tokenHasNoAPIs",
  "tokenNotAvailable",
  "catalogAccessUnavailable",
  "chooseTokenToInvoke",
] as const;

describe("apiCatalog scope messages", () => {
  it.each([["en", en], ["zh", zh]] as const)("resolves every catalog scope message for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiCatalog", onError: (error) => errors.push(error) });

    for (const key of catalogScopeKeys) {
      expect(messages.apiCatalog[key], `${locale}.apiCatalog.${key}`).toMatch(/\S/);
      expect(t(key), `${locale}.apiCatalog.${key} unresolved`).not.toBe(key);
    }
    expect(errors).toEqual([]);
  });

  it("keeps Chinese and English catalog scope keys exactly aligned", () => {
    expect(Object.keys(zh.apiCatalog).sort()).toEqual(Object.keys(en.apiCatalog).sort());
  });
});

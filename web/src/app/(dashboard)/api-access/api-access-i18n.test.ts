import { createTranslator } from "next-intl";
import { describe, expect, it } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";

describe("apiAccess runtime messages", () => {
  it.each([["en", en], ["zh", zh]] as const)("resolves invoke-only permission messages for %s", (locale, messages) => {
    const errors: unknown[] = [];
    const t = createTranslator({ locale, messages, namespace: "apiAccess", onError: (error) => errors.push(error) });

    for (const key of [
      "resourceOptions.api_service",
      "resourceOptions.api_route",
      "actionOptions.invoke",
      "permissionTargetRequired",
      "permissionServiceRequired",
      "emptyRolesDescription",
      "explicitTokenOnly",
      "members",
      "membersDescription",
    ] as const) {
      expect(t(key), `${locale}.${key}`).toBeTruthy();
    }

    expect(Object.keys(messages.apiAccess.resourceOptions)).toEqual(["api_service", "api_route"]);
    expect(Object.keys(messages.apiAccess.actionOptions)).toEqual(["invoke"]);
    expect(errors, `${locale} missing runtime messages`).toEqual([]);
  });
});

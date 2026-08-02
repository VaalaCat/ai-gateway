import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import { checkRequiredRuntimeKeys } from "../../../scripts/check-i18n.mjs";
import { channelBatchRuntimeMessageKeys } from "./batch-edit-runtime-message-keys";

describe("channel batch runtime message keys", () => {
  it("exports every review-required channel interaction key from the production source", () => {
    expect(channelBatchRuntimeMessageKeys).toEqual(expect.arrayContaining([
      "clearSelection",
      "channelStatusUpdating",
      "noBatchFieldSelected",
      "publicDisplayNameAutoPreview",
    ]));
  });

  it("looks up every production key in both locale catalogs", () => {
    for (const locale of ["zh", "en"]) {
      const messages = JSON.parse(readFileSync(`src/i18n/${locale}.json`, "utf8"));
      const required = channelBatchRuntimeMessageKeys.map((key) => `channels.${key}`);

      expect(checkRequiredRuntimeKeys(messages, required)).toEqual([]);
    }
  });
});

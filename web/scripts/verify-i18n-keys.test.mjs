import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

test("modelMarketplace messages format with the shared runtime samples", () => {
  const result = spawnSync(
    process.execPath,
    ["--import", "tsx", "scripts/verify-i18n-keys.ts", "modelMarketplace"],
    { cwd: process.cwd(), encoding: "utf8" },
  );
  assert.equal(result.status, 0, result.stderr || result.stdout);
});

test("apiServices messages format with the shared runtime samples", () => {
  const result = spawnSync(
    process.execPath,
    ["--import", "tsx", "scripts/verify-i18n-keys.ts", "apiServices"],
    { cwd: process.cwd(), encoding: "utf8" },
  );
  assert.equal(result.status, 0, result.stderr || result.stdout);
});

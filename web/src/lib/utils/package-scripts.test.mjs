import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const packageJSON = JSON.parse(
  readFileSync(new URL("../../../package.json", import.meta.url), "utf8"),
);

test("defines the Agent transport i18n checker", () => {
  assert.equal(
    packageJSON.scripts["check:agent-transport-i18n"],
    "node scripts/check-agent-transport-i18n.mjs",
  );
});

test("runs the Agent transport i18n checker from lint", () => {
  assert.match(
    packageJSON.scripts.lint,
    /(?:npm run|pnpm) check:agent-transport-i18n/,
  );
});

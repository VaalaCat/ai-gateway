import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { humanizeNumberUnit } from "./number-unit.ts";

const systemPageSource = readFileSync(
  new URL("../../app/(dashboard)/system/page.tsx", import.meta.url),
  "utf8",
);
const byokSettingsSource = readFileSync(
  new URL("../../components/system/byok-settings.tsx", import.meta.url),
  "utf8",
);
const numberUnitInputSource = readFileSync(
  new URL("../../components/business/number-unit-input.tsx", import.meta.url),
  "utf8",
);
const responseSectionSource = readFileSync(new URL("../../components/channel/channel-form/sections/response.tsx", import.meta.url), "utf8");
const affinitySectionSource = readFileSync(new URL("../../components/channel/channel-form/sections/affinity.tsx", import.meta.url), "utf8");
const resilienceSectionSource = readFileSync(new URL("../../components/channel/channel-form/sections/resilience.tsx", import.meta.url), "utf8");
const batchEditSource = readFileSync(new URL("../../components/channel/channel-batch-edit-dialog.tsx", import.meta.url), "utf8");
const agentRelaySource = readFileSync(new URL("../../components/system/agent-relay-settings.tsx", import.meta.url), "utf8");
const rateLimiterSource = readFileSync(new URL("../../components/rate-limiter/rate-limiter-form.tsx", import.meta.url), "utf8");
const agentsPageSource = readFileSync(new URL("../../app/(dashboard)/agents/page.tsx", import.meta.url), "utf8");
const usersPageSource = readFileSync(new URL("../../app/(dashboard)/users/page.tsx", import.meta.url), "utf8");

function findJSXBlock(source, componentName, identifyingProp) {
  const blocks = source.match(
    new RegExp(`<${componentName}\\b[\\s\\S]*?\\/>`, "g"),
  ) ?? [];
  const block = blocks.find((candidate) => candidate.includes(identifyingProp));

  assert.ok(
    block,
    `${componentName} block containing ${identifyingProp} should exist`,
  );
  return block.replace(/\s+/g, " ");
}

function assertNumFieldMapping({ label, value, unit, humanizeAs, onChange }) {
  const block = findJSXBlock(systemPageSource, "NumField", `value={${value}}`);

  assert.ok(block.includes(`label={t("${label}")}`), `${label} label wiring`);
  if (unit === null) {
    assert.doesNotMatch(block, /\bunit=/, `${label} should not show a raw unit`);
  } else {
    assert.ok(block.includes(unit), `${label} unit wiring`);
  }
  assert.ok(
    block.includes(`humanizeAs="${humanizeAs}"`),
    `${label} human-readable wiring`,
  );
  assert.ok(
    block.includes(`onChange={${onChange}}`),
    `${label} raw onChange wiring`,
  );
}

test("humanizes numbers without changing their stored units", () => {
  assert.equal(humanizeNumberUnit("30000", "milliseconds"), "30.0s");
  assert.equal(humanizeNumberUnit("300", "seconds"), "5m 0s");
  assert.equal(humanizeNumberUnit("10485760", "bytes"), "10.0 MB");
  assert.equal(humanizeNumberUnit("16384", "kilobytes"), "16.0 MB");
  assert.equal(humanizeNumberUnit("0.2", "ratio"), "20%");
  assert.equal(humanizeNumberUnit("0.125", "ratio"), "12.5%");
  assert.equal(humanizeNumberUnit("0.123456", "ratio"), "12.3456%");
  assert.equal(humanizeNumberUnit("100000", "quota"), "$ 1.00");
  assert.equal(humanizeNumberUnit(100000, "quota"), "$ 1.00");
  assert.equal(humanizeNumberUnit("-100000", "quota"), "$ -1.00");
});

test("rejects empty, non-numeric, negative, and non-finite values", () => {
  for (const rawValue of ["", "   ", "not-a-number", "-1", "Infinity", "1e309"]) {
    assert.equal(humanizeNumberUnit(rawValue, "milliseconds"), null);
  }
});

test("rejects finite values whose display-unit conversion overflows", () => {
  assert.equal(humanizeNumberUnit(Number.MAX_VALUE, "seconds"), null);
  assert.equal(humanizeNumberUnit(Number.MAX_VALUE, "kilobytes"), null);
  assert.equal(humanizeNumberUnit(Number.MAX_VALUE, "ratio"), null);
});

test("controls number hints from the real input focus and non-touch hover", () => {
  assert.equal(
    (numberUnitInputSource.match(/<Input\b/g) ?? []).length,
    1,
    "human-readable state changes should preserve one real input",
  );
  assert.match(
    numberUnitInputSource,
    /const input\s*=\s*humanReadable\s*!==\s*undefined\s*\?/,
  );
  assert.match(
    numberUnitInputSource,
    /<Tooltip\s+open=\{\s*Boolean\(humanReadable\)\s*&&\s*!isDismissed\s*&&\s*\(isFocused\s*\|\|\s*isHovered\)\s*\}/,
  );
  assert.match(
    numberUnitInputSource,
    /\{Boolean\(humanReadable\)\s*\?\s*\(\s*<TooltipContent[\s\S]*?onEscapeKeyDown=\{handleEscapeKeyDown\}/,
  );

  const input = findJSXBlock(
    numberUnitInputSource,
    "Input",
    "onFocus={handleFocus}",
  );
  for (const eventProp of [
    "onFocus={handleFocus}",
    "onBlur={handleBlur}",
    "onPointerEnter={handlePointerEnter}",
    "onPointerLeave={handlePointerLeave}",
  ]) {
    assert.ok(input.includes(eventProp), `${eventProp} should be composed`);
  }

  assert.match(
    numberUnitInputSource,
    /const handleFocus[\s\S]*?setIsDismissed\(false\)[\s\S]*?setIsFocused\(true\)[\s\S]*?onFocus\?\.\(event\)/,
  );
  assert.match(
    numberUnitInputSource,
    /const handleBlur[\s\S]*?setIsFocused\(false\)[\s\S]*?onBlur\?\.\(event\)/,
  );
  assert.match(
    numberUnitInputSource,
    /const handlePointerEnter[\s\S]*?event\.pointerType\s*!==\s*"touch"[\s\S]*?setIsDismissed\(false\)[\s\S]*?setIsHovered\(true\)[\s\S]*?onPointerEnter\?\.\(event\)/,
  );
  assert.match(
    numberUnitInputSource,
    /const handlePointerLeave[\s\S]*?setIsHovered\(false\)[\s\S]*?onPointerLeave\?\.\(event\)/,
  );
  assert.match(
    numberUnitInputSource,
    /const handleEscapeKeyDown[\s\S]*?setIsDismissed\(true\)/,
  );
});

test("wires raw system setting units to human-readable number hints", () => {
  const mappings = [
    {
      label: "fallbackSleep",
      value: "draft.fallbackSleep.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.fallbackSleep.change",
    },
    {
      label: "retryBackoffBase",
      value: "draft.retryBackoffBase.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.retryBackoffBase.change",
    },
    {
      label: "retryBackoffMax",
      value: "draft.retryBackoffMax.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.retryBackoffMax.change",
    },
    {
      label: "breakerCooldown",
      value: "draft.breakerCooldown.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.breakerCooldown.change",
    },
    {
      label: "sseKeepalive",
      value: "draft.sseKeepalive.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.sseKeepalive.change",
    },
    {
      label: "queueTime",
      value: "draft.queueTime.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.queueTime.change",
    },
    {
      label: "affinityTTL",
      value: "draft.affinityTTL.value",
      unit: 'unit="s"',
      humanizeAs: "seconds",
      onChange: "draft.affinityTTL.change",
    },
    {
      label: "traceMaxBodySize",
      value: "String(draft.traceMaxBodyKB.value)",
      unit: 'unit={t("traceMaxBodySizeUnit")}',
      humanizeAs: "kilobytes",
      onChange: "(value) => draft.traceMaxBodyKB.change(Number(value))",
    },
    {
      label: "imageInlineFetchTimeoutSec",
      value: "draft.imageInlineFetchTimeoutSec.value",
      unit: 'unit="s"',
      humanizeAs: "seconds",
      onChange: "draft.imageInlineFetchTimeoutSec.change",
    },
    {
      label: "imageInlineMaxBytes",
      value: "draft.imageInlineMaxBytes.value",
      unit: 'unit="bytes"',
      humanizeAs: "bytes",
      onChange: "draft.imageInlineMaxBytes.change",
    },
    {
      label: "minQuotaReserve",
      value: "draft.minQuotaReserve.value",
      unit: 'unit="quota"',
      humanizeAs: "quota",
      onChange: "draft.minQuotaReserve.change",
    },
    {
      label: "pricingDisagreementThreshold",
      value: "draft.pricingThreshold.value",
      unit: null,
      humanizeAs: "ratio",
      onChange: "draft.pricingThreshold.change",
    },
    {
      label: "rebuildSliceSleep",
      value: "draft.rebuildSliceSleep.value",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "draft.rebuildSliceSleep.change",
    },
  ];
  for (const mapping of mappings) {
    assertNumFieldMapping(mapping);
  }

  const liveHumanizedMappings = (
    systemPageSource.match(/<NumField\b[\s\S]*?\/>/g) ?? []
  )
    .filter((block) => /\bhumanizeAs=/.test(block))
    .map((block) => {
      const value = block.match(/\bvalue=\{([^}]+)\}/)?.[1]?.trim();
      const humanizeAs = block.match(/\bhumanizeAs="([^"]+)"/)?.[1];
      assert.ok(value, "humanized NumField should have a value expression");
      assert.ok(humanizeAs, "humanized NumField should have a conversion kind");
      return `${value}:${humanizeAs}`;
    });
  const allowedHumanizedMappings = mappings.map(
    ({ value, humanizeAs }) => `${value}:${humanizeAs}`,
  );
  assert.equal(liveHumanizedMappings.length, allowedHumanizedMappings.length);
  assert.deepEqual(
    [...liveHumanizedMappings].sort(),
    [...allowedHumanizedMappings].sort(),
  );

  const numFieldDefinition = systemPageSource.slice(
    systemPageSource.indexOf("function NumField"),
    systemPageSource.indexOf("export default function"),
  );
  assert.match(numFieldDefinition, /<NumberUnitInput\b/);
  assert.match(numFieldDefinition, /\bvalue=\{\s*value\s*\}/);
  assert.match(
    numFieldDefinition,
    /onChange=\{\s*\(e\)\s*=>\s*onChange\(e\.target\.value\)\s*\}/,
  );
  assert.match(
    numFieldDefinition,
    /humanReadable=\{\s*humanizeAs\s*\?\s*humanizeNumberUnit\(value,\s*humanizeAs\)\s*:\s*undefined\s*\}/,
  );

  const handleSaveStart = systemPageSource.indexOf(
    "const handleSaveSettings =",
  );
  const saveActionStart = systemPageSource.indexOf(
    "const saveAction:",
    handleSaveStart,
  );
  assert.ok(handleSaveStart >= 0, "handleSaveSettings should exist");
  assert.ok(
    saveActionStart > handleSaveStart,
    "saveAction should follow handleSaveSettings",
  );
  const handleSaveSettings = systemPageSource.slice(
    handleSaveStart,
    saveActionStart,
  );
  const compactHandleSaveSettings = handleSaveSettings
    .replace(/,\s*\)/g, ")")
    .replace(/\s+/g, "");
  assert.doesNotMatch(handleSaveSettings, /humanizeNumberUnit/);

  assert.match(
    systemPageSource,
    /Math\.round\(\s*Number\(\s*settings\.settings\.trace_max_body_size\s*\)\s*\/\s*1024\s*\)/,
  );
  for (const rawSaveStatement of [
    "updates.trace_max_body_size = String(displayKB * 1024)",
    "updates.fallback_sleep_ms = String(n)",
    "updates.retry_backoff_base_ms = String(n)",
    "updates.retry_backoff_max_ms = String(n)",
    "updates.breaker_cooldown_ms = String(n)",
    "updates.min_quota_reserve = String(Number(minQuotaReserveInput) || 0)",
    "updates.sse_keepalive_ms = String(n)",
    "updates.queue_time_ms = String(n)",
    "updates.affinity_ttl_sec = String(parseInt(displayAffinityTTL, 10) || 300)",
    "updates.image_inline_fetch_timeout_sec = String(displayImageInlineFetchTimeoutSec)",
    "updates.image_inline_max_bytes = String(displayImageInlineMaxBytes)",
    "updates.pricing_disagreement_threshold = displayPricingThreshold",
  ]) {
    assert.ok(
      compactHandleSaveSettings.includes(rawSaveStatement.replace(/\s+/g, "")),
      `${rawSaveStatement} should preserve its raw save contract`,
    );
  }

  const traceField = findJSXBlock(
    systemPageSource,
    "NumField",
    "value={String(draft.traceMaxBodyKB.value)}",
  );
  assert.ok(traceField.includes("min={4}"));
  assert.ok(traceField.includes("max={16384}"));
  assert.ok(traceField.includes("onChange={(value) => draft.traceMaxBodyKB.change(Number(value))}"));
  assert.ok(systemPageSource.includes('{t("traceMaxBodySizeRange")}'));

  const imageBytesField = findJSXBlock(
    systemPageSource,
    "NumField",
    "value={draft.imageInlineMaxBytes.value}",
  );
  assert.ok(imageBytesField.includes("min={1024}"));
  assert.ok(imageBytesField.includes("max={104857600}"));
  assert.match(
    systemPageSource,
    /const currentImageInlineMaxBytes\s*=\s*settings\?\.settings\?\.image_inline_max_bytes\s*\?\s*Number\(settings\.settings\.image_inline_max_bytes\)\s*:\s*10485760\s*;/,
  );

  const pricingThresholdField = findJSXBlock(
    systemPageSource,
    "NumField",
    "value={draft.pricingThreshold.value}",
  );
  assert.ok(
    pricingThresholdField.includes(
      'desc={t("pricingDisagreementThresholdDesc")}',
    ),
  );
  assert.ok(pricingThresholdField.includes("step={0.05}"));
  assert.ok(pricingThresholdField.includes("min={0}"));
  assert.ok(pricingThresholdField.includes("max={1}"));
  assert.match(
    systemPageSource,
    /const currentPricingThreshold\s*=\s*settings\?\.settings\?\.pricing_disagreement_threshold\s*\?\?\s*"0\.2"\s*;/,
  );
  assert.match(
    systemPageSource,
    /const displayPricingThreshold\s*=\s*pricingThresholdInput\s*\?\?\s*currentPricingThreshold\s*;/,
  );
});

test("wires the BYOK fee ratio hint without changing its numeric contract", () => {
  const feeRatioField = findJSXBlock(
    byokSettingsSource,
    "NumberUnitInput",
    "value={feeRatio}",
  );

  assert.ok(feeRatioField.includes('type="number"'));
  assert.ok(feeRatioField.includes('step="0.01"'));
  assert.ok(feeRatioField.includes("min={0}"));
  assert.ok(feeRatioField.includes("max={1}"));
  assert.doesNotMatch(feeRatioField, /\bunit=/);
  assert.ok(
    feeRatioField.includes(
      'humanReadable={humanizeNumberUnit(feeRatio, "ratio")}',
    ),
  );
  assert.ok(
    feeRatioField.includes(
      "onChange={(e) => setFeeRatio(Number(e.target.value))}",
    ),
  );
  assert.ok(
    byokSettingsSource.includes("byok_service_fee_ratio: String(feeRatio)"),
  );
  assert.match(
    byokSettingsSource,
    /const parsed\s*=\s*parseFloat\(settings\?\.byok_service_fee_ratio\s*\?\?\s*'0\.1'\)\s*;/,
  );
  assert.match(
    byokSettingsSource,
    /return isNaN\(parsed\)\s*\?\s*0\.1\s*:\s*parsed\s*;/,
  );

  const maxChannelsField = findJSXBlock(
    byokSettingsSource,
    "Input",
    "value={maxChannels}",
  );
  assert.ok(maxChannelsField.includes('type="number"'));
});

test("wires ratio hints only to channel pricing fields", () => {
  assert.match(responseSectionSource, /<NumberUnitInput[\s\S]*?value=\{form\.price_ratio\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(form\.price_ratio, "ratio"\)\}/);
  assert.match(batchEditSource, /key === "price_ratio"[\s\S]*?<NumberUnitInput[\s\S]*?humanReadable=\{humanizeNumberUnit\(String\(form\[key\]\), "ratio"\)\}/);
  assert.doesNotMatch(batchEditSource, /key === "weight"[\s\S]{0,300}humanizeNumberUnit/);
  assert.doesNotMatch(batchEditSource, /key === "priority"[\s\S]{0,300}humanizeNumberUnit/);
});

test("wires duration hints to second and millisecond fields", () => {
  assert.match(affinitySectionSource, /<NumberUnitInput[\s\S]*?unit="s"[\s\S]*?humanReadable=\{humanizeNumberUnit\([\s\S]*?"seconds",?\s*\)\}/);
  assert.match(resilienceSectionSource, /isDuration[\s\S]*?<NumberUnitInput[\s\S]*?unit="ms"[\s\S]*?humanReadable=\{humanizeNumberUnit\([\s\S]*?"milliseconds"\)\}/);
  assert.match(agentRelaySource, /<NumberUnitInput[\s\S]*?value=\{successTTL\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(successTTL, "seconds"\)\}/);
  assert.match(agentRelaySource, /<NumberUnitInput[\s\S]*?value=\{retryMin\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(retryMin, "seconds"\)\}/);
  assert.match(agentRelaySource, /<NumberUnitInput[\s\S]*?value=\{retryMax\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(retryMax, "seconds"\)\}/);
  assert.match(rateLimiterSource, /<NumberUnitInput[\s\S]*?value=\{windowMs\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(windowMs, "milliseconds"\)\}/);
  assert.match(rateLimiterSource, /<NumberUnitInput[\s\S]*?value=\{queueTimeMs\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(queueTimeMs, "milliseconds"\)\}/);
  assert.match(agentsPageSource, /<NumberUnitInput[\s\S]*?value=\{enrollTTL\}[\s\S]*?humanReadable=\{humanizeNumberUnit\(enrollTTL, "seconds"\)\}/);
});

test("uses the shared signed quota formatter for user quota adjustments", () => {
  assert.match(usersPageSource, /const quotaDeltaMoney\s*=\s*humanizeNumberUnit\(quotaDelta, "quota"\)/);
  assert.match(usersPageSource, /<MoneyCell quota=\{row\.original\.quota\}/);
  assert.match(usersPageSource, /<MoneyCell quota=\{row\.original\.used_quota\}/);
  assert.doesNotMatch(usersPageSource, /\/\s*100000/);
});

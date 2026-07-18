import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { humanizeSettingNumber } from "./system-setting-number.ts";

const systemPageSource = readFileSync(
  new URL("../../app/(dashboard)/system/page.tsx", import.meta.url),
  "utf8",
);
const byokSettingsSource = readFileSync(
  new URL("../../components/system/byok-settings.tsx", import.meta.url),
  "utf8",
);
const settingNumberInputSource = readFileSync(
  new URL("../../components/system/setting-number-input.tsx", import.meta.url),
  "utf8",
);

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

test("humanizes system setting numbers without changing their stored units", () => {
  assert.equal(humanizeSettingNumber("30000", "milliseconds"), "30.0s");
  assert.equal(humanizeSettingNumber("300", "seconds"), "5m 0s");
  assert.equal(humanizeSettingNumber("10485760", "bytes"), "10.0 MB");
  assert.equal(humanizeSettingNumber("16384", "kilobytes"), "16.0 MB");
  assert.equal(humanizeSettingNumber("0.2", "ratio"), "20%");
  assert.equal(humanizeSettingNumber("0.125", "ratio"), "12.5%");
  assert.equal(humanizeSettingNumber("0.123456", "ratio"), "12.3456%");
  assert.equal(humanizeSettingNumber("100000", "quota"), "$ 1.00");
  assert.equal(humanizeSettingNumber(100000, "quota"), "$ 1.00");
});

test("rejects empty, non-numeric, negative, and non-finite values", () => {
  for (const rawValue of ["", "   ", "not-a-number", "-1", "Infinity", "1e309"]) {
    assert.equal(humanizeSettingNumber(rawValue, "milliseconds"), null);
  }
});

test("rejects finite values whose display-unit conversion overflows", () => {
  assert.equal(humanizeSettingNumber(Number.MAX_VALUE, "seconds"), null);
  assert.equal(humanizeSettingNumber(Number.MAX_VALUE, "kilobytes"), null);
  assert.equal(humanizeSettingNumber(Number.MAX_VALUE, "ratio"), null);
});

test("controls number hints from the real input focus and non-touch hover", () => {
  assert.equal(
    (settingNumberInputSource.match(/<Input\b/g) ?? []).length,
    1,
    "human-readable state changes should preserve one real input",
  );
  assert.match(
    settingNumberInputSource,
    /const input\s*=\s*humanReadable\s*!==\s*undefined\s*\?/,
  );
  assert.match(
    settingNumberInputSource,
    /<Tooltip\s+open=\{\s*Boolean\(humanReadable\)\s*&&\s*!isDismissed\s*&&\s*\(isFocused\s*\|\|\s*isHovered\)\s*\}/,
  );
  assert.match(
    settingNumberInputSource,
    /\{Boolean\(humanReadable\)\s*\?\s*\(\s*<TooltipContent[\s\S]*?onEscapeKeyDown=\{handleEscapeKeyDown\}/,
  );

  const input = findJSXBlock(
    settingNumberInputSource,
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
    settingNumberInputSource,
    /const handleFocus[\s\S]*?setIsDismissed\(false\)[\s\S]*?setIsFocused\(true\)[\s\S]*?onFocus\?\.\(event\)/,
  );
  assert.match(
    settingNumberInputSource,
    /const handleBlur[\s\S]*?setIsFocused\(false\)[\s\S]*?onBlur\?\.\(event\)/,
  );
  assert.match(
    settingNumberInputSource,
    /const handlePointerEnter[\s\S]*?event\.pointerType\s*!==\s*"touch"[\s\S]*?setIsDismissed\(false\)[\s\S]*?setIsHovered\(true\)[\s\S]*?onPointerEnter\?\.\(event\)/,
  );
  assert.match(
    settingNumberInputSource,
    /const handlePointerLeave[\s\S]*?setIsHovered\(false\)[\s\S]*?onPointerLeave\?\.\(event\)/,
  );
  assert.match(
    settingNumberInputSource,
    /const handleEscapeKeyDown[\s\S]*?setIsDismissed\(true\)/,
  );
});

test("wires raw system setting units to human-readable number hints", () => {
  const mappings = [
    {
      label: "fallbackSleep",
      value: "displayFallbackSleep",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "setFallbackSleepInput",
    },
    {
      label: "retryBackoffBase",
      value: "displayRetryBackoffBase",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "setRetryBackoffBaseInput",
    },
    {
      label: "retryBackoffMax",
      value: "displayRetryBackoffMax",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "setRetryBackoffMaxInput",
    },
    {
      label: "breakerCooldown",
      value: "displayBreakerCooldown",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "setBreakerCooldownInput",
    },
    {
      label: "sseKeepalive",
      value: "displaySseKeepalive",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "setSseKeepaliveInput",
    },
    {
      label: "queueTime",
      value: "displayQueueTime",
      unit: 'unit="ms"',
      humanizeAs: "milliseconds",
      onChange: "setQueueTimeInput",
    },
    {
      label: "affinityTTL",
      value: "displayAffinityTTL",
      unit: 'unit="s"',
      humanizeAs: "seconds",
      onChange: "setAffinityTTLInput",
    },
    {
      label: "traceMaxBodySize",
      value: "String(displayKB)",
      unit: 'unit={t("traceMaxBodySizeUnit")}',
      humanizeAs: "kilobytes",
      onChange: "(value) => setTraceMaxBodyKB(Number(value))",
    },
    {
      label: "imageInlineFetchTimeoutSec",
      value: "displayImageInlineFetchTimeoutSec",
      unit: 'unit="s"',
      humanizeAs: "seconds",
      onChange: "setImageInlineFetchTimeoutSecInput",
    },
    {
      label: "imageInlineMaxBytes",
      value: "displayImageInlineMaxBytes",
      unit: 'unit="bytes"',
      humanizeAs: "bytes",
      onChange: "setImageInlineMaxBytesInput",
    },
    {
      label: "minQuotaReserve",
      value: "displayMinQuotaReserve",
      unit: 'unit="quota"',
      humanizeAs: "quota",
      onChange: "setMinQuotaReserveInput",
    },
    {
      label: "pricingDisagreementThreshold",
      value: "displayPricingThreshold",
      unit: null,
      humanizeAs: "ratio",
      onChange: "setPricingThresholdInput",
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
  assert.match(numFieldDefinition, /<SettingNumberInput\b/);
  assert.match(numFieldDefinition, /\bvalue=\{\s*value\s*\}/);
  assert.match(
    numFieldDefinition,
    /onChange=\{\s*\(e\)\s*=>\s*onChange\(e\.target\.value\)\s*\}/,
  );
  assert.match(
    numFieldDefinition,
    /humanReadable=\{\s*humanizeAs\s*\?\s*humanizeSettingNumber\(value,\s*humanizeAs\)\s*:\s*undefined\s*\}/,
  );

  const handleSaveStart = systemPageSource.indexOf(
    "const handleSaveSettings =",
  );
  const handlePreviewStart = systemPageSource.indexOf(
    "const handlePreview =",
    handleSaveStart,
  );
  assert.ok(handleSaveStart >= 0, "handleSaveSettings should exist");
  assert.ok(
    handlePreviewStart > handleSaveStart,
    "handlePreview should follow handleSaveSettings",
  );
  const handleSaveSettings = systemPageSource.slice(
    handleSaveStart,
    handlePreviewStart,
  );
  assert.doesNotMatch(handleSaveSettings, /humanizeSettingNumber/);

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
      handleSaveSettings.includes(rawSaveStatement),
      `${rawSaveStatement} should preserve its raw save contract`,
    );
  }

  const traceField = findJSXBlock(
    systemPageSource,
    "NumField",
    "value={String(displayKB)}",
  );
  assert.ok(traceField.includes("min={4}"));
  assert.ok(traceField.includes("max={16384}"));
  assert.ok(traceField.includes("onChange={(value) => setTraceMaxBodyKB(Number(value))}"));
  assert.ok(systemPageSource.includes('{t("traceMaxBodySizeRange")}'));

  const imageBytesField = findJSXBlock(
    systemPageSource,
    "NumField",
    "value={displayImageInlineMaxBytes}",
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
    "value={displayPricingThreshold}",
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
    "SettingNumberInput",
    "value={feeRatio}",
  );

  assert.ok(feeRatioField.includes('type="number"'));
  assert.ok(feeRatioField.includes('step="0.01"'));
  assert.ok(feeRatioField.includes("min={0}"));
  assert.ok(feeRatioField.includes("max={1}"));
  assert.doesNotMatch(feeRatioField, /\bunit=/);
  assert.ok(
    feeRatioField.includes(
      'humanReadable={humanizeSettingNumber(feeRatio, "ratio")}',
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

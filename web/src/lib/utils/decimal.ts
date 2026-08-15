const POSITIVE_DECIMAL = /^[1-9]\d*$/;
const NON_NEGATIVE_DECIMAL = /^(0|[1-9]\d*)$/;

function parseSafeInteger(value: unknown, pattern: RegExp) {
  if (typeof value === "number") {
    return Number.isSafeInteger(value) ? value : undefined;
  }
  if (typeof value !== "string" || !pattern.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
}

export function parsePositiveDecimal(value: unknown) {
  const parsed = parseSafeInteger(value, POSITIVE_DECIMAL);
  return parsed !== undefined && parsed > 0 ? parsed : undefined;
}

export function parseNonNegativeDecimal(value: unknown) {
  const parsed = parseSafeInteger(value, NON_NEGATIVE_DECIMAL);
  return parsed !== undefined && parsed >= 0 ? parsed : undefined;
}

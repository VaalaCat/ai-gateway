export function nonNegativeFiniteNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0
    ? value
    : null;
}

export function percentValueOrNull(value: unknown): number | null {
  const percent = nonNegativeFiniteNumber(value);
  return percent !== null && percent <= 100 ? percent : null;
}

export function unixSecondsOrNull(value: unknown): number | null {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return null;

  const milliseconds = value * 1_000;
  if (!Number.isFinite(milliseconds)) return null;

  return Number.isFinite(new Date(milliseconds).getTime()) ? value : null;
}

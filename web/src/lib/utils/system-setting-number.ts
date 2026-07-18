import { formatDuration, formatFileSize, formatMoneyCompact } from "./format";

export type SettingNumberKind =
  | "milliseconds"
  | "seconds"
  | "bytes"
  | "kilobytes"
  | "ratio"
  | "quota";

export function humanizeSettingNumber(
  rawValue: string | number,
  kind: SettingNumberKind,
): string | null {
  if (typeof rawValue === "string" && rawValue.trim() === "") return null;

  const value = Number(rawValue);
  if (!Number.isFinite(value) || value < 0) return null;

  switch (kind) {
    case "milliseconds":
      return formatDuration(value);
    case "seconds": {
      const milliseconds = value * 1000;
      return Number.isFinite(milliseconds) ? formatDuration(milliseconds) : null;
    }
    case "bytes":
      return formatFileSize(value);
    case "kilobytes": {
      const bytes = value * 1024;
      return Number.isFinite(bytes) ? formatFileSize(bytes) : null;
    }
    case "ratio": {
      const percentage = value * 100;
      return Number.isFinite(percentage)
        ? `${Number(percentage.toFixed(4))}%`
        : null;
    }
    case "quota":
      return formatMoneyCompact(value);
  }
}

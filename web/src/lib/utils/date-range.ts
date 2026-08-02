import { addDays, format, isValid, parse } from "date-fns";

const DATE_FMT = "yyyy-MM-dd";

function parseStrictDate(value: string): Date | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return undefined;
  const parsed = parse(value, DATE_FMT, new Date());
  return isValid(parsed) && format(parsed, DATE_FMT) === value ? parsed : undefined;
}

export function isFiniteUnixSeconds(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value > 0;
}

/** unix seconds → "YYYY-MM-DD"，使用本地时区。 */
export function tsToDateStr(ts: number): string {
  if (!isFiniteUnixSeconds(ts)) return "";
  const date = new Date(ts * 1000);
  return isValid(date) ? format(date, DATE_FMT) : "";
}

/**
 * "YYYY-MM-DD" → unix seconds 本地时区；空串返回 0
 * @param atEnd true 表示当日 23:59:59.999，否则 00:00:00.000
 */
export function dateStrToTs(s: string, atEnd: boolean): number {
  const d = parseStrictDate(s);
  if (!d) return 0;
  const ms = atEnd
    ? d.setHours(23, 59, 59, 999)
    : d.setHours(0, 0, 0, 0);
  return Math.floor(ms / 1000);
}

/** 本地日历日结束边界 → 次日本地 00:00:00 unix 秒（exclusive）。 */
export function dateStrToExclusiveEndTs(s: string): number {
  const d = parseStrictDate(s);
  if (!d) return 0;
  return Math.floor(addDays(d, 1).setHours(0, 0, 0, 0) / 1000);
}

export function buildCompleteDateRange(
  startDate: string,
  endDate: string,
  maxDays?: number,
): { startDate: string; endDate: string } {
  const parsedStart = parseStrictDate(startDate);
  const parsedEnd = parseStrictDate(endDate);
  if (!parsedStart && !parsedEnd) return { startDate: "", endDate: "" };

  let from = parsedStart ?? parsedEnd!;
  let to = parsedEnd ?? parsedStart!;
  if (from > to) [from, to] = [to, from];

  const rangeLimit =
    typeof maxDays === "number" && Number.isFinite(maxDays)
      ? Math.max(1, Math.floor(maxDays))
      : undefined;
  if (rangeLimit) {
    const earliest = addDays(to, -(rangeLimit - 1));
    if (from < earliest) from = earliest;
  }

  return {
    startDate: format(from, DATE_FMT),
    endDate: format(to, DATE_FMT),
  };
}

/**
 * 把用户在本地时区选的日历日范围（yyyy-MM-dd 字符串），转成覆盖该本地范围
 * 的 UTC 日历日字符串范围。daily 表按 UTC 日聚合，发 UTC 日给后端能"包住"
 * 用户本地选日的所有请求；代价：单日查询可能返回最多 48h 数据（多 1 个 UTC 日）。
 *
 * 空串透传（让 buildQuery 跳过该端）。非法字符串也按空串处理；合法输入必须是
 * 严格 yyyy-MM-dd，避免把 Invalid Date 传给 toISOString。
 *
 * @example  GMT+8 用户选 from="2026-05-19" / to=""
 *   localDateRangeToUTCRange("2026-05-19", "")
 *   → { from: "2026-05-18", to: "" }
 *
 * @example  GMT+8 用户选 from="2026-05-19" / to="2026-05-19"
 *   localDateRangeToUTCRange("2026-05-19", "2026-05-19")
 *   → { from: "2026-05-18", to: "2026-05-19" }
 */
export function localDateRangeToUTCRange(
  from: string,
  to: string,
): { from: string; to: string } {
  const fromDate = parseStrictDate(from);
  const toDate = parseStrictDate(to);
  const utcFrom = fromDate
    ? new Date(fromDate.setHours(0, 0, 0, 0)).toISOString().slice(0, 10)
    : "";
  const utcTo = toDate
    ? new Date(toDate.setHours(23, 59, 59, 999)).toISOString().slice(0, 10)
    : "";
  return { from: utcFrom, to: utcTo };
}

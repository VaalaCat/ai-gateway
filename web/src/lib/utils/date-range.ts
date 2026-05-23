import { format, parse } from "date-fns";

const DATE_FMT = "yyyy-MM-dd";

/**
 * unix seconds → "YYYY-MM-DD" 本地时区；ts 为 0/falsy 返回空串
 */
export function tsToDateStr(ts: number): string {
  if (!ts) return "";
  return format(new Date(ts * 1000), DATE_FMT);
}

/**
 * "YYYY-MM-DD" → unix seconds 本地时区；空串返回 0
 * @param atEnd true 表示当日 23:59:59.999，否则 00:00:00.000
 */
export function dateStrToTs(s: string, atEnd: boolean): number {
  if (!s) return 0;
  const d = parse(s, DATE_FMT, new Date());
  const ms = atEnd
    ? d.setHours(23, 59, 59, 999)
    : d.setHours(0, 0, 0, 0);
  return Math.floor(ms / 1000);
}

/**
 * 把用户在本地时区选的日历日范围（yyyy-MM-dd 字符串），转成覆盖该本地范围
 * 的 UTC 日历日字符串范围。daily 表按 UTC 日聚合，发 UTC 日给后端能"包住"
 * 用户本地选日的所有请求；代价：单日查询可能返回最多 48h 数据（多 1 个 UTC 日）。
 *
 * 空串透传（让 buildQuery 跳过该端）。非法字符串行为依赖浏览器 Date 解析，
 * 调用方应在 DateRangeInputs 校验后再调（Calendar 控件保证 yyyy-MM-dd 合法）。
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
  const utcFrom = from
    ? new Date(`${from}T00:00:00`).toISOString().slice(0, 10)
    : "";
  const utcTo = to
    ? new Date(`${to}T23:59:59.999`).toISOString().slice(0, 10)
    : "";
  return { from: utcFrom, to: utcTo };
}

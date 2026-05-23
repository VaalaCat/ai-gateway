import { ApiError } from "./client";

/**
 * 把 mutation catch 到的 error 转成给 toast.error 用的字符串。
 * 优先用 ApiError.message（来自后端 JSON body.error 字段，含具体原因，
 * 如 "base_url not in allowlist"），其次 native Error.message，最后
 * fallback 到调用方提供的本地化兜底文案。
 *
 * 用法：
 *   try { ... } catch (e) {
 *     toast.error(formatErrorToast(e, t("saveFailed")));
 *   }
 */
export function formatErrorToast(e: unknown, fallback: string): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return fallback;
}

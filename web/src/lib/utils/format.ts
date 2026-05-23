export function formatDate(timestamp: number): string {
  if (!timestamp) return "-";
  return new Date(timestamp * 1000).toLocaleString();
}

export function formatRelativeTime(timestamp: number): string {
  const now = Math.floor(Date.now() / 1000);
  const diff = now - timestamp;
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.floor(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  if (ms < 3_600_000) {
    const m = Math.floor(ms / 60_000);
    const s = Math.floor((ms % 60_000) / 1000);
    return `${m}m ${s}s`;
  }
  const h = Math.floor(ms / 3_600_000);
  const m = Math.floor((ms % 3_600_000) / 60_000);
  return `${h}h ${m}m`;
}

/** 1 美元 = 100000 quota 单位(后端 cost 字段以此为单位) */
export const UNIT_QUOTA_SCALE = 100_000;

// ─── compact / exact number formatters ─────────────────────────────────────
// 阈值表 + 单次循环替代 if-else 链; money / tokens / requests 共用此函数

const COMPACT_TIERS: ReadonlyArray<{ min: number; suffix: string; div: number }> = [
  { min: 1e9, suffix: "B", div: 1e9 },
  { min: 1e6, suffix: "M", div: 1e6 },
  { min: 1e3, suffix: "K", div: 1e3 },
];

function compactNumber(n: number, decimals = 2): string {
  if (!Number.isFinite(n)) return "—";
  const abs = Math.abs(n);
  for (const t of COMPACT_TIERS) {
    if (abs >= t.min) {
      const scaled = n / t.div;
      // 四舍五入后若溢出当前 tier (例: 999999/1000=999.999 → toFixed(2)=1000.00),
      // 跳过此档让外层尝试更高 tier
      if (Math.abs(Number(scaled.toFixed(decimals))) >= 1000) {
        continue;
      }
      return `${scaled.toFixed(decimals)}${t.suffix}`;
    }
  }
  return n.toFixed(decimals);
}

/**
 * quota 单位 → 紧凑美元字符串。
 *   - < $0.01: 4 位小数 (避免微额变 $0.00)
 *   - < $1k: 2 位小数
 *   - >= $1k: K/M/B 缩放 2 位
 * NaN / Infinity → "—"
 */
export function formatMoneyCompact(quota: number): string {
  if (!Number.isFinite(quota)) return "—";
  const usd = quota / UNIT_QUOTA_SCALE;
  if (usd === 0) return "$ 0.00";
  const abs = Math.abs(usd);
  if (abs < 0.01) return `$ ${usd.toFixed(4)}`;
  if (abs < 1000) return `$ ${usd.toFixed(2)}`;
  return `$ ${compactNumber(usd, 2)}`;
}

/**
 * quota 单位 → 完整精度美元字符串 (6 位小数 + 千分位)。tooltip / hover 用。
 */
export function formatMoneyExact(quota: number): string {
  if (!Number.isFinite(quota)) return "—";
  const usd = quota / UNIT_QUOTA_SCALE;
  return `$ ${usd.toLocaleString("en-US", {
    minimumFractionDigits: 6,
    maximumFractionDigits: 6,
  })}`;
}

/** 整数 → 紧凑表示 (123 / 1.23K / 1.23M / 1.23B)。 < 1000 不缩, 直接整数。 */
export function formatTokensCompact(n: number): string {
  if (!Number.isFinite(n)) return "—";
  const abs = Math.abs(n);
  if (abs < 1000) return Math.trunc(n).toString();
  return compactNumber(n, 2);
}

/** 整数 → 千分位完整 (1,234,567)。tooltip / hover 用。 */
export function formatTokensExact(n: number): string {
  if (!Number.isFinite(n)) return "—";
  return Math.trunc(n).toLocaleString("en-US");
}

/** 同 formatTokensCompact, 语义复制保留, 调用方 grep 时一眼分辨 requests 列。 */
export function formatRequestsCompact(n: number): string {
  return formatTokensCompact(n);
}

/** 同 formatTokensExact。 */
export function formatRequestsExact(n: number): string {
  return formatTokensExact(n);
}

/**
 * 比例 (0-1) → 百分比字符串。
 *   formatPercent(0.1234) → "12.3%"
 */
export function formatPercent(ratio: number, decimals = 1): string {
  if (!Number.isFinite(ratio)) return "—";
  return `${(ratio * 100).toFixed(decimals)}%`;
}

export function formatPrice(price: number): string {
  return `$${price.toFixed(2)} / 1M`;
}

export function formatSuccessRate(successCount: number, requestCount: number): string {
  if (requestCount === 0) return "-";
  return `${((successCount / requestCount) * 100).toFixed(1)}%`;
}

export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const s = Math.floor(seconds);
  if (s < 60) return `${s}s`;
  if (s < 3600) {
    const m = Math.floor(s / 60);
    const r = s % 60;
    return `${m}m ${r}s`;
  }
  if (s < 86400) {
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return `${h}h ${m}m`;
  }
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  return `${d}d ${h}h`;
}

export function formatFileSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${Math.floor(bytes)} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

export function formatRate(perSecond: number): string {
  if (!Number.isFinite(perSecond) || perSecond < 0) return "—";
  if (perSecond === 0) return "0 req/s";
  if (perSecond < 0.1) return "<0.1 req/s";
  if (perSecond < 1000) return `${perSecond.toFixed(1)} req/s`;
  return `${(perSecond / 1000).toFixed(1)}K req/s`;
}

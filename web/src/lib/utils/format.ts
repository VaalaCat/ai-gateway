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
  if (!ms) return "-";
  return `${ms} ms`;
}

const QUOTA_PER_DOLLAR = 100_000;

export function formatCurrency(amount: number, decimals = 6): string {
  const usd = amount / QUOTA_PER_DOLLAR;
  return `$ ${usd.toFixed(decimals)}`;
}

export function formatPrice(price: number): string {
  return `$${price.toFixed(2)} / 1M`;
}

export function formatSuccessRate(successCount: number, requestCount: number): string {
  if (requestCount === 0) return "-";
  return `${((successCount / requestCount) * 100).toFixed(1)}%`;
}

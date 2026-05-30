"use client";

import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { TraceDetail } from "@/components/business/trace-detail";
import { formatDuration } from "@/lib/utils/format";
import type { UsageLog } from "@/lib/types";

interface FallbackChainProps {
  chain: NonNullable<UsageLog["fallback_chain"]>;
  requestId: string;
}

export function FallbackChain({ chain, requestId }: FallbackChainProps) {
  const t = useTranslations("logs");

  return (
    <div className="rounded-md border p-3 space-y-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <span>{t("chainTitle")}</span>
        <span className="text-muted-foreground font-normal">
          {chain.length} {t("chainAttempts")}
        </span>
      </div>

      <div className="space-y-3">
        {chain.map((entry, idx) => {
          const isLast = idx === chain.length - 1;

          return (
            <div key={entry.seq} className="space-y-1">
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="text-muted-foreground font-mono text-xs w-4">
                  {entry.seq}
                </span>
                <span className="font-medium">{entry.channel_name}</span>

                <Badge variant="secondary" className="text-xs font-normal">
                  {entry.source}
                </Badge>

                {entry.retries > 0 && (
                  <span className="text-muted-foreground text-xs">
                    ↻×{entry.retries}
                  </span>
                )}

                {entry.by_affinity && (
                  <span className="text-amber-500 text-xs">★</span>
                )}

                {entry.breaker_open ? (
                  <span className="text-muted-foreground text-xs">
                    ⊘ {t("chainSkippedBreaker")}
                  </span>
                ) : entry.status === "ok" ? (
                  <Badge className="bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 text-xs font-normal">
                    ✓ {entry.http_status ?? 200}
                  </Badge>
                ) : (
                  <Badge variant="destructive" className="text-xs font-normal">
                    ✗ {entry.http_status ?? ""} {entry.error_type ?? ""}
                  </Badge>
                )}

                <span className="text-muted-foreground text-xs">
                  {formatDuration(entry.duration_ms)}
                </span>

                {isLast && (
                  <span className="text-muted-foreground text-xs">
                    ◀ {t("chainAdopted")}
                  </span>
                )}
              </div>

              {entry.has_trace && (
                <div className="pl-6">
                  <TraceDetail requestId={requestId} attemptIndex={entry.seq - 1} />
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

"use client";

import { useTranslations } from "next-intl";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { formatPercent } from "@/lib/utils/format";
import type {
  CacheEntityStats,
  ClusterEntityStats,
} from "@/lib/types";

type Row = {
  hits: number;
  misses: number;
  evictions: number;
  negative_hits: number;
  size: number;
  capacity: number;
  load_errors: number;
  invalidations: number;
  /** null 表示无意义（full-sync 实体或分母为 0），渲染 "—"。 */
  hit_rate: number | null;
  /** null 表示无意义（capacity 为 0），渲染 "—"。 */
  util: number | null;
};

interface CacheStatsTableProps {
  data: Record<string, CacheEntityStats> | Record<string, ClusterEntityStats> | undefined;
  mode: "agent" | "cluster";
}

function toRow(
  raw: CacheEntityStats | ClusterEntityStats | undefined,
  mode: "agent" | "cluster",
): Row {
  if (!raw) {
    return { hits: 0, misses: 0, evictions: 0, negative_hits: 0, size: 0, capacity: 0, load_errors: 0, invalidations: 0, hit_rate: null, util: null };
  }
  if (mode === "cluster") {
    const c = raw as ClusterEntityStats;
    return {
      hits: c.hits, misses: c.misses, evictions: c.evictions, negative_hits: c.negative_hits,
      size: c.size, capacity: c.capacity, hit_rate: c.hit_rate, util: c.util,
      load_errors: c.load_errors, invalidations: c.invalidations,
    };
  }
  const a = raw as CacheEntityStats;
  const denom = a.hits + a.misses;
  return {
    hits: a.hits, misses: a.misses, evictions: a.evictions, negative_hits: a.negative_hits,
    size: a.size, capacity: a.capacity,
    load_errors: a.load_errors, invalidations: a.invalidations,
    hit_rate: denom > 0 ? a.hits / denom : null,
    util: a.capacity > 0 ? a.size / a.capacity : null,
  };
}

function hitRateClass(v: number | null): string {
  if (v === null) return "text-muted-foreground";
  if (v < 0.5) return "text-destructive font-medium";
  if (v < 0.7) return "text-yellow-600 dark:text-yellow-400";
  return "";
}

function utilBarColor(v: number | null): string {
  if (v === null) return "";
  if (v > 0.95) return "bg-yellow-500";
  if (v > 0.8) return "bg-blue-500";
  return "bg-green-500";
}

function UtilCell({ util }: { util: number | null }) {
  if (util === null) {
    return <span className="text-muted-foreground">—</span>;
  }
  const pct = Math.min(100, Math.max(0, util * 100));
  return (
    <div className="flex items-center gap-2 min-w-[7rem]">
      <div className="w-20 h-1.5 rounded bg-muted overflow-hidden">
        <div className={cn("h-full", utilBarColor(util))} style={{ width: `${pct}%` }} />
      </div>
      <span className="tabular-nums text-xs">{pct.toFixed(0)}%</span>
    </div>
  );
}

export function CacheStatsTable({ data, mode }: CacheStatsTableProps) {
  const t = useTranslations("monitoring");

  return (
    <div className="overflow-x-auto">
      <Table className="min-w-[640px]">
        <TableHeader>
          <TableRow>
            <TableHead className="w-32">{t("tableEntity")}</TableHead>
            <TableHead className="text-right">{t("tableHitRate")}</TableHead>
            <TableHead className="text-right">{t("tableNegative")}</TableHead>
            <TableHead className="text-right">{t("tableEvictions")}</TableHead>
            <TableHead className="text-right">{t("tableLoadErrors")}</TableHead>
            <TableHead className="text-right">{t("tableInvalidations")}</TableHead>
            <TableHead className="text-right">{t("tableSizeCap")}</TableHead>
            <TableHead>{t("tableUtil")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {Object.keys(data ?? {}).sort().map((name) => {
            const raw = data?.[name];
            const row = toRow(raw, mode);
            const isIndex = (raw as { kind?: string } | undefined)?.kind === "index";
            const extra = (raw as { extra?: Record<string, number> } | undefined)?.extra;
            const dash = <span className="text-muted-foreground">—</span>;
            return (
              <TableRow key={name}>
                <TableCell className="font-mono">
                  {name}
                  {isIndex && (
                    <span className="ml-1 rounded bg-muted px-1 text-[10px] text-muted-foreground">index</span>
                  )}
                </TableCell>
                <TableCell className={cn("text-right tabular-nums", hitRateClass(row.hit_rate))}>
                  {isIndex ? dash : row.hit_rate === null ? "—" : formatPercent(row.hit_rate)}
                </TableCell>
                <TableCell className="text-right tabular-nums">{isIndex ? dash : row.negative_hits}</TableCell>
                <TableCell className="text-right tabular-nums">{isIndex ? dash : row.evictions}</TableCell>
                <TableCell className="text-right tabular-nums">{isIndex ? dash : row.load_errors}</TableCell>
                <TableCell className="text-right tabular-nums">{isIndex ? dash : row.invalidations}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {row.size}
                  {isIndex ? "" : <>{" / "}{row.capacity > 0 ? row.capacity : dash}</>}
                </TableCell>
                <TableCell>
                  {isIndex ? (
                    <div className="flex flex-wrap gap-1">
                      {extra && Object.entries(extra).map(([k, v]) => (
                        <span key={k} className="rounded bg-muted px-1.5 py-0.5 text-[10px] tabular-nums">
                          {k} {v}
                        </span>
                      ))}
                    </div>
                  ) : (
                    <UtilCell util={row.util} />
                  )}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}

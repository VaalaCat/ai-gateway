"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { ChevronRight } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EntityLabel } from "@/components/business/entity-label";
import { cn } from "@/lib/utils";
import { parseBucket } from "@/lib/observability";
import { useLimiterUsage } from "@/lib/api/observability";
import type { LimiterBucketRow } from "@/lib/types";

// 饱和度条配色复用 cache-stats-table 的 UtilCell 语义。
function barColor(ratio: number): string {
  if (ratio > 0.95) return "bg-yellow-500";
  if (ratio > 0.8) return "bg-blue-500";
  return "bg-green-500";
}

/** 一个 occupied/capacity 饱和度条；capacity 为 0 渲染 "—"。 */
function SaturationBar({
  occupied,
  capacity,
}: {
  occupied: number;
  capacity: number;
}) {
  if (capacity <= 0) {
    return <span className="text-muted-foreground">—</span>;
  }
  const ratio = occupied / capacity;
  const pct = Math.min(100, Math.max(0, ratio * 100));
  return (
    <div className="flex items-center gap-2 min-w-[10rem]">
      <div className="w-24 h-1.5 rounded bg-muted overflow-hidden">
        <div className={cn("h-full", barColor(ratio))} style={{ width: `${pct}%` }} />
      </div>
      <span className="tabular-nums text-xs whitespace-nowrap">
        {occupied} / {capacity}
      </span>
    </div>
  );
}

function Waiters({ n }: { n: number }) {
  return (
    <span className={cn("tabular-nums", n > 0 && "text-amber-600 dark:text-amber-400 font-medium")}>
      {n}
    </span>
  );
}

/** 把某条 rule 名下若干 bucket 解析出的资源渲染成可读标签。 */
function ResourceLabel({ bucket }: { bucket: string }) {
  const parsed = parseBucket(bucket);
  if (parsed.entity) {
    return <EntityLabel entity={parsed.entity.type} id={parsed.entity.id} />;
  }
  // shared / unknown：直接展示原始 bucket 串。
  return <span className="font-mono text-muted-foreground">{bucket}</span>;
}

interface RuleGroup {
  limiter_id: number;
  name: string;
  metric: string;
  key_by: string;
  buckets: LimiterBucketRow[];
  sumOccupied: number;
  sumCapacity: number;
  sumWaiters: number;
}

function groupByRule(buckets: LimiterBucketRow[]): RuleGroup[] {
  const map = new Map<number, RuleGroup>();
  for (const b of buckets) {
    let g = map.get(b.limiter_id);
    if (!g) {
      g = {
        limiter_id: b.limiter_id,
        name: b.name,
        metric: b.metric,
        key_by: b.key_by,
        buckets: [],
        sumOccupied: 0,
        sumCapacity: 0,
        sumWaiters: 0,
      };
      map.set(b.limiter_id, g);
    }
    g.buckets.push(b);
    g.sumOccupied += b.occupied;
    g.sumCapacity += b.capacity;
    g.sumWaiters += b.waiters;
  }
  return [...map.values()].sort((a, b) => b.sumOccupied - a.sumOccupied);
}

// ---- per-agent drill (一个 bucket 下各节点本地占用) ----
function AgentRows({ row }: { row: LimiterBucketRow }) {
  const t = useTranslations("observability");
  if (!row.per_agent || row.per_agent.length === 0) {
    return (
      <div className="px-3 py-2 text-xs text-muted-foreground">{t("limiter.noAgents")}</div>
    );
  }
  return (
    <Table className="bg-muted/20">
      <TableHeader>
        <TableRow>
          <TableHead className="h-7 text-xs">{t("limiter.colAgent")}</TableHead>
          <TableHead className="h-7 text-xs">{t("limiter.colSaturation")}</TableHead>
          <TableHead className="h-7 text-xs text-right">{t("limiter.colWaiters")}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {row.per_agent.map((a) => (
          <TableRow key={a.agent_id}>
            <TableCell className="py-1.5 text-sm">{a.agent_name}</TableCell>
            <TableCell className="py-1.5">
              <SaturationBar occupied={a.occupied} capacity={a.capacity} />
            </TableCell>
            <TableCell className="py-1.5 text-right">
              <Waiters n={a.waiters} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

// ---- 一个 bucket 行（可展开看 per_agent） ----
function BucketRow({ row }: { row: LimiterBucketRow }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded border bg-background">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-muted/40"
      >
        <ChevronRight
          className={cn("size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
        />
        <span className="min-w-[8rem] text-sm">
          <ResourceLabel bucket={row.bucket} />
        </span>
        <span className="flex-1">
          <SaturationBar occupied={row.occupied} capacity={row.capacity} />
        </span>
        <Waiters n={row.waiters} />
      </button>
      {open && <AgentRows row={row} />}
    </div>
  );
}

// ---- 一条 rule（可展开看其 buckets） ----
function RuleRow({ group }: { group: RuleGroup }) {
  const t = useTranslations("observability");
  const [open, setOpen] = useState(false);
  const ratio = group.sumCapacity > 0 ? group.sumOccupied / group.sumCapacity : null;
  return (
    <Card>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/30 rounded-lg"
      >
        <ChevronRight
          className={cn("size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
        />
        <span className="font-medium">{group.name}</span>
        <Badge variant="secondary" className="font-mono text-[10px]">
          {group.metric}/{group.key_by}
        </Badge>
        <span className="flex-1">
          {ratio === null ? (
            <span className="text-muted-foreground">—</span>
          ) : (
            <SaturationBar occupied={group.sumOccupied} capacity={group.sumCapacity} />
          )}
        </span>
        <span className="text-sm text-muted-foreground">
          {t("limiter.colWaiters")}: <Waiters n={group.sumWaiters} />
        </span>
      </button>
      {open && (
        <CardContent className="space-y-1.5 pt-0">
          {group.buckets.map((b) => (
            <BucketRow key={`${b.limiter_id}-${b.bucket}`} row={b} />
          ))}
        </CardContent>
      )}
    </Card>
  );
}

function ByRule({ buckets }: { buckets: LimiterBucketRow[] }) {
  const groups = useMemo(() => groupByRule(buckets), [buckets]);
  return (
    <div className="space-y-2">
      {groups.map((g) => (
        <RuleRow key={g.limiter_id} group={g} />
      ))}
    </div>
  );
}

function ByResource({ buckets }: { buckets: LimiterBucketRow[] }) {
  const t = useTranslations("observability");
  const rows = useMemo(
    () => [...buckets].sort((a, b) => b.occupied - a.occupied),
    [buckets],
  );
  return (
    <div className="overflow-x-auto">
      <Table className="min-w-[640px]">
        <TableHeader>
          <TableRow>
            <TableHead>{t("limiter.colResource")}</TableHead>
            <TableHead>{t("limiter.colRule")}</TableHead>
            <TableHead>{t("limiter.colMetric")}</TableHead>
            <TableHead>{t("limiter.colSaturation")}</TableHead>
            <TableHead className="text-right">{t("limiter.colWaiters")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={`${r.limiter_id}-${r.bucket}`}>
              <TableCell>
                <ResourceLabel bucket={r.bucket} />
              </TableCell>
              <TableCell className="text-sm">{r.name}</TableCell>
              <TableCell>
                <Badge variant="secondary" className="font-mono text-[10px]">
                  {r.metric}/{r.key_by}
                </Badge>
              </TableCell>
              <TableCell>
                <SaturationBar occupied={r.occupied} capacity={r.capacity} />
              </TableCell>
              <TableCell className="text-right">
                <Waiters n={r.waiters} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

export function LimiterDashboard() {
  const t = useTranslations("observability");
  const { data } = useLimiterUsage();
  const buckets = data?.buckets ?? [];

  const totals = useMemo(() => {
    let occupied = 0;
    let capacity = 0;
    let waiters = 0;
    for (const b of buckets) {
      occupied += b.occupied;
      capacity += b.capacity;
      waiters += b.waiters;
    }
    return { occupied, capacity, waiters };
  }, [buckets]);

  const failedAgents = data?.failed_agents ?? [];

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{t("limiter.clusterTitle")}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          <div className="flex flex-wrap items-baseline gap-x-8 gap-y-2">
            <div>
              <span className="text-2xl font-semibold tabular-nums">
                {totals.occupied}
              </span>
              <span className="text-muted-foreground"> / {totals.capacity}</span>
              <span className="ml-2 text-sm text-muted-foreground">
                {t("limiter.colSaturation")}
              </span>
            </div>
            <div>
              <span className="text-2xl font-semibold tabular-nums">
                <Waiters n={totals.waiters} />
              </span>
              <span className="ml-2 text-sm text-muted-foreground">
                {t("limiter.colWaiters")}
              </span>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">{t("limiter.localSumNote")}</p>
          {failedAgents.length > 0 && (
            <p className="text-xs text-amber-600 dark:text-amber-400">
              {t("failedAgents", { n: failedAgents.length })}
            </p>
          )}
        </CardContent>
      </Card>

      {buckets.length === 0 ? (
        <Card>
          <CardContent className="py-10 text-center text-sm text-muted-foreground">
            {t("limiter.empty")}
          </CardContent>
        </Card>
      ) : (
        <Tabs defaultValue="byRule">
          <TabsList>
            <TabsTrigger value="byRule">{t("limiter.byRule")}</TabsTrigger>
            <TabsTrigger value="byResource">{t("limiter.byResource")}</TabsTrigger>
          </TabsList>
          <TabsContent value="byRule">
            <ByRule buckets={buckets} />
          </TabsContent>
          <TabsContent value="byResource">
            <ByResource buckets={buckets} />
          </TabsContent>
        </Tabs>
      )}
    </div>
  );
}

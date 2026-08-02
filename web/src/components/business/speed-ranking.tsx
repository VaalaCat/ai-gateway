"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ModelName } from "@/components/business/model-name";
import { formatDuration } from "@/lib/utils/format";
import type { SpeedRow } from "@/lib/api/dashboard";

export type SpeedRankingEntity = "model" | "channel";

export interface SpeedRankingProps {
  rows: SpeedRow[];
  entity: SpeedRankingEntity;
  /** ttft: 按 ttft_p95_ms 升序(越低越快); tps: 按 tps_p5 降序(越高越快) */
  metric: "ttft" | "tps";
  title: string;
  /** 表头列名(i18n) */
  rankLabel: string;
  nameLabel: string;
  valueLabel: string;
  emptyText: string;
  topN: number;
  className?: string;
}

/**
 * 最快双榜:TTFT 按 p95 尾延迟、TPS 按 p5 下尾速度排序,
 * 均值会被大量快请求掩盖偶发慢请求,不适合当"最快榜"的排序口径。
 * 缺 percentile 或值为 0 的行排在有效样本之后，并用 em dash 表示无名次、无有效值。
 */
export function SpeedRanking({
  rows,
  entity,
  metric,
  title,
  rankLabel,
  nameLabel,
  valueLabel,
  emptyText,
  topN,
  className,
}: SpeedRankingProps) {
  const hasSample = (row: SpeedRow) =>
    metric === "ttft" ? (row.ttft_p95_ms ?? 0) > 0 : (row.tps_p5 ?? 0) > 0;
  const ranked = rows
    .slice()
    .sort((a, b) => {
      const aValid = hasSample(a);
      const bValid = hasSample(b);
      if (aValid !== bValid) return aValid ? -1 : 1;
      if (!aValid) return 0;
      return metric === "ttft"
        ? (a.ttft_p95_ms ?? 0) - (b.ttft_p95_ms ?? 0)
        : (b.tps_p5 ?? 0) - (a.tps_p5 ?? 0);
    })
    .slice(0, topN);

  return (
    <Card className={className}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
      </CardHeader>
      <CardContent>
        {ranked.length === 0 ? (
          <p className="text-muted-foreground">{emptyText}</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">{rankLabel}</TableHead>
                <TableHead>{nameLabel}</TableHead>
                <TableHead className="text-right">{valueLabel}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {ranked.map((r, i) => (
                <TableRow key={`${r.id ?? ""}-${r.name}-${i}`}>
                  <TableCell className="text-muted-foreground tabular-nums">{hasSample(r) ? i + 1 : "—"}</TableCell>
                  <TableCell>
                    {entity === "model" ? <ModelName name={r.name} /> : r.name}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    {!hasSample(r) ? "—" : metric === "ttft"
                      ? formatDuration(r.ttft_p95_ms ?? 0)
                      : (r.tps_p5 ?? 0).toFixed(1)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

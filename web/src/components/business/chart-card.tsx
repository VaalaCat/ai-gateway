"use client";

import { useTranslations } from "next-intl";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { ResponsiveChartFrame } from "@/components/business/responsive-chart-frame";
import { cn } from "@/lib/utils";

export interface ChartFrameOptions {
  minHeight?: number;
  aspect?: `${number}/${number}`;
  legend?: React.ReactNode;
  legendPlacement?: "bottom" | "responsive-side";
}

interface ChartCardProps {
  title: string;
  sub?: string;
  action?: React.ReactNode;
  loading?: boolean;
  empty?: boolean;
  emptyHint?: string;
  error?: React.ReactNode;
  height?: number;
  chartFrame?: ChartFrameOptions;
  children: React.ReactNode;
  className?: string;
}

export function ChartCard({
  title,
  sub,
  action,
  loading,
  empty,
  emptyHint,
  error,
  height = 224,
  chartFrame,
  children,
  className,
}: ChartCardProps) {
  const t = useTranslations("common");
  const content = loading ? (
    <Skeleton className="h-full w-full" />
  ) : error != null ? (
    <div className="flex h-full w-full items-center justify-center text-sm text-destructive">
      {error}
    </div>
  ) : empty ? (
    <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
      {emptyHint ?? t("noData")}
    </div>
  ) : (
    children
  );
  const hasStatus = Boolean(loading || empty || error != null);

  return (
    <Card className={cn("min-w-0 rounded-[8px]", className)}>
      <CardHeader className="flex min-w-0 flex-col gap-2 space-y-0 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 space-y-1">
          <CardTitle className="text-heading break-words">{title}</CardTitle>
          {sub && <CardDescription>{sub}</CardDescription>}
        </div>
        {action ? <div className="flex min-w-0 flex-wrap items-center gap-2 sm:justify-end">{action}</div> : null}
      </CardHeader>
      <CardContent className="min-w-0">
        {chartFrame ? (
          <ResponsiveChartFrame
            minHeight={chartFrame.minHeight ?? height}
            aspect={chartFrame.aspect}
            legend={hasStatus ? undefined : chartFrame.legend}
            legendPlacement={chartFrame.legendPlacement}
          >
            {content}
          </ResponsiveChartFrame>
        ) : hasStatus ? (
          <ResponsiveChartFrame minHeight={height}>{content}</ResponsiveChartFrame>
        ) : (
          content
        )}
      </CardContent>
    </Card>
  );
}

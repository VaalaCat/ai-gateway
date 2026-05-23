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
import { cn } from "@/lib/utils";

interface ChartCardProps {
  title: string;
  sub?: string;
  action?: React.ReactNode;
  loading?: boolean;
  empty?: boolean;
  emptyHint?: string;
  height?: number;
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
  height = 320,
  children,
  className,
}: ChartCardProps) {
  const t = useTranslations("common");
  const heightStyle = { height: `${height}px` } as const;

  return (
    <Card className={className}>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0">
        <div className="space-y-1">
          <CardTitle>{title}</CardTitle>
          {sub && <CardDescription>{sub}</CardDescription>}
        </div>
        {action}
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className={cn("w-full")} style={heightStyle} />
        ) : empty ? (
          <div
            className="flex w-full items-center justify-center text-sm text-muted-foreground"
            style={heightStyle}
          >
            {emptyHint ?? t("noData")}
          </div>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}

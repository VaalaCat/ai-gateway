"use client";

import { useTranslations } from "next-intl";
import { Activity } from "lucide-react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";

import { CostCell } from "@/components/business/cost-cell";
import { DurationCell } from "@/components/business/duration-cell";
import { DateCell } from "@/components/business/date-cell";
import { TokensCell } from "@/components/business/tokens-cell";
import { useLogs } from "@/lib/api/logs";

interface ProfileUsageCardProps {
  userId: number;
}

const COL_COUNT = 5;

export function ProfileUsageCard({ userId }: ProfileUsageCardProps) {
  const t = useTranslations("profile");
  const tc = useTranslations("common");
  const tl = useTranslations("logs");

  const { data, isLoading } = useLogs({ user_id: userId, page_size: 10 });
  const logs = data?.data ?? [];

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-2 space-y-0">
        <Activity className="size-5 text-muted-foreground" />
        <CardTitle className="text-lg">{t("myUsage")}</CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <Table className="text-body">
          <TableHeader>
            <TableRow>
              <TableHead>{tl("modelName")}</TableHead>
              <TableHead className="text-right">{tl("promptTokens")} / {tl("completionTokens")}</TableHead>
              <TableHead className="text-right">{tl("totalCost")}</TableHead>
              <TableHead className="hidden md:table-cell text-right">{tl("duration")}</TableHead>
              <TableHead className="hidden md:table-cell text-right">{tc("createdAt")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {isLoading ? (
              [0, 1, 2].map((i) => (
                <TableRow key={i}>
                  <TableCell colSpan={COL_COUNT}>
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                </TableRow>
              ))
            ) : logs.length === 0 ? (
              <TableRow>
                <TableCell colSpan={COL_COUNT} className="py-8 text-center text-muted-foreground">
                  {tc("noData")}
                </TableCell>
              </TableRow>
            ) : (
              logs.map((log) => (
                <TableRow key={log.id}>
                  <TableCell>
                    <Badge variant="outline">{log.model_name}</Badge>
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    <TokensCell tokens={log.prompt_tokens} /> / <TokensCell tokens={log.completion_tokens} />
                  </TableCell>
                  <TableCell className="text-right tabular-nums">
                    <CostCell amount={log.total_cost} />
                  </TableCell>
                  <TableCell className="hidden md:table-cell text-right tabular-nums">
                    <DurationCell ms={log.duration} />
                  </TableCell>
                  <TableCell className="hidden md:table-cell text-right text-muted-foreground">
                    <DateCell timestamp={log.created_at} />
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

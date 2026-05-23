"use client";

import { useTranslations } from "next-intl";
import { Key } from "lucide-react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Skeleton } from "@/components/ui/skeleton";

import { StatusBadge } from "@/components/business/status-badge";
import { CopyableText } from "@/components/business/copyable-text";
import { useTokens } from "@/lib/api/tokens";
import { tsToDateStr } from "@/lib/utils/date-range";

interface ProfileTokensCardProps {
  userId: number;
}

const COL_COUNT = 5;

export function ProfileTokensCard({ userId }: ProfileTokensCardProps) {
  const t = useTranslations("profile");
  const tc = useTranslations("common");
  const tt = useTranslations("tokens");

  const { data, isLoading } = useTokens({ page_size: 100 });
  const myTokens = (data?.data ?? []).filter((tk) => tk.user_id === userId);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center gap-2 space-y-0">
        <Key className="size-5 text-muted-foreground" />
        <CardTitle className="text-lg">{t("myTokens")}</CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <Table className="text-body">
          <TableHeader>
            <TableRow>
              <TableHead>{tt("name")}</TableHead>
              <TableHead>{tt("key")}</TableHead>
              <TableHead>{tc("status")}</TableHead>
              <TableHead className="hidden md:table-cell">{tt("expiry")}</TableHead>
              <TableHead className="hidden md:table-cell">{tt("modelsColumn")}</TableHead>
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
            ) : myTokens.length === 0 ? (
              <TableRow>
                <TableCell colSpan={COL_COUNT} className="py-8 text-center text-muted-foreground">
                  {tc("noData")}
                </TableCell>
              </TableRow>
            ) : (
              myTokens.map((tk) => (
                <TableRow key={tk.id}>
                  <TableCell className="font-medium">{tk.name}</TableCell>
                  <TableCell>
                    <CopyableText text={tk.key} display={tk.key.slice(0, 8) + "…"} />
                  </TableCell>
                  <TableCell>
                    <StatusBadge status={tk.status} />
                  </TableCell>
                  <TableCell className="hidden md:table-cell text-muted-foreground">
                    {tk.expired_at ? tsToDateStr(tk.expired_at) : "—"}
                  </TableCell>
                  <TableCell className="hidden md:table-cell text-muted-foreground truncate max-w-xs">
                    {tk.models || "—"}
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

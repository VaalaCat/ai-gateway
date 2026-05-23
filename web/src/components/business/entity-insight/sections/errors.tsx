"use client";

import { useTranslations } from "next-intl";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { ErrorSample } from "@/lib/api/insights";

export function ErrorsSection({ rows }: { rows: ErrorSample[] }) {
  const t = useTranslations("insights.section");
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("errors")}</CardTitle>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("noErrors")}</p>
        ) : (
          <Table className="text-body">
            <TableHeader>
              <TableRow className="text-muted-foreground">
                <TableHead>{t("errorCol.time")}</TableHead>
                <TableHead>{t("errorCol.stage")}</TableHead>
                <TableHead>{t("errorCol.channel")}</TableHead>
                <TableHead>{t("errorCol.model")}</TableHead>
                <TableHead>{t("errorCol.message")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((r, i) => (
                <TableRow key={i}>
                  <TableCell className="text-muted-foreground">
                    {new Date(r.ts * 1000).toLocaleString()}
                  </TableCell>
                  <TableCell>{r.stage ?? ""}</TableCell>
                  <TableCell>{r.channel ?? ""}</TableCell>
                  <TableCell>{r.model ?? ""}</TableCell>
                  <TableCell className="max-w-md truncate" title={r.message}>
                    {r.message}
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

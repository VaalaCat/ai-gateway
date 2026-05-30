"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { CopyableText } from "@/components/business/copyable-text";
import type { InflightSnapshot } from "@/lib/api/agents";

export interface InflightRow extends InflightSnapshot {
  agent_id?: number;
  agent_name?: string;
}

interface InflightTableProps {
  rows: InflightRow[];
  showAgent?: boolean;
  onInterrupt?: (row: InflightRow) => void;
  emptyText: string;
}

export function InflightTable({ rows, showAgent, onInterrupt, emptyText }: InflightTableProps) {
  const t = useTranslations("agents");
  const [target, setTarget] = useState<InflightRow | null>(null);

  if (rows.length === 0) {
    return <p className="text-xs text-muted-foreground px-4 py-3">{emptyText}</p>;
  }

  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            {showAgent && <TableHead className="h-8">{t("inflightColNode")}</TableHead>}
            <TableHead className="h-8">{t("inflightColModel")}</TableHead>
            <TableHead className="h-8">{t("inflightColChannel")}</TableHead>
            <TableHead className="h-8">{t("inflightColStage")}</TableHead>
            <TableHead className="h-8">{t("inflightColElapsed")}</TableHead>
            <TableHead className="h-8">{t("inflightColStream")}</TableHead>
            <TableHead className="h-8">{t("inflightColReqId")}</TableHead>
            {onInterrupt && <TableHead className="h-8">{t("inflightColActions")}</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => {
            const elapsedSec = (row.elapsed_ms / 1000).toFixed(1);
            const isSlow = row.elapsed_ms > 60000;
            return (
              <TableRow key={`${row.agent_id ?? 0}-${row.id}`}>
                {showAgent && <TableCell className="py-1.5">{row.agent_name}</TableCell>}
                <TableCell className="py-1.5">{row.model}</TableCell>
                <TableCell className="py-1.5">
                  <span>{row.channel_name}</span>
                  <span className="ml-1 text-muted-foreground">#{row.channel_id}</span>
                </TableCell>
                <TableCell className="py-1.5">{row.stage}</TableCell>
                <TableCell className="py-1.5">
                  {isSlow ? (
                    <span className="text-destructive font-medium">
                      {elapsedSec}s ({t("inflightSlowHint")})
                    </span>
                  ) : (
                    <span>{elapsedSec}s</span>
                  )}
                </TableCell>
                <TableCell className="py-1.5">
                  {row.is_stream ? (
                    <Badge variant="secondary" className="text-xs px-1.5 py-0">
                      {t("inflightStreamYes")}
                    </Badge>
                  ) : (
                    <span className="text-muted-foreground">{t("inflightStreamNo")}</span>
                  )}
                </TableCell>
                <TableCell className="py-1.5 font-mono">
                  <CopyableText text={row.req_id} />
                </TableCell>
                {onInterrupt && (
                  <TableCell className="py-1.5">
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 text-xs text-destructive"
                      onClick={() => setTarget(row)}
                    >
                      {t("inflightInterrupt")}
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>

      <AlertDialog open={target !== null} onOpenChange={(o) => { if (!o) setTarget(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("inflightInterruptConfirmTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("inflightInterruptConfirmDesc")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("inflightInterruptCancel")}</AlertDialogCancel>
            <AlertDialogAction
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
              onClick={() => { if (target) onInterrupt?.(target); setTarget(null); }}
            >
              {t("inflightInterrupt")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

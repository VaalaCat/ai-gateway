"use client";

import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, Database, RotateCcw, Square, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { DatePicker } from "@/components/business/date-picker/date-picker";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Progress } from "@/components/ui/progress";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { useDataCleanup, type CleanupRun } from "@/lib/hooks/use-data-cleanup";
import { buildCleanupCategoryRows, type CleanupCategoryID } from "@/lib/system-cleanup";
import type { CleanupTableName, TableStats } from "@/lib/types";

const tableTranslationKeys: Record<CleanupTableName, string> = {
  request_logs: "requestLogs",
  request_traces: "requestTraces",
  usage_hourly_buckets: "usageHourlyBuckets",
  usage_duration_histograms: "usageDurationHistograms",
  usage_ttft_histograms: "usageTTFTHistograms",
  usage_tps_histograms: "usageTPSHistograms",
  usage_user_ttft_histograms: "usageUserTTFTHistograms",
  usage_user_tps_histograms: "usageUserTPSHistograms",
  billing_logs: "billingLogs",
  token_daily_billings: "tokenDailyBillings",
  channel_daily_billings: "channelDailyBillings",
};

export interface DataCleanupRunner {
  run: CleanupRun;
  preview(categoryID: CleanupCategoryID, cutoffDate: string): Promise<void>;
  start(): Promise<void>;
  stop(): void;
  retry(): Promise<void>;
  reset(): void;
}

export function utcCalendarToday(now: Date) {
  return new Date(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
}

interface DataCleanupStatsProps {
  tables: TableStats[];
  runner?: DataCleanupRunner;
}

function progressValue(deleted: number, total: number) {
  if (total <= 0) return 0;
  return Math.min(100, (deleted / total) * 100);
}

export function DataCleanupStats({ tables, runner: injectedRunner }: DataCleanupStatsProps) {
  const t = useTranslations("system.cleanup");
  const liveRunner = useDataCleanup();
  const runner = injectedRunner ?? liveRunner;
  const rows = useMemo(() => buildCleanupCategoryRows(tables), [tables]);
  const counts = useMemo(
    () => new Map(tables.map((table) => [`${table.database}:${table.name}`, table.count])),
    [tables],
  );
  const [expanded, setExpanded] = useState<CleanupCategoryID>();
  const [selected, setSelected] = useState<CleanupCategoryID>();
  const [cutoffDate, setCutoffDate] = useState("");
  const [billingRiskAccepted, setBillingRiskAccepted] = useState(false);
  const category = rows.find((row) => row.id === selected);
  const busy = runner.run.status === "deleting" || runner.run.status === "previewing";
  const ready = runner.run.status === "ready"
    && runner.run.categoryID === selected
    && runner.run.cutoffDate === cutoffDate;
  const utcToday = utcCalendarToday(new Date());

  const openCleanup = (categoryID: CleanupCategoryID) => {
    runner.reset();
    setSelected(categoryID);
    setCutoffDate("");
    setBillingRiskAccepted(false);
  };

  const setDialogOpen = (open: boolean) => {
    if (!open && busy) return;
    if (!open) setSelected(undefined);
  };

  const changeCutoffDate = (value: string) => {
    if (value !== cutoffDate && runner.run.status !== "idle") {
      runner.reset();
      setBillingRiskAccepted(false);
    }
    setCutoffDate(value);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Database />
          {t("title")}
        </CardTitle>
        <CardDescription>{t("description")}</CardDescription>
      </CardHeader>
      <CardContent>
        <TooltipProvider>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("category")}</TableHead>
              <TableHead className="w-24">{t("database")}</TableHead>
              <TableHead className="w-28 text-right">{t("rowCount")}</TableHead>
              <TableHead className="w-24 text-right">{t("actions.title")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={row.id}>
                <TableCell className="min-w-0 align-top">
                  <Collapsible open={expanded === row.id} onOpenChange={(open) => setExpanded(open ? row.id : undefined)}>
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="min-w-0 font-medium break-words">{t(`categories.${row.id}`)}</span>
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <CollapsibleTrigger asChild>
                            <Button variant="ghost" size="icon-xs" aria-label={t("actions.showDetails")}>
                              {expanded === row.id ? <ChevronDown /> : <ChevronRight />}
                            </Button>
                          </CollapsibleTrigger>
                        </TooltipTrigger>
                        <TooltipContent>{t("actions.showDetails")}</TooltipContent>
                      </Tooltip>
                    </div>
                    <CollapsibleContent>
                      <div className="mt-2 flex flex-col gap-1 text-xs text-muted-foreground">
                        {row.tables.map((table) => (
                          <div key={`${table.database}:${table.table}`} className="flex max-w-xl items-baseline justify-between gap-4">
                            <span className="break-words">{t(`tables.${tableTranslationKeys[table.table]}`)}</span>
                            <span className="shrink-0 font-mono tabular-nums">
                              {(counts.get(`${table.database}:${table.table}`) ?? 0).toLocaleString()}
                            </span>
                          </div>
                        ))}
                      </div>
                    </CollapsibleContent>
                  </Collapsible>
                </TableCell>
                <TableCell className="align-top text-muted-foreground">{row.tables[0]?.database}</TableCell>
                <TableCell className="text-right align-top font-mono tabular-nums">{row.count.toLocaleString()}</TableCell>
                <TableCell className="text-right align-top">
                  <Button variant="destructive" size="sm" onClick={() => openCleanup(row.id)} aria-label={t("actions.clearCategory")}>
                    <Trash2 data-icon="inline-start" />
                    {t("actions.clearCategory")}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        </TooltipProvider>
      </CardContent>

      <Dialog open={selected !== undefined} onOpenChange={setDialogOpen}>
        <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{category ? t("dialogTitle", { category: t(`categories.${category.id}`) }) : t("dialogTitleFallback")}</DialogTitle>
            <DialogDescription>{t("cutoffDescription")}</DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-5">
            <div className="flex flex-col gap-2">
              <Label>{t("cutoffDate")}</Label>
              <DatePicker
                value={cutoffDate}
                onChange={changeCutoffDate}
                placeholder={t("selectCutoff")}
                disabledRange={{ after: utcToday }}
                disabled={busy}
              />
              <p className="text-xs text-muted-foreground">{t("cutoffUTC")}</p>
            </div>

            <Button
              variant="outline"
              disabled={!selected || !cutoffDate || busy}
              onClick={() => selected && void runner.preview(selected, cutoffDate)}
            >
              {t("actions.preview")}
            </Button>

            {runner.run.tables.length > 0 && (
              <div className="flex flex-col gap-3">
                <div className="flex items-center justify-between gap-4 text-sm">
                  <span>{t("progress")}</span>
                  <span className="font-mono tabular-nums">{runner.run.deleted.toLocaleString()} / {runner.run.totalToDelete.toLocaleString()}</span>
                </div>
                <Progress
                  value={progressValue(runner.run.deleted, runner.run.totalToDelete)}
                  className="h-2"
                  aria-label={t("progress")}
                />
                <div className="flex flex-col gap-2">
                  {runner.run.tables.map((table) => (
                    <div key={`${table.database}:${table.table}`} className="flex items-start justify-between gap-4 text-sm">
                      <div className="min-w-0">
                        <p className="break-words">{t(`tables.${tableTranslationKeys[table.table]}`)}</p>
                        {table.error && <p className="text-xs text-destructive">{table.error}</p>}
                      </div>
                      <span className="shrink-0 font-mono tabular-nums">{table.deleted.toLocaleString()} / {table.to_delete.toLocaleString()}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {selected === "billingFacts" && ready && (
              <Alert variant="destructive">
                <AlertTitle>{t("billingFactsRiskTitle")}</AlertTitle>
                <AlertDescription className="flex flex-col gap-3">
                  <p>{t("billingFactsRisk")}</p>
                  <div className="flex items-start gap-2">
                    <Checkbox
                      id="accept-billing-risk"
                      checked={billingRiskAccepted}
                      onCheckedChange={(checked) => setBillingRiskAccepted(checked === true)}
                      aria-label={t("acceptBillingRisk")}
                    />
                    <Label htmlFor="accept-billing-risk">{t("acceptBillingRisk")}</Label>
                  </div>
                </AlertDescription>
              </Alert>
            )}

            {runner.run.status === "paused" && (
              <Alert variant="destructive">
                <AlertTitle>{t("paused")}</AlertTitle>
                <AlertDescription>{t("pausedDescription")}</AlertDescription>
              </Alert>
            )}
            {runner.run.status === "completed" && (
              <p className="text-sm font-medium">{t("completedDeleted", { count: runner.run.deleted })}</p>
            )}
            {runner.run.status === "stopped" && <p className="text-sm">{t("stopped")}</p>}
          </div>

          <DialogFooter>
            {busy ? (
              <Button variant="destructive" onClick={runner.stop}>
                <Square data-icon="inline-start" />
                {t("actions.stop")}
              </Button>
            ) : runner.run.status === "paused" ? (
              <Button onClick={() => void runner.retry()}>
                <RotateCcw data-icon="inline-start" />
                {t("actions.retry")}
              </Button>
            ) : ready ? (
              <Button
                variant="destructive"
                disabled={runner.run.totalToDelete === 0 || (selected === "billingFacts" && !billingRiskAccepted)}
                onClick={() => void runner.start()}
              >
                <Trash2 data-icon="inline-start" />
                {t("actions.confirmCleanup")}
              </Button>
            ) : null}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

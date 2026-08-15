"use client";

import { useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { keepPreviousData } from "@tanstack/react-query";
import { TimerReset } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { ObservabilityHeader } from "@/components/business/observability-header";
import { ColumnVisibility } from "@/components/data-table/column-visibility";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec, FilterValues } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { isLogDatabaseUnavailable, useAPIRequestLogs, type APIRequestLog, type APIRequestLogParams } from "@/lib/api/api-logs";
import { useCapabilities } from "@/lib/api/capabilities";
import { ApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth";
import { useUserPref } from "@/hooks/use-user-pref";
import { PAGE_SIZES } from "@/lib/constants";
import { parseNonNegativeDecimal, parsePositiveDecimal } from "@/lib/utils/decimal";
import { dateStrToExclusiveEndTs, tsToDateStr } from "@/lib/utils/date-range";
import { defaultLogColumnVisibility, useLogColumns } from "./_components/log-columns";
import { APIRequestDetails } from "./_components/request-details";

function positiveInteger(value: FilterValues[string]) {
  return parsePositiveDecimal(value);
}

function unixSeconds(value: FilterValues[string]) {
  return parseNonNegativeDecimal(value);
}

function statusCode(value: FilterValues[string]) {
  const parsed = parseNonNegativeDecimal(value);
  return parsed === 0 || (parsed !== undefined && parsed >= 100 && parsed <= 999)
    ? parsed
    : undefined;
}

function defaultLogTimeWindow(): FilterValues {
  const end = Math.floor(Date.now() / 1_000);
  return {
    start: end - 7 * 86_400,
    end,
  };
}

function logChildPickerQuery(values: FilterValues) {
  const apiServiceId = positiveInteger(values.api_service_id);
  return { apiServiceId, disabled: apiServiceId === undefined };
}

function logQuery(
  values: FilterValues,
  page: number,
  pageSize: number,
): APIRequestLogParams {
  const requestID = typeof values.request_id === "string" ? values.request_id : "";
  const apiServiceID = positiveInteger(values.api_service_id);
  const apiRouteID = positiveInteger(values.api_route_id);
  const apiUpstreamID = positiveInteger(values.api_upstream_id);
  const tokenID = positiveInteger(values.token_id);
  const status = statusCode(values.status_code);
  const start = unixSeconds(values.start);
  const end = unixSeconds(values.end);

  return {
    ...(requestID ? { request_id: requestID } : {}),
    ...(apiServiceID !== undefined ? { api_service_id: apiServiceID } : {}),
    ...(apiRouteID !== undefined ? { api_route_id: apiRouteID } : {}),
    ...(apiUpstreamID !== undefined ? { api_upstream_id: apiUpstreamID } : {}),
    ...(tokenID !== undefined ? { token_id: tokenID } : {}),
    ...(status !== undefined ? { status_code: status } : {}),
    ...(start !== undefined ? { start } : {}),
    ...(end !== undefined ? { end } : {}),
    page,
    page_size: pageSize,
  };
}

function LogError({ error }: { error: unknown }) {
  const t = useTranslations("apiLogs");
  const key = isLogDatabaseUnavailable(error)
    ? "logUnavailable"
    : error instanceof ApiError && error.status === 403
      ? "permissionDenied"
      : error instanceof ApiError && error.status === 400
        ? "invalidFilters"
        : "loadFailed";
  return (
    <Alert variant="destructive">
      <AlertTitle>{t(key)}</AlertTitle>
      <AlertDescription>{t(`${key}Description`)}</AlertDescription>
    </Alert>
  );
}

function PageSkeleton() {
  const t = useTranslations("apiLogs");
  return (
    <div className="flex flex-col gap-4" role="status" aria-label={t("loading")}>
      <Skeleton className="h-10 w-full" />
      <Skeleton className="h-64 w-full" />
    </div>
  );
}

export default function APILogsPage() {
  const t = useTranslations("apiLogs");
  const { user, isAdmin } = useAuth();
  const capability = useCapabilities(user?.user_id);
  const enabled = capability.data?.generic_api?.logs === true;
  const patchSearchParams = useSearchParamPatch();
  const filterSpec = useMemo(() => ({
    request_id: { kind: "text", label: t("requestID"), debounceMs: 300 },
    status_code: { kind: "text", label: t("statusCode"), debounceMs: 300, controlWidth: "compact" },
    ...(isAdmin ? {
      api_service_id: { kind: "picker", entity: "api-service" },
      api_route_id: {
      kind: "picker",
      entity: "api-route",
      advanced: true,
      pickerQuery: logChildPickerQuery,
      },
      api_upstream_id: {
      kind: "picker",
      entity: "api-upstream",
      advanced: true,
      pickerQuery: logChildPickerQuery,
      },
      token_id: { kind: "picker", entity: "token", advanced: true },
    } : {}),
  }) satisfies FilterSpec, [isAdmin, t]);
  const urlFilterSpec = useMemo(() => ({
    time: { kind: "time", defaultDays: 7, maxHourDays: 365, showGran: false },
    ...filterSpec,
  }) satisfies FilterSpec, [filterSpec]);
  // behavior change: an empty URL reads one fixed rolling seven-day range without persisting defaults.
  const [defaultFilters] = useState(defaultLogTimeWindow);
  const [filters, setFilters] = useFilterState(urlFilterSpec, {
    defaults: defaultFilters,
    patchSearchParams,
  });
  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.LOGS, { patchSearchParams });
  const [rawLog, setRawLog] = useState<APIRequestLog | null>(null);
  const [autoRefreshMs, setAutoRefreshMs] = useUserPref<number | null>("api-logs-auto-refresh", null);
  const autoRefreshLabel = autoRefreshMs === null
    ? t("autoRefreshOff")
    : t("autoRefreshEvery", { seconds: autoRefreshMs / 1_000 });
  const query = useMemo(() => logQuery(filters, page, pageSize), [filters, page, pageSize]);
  const logs = useAPIRequestLogs(query, isAdmin ? "admin" : "portal", {
    enabled,
    placeholderData: keepPreviousData,
    refetchInterval: autoRefreshMs ?? false,
  });
  const columns = useLogColumns(setRawLog, { showInternal: isAdmin });
  const rawLogText = useMemo(() => rawLog ? JSON.stringify(rawLog, null, 2) : "", [rawLog]);
  const responsePage = logs.data?.page ?? page;
  const responsePageSize = logs.data?.page_size ?? pageSize;
  const total = logs.data?.total ?? 0;
  const pageCount = Math.max(1, Math.ceil(total / responsePageSize));
  const headerStart = unixSeconds(filters.start) ?? unixSeconds(defaultFilters.start) ?? 0;
  const headerExclusiveEnd = unixSeconds(filters.end) ?? unixSeconds(defaultFilters.end) ?? headerStart + 86_400;
  const headerRange = {
    start: headerStart,
    end: Math.max(headerStart, headerExclusiveEnd - 1),
    gran: "day" as const,
  };

  function updateFilters(next: Partial<FilterValues>) {
    setFilters("api_service_id" in next
      ? { ...next, api_route_id: undefined, api_upstream_id: undefined }
      : next);
  }

  useEffect(() => {
    if (!logs.data || logs.error || logs.isPlaceholderData) return;
    const normalizedPage = Math.min(logs.data.page, pageCount);
    if (page !== normalizedPage || pageSize !== logs.data.page_size) {
      setPagination(normalizedPage, logs.data.page_size);
    }
  }, [logs.data, logs.error, logs.isPlaceholderData, page, pageCount, pageSize, setPagination]);

  return (
    <div className="space-y-4">
      <ObservabilityHeader
        title={t("title")}
        subtitle={t("description")}
        range={headerRange}
        // behavior change: ObservabilityHeader emits an inclusive end; API log queries use an exclusive end.
        onRangeChange={({ start, end }) => setFilters({
          start,
          end: dateStrToExclusiveEndTs(tsToDateStr(end)),
        })}
        onRefresh={() => void logs.refetch()}
        refreshing={logs.isFetching}
        showGranularity={false}
        maxDays={365}
      />
      {capability.isPending || capability.isLoading ? <PageSkeleton /> : capability.error ? (
        <LogError error={capability.error} />
      ) : !enabled ? (
        <Alert>
          <AlertTitle>{t("unavailable")}</AlertTitle>
          <AlertDescription>{t("permissionRequired")}</AlertDescription>
        </Alert>
      ) : logs.error ? (
        <LogError error={logs.error} />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={logs.data?.data ?? []}
            loading={logs.isLoading}
            total={total}
            page={responsePage}
            pageSize={responsePageSize}
            pageCount={pageCount}
            onPaginationChange={(nextPage, nextSize) => setPagination(nextSize === responsePageSize ? nextPage : 1, nextSize)}
            defaultColumnVisibility={defaultLogColumnVisibility}
            getRowId={(row) => row.request_id}
            renderExpandedRow={(row) => <APIRequestDetails request={row.original} showInternal={isAdmin} />}
            storageKey="api-request-logs-columns"
            toolbar={(table) => (
              <FilterableToolbar
                spec={filterSpec}
                value={filters}
                onChange={updateFilters}
                secondaryContent={(
                  <TooltipProvider>
                    <Tooltip>
                      <Select
                        value={autoRefreshMs === null ? "off" : String(autoRefreshMs)}
                        onValueChange={(value) => setAutoRefreshMs(value === "off" ? null : Number(value))}
                      >
                        <TooltipTrigger asChild>
                          <SelectTrigger
                            aria-label={autoRefreshLabel}
                            className="!size-9 justify-center gap-0 overflow-hidden px-0 sm:!h-8 sm:!w-40 sm:justify-between sm:gap-2 sm:overflow-visible sm:px-3 [&_[data-slot=select-icon]]:hidden sm:[&_[data-slot=select-icon]]:block"
                            size="sm"
                          >
                            <TimerReset className="sm:hidden" />
                            <span className="sr-only sm:not-sr-only"><SelectValue /></span>
                          </SelectTrigger>
                        </TooltipTrigger>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value="off">{t("autoRefreshOff")}</SelectItem>
                            <SelectItem value="5000">{t("autoRefreshEvery", { seconds: 5 })}</SelectItem>
                            <SelectItem value="10000">{t("autoRefreshEvery", { seconds: 10 })}</SelectItem>
                            <SelectItem value="30000">{t("autoRefreshEvery", { seconds: 30 })}</SelectItem>
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                      <TooltipContent>{autoRefreshLabel}</TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                )}
                primaryAction={<ColumnVisibility table={table} />}
              />
            )}
          />
          <Dialog open={Boolean(rawLog)} onOpenChange={(open) => { if (!open) setRawLog(null); }}>
            <DialogContent className="sm:max-w-3xl">
              <DialogHeader><DialogTitle>{t("rawJsonTitle")}</DialogTitle></DialogHeader>
              <pre className="max-h-[60vh] overflow-auto rounded-md bg-muted p-3 text-meta"><code>{rawLogText}</code></pre>
              <DialogFooter>
                <DialogClose asChild><Button variant="outline">{t("close")}</Button></DialogClose>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </>
      )}
    </div>
  );
}

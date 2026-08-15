"use client";

import Link from "next/link";
import { useCallback, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import type { ColumnDef, ExpandedState, Table as TanStackTable, VisibilityState } from "@tanstack/react-table";
import { ChevronRight, MoreHorizontal, Plus } from "lucide-react";

import { ProtocolBadge } from "@/components/business/api-badges";
import { BackgroundRefreshStatus } from "@/components/business/background-refresh-status";
import { StatusBadge } from "@/components/business/status-badge";
import { ColumnVisibility } from "@/components/data-table/column-visibility";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec, FilterValues } from "@/components/data-table/filter-spec";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { useBreakpoint } from "@/lib/hooks/use-breakpoint";
import {
  type APIBackend,
  type APIRoute,
  type APIService,
  useAPIBackend,
} from "@/lib/api/api-services";

export interface RouteTableRow {
  route: APIRoute;
}

export interface RouteTableFilters {
  search: string;
  status?: number;
}

export interface RouteDataTableProps {
  service: APIService;
  origin: string;
  routes: APIRoute[];
  loading: boolean;
  refreshing: boolean;
  error: unknown;
  onRetry: () => void;
  total: number;
  displayedPage: number;
  displayedPageSize: number;
  requestedPage: number;
  isPlaceholderData: boolean;
  filters: RouteTableFilters;
  expandedRouteID?: number;
  canManage: boolean;
  onFiltersChange: (patch: Partial<RouteTableFilters>) => void;
  onExpandedRouteChange: (routeID?: number) => void;
  onPaginationChange: (page: number, pageSize: number) => void;
  onDeleteRoute: (route: APIRoute) => void;
  renderExpandedRoute: (route: APIRoute) => React.ReactNode;
}

type RouteTableTranslation = (key: string, values?: Record<string, string | number>) => string;

export interface RouteTableColumnContext {
  service: APIService;
  origin: string;
  expandedRouteID?: number;
  canManage: boolean;
  onExpandedRouteChange: (routeID?: number) => void;
  onDeleteRoute: (route: APIRoute) => void;
  t: RouteTableTranslation;
  compact: boolean;
}

function decodePathSegment(segment: string) {
  try {
    return decodeURIComponent(segment);
  } catch {
    return undefined;
  }
}

function publicRoutePath(raw: string) {
  try {
    const relative = raw.startsWith("/");
    const parsed = new URL(raw, relative ? "http://route-search.local" : undefined);
    if (!relative && parsed.protocol !== "http:" && parsed.protocol !== "https:") return undefined;
    return parsed.pathname.split("/").filter(Boolean);
  } catch {
    return undefined;
  }
}

export function normalizeRouteTableSearch(raw: string, serviceSlug?: string) {
  const trimmed = raw.trim();
  const path = publicRoutePath(trimmed);
  if (!path || path[0] !== "v1" || path[1] !== "api" || path.length < 4) return trimmed;
  const decodedService = decodePathSegment(path[2]!);
  const decodedRoute = decodePathSegment(path[3]!);
  if (!decodedRoute || (serviceSlug !== undefined && decodedService !== serviceSlug)) return trimmed;
  return decodedRoute;
}

function compactMethods(route: APIRoute) {
  if (route.allowed_methods.length === 0) return { labels: [] as string[], remaining: 0 };
  return {
    labels: route.allowed_methods.slice(0, 2),
    remaining: Math.max(0, route.allowed_methods.length - 2),
  };
}

function RouteTargetCell({ route, t }: { route: APIRoute; t: RouteTableTranslation }) {
  const target = useAPIBackend(route.backend_id);
  if (target.isLoading) {
    const label = t("targetLoading");
    return (
      <div role="status" aria-live="polite" aria-label={label}>
        <span className="sr-only">{label}</span>
        <Skeleton aria-hidden="true" className="h-8 w-32" />
      </div>
    );
  }
  if (target.error || !target.data) return <span className="text-sm text-destructive">{t("targetLoadFailed")}</span>;
  return <TargetSummary target={target.data} t={t} />;
}

function TargetSummary({ target, t }: { target: APIBackend; t: RouteTableTranslation }) {
  return (
    <div className="flex min-w-0 max-w-64 flex-col gap-0.5">
      <span className="truncate font-medium" title={target.name}>{target.name}</span>
      <span className="text-xs text-muted-foreground">
        {t("endpointCount", { count: target.upstream_count })}
      </span>
    </div>
  );
}

function expandColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "expand",
    header: "",
    enableHiding: false,
    size: 40,
    cell: ({ row }) => {
      const route = row.original.route;
      const expanded = ctx.expandedRouteID === route.id;
      return (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="max-sm:min-h-11 max-sm:min-w-11"
          aria-label={ctx.t(expanded ? "collapseRoute" : "expandRoute", { route: route.slug })}
          aria-expanded={expanded}
          onClick={(event) => {
            event.stopPropagation();
            ctx.onExpandedRouteChange(expanded ? undefined : route.id);
          }}
        >
          <ChevronRight className={expanded ? "rotate-90" : undefined} />
        </Button>
      );
    },
  };
}

function routePath(service: APIService, route: APIRoute) {
  return `/v1/api/${service.slug}/${route.slug}${route.forward_subpath ? "/…" : ""}`;
}

function MethodSummary({ route, t }: { route: APIRoute; t: RouteTableTranslation }) {
  const methods = compactMethods(route);
  return (
    <div className="flex items-center gap-1 overflow-hidden">
      {methods.labels.length === 0
        ? <Badge variant="secondary">{t("allMethodsShort")}</Badge>
        : methods.labels.map((method) => <Badge key={method} variant="secondary">{method}</Badge>)}
      {methods.remaining > 0 ? <Badge variant="outline">+{methods.remaining}</Badge> : null}
    </div>
  );
}

function routeColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "route",
    header: ctx.t("routes"),
    enableHiding: false,
    size: ctx.compact ? 230 : 300,
    meta: { cellClassName: "min-w-0" },
    cell: ({ row }) => {
      const route = row.original.route;
      const path = routePath(ctx.service, route);
      return (
        <div className="flex min-w-0 flex-col gap-1 py-0.5">
          <div className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium" title={route.slug}>{route.slug}</span>
            {ctx.compact ? <StatusBadge status={route.status} /> : null}
          </div>
          <div className="flex min-w-0 items-center gap-2">
            {ctx.compact ? <MethodSummary route={route} t={ctx.t} /> : null}
            <code className="min-w-0 truncate text-xs text-muted-foreground" title={path}>{path}</code>
          </div>
        </div>
      );
    },
  };
}

function methodsColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "methods",
    header: ctx.t("allowedMethods"),
    cell: ({ row }) => {
      return <MethodSummary route={row.original.route} t={ctx.t} />;
    },
  };
}

function targetColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "target",
    header: ctx.t("target"),
    cell: ({ row }) => <RouteTargetCell route={row.original.route} t={ctx.t} />,
  };
}

function protocolColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "protocols",
    header: ctx.t("protocols"),
    cell: ({ row }) => (
      <div className="flex items-center gap-1 overflow-hidden">
        {row.original.route.protocols.map((protocol) => <ProtocolBadge key={protocol} protocol={protocol} />)}
      </div>
    ),
  };
}

function statusColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "status",
    header: ctx.t("status"),
    cell: ({ row }) => <StatusBadge status={row.original.route.status} />,
  };
}

function actionsColumn(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow> {
  return {
    id: "actions",
    header: "",
    enableHiding: false,
    size: 48,
    cell: ({ row }) => {
      if (!ctx.canManage) return null;
      const route = row.original.route;
      return (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="ghost" size="icon-sm" className="max-sm:min-h-11 max-sm:min-w-11" aria-label={ctx.t("routeActions")}>
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" mobileTouchSize="comfortable">
            <DropdownMenuGroup>
              <DropdownMenuItem asChild>
                <Link href={`/api-services/routes/edit?id=${route.id}&service_id=${ctx.service.id}`}>{ctx.t("editRoute")}</Link>
              </DropdownMenuItem>
              <DropdownMenuItem variant="destructive" onSelect={() => ctx.onDeleteRoute(route)}>{ctx.t("deleteRoute")}</DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      );
    },
  };
}

export function buildRouteTableColumns(ctx: RouteTableColumnContext): ColumnDef<RouteTableRow>[] {
  return [
    expandColumn(ctx),
    routeColumn(ctx),
    methodsColumn(ctx),
    targetColumn(ctx),
    protocolColumn(ctx),
    statusColumn(ctx),
    actionsColumn(ctx),
  ];
}

function routeTableFilterSpec(t: RouteTableTranslation): FilterSpec {
  return {
    search: { kind: "text", label: t("routeSearch"), placeholder: t("routeSearch"), debounceMs: 300 },
    status: {
      kind: "enum",
      label: t("status"),
      options: [
        { value: "1", label: t("enabled") },
        { value: "0", label: t("disabled") },
      ],
    },
  };
}

function filterValues(filters: RouteTableFilters): FilterValues {
  return {
    search: filters.search,
    status: filters.status === undefined ? "" : String(filters.status),
  };
}

function filterPatch(patch: Partial<FilterValues>, serviceSlug: string): Partial<RouteTableFilters> {
  const normalized: Partial<RouteTableFilters> = {};
  if (patch.search !== undefined) normalized.search = normalizeRouteTableSearch(String(patch.search), serviceSlug);
  if (patch.status !== undefined) normalized.status = patch.status === "0" ? 0 : patch.status === "1" ? 1 : undefined;
  return normalized;
}

function RouteTableToolbar({
  table,
  service,
  filters,
  refreshing,
  canManage,
  onFiltersChange,
  t,
}: {
  table: TanStackTable<RouteTableRow>;
  service: APIService;
  filters: RouteTableFilters;
  refreshing: boolean;
  canManage: boolean;
  onFiltersChange: (patch: Partial<RouteTableFilters>) => void;
  t: RouteTableTranslation;
}) {
  const spec = useMemo(() => routeTableFilterSpec(t), [t]);
  return (
    <FilterableToolbar
      spec={spec}
      value={filterValues(filters)}
      onChange={(patch) => onFiltersChange(filterPatch(patch, service.slug))}
      mobileTouchSize="comfortable"
      firstFilterFullWidthOnMobile
      secondaryContent={(
        <>
          <BackgroundRefreshStatus refreshing={refreshing} label={t("refreshingRoutes")} />
          <ColumnVisibility table={table} mobileTouchSize="comfortable" />
        </>
      )}
      primaryAction={canManage ? (
        <Button asChild size="sm" className="max-sm:size-11 max-sm:p-0">
          <Link href={`/api-services/routes/new?service_id=${service.id}`} aria-label={t("createRoute")}>
            <Plus data-icon="inline-start" />
            <span className="max-sm:sr-only">{t("createRoute")}</span>
          </Link>
        </Button>
      ) : undefined}
    />
  );
}

function expandedRouteID(state: ExpandedState) {
  if (state === true) return undefined;
  const open = Object.entries(state).find(([, expanded]) => expanded)?.[0];
  if (open === undefined) return undefined;
  const routeID = Number(open);
  return Number.isSafeInteger(routeID) && routeID > 0 ? routeID : undefined;
}

export function RouteDataTable({
  service,
  origin,
  routes,
  loading,
  refreshing,
  error,
  onRetry,
  total,
  displayedPage,
  displayedPageSize,
  requestedPage,
  isPlaceholderData,
  filters,
  expandedRouteID: openRouteID,
  canManage,
  onFiltersChange,
  onExpandedRouteChange,
  onPaginationChange,
  onDeleteRoute,
  renderExpandedRoute,
}: RouteDataTableProps) {
  const translate = useTranslations("apiServices");
  const t: RouteTableTranslation = useCallback(
    (key, values) => translate(key as never, values as never),
    [translate],
  );
  const rows = useMemo(() => routes.map((route) => ({ route })), [routes]);
  const breakpoint = useBreakpoint();
  const compact = breakpoint === "xs";
  const [desktopColumnVisibility, setDesktopColumnVisibility] = useState<VisibilityState>({});
  const columnVisibility = compact
    ? { methods: false, target: false, protocols: false, status: false }
    : desktopColumnVisibility;
  const columns = useMemo(() => buildRouteTableColumns({
    service,
    origin,
    expandedRouteID: openRouteID,
    canManage,
    onExpandedRouteChange,
    onDeleteRoute,
    t,
    compact,
  }), [canManage, compact, onDeleteRoute, onExpandedRouteChange, openRouteID, origin, service, t]);
  const paginationPending = isPlaceholderData || requestedPage !== displayedPage;

  return (
    <div data-testid="route-data-table-region" className="flex min-w-0 max-w-full flex-col gap-4 max-sm:[&_[data-slot=select-trigger]]:min-h-11 max-sm:[&_input]:min-h-11">
      {error ? (
        <Alert variant="destructive">
          <AlertTitle>{t("routesLoadFailed")}</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>{t("routesLoadFailedDescription")}</span>
            <Button type="button" size="sm" variant="outline" onClick={onRetry}>{t("retry")}</Button>
          </AlertDescription>
        </Alert>
      ) : null}
      <DataTable
        columns={columns}
        data={rows}
        loading={loading}
        total={total}
        page={displayedPage}
        pageSize={displayedPageSize}
        pageCount={Math.max(1, Math.ceil(total / displayedPageSize))}
        paginationDisabled={paginationPending}
        onPaginationChange={onPaginationChange}
        getRowId={(row) => String(row.route.id)}
        expandedState={openRouteID ? { [String(openRouteID)]: true } : {}}
        onExpandedStateChange={(state) => onExpandedRouteChange(expandedRouteID(state))}
        renderExpandedRow={(row) => renderExpandedRoute(row.original.route)}
        expandedRowWidth="viewport"
        tableLayout={compact ? "fixed" : "auto"}
        columnVisibilityState={columnVisibility}
        onColumnVisibilityChange={(next) => {
          if (!compact) setDesktopColumnVisibility(next);
        }}
        toolbar={(table) => (
          <RouteTableToolbar
            table={table}
            service={service}
            filters={filters}
            refreshing={refreshing}
            canManage={canManage}
            onFiltersChange={onFiltersChange}
            t={t}
          />
        )}
      />
    </div>
  );
}

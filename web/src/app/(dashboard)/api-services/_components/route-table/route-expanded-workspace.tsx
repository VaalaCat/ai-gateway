"use client";

import Link from "next/link";
import { MoreHorizontal, Plus } from "lucide-react";
import { useTranslations } from "next-intl";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ColumnDef } from "@tanstack/react-table";

import { ProtocolBadge } from "@/components/business/api-badges";
import { StatusBadge } from "@/components/business/status-badge";
import { DataTable } from "@/components/data-table/data-table";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import {
  useAPIBackend,
  useAllAPIUpstreams,
  useAPIRoutePreview,
  type APIBackend,
  type APIRoute,
  type APIRoutePreview,
  type APIRoutePreviewInput,
  type APIService,
  type APIUpstream,
} from "@/lib/api/api-services";

import { SegmentedURL } from "../segmented-url";
import { routeReturnQuery } from "../route-return";
import { DeleteConfirmationDialog } from "../../delete-confirmation-dialog";
import { buildConfiguredEndpointURL, buildConfiguredPublicRouteURL } from "./configured-route-url";
import { InlineRouteInvocationPreview } from "./inline-route-invocation-preview";
import { routePreviewDependencyRevision } from "@/lib/route-preview-dependency-revision";
import { useBreakpoint } from "@/lib/hooks/use-breakpoint";

export interface RouteExpandedWorkspaceProps {
  service: APIService;
  origin: string;
  route: APIRoute;
  canManage: boolean;
  onDeleteTarget: (target: APIBackend) => void;
  onDeleteEndpoint: (endpoint: APIUpstream) => Promise<void> | void;
}

type Translation = (key: string, values?: Record<string, string | number>) => string;
type TargetState = "loading" | "error" | "missing" | "ready";

interface TargetViewProps {
  service: APIService;
  route: APIRoute;
  target?: APIBackend;
  canManage: boolean;
  onDeleteTarget: (target: APIBackend) => void;
  onRetry: () => void;
  t: Translation;
}

type TargetStateRenderer = (props: TargetViewProps) => React.ReactNode;

export function previewDraftForConfiguredRoute(route: APIRoute): APIRoutePreviewInput {
  return {
    api_service_id: route.api_service_id,
    slug: route.slug,
    upstream_path: route.upstream_path,
    forward_subpath: route.forward_subpath,
    sample: {
      method: route.example_request.method || "GET",
      subpath: "",
      query: "",
      headers: {},
      body: "",
    },
    target: { mode: "existing", backend_id: route.backend_id },
  };
}

function RouteEditLink({ service, route, t }: { service: APIService; route: APIRoute; t: Translation }) {
  return (
    <Button asChild size="sm" variant="outline">
      <Link href={`/api-services/routes/edit?id=${route.id}&service_id=${service.id}`}>{t("editRoute")}</Link>
    </Button>
  );
}

function TargetActions({ service, route, target, onDelete, t }: { service: APIService; route: APIRoute; target: APIBackend; onDelete: () => void; t: Translation }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" className="max-sm:min-h-11 max-sm:min-w-11" aria-label={t("targetActions")}>
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" mobileTouchSize="comfortable">
        <DropdownMenuGroup>
          <DropdownMenuItem asChild>
            <Link href={`/api-services/backends/edit?id=${target.id}&service_id=${service.id}&${routeReturnQuery(route)}`}>{t("editTarget")}</Link>
          </DropdownMenuItem>
          <DropdownMenuItem variant="destructive" onSelect={onDelete}>{t("deleteTarget")}</DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function renderTargetSkeleton({ t }: TargetViewProps) {
  const label = t("targetLoading");
  return (
    <div className="flex min-w-0 items-center justify-between gap-3" role="status" aria-live="polite" aria-label={label}>
      <span className="sr-only">{label}</span>
      <div className="flex flex-col gap-2">
        <Skeleton aria-hidden="true" className="h-5 w-40" />
        <Skeleton aria-hidden="true" className="h-4 w-24" />
      </div>
      <Skeleton aria-hidden="true" className="size-9" />
    </div>
  );
}

function renderTargetError({ service, route, canManage, onRetry, t }: TargetViewProps) {
  return (
    <Alert variant="destructive">
      <AlertTitle>{t("targetLoadFailed")}</AlertTitle>
      <AlertDescription>
        <p>{t("targetLoadFailedDescription")}</p>
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" onClick={onRetry}>{t("retry")}</Button>
          {canManage ? <RouteEditLink service={service} route={route} t={t} /> : null}
        </div>
      </AlertDescription>
    </Alert>
  );
}

function renderMissingTarget({ service, route, canManage, t }: TargetViewProps) {
  return (
    <Alert variant="destructive">
      <AlertTitle>{t("routeTargetMissing")}</AlertTitle>
      <AlertDescription>
        <p>{t("routeTargetMissingDescription")}</p>
        {canManage ? <RouteEditLink service={service} route={route} t={t} /> : null}
      </AlertDescription>
    </Alert>
  );
}

function renderTarget({ service, route, target, canManage, onDeleteTarget, t }: TargetViewProps) {
  if (!target) return null;
  return (
    <div className="flex min-w-0 items-start justify-between gap-3">
      <div className="min-w-0">
        <h3 className="break-words text-base font-semibold">{target.name}</h3>
        <p className="text-sm text-muted-foreground">{t("endpointCount", { count: target.upstream_count })}</p>
      </div>
      {canManage ? <TargetActions service={service} target={target} route={route} onDelete={() => onDeleteTarget(target)} t={t} /> : null}
    </div>
  );
}

const targetStateViews: Record<TargetState, TargetStateRenderer> = {
  loading: renderTargetSkeleton,
  error: renderTargetError,
  missing: renderMissingTarget,
  ready: renderTarget,
};

function targetState(query: { data?: APIBackend; isLoading: boolean; error: unknown }): TargetState {
  if (query.isLoading) return "loading";
  if (typeof query.error === "object" && query.error !== null && "status" in query.error && query.error.status === 404) return "missing";
  if (query.error) return "error";
  return query.data ? "ready" : "missing";
}

function EndpointActions({ service, route, endpoint, onDelete, t }: { service: APIService; route: APIRoute; endpoint: APIUpstream; onDelete: () => void; t: Translation }) {
  const context = routeReturnQuery(route);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" className="max-sm:min-h-11 max-sm:min-w-11" aria-label={t("endpointActions")}>
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" mobileTouchSize="comfortable">
        <DropdownMenuGroup>
          <DropdownMenuItem asChild>
            <Link href={`/api-services/upstreams/edit?id=${endpoint.id}&service_id=${service.id}&${context}`}>{t("editUpstream")}</Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link href={`/api-services/upstreams/new?service_id=${service.id}&copy_id=${endpoint.id}&${context}`}>{t("copyEndpointConfig")}</Link>
          </DropdownMenuItem>
          <DropdownMenuItem variant="destructive" onSelect={onDelete}>{t("deleteUpstream")}</DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function previewEndpoint(preview: APIRoutePreview | undefined, endpointID: number) {
  return preview?.endpoints.find((item) => item.upstream_id === endpointID);
}

function EndpointURL({ route, endpoint, preview, previewLoading, t }: {
  route: APIRoute;
  endpoint: APIUpstream;
  preview?: APIRoutePreview;
  previewLoading: boolean;
  t: Translation;
}) {
  if (previewLoading) {
    const label = t("configuredEndpointURLLoading");
    return (
      <div role="status" aria-live="polite" aria-label={label}>
        <span className="sr-only">{label}</span>
        <Skeleton aria-hidden="true" className="h-7 w-full max-w-xl" />
      </div>
    );
  }
  const candidate = previewEndpoint(preview, endpoint.id);
  if (!candidate) return <span className="text-xs text-destructive">{t("configuredEndpointURLUnavailable")}</span>;
  const configuredURL = buildConfiguredEndpointURL({
    finalURL: candidate.final_url,
    endpointName: endpoint.name,
    routePath: route.upstream_path,
    forwardSubpath: route.forward_subpath,
  });
  return <SegmentedURL {...configuredURL} layout="truncate" copyLabel={t("copyEndpointURL", { name: endpoint.name })} />;
}

function EndpointRows({ target, service, route, endpoints, preview, previewLoading, canManage, onDeleteEndpoint, restoreFocus, t }: {
  target: APIBackend;
  service: APIService;
  route: APIRoute;
  endpoints: APIUpstream[];
  preview?: APIRoutePreview;
  previewLoading: boolean;
  canManage: boolean;
  onDeleteEndpoint: (endpoint: APIUpstream) => Promise<void> | void;
  restoreFocus: () => void;
  t: Translation;
}) {
  const [deleting, setDeleting] = useState<APIUpstream | null>(null);
  const compact = useBreakpoint() === "xs";
  const restoreAfterClose = useRef(false);
  const deletingLastEnabled = deleting?.status === 1 && target.enabled_upstream_count === 1;
  useEffect(() => {
    if (deleting || !restoreAfterClose.current) return;
    restoreAfterClose.current = false;
    window.requestAnimationFrame(restoreFocus);
  }, [deleting, restoreFocus]);
  const columns = useMemo<ColumnDef<APIUpstream>[]>(() => [
    {
      id: "name",
      header: t("name"),
      size: compact ? 260 : 200,
      meta: { cellClassName: "min-w-0" },
      cell: ({ row }) => (
        <div data-testid={`endpoint-row-${row.original.id}`} className="flex min-w-0 flex-col gap-1.5 py-0.5">
          <div className="flex min-w-0 items-center gap-2">
            {compact ? <StatusBadge status={row.original.status} /> : null}
            <span className="block truncate font-medium" title={row.original.name}>{row.original.name}</span>
          </div>
          {compact ? (
            <EndpointURL
              route={route}
              endpoint={row.original}
              preview={preview}
              previewLoading={previewLoading}
              t={t}
            />
          ) : null}
        </div>
      ),
    },
    {
      id: "url",
      header: t("requestURL"),
      size: 520,
      meta: { cellClassName: "min-w-0" },
      cell: ({ row }) => (
        <EndpointURL
          route={route}
          endpoint={row.original}
          preview={preview}
          previewLoading={previewLoading}
          t={t}
        />
      ),
    },
    {
      id: "status",
      header: t("status"),
      size: 104,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: "actions",
      header: "",
      size: 48,
      enableHiding: false,
      cell: ({ row }) => canManage ? (
        <div className="flex justify-end" data-row-interaction>
          <EndpointActions
            service={service}
            route={route}
            endpoint={row.original}
            onDelete={() => setDeleting(row.original)}
            t={t}
          />
        </div>
      ) : null,
    },
  ], [canManage, compact, preview, previewLoading, route, service, t]);
  return (
    <>
      <DataTable
        columns={columns}
        data={endpoints}
        getRowId={(endpoint) => String(endpoint.id)}
        tableLayout="fixed"
        columnVisibilityState={compact ? { url: false, status: false } : {}}
        onColumnVisibilityChange={() => {}}
        showFooter={false}
      />
      {deleting ? (
        <DeleteConfirmationDialog
          open
          onOpenChange={(open) => { if (!open) setDeleting(null); }}
          subject={deleting.name}
          title={deletingLastEnabled ? t("confirmDeleteLastEndpointTitle") : undefined}
          confirmLabel={deletingLastEnabled ? t("confirmDeleteEndpoint") : undefined}
          description={deletingLastEnabled ? t("lastEndpointDeleteDescription", { count: target.route_count }) : undefined}
          onConfirm={async () => {
            await onDeleteEndpoint(deleting);
            restoreAfterClose.current = true;
          }}
        />
      ) : null}
    </>
  );
}

function RouteSummary({ origin, service, route, t }: {
  origin: string;
  service: APIService;
  route: APIRoute;
  t: Translation;
}) {
  const publicURL = buildConfiguredPublicRouteURL({
    origin,
    serviceSlug: service.slug,
    routeSlug: route.slug,
    forwardSubpath: route.forward_subpath,
  });
  const methods = route.allowed_methods.length > 0 ? route.allowed_methods : [t("allMethodsShort")];
  return (
    <div className="flex min-w-0 flex-col gap-3 rounded-md border bg-background p-3">
      <div className="flex flex-wrap items-center gap-1.5">
        <StatusBadge status={route.status} />
        {route.protocols.map((protocol) => <ProtocolBadge key={protocol} protocol={protocol} />)}
        {methods.map((method) => <span key={method} className="rounded-md bg-muted px-2 py-0.5 text-xs font-medium">{method}</span>)}
        <span className="text-xs text-muted-foreground">{t(route.forward_subpath ? "forwardSubpathEnabled" : "forwardSubpathDisabled")}</span>
      </div>
      <dl className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="min-w-0">
          <dt className="text-xs font-medium text-muted-foreground">{t("path")}</dt>
          <dd className="truncate font-mono text-sm" title={`/${route.slug}`}>/{route.slug}</dd>
        </div>
        <div className="min-w-0">
          <dt className="text-xs font-medium text-muted-foreground">{t("upstreamPath")}</dt>
          <dd className="truncate font-mono text-sm" title={route.upstream_path}>{route.upstream_path}</dd>
        </div>
      </dl>
      <div className="min-w-0 border-t pt-3">
        <div className="mb-1.5 text-xs font-medium text-muted-foreground">{t("requestURL")}</div>
        <SegmentedURL {...publicURL} copyLabel={t("copyClientRequest")} />
      </div>
    </div>
  );
}

function EndpointsLoading({ t }: { t: Translation }) {
  const label = t("endpointsLoading");
  return (
    <div className="flex flex-col gap-3" role="status" aria-live="polite" aria-label={label}>
      <span className="sr-only">{label}</span>
      {[0, 1].map((index) => <Skeleton key={index} aria-hidden="true" className="h-12 w-full" />)}
    </div>
  );
}

function EndpointsError({ onRetry, t }: { onRetry: () => void; t: Translation }) {
  return (
    <Alert variant="destructive">
      <AlertTitle>{t("endpointsLoadFailed")}</AlertTitle>
      <AlertDescription>
        <p>{t("endpointsLoadFailedDescription")}</p>
        <Button type="button" size="sm" variant="outline" onClick={onRetry}>{t("retry")}</Button>
      </AlertDescription>
    </Alert>
  );
}

function EndpointsEmpty({ t }: { t: Translation }) {
  return (
    <Empty className="min-h-32 py-6 md:p-6">
      <EmptyHeader>
        <EmptyTitle>{t("noEndpoints")}</EmptyTitle>
        <EmptyDescription>{t("noEndpointsDescription")}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  );
}

function AddEndpointLink({ service, route, target, t }: { service: APIService; route: APIRoute; target: APIBackend; t: Translation }) {
  return (
    <Button asChild size="sm">
      <Link href={`/api-services/upstreams/new?service_id=${service.id}&backend_id=${target.id}&${routeReturnQuery(route)}`}>
        <Plus data-icon="inline-start" />
        {t("addEndpoint")}
      </Link>
    </Button>
  );
}

export function RouteExpandedWorkspace(props: RouteExpandedWorkspaceProps): React.ReactNode {
  const { service, route, canManage, onDeleteTarget, onDeleteEndpoint } = props;
  const translate = useTranslations("apiServices");
  const t: Translation = (key, values) => translate(key as never, values as never);
  const backend = useAPIBackend(route.backend_id);
  const state = targetState(backend);
  const target = state === "ready" ? backend.data : undefined;
  const targetReady = Boolean(target?.id);
  const endpoints = useAllAPIUpstreams(
    target?.id ?? 0,
    { enabled: targetReady },
  );
  const rows = endpoints.data ?? [];
  const previewDependenciesReady = target !== undefined && endpoints.data !== undefined && !endpoints.error;
  const dependencyRevision = target ? routePreviewDependencyRevision(route, target, rows) : "unavailable";
  const preview = useAPIRoutePreview(previewDraftForConfiguredRoute(route), {
    enabled: previewDependenciesReady,
    cacheKey: `${route.id}:configured:${dependencyRevision}`,
  });
  const renderTargetView = targetStateViews[state];
  const noEnabledEndpoint = !endpoints.isLoading && !endpoints.error && rows.every((endpoint) => endpoint.status !== 1);
  const [previewOpen, setPreviewOpen] = useState(false);
  const previewTriggerRef = useRef<HTMLButtonElement>(null);
  const endpointHeadingRef = useRef<HTMLHeadingElement>(null);
  const restorePreviewFocusRef = useRef(false);

  const closePreview = () => {
    restorePreviewFocusRef.current = true;
    setPreviewOpen(false);
  };

  return (
    <section
      data-testid={`route-expanded-workspace-${route.id}`}
      className="grid min-w-0 max-w-full grid-cols-1 gap-4"
    >
      <RouteSummary origin={props.origin} service={service} route={route} t={t} />

      {renderTargetView({
        service,
        route,
        target,
        canManage,
        onDeleteTarget,
        onRetry: () => void backend.refetch(),
        t,
      })}

      {target ? (
        <div className="flex min-w-0 flex-col gap-3">
          <h4 ref={endpointHeadingRef} tabIndex={-1} className="text-sm font-semibold">{t("endpointSection")}</h4>
          {noEnabledEndpoint ? (
            <Alert variant="destructive">
              <AlertTitle>{t("targetReturns503")}</AlertTitle>
              <AlertDescription>{t("targetReturns503Description")}</AlertDescription>
            </Alert>
          ) : null}
          {preview.error && !endpoints.error ? (
            <Alert variant="destructive">
              <AlertTitle>{t("configuredEndpointPreviewFailed")}</AlertTitle>
              <AlertDescription>
                <p>{t("configuredEndpointPreviewFailedDescription")}</p>
                <Button type="button" size="sm" variant="outline" onClick={() => void preview.refetch()}>{t("retryPreview")}</Button>
              </AlertDescription>
            </Alert>
          ) : null}
          {endpoints.isLoading ? <EndpointsLoading t={t} /> : null}
          {endpoints.error ? <EndpointsError onRetry={() => void endpoints.refetch()} t={t} /> : null}
          {!endpoints.isLoading && !endpoints.error && rows.length === 0 ? <EndpointsEmpty t={t} /> : null}
          {!endpoints.isLoading && !endpoints.error ? (
            <EndpointRows
              service={service}
              target={target}
              route={route}
              endpoints={rows}
              preview={preview.error ? undefined : preview.data}
              previewLoading={preview.isLoading}
              canManage={canManage}
              onDeleteEndpoint={onDeleteEndpoint}
              restoreFocus={() => endpointHeadingRef.current?.focus()}
              t={t}
            />
          ) : null}
          {canManage ? (
            <EmptyContent className="max-w-none items-end">
              <AddEndpointLink service={service} route={route} target={target} t={t} />
            </EmptyContent>
          ) : null}
          {previewOpen ? (
            <div className="flex min-w-0 flex-col gap-3">
              <div className="flex justify-end"><Button type="button" variant="outline" size="sm" className="max-sm:min-h-11" onClick={closePreview}>{t("collapseInvocationPreview")}</Button></div>
              <InlineRouteInvocationPreview origin={props.origin} serviceSlug={service.slug} route={route} target={target} dependencyRevision={dependencyRevision} />
            </div>
          ) : (
            <Button ref={(node) => { previewTriggerRef.current = node; if (node && restorePreviewFocusRef.current) { node.focus(); restorePreviewFocusRef.current = false; } }} type="button" variant="outline" size="sm" className="max-sm:min-h-11 self-start" onClick={() => setPreviewOpen(true)}>{t("previewInvocation")}</Button>
          )}
        </div>
      ) : null}
    </section>
  );
}

"use client";

import Link from "next/link";
import { Suspense, useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { Copy, Download, LoaderCircle, MoreHorizontal, Play } from "lucide-react";

import { StatusBadge } from "@/components/business/status-badge";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { canManageAPIService, useCapabilities } from "@/lib/api/capabilities";
import {
  type APIBackend,
  type APIRoute,
  type APIService,
  type APIUpstream,
  useAPIRoutes,
  useAPIService,
  useDeleteAPIBackend,
  useDeleteAPIRoute,
  useDeleteAPIService,
  useDeleteAPIUpstream,
  useGetOpenAPIDocument,
  downloadServiceOpenAPI,
} from "@/lib/api/api-services";
import { useAuth } from "@/lib/auth";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { formatMoneyCompact } from "@/lib/utils/format";

import { apiBackendDeleteErrorMessage, apiServiceErrorMessage } from "../api-service-error";
import { DeleteConfirmationDialog } from "../delete-confirmation-dialog";
import { RouteDataTable, type RouteTableFilters } from "../_components/route-table/route-data-table";
import { RouteExpandedWorkspace } from "../_components/route-table/route-expanded-workspace";

const allowedPageSizes = new Set([10, 20, 50]);

export interface RouteTableURLState {
  search: string;
  status?: number;
  page: number;
  pageSize: number;
  routeID?: number;
}

function positiveID(raw: string | null) {
  const value = Number(raw);
  return Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

export function readRouteTableURLState(params: Pick<URLSearchParams, "get">): RouteTableURLState {
  const statusRaw = params.get("route_status");
  const status = statusRaw === "0" ? 0 : statusRaw === "1" ? 1 : undefined;
  const page = positiveID(params.get("route_page")) ?? 1;
  const requestedPageSize = positiveID(params.get("route_page_size"));
  const pageSize = requestedPageSize !== undefined && allowedPageSizes.has(requestedPageSize) ? requestedPageSize : 10;
  const routeID = positiveID(params.get("route"));
  return {
    search: params.get("route_search") ?? "",
    ...(status === undefined ? {} : { status }),
    page,
    pageSize,
    ...(routeID === undefined ? {} : { routeID }),
  };
}

export function patchRouteTableURL(serviceID: number, state: RouteTableURLState) {
  const params = new URLSearchParams();
  params.set("id", String(serviceID));
  if (state.search) params.set("route_search", state.search);
  if (state.status !== undefined) params.set("route_status", String(state.status));
  if (state.page !== 1) params.set("route_page", String(state.page));
  if (state.pageSize !== 10) params.set("route_page_size", String(state.pageSize));
  if (state.routeID !== undefined) params.set("route", String(state.routeID));
  return `/api-services/detail?${params.toString()}`;
}

function statusOf(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined;
}

function DetailSkeleton() {
  const t = useTranslations("apiServices");
  return <PageLayout title={t("detailTitle")} maxWidth="full"><div className="flex min-w-0 flex-col gap-4"><Skeleton className="h-8 w-56 max-w-full" /><Skeleton className="h-16 w-full" /><Skeleton className="h-64 w-full" /></div></PageLayout>;
}

function useCurrentOrigin() {
  return useSyncExternalStore(() => () => {}, () => window.location.origin, () => "");
}

function ServiceError({ error }: { error: unknown }) {
  const t = useTranslations("apiServices");
  const status = statusOf(error);
  const key = status === 404 ? "serviceNotFound" : status === 403 ? "permissionDenied" : "loadFailed";
  return <PageLayout title={t("detailTitle")}><Alert variant={status === 404 ? "default" : "destructive"}><AlertTitle>{t(key)}</AlertTitle><AlertDescription>{t(`${key}Description`)}</AlertDescription></Alert></PageLayout>;
}

function ServiceActions({ service, onDelete }: { service: APIService; onDelete: () => void }) {
  const t = useTranslations("apiServices");
  return <DropdownMenu><DropdownMenuTrigger asChild><Button type="button" variant="outline" size="icon" aria-label={t("serviceActions")}><MoreHorizontal /></Button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuGroup><DropdownMenuItem asChild><Link href={`/api-services/edit?id=${service.id}`}>{t("editService")}</Link></DropdownMenuItem><DropdownMenuItem asChild><Link href="/api-access">{t("apiAccess")}</Link></DropdownMenuItem><DropdownMenuItem asChild><Link href={`/rate-limiters?target_type=api_service&target_id=${service.id}`}>{t("rateLimiters")}</Link></DropdownMenuItem><DropdownMenuItem asChild><Link href={`/api-logs?api_service_id=${service.id}`}>{t("apiLogs")}</Link></DropdownMenuItem><DropdownMenuItem asChild><Link href={`/agent-routes?source_type=api_service&source_id=${service.id}`}>{t("agentRoutes")}</Link></DropdownMenuItem></DropdownMenuGroup><DropdownMenuSeparator /><DropdownMenuGroup><DropdownMenuItem variant="destructive" onSelect={onDelete}>{t("deleteService")}</DropdownMenuItem></DropdownMenuGroup></DropdownMenuContent></DropdownMenu>;
}

function OpenAPIEditorAction({ serviceID }: { serviceID: number }) {
  const t = useTranslations("apiServices");
  const document = useGetOpenAPIDocument(serviceID);
  const version = document.data?.service.document.openapi;
  if (typeof version !== "string" || version.trim() === "") return null;
  return <Button asChild type="button" variant="outline"><Link href={`/api-services/openapi?id=${serviceID}`}>{t("editOpenAPIDocument")}</Link></Button>;
}

type DeletingEntity =
  | { kind: "route"; route: APIRoute }
  | { kind: "target"; target: APIBackend; route: APIRoute };

interface DeleteOperation {
  id: number;
  entity: DeletingEntity;
  locationSearch: string;
}

function nextURLState(state: RouteTableURLState, patch: Partial<RouteTableURLState>) {
  return { ...state, ...patch };
}

function maximumRoutePage(total: number, pageSize: number) {
  return Math.max(1, Math.ceil(total / pageSize));
}

function useEntityDeleteCoordinator({ serviceID, removeRoute, removeTarget, replace, restoreFocus }: {
  serviceID: number;
  removeRoute: (id: number) => Promise<unknown>;
  removeTarget: (id: number) => Promise<unknown>;
  replace: (state: RouteTableURLState) => void;
  restoreFocus: () => void;
}) {
  const [operation, setOperation] = useState<DeleteOperation>();
  const latestID = useRef(0);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; latestID.current += 1; };
  }, []);

  const open = useCallback((entity: DeletingEntity) => {
    const id = latestID.current + 1;
    latestID.current = id;
    setOperation({ id, entity, locationSearch: window.location.search });
  }, []);
  const onOpenChange = useCallback((next: boolean) => {
    if (next) return;
    latestID.current += 1;
    setOperation(undefined);
  }, []);
  const isCurrent = useCallback((candidate: DeleteOperation) => mounted.current
    && latestID.current === candidate.id
    && window.location.search === candidate.locationSearch
    && positiveID(new URLSearchParams(window.location.search).get("id")) === serviceID, [serviceID]);

  const confirm = useCallback(async () => {
    if (!operation) return false;
    try {
      if (operation.entity.kind === "route") await removeRoute(operation.entity.route.id);
      else await removeTarget(operation.entity.target.id);
    } catch (reason) {
      if (isCurrent(operation)) throw reason;
      return false;
    }
    if (!isCurrent(operation)) return false;
    const latestState = readRouteTableURLState(new URLSearchParams(window.location.search));
    const affectedRouteID = operation.entity.route.id;
    if (latestState.routeID === affectedRouteID) replace(nextURLState(latestState, { routeID: undefined }));
    restoreFocus();
    return true;
  }, [isCurrent, operation, removeRoute, removeTarget, replace, restoreFocus]);

  return { operation, open, onOpenChange, confirm };
}

export function APIServiceWorkspace({ service, canManage, origin }: { service: APIService; canManage: boolean; origin: string }) {
  const t = useTranslations("apiServices");
  const tc = useTranslations("common");
  const router = useRouter();
  const params = useSearchParams();
  const routeState = readRouteTableURLState(params);
  const routes = useAPIRoutes({
    api_service_id: service.id,
    ...(routeState.search ? { search: routeState.search } : {}),
    ...(routeState.status === undefined ? {} : { status: routeState.status }),
    page: routeState.page,
    page_size: routeState.pageSize,
  }, { retainPreviousData: true });
  const removeService = useDeleteAPIService();
  const removeRoute = useDeleteAPIRoute();
  const removeTarget = useDeleteAPIBackend();
  const removeEndpoint = useDeleteAPIUpstream();
  const [deleteServiceOpen, setDeleteServiceOpen] = useState(false);
  const [serviceDeleted, setServiceDeleted] = useState(false);
  const [exportingOpenAPI, setExportingOpenAPI] = useState(false);
  const [openAPIExportError, setOpenAPIExportError] = useState<string>();
  const routeHeadingRef = useRef<HTMLHeadingElement>(null);
  const lastPageConvergence = useRef<string | undefined>(undefined);
  const response = routes.data;
  const displayedRoutes = response?.api_service_id === undefined || response.api_service_id === service.id ? response?.data ?? [] : [];
  const responseMatchesRequest = !routes.isPlaceholderData && response?.page === routeState.page && response.page_size === routeState.pageSize;
  const expandedRoute = !responseMatchesRequest || routeState.routeID === undefined ? undefined : displayedRoutes.find((route) => route.id === routeState.routeID && route.api_service_id === service.id);
  const routeOutsideCurrentPage = routeState.routeID !== undefined && response !== undefined && !routes.error && responseMatchesRequest && expandedRoute === undefined;
  const baseURL = origin ? `${origin}/v1/api/${service.slug}` : `/v1/api/${service.slug}`;

  const replaceRouteState = useCallback((next: RouteTableURLState) => {
    router.replace(patchRouteTableURL(service.id, next));
  }, [router, service.id]);
  const restoreRouteHeadingFocus = useCallback(() => window.requestAnimationFrame(() => routeHeadingRef.current?.focus()), []);
  useEffect(() => {
    if (routes.isPlaceholderData || routes.error || !responseMatchesRequest || response?.api_service_id !== service.id) return;
    const maxPage = maximumRoutePage(response.total, response.page_size);
    if (routeState.page <= maxPage) {
      lastPageConvergence.current = undefined;
      return;
    }
    const snapshot = `${window.location.search}:${response.page}:${response.page_size}:${response.total}`;
    if (lastPageConvergence.current === snapshot) return;
    lastPageConvergence.current = snapshot;
    replaceRouteState(nextURLState(routeState, { page: maxPage, routeID: undefined }));
  }, [replaceRouteState, response, responseMatchesRequest, routeState, routes.error, routes.isPlaceholderData, service.id]);
  const deleteCoordinator = useEntityDeleteCoordinator({
    serviceID: service.id,
    removeRoute: removeRoute.mutateAsync,
    removeTarget: removeTarget.mutateAsync,
    replace: replaceRouteState,
    restoreFocus: restoreRouteHeadingFocus,
  });

  const exportOpenAPI = async () => {
    setOpenAPIExportError(undefined);
    setExportingOpenAPI(true);
    try { await downloadServiceOpenAPI(service.id, service.slug); }
    catch (reason) { setOpenAPIExportError(apiServiceErrorMessage(t, reason, "openAPIExportFailed")); }
    finally { setExportingOpenAPI(false); }
  };

  if (serviceDeleted) return <DetailSkeleton />;
  return (
    <PageLayout
      title={service.name}
      description={service.description || t("serviceDescriptionEmpty")}
      metadata={<><StatusBadge status={service.status} /><Badge variant="outline" className="font-mono">{service.slug}</Badge><Badge variant="outline"><span className="sr-only">{t("pricePerCall")}: </span>{formatMoneyCompact(service.price_per_call)}</Badge></>}
      maxWidth="full"
      actions={<><Button asChild><Link href={`/api-catalog?service_id=${service.id}`}><Play data-icon="inline-start" />{t("tryAPI")}</Link></Button>{canManage ? <OpenAPIEditorAction serviceID={service.id} /> : null}{canManage ? <Button type="button" variant="outline" onClick={() => void exportOpenAPI()} disabled={exportingOpenAPI}>{exportingOpenAPI ? <><LoaderCircle data-icon="inline-start" className="animate-spin" />{t("exportingOpenAPI")}</> : <><Download data-icon="inline-start" />{t("exportOpenAPI")}</>}</Button> : null}{canManage ? <ServiceActions service={service} onDelete={() => setDeleteServiceOpen(true)} /> : null}</>}
    >
      <div data-testid="api-service-workspace" className="flex min-w-0 max-w-full flex-col gap-6 overflow-x-clip">
        <div className="flex min-w-0 items-start gap-2">
          <div className="min-w-0 flex-1"><p className="text-xs font-medium text-muted-foreground">{t("baseUrl")}</p><code className="block min-w-0 overflow-x-auto whitespace-nowrap text-sm font-semibold">{baseURL}</code></div>
          <Button type="button" variant="ghost" size="icon-sm" className="size-11" aria-label={t("copyBaseUrl")} onClick={() => void copyTextWithFeedback(baseURL, { success: tc("copied"), error: tc("copyFailed") })}><Copy /></Button>
        </div>
        {openAPIExportError ? (
          <Alert variant="destructive">
            <AlertTitle>{t("openAPIExportFailed")}</AlertTitle>
            {openAPIExportError === t("openAPIExportFailed") ? null : (
              <AlertDescription>{openAPIExportError}</AlertDescription>
            )}
          </Alert>
        ) : null}
        <section className="flex min-w-0 max-w-full flex-col gap-4">
          <h2 ref={routeHeadingRef} tabIndex={-1} className="text-xl font-semibold tracking-tight">{t("routesLabel")}</h2>
          {routeOutsideCurrentPage ? <Alert><AlertTitle>{t("routeOutsideCurrentPage")}</AlertTitle><AlertDescription className="flex flex-col items-start gap-2"><span>{t("routeOutsideCurrentPageDescription")}</span><Button type="button" size="sm" variant="outline" onClick={() => replaceRouteState({ search: "", page: 1, pageSize: routeState.pageSize })}>{t("clearRouteFilters")}</Button></AlertDescription></Alert> : null}
          <RouteDataTable
            service={service}
            origin={origin}
            routes={displayedRoutes}
            loading={routes.isLoading && response === undefined}
            refreshing={routes.isFetching}
            error={routes.error}
            onRetry={() => void routes.refetch()}
            total={response?.total ?? 0}
            displayedPage={response?.page ?? routeState.page}
            displayedPageSize={response?.page_size ?? routeState.pageSize}
            requestedPage={routeState.page}
            isPlaceholderData={routes.isPlaceholderData}
            filters={{ search: routeState.search, status: routeState.status }}
            expandedRouteID={expandedRoute?.id}
            canManage={canManage}
            onFiltersChange={(patch: Partial<RouteTableFilters>) => replaceRouteState({ ...routeState, ...patch, page: 1, routeID: undefined })}
            onExpandedRouteChange={(routeID) => replaceRouteState({ ...routeState, routeID })}
            onPaginationChange={(page, pageSize) => replaceRouteState({ ...routeState, page, pageSize, routeID: undefined })}
            onDeleteRoute={(route) => deleteCoordinator.open({ kind: "route", route })}
            renderExpandedRoute={(route) => <RouteExpandedWorkspace service={service} origin={origin} route={route} canManage={canManage} onDeleteTarget={(target) => deleteCoordinator.open({ kind: "target", target, route })} onDeleteEndpoint={async (endpoint: APIUpstream) => { await removeEndpoint.mutateAsync(endpoint.id); }} />}
          />
        </section>
      </div>
      {deleteCoordinator.operation ? <DeleteConfirmationDialog key={deleteCoordinator.operation.id} open onOpenChange={deleteCoordinator.onOpenChange} subject={deleteCoordinator.operation.entity.kind === "route" ? deleteCoordinator.operation.entity.route.slug : deleteCoordinator.operation.entity.target.name} description={deleteCoordinator.operation.entity.kind === "target" ? t("deleteTargetDescription", { subject: deleteCoordinator.operation.entity.target.name }) : undefined} details={deleteCoordinator.operation.entity.kind === "target" && deleteCoordinator.operation.entity.target.route_count > 0 ? <Alert><AlertTitle>{t("sharedTargetImpact", { count: deleteCoordinator.operation.entity.target.route_count })}</AlertTitle><AlertDescription>{t("backendInUse", { count: deleteCoordinator.operation.entity.target.route_count })}</AlertDescription></Alert> : undefined} errorMessage={deleteCoordinator.operation.entity.kind === "target" ? (error) => apiBackendDeleteErrorMessage(t, error) : undefined} onConfirm={deleteCoordinator.confirm} /> : null}
      {deleteServiceOpen ? <DeleteConfirmationDialog open onOpenChange={setDeleteServiceOpen} subject={service.name} onConfirm={async () => { await removeService.mutateAsync(service.id); setServiceDeleted(true); setDeleteServiceOpen(false); router.replace("/api-services"); }} /> : null}
    </PageLayout>
  );
}

function DetailContent() {
  const t = useTranslations("apiServices");
  const params = useSearchParams();
  const serviceId = positiveID(params.get("id"));
  const { user } = useAuth();
  const capability = useCapabilities(user?.user_id);
  const enabled = serviceId !== undefined && capability.data?.generic_api?.services === true;
  const service = useAPIService(serviceId ?? 0, { enabled });
  const origin = useCurrentOrigin();
  if (!serviceId) return <ServiceError error={{ status: 404 }} />;
  if (capability.isPending || capability.isLoading) return <DetailSkeleton />;
  if (capability.error) return <ServiceError error={capability.error} />;
  if (!enabled) return <PageLayout title={t("detailTitle")}><Alert><AlertTitle>{t("unavailable")}</AlertTitle><AlertDescription>{t("permissionRequired")}</AlertDescription></Alert></PageLayout>;
  if (service.isLoading) return <DetailSkeleton />;
  if (service.error || !service.data || service.data.id !== serviceId) return <ServiceError error={service.error ?? { status: 404 }} />;
  return <APIServiceWorkspace key={service.data.id} service={service.data} canManage={canManageAPIService(capability.data, service.data.id)} origin={origin} />;
}

export default function APIServiceDetailPage() {
  return <Suspense fallback={<DetailSkeleton />}><DetailContent /></Suspense>;
}

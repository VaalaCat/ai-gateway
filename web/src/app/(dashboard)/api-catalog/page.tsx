"use client";

import { useCallback, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { useTranslations } from "next-intl";
import { useSearchParams } from "next/navigation";
import { BookOpen } from "lucide-react";

import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import {
  type APICatalogRoute,
  type APICatalogService,
  useAPICatalogEffective,
  useAPICatalogRoutes,
  useAPICatalogServices,
} from "@/lib/api/api-access";
import { useAuth } from "@/lib/auth";

import {
  normalizeProtocol,
  parseCatalogSelection,
  pickCatalogID,
  type CatalogProtocol,
} from "./_components/catalog-selection";
import {
  catalogScopeKey,
  parseCatalogTokenID,
  toCatalogAccessScope,
  type CatalogAccessScope,
} from "./_components/catalog-token-scope";
import { findCatalogScopeFailure } from "./_components/catalog-scope-failure";
import {
  initialCatalogTokenInvalidationGuard,
  transitionCatalogTokenInvalidationGuard,
} from "./_components/catalog-token-invalidation";
import {
  catalogScopeVisitIdentity,
  transitionCatalogScopeVisit,
} from "./_components/catalog-scope-visit";
import { RouteNavigator } from "./_components/route-navigator";
import { InvocationWorkbench } from "./_components/invocation-workbench";
import { CatalogTokenPicker, CatalogToolbar } from "./_components/catalog-toolbar";
import {
  createRequestExampleDraft,
  type RequestExampleDraft,
} from "./_components/request-example-draft";
import { ServiceNavigator } from "./_components/service-navigator";
import { useInvocationToken } from "../api-services/_components/use-invocation-token";

interface LoadedPage<T> {
  page: number;
  items: T[];
  total: number;
}

interface ScopedLoadedPage<T> extends LoadedPage<T> {
  scopeKey: string;
}

interface ScopedPageMap<K, T> {
  scopeKey: string;
  pages: Map<K, ScopedLoadedPage<T>>;
}

const subscribeBrowserOrigin = () => () => {};

function catalogScopeIdentity(viewerUserID: number, scope: CatalogAccessScope) {
  return `${viewerUserID}:${catalogScopeKey(scope).join(":")}`;
}

function scopedPage<T>(scopeKey: string): ScopedLoadedPage<T> {
  return { scopeKey, page: 1, items: [], total: 0 };
}

function useRememberedCatalogTokenID(viewerUserID: number, onTokenIDChanged: (tokenID: number) => void) {
  const storageKey = `aigw:api-catalog-token-id:${viewerUserID}`;
  const observedTokenID = useRef(0);
  return useSyncExternalStore(
    (notify) => {
      observedTokenID.current = parseCatalogTokenID(window.localStorage.getItem(storageKey));
      const listener = (event: StorageEvent) => {
        if (event.key !== storageKey) return;
        const nextTokenID = parseCatalogTokenID(window.localStorage.getItem(storageKey));
        if (nextTokenID !== observedTokenID.current) {
          observedTokenID.current = nextTokenID;
          onTokenIDChanged(nextTokenID);
        }
        notify();
      };
      window.addEventListener("storage", listener);
      return () => window.removeEventListener("storage", listener);
    },
    () => {
      try {
        return parseCatalogTokenID(window.localStorage.getItem(storageKey));
      } catch {
        return 0;
      }
    },
    () => 0,
  );
}

function useScopeWitness(scopeKey: string) {
  const currentScopeKey = useRef(scopeKey);
  useLayoutEffect(() => {
    currentScopeKey.current = scopeKey;
  }, [scopeKey]);
  return currentScopeKey;
}

export function useBrowserOrigin() {
  return useSyncExternalStore(
    subscribeBrowserOrigin,
    () => window.location.origin,
    () => "",
  );
}

function mergeItems<T extends { id: number }>(loaded: T[], current: T[]) {
  const byID = new Map(loaded.map((item) => [item.id, item]));
  for (const item of current) byID.set(item.id, item);
  return [...byID.values()];
}

function statusOf(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined;
}

function currentPageExhausted<T>(data: { data: T[]; total: number } | undefined, loadedCount: number) {
  return data !== undefined && (loadedCount >= data.total || data.data.length === 0);
}

export default function APICatalogPage() {
  const t = useTranslations("apiCatalog");
  const workspace = useCatalogWorkspace();

  return (
    <PageLayout
      title={t("title")}
      description={t("description")}
      maxWidth="full"
    >
      <CatalogWorkspace workspace={workspace} />
    </PageLayout>
  );
}

function useCatalogWorkspace() {
  const searchParams = useSearchParams();
  const patchSearchParams = useSearchParamPatch();
  const { user, isAdmin } = useAuth();
  const viewerUserID = user?.user_id ?? 0;
  const requested = parseCatalogSelection(searchParams);
  const [scopeVisit, setScopeVisit] = useState({ tokenID: 0, epoch: 0 });
  const advanceScopeVisit = useCallback((tokenID: number) => {
    setScopeVisit((current) => transitionCatalogScopeVisit(current, tokenID).visit);
  }, []);
  const [tokenChoice, setTokenChoice] = useState<{ viewerUserID: number; tokenID?: number }>({ viewerUserID: 0 });
  const [tokenUnavailableNotice, setTokenUnavailableNotice] = useState(false);
  const selectedFromChoice = tokenChoice.viewerUserID === viewerUserID;
  const onRememberedTokenIDChanged = useCallback((tokenID: number) => {
    if (!selectedFromChoice) advanceScopeVisit(tokenID);
  }, [advanceScopeVisit, selectedFromChoice]);
  const rememberedTokenID = useRememberedCatalogTokenID(viewerUserID, onRememberedTokenIDChanged);
  const selectedTokenID = selectedFromChoice
    ? tokenChoice.tokenID ?? 0
    : rememberedTokenID;
  const setSelectedTokenID = useCallback((tokenID: number) => {
    setTokenUnavailableNotice(false);
    if (tokenID === selectedTokenID) return;
    setTokenChoice({
      viewerUserID,
      tokenID,
    });
    advanceScopeVisit(tokenID);
  }, [advanceScopeVisit, selectedTokenID, setTokenUnavailableNotice, viewerUserID]);
  // behavior change: ordinary users wait for a Token before requesting any catalog data.
  const catalogScope = toCatalogAccessScope(Boolean(isAdmin), selectedTokenID);
  const scopeKey = catalogScopeIdentity(viewerUserID, catalogScope);
  const scopeVisitKey = catalogScopeVisitIdentity(scopeKey, scopeVisit);
  const previousScopeKey = useRef(scopeKey);
  const [serviceSearchState, setServiceSearchState] = useState({ scopeKey: "", value: "" });
  const [routeSearchState, setRouteSearchState] = useState<{ scopeKey: string; serviceID?: number; value: string }>({ scopeKey: "", value: "" });
  const [knownService, setKnownService] = useState<{ scopeKey: string; item: APICatalogService }>();
  const [knownRoutes, setKnownRoutes] = useState<Map<number, { scopeKey: string; item: APICatalogRoute }>>(() => new Map());
  const scopedKnownService = knownService?.scopeKey === scopeKey ? knownService.item : undefined;
  const servicePages = usePagedServices(viewerUserID, catalogScope, scopeVisitKey, requested.serviceID, scopedKnownService);
  const routePages = usePagedRoutes(
    viewerUserID,
    catalogScope,
    scopeVisitKey,
    servicePages.choice.id,
    requested.routeID,
    servicePages.choice.id === undefined || knownRoutes.get(servicePages.choice.id)?.scopeKey !== scopeKey
      ? undefined
      : knownRoutes.get(servicePages.choice.id)?.item,
  );
  const serviceID = servicePages.choice.id;
  const selectedService = servicePages.items.find((service) => service.id === serviceID);
  const routeID = routePages.choice.id;
  const selectedRoute = routePages.items.find((route) => route.id === routeID);
  const effective = useAPICatalogEffective(viewerUserID, catalogScope, serviceID ?? 0, { enabled: serviceID !== undefined });
  const protocol = normalizeProtocol(requested.protocol, selectedRoute);
  const serviceSearch = serviceSearchState.scopeKey === scopeKey ? serviceSearchState.value : "";
  const committedServiceSearch = serviceSearch.trim();
  const committedRouteSearch = routeSearchState.scopeKey === scopeKey && routeSearchState.serviceID === serviceID
    ? routeSearchState.value.trim()
    : "";
  const serviceCandidates = usePagedServiceSearch(viewerUserID, catalogScope, scopeVisitKey, committedServiceSearch);
  const routeCandidates = usePagedRouteSearch(viewerUserID, catalogScope, scopeVisitKey, serviceID, committedRouteSearch);
  const serviceSearchActive = committedServiceSearch !== "";
  const routeSearchActive = committedRouteSearch !== "";
  const serviceOptionItems = serviceSearchActive
    ? mergeItems(selectedService ? [selectedService] : [], serviceCandidates.items)
    : servicePages.items;
  const routeOptionItems = routeSearchActive
    ? mergeItems(selectedRoute ? [selectedRoute] : [], routeCandidates.items)
    : routePages.items;
  const serviceOptionQuery = serviceSearchActive ? serviceCandidates.query : servicePages.query;
  const routeOptionQuery = routeSearchActive ? routeCandidates.query : routePages.query;
  const serviceOptionTotal = serviceSearchActive ? serviceCandidates.total : servicePages.total;
  const routeOptionTotal = routeSearchActive ? routeCandidates.total : routePages.total;
  const serviceOptionLoaded = serviceSearchActive ? serviceCandidates.items.length : servicePages.loadedCount;
  const routeOptionLoaded = routeSearchActive ? routeCandidates.items.length : routePages.loadedCount;
  const invocationToken = useInvocationToken({
    viewerUserID,
    // behavior change: the catalog's selected Token, including an administrator-selected external Token, is authoritative.
    ownerUserID: undefined,
    apiServiceID: selectedService?.id ?? 0,
    apiRouteID: selectedRoute?.id ?? 0,
  }, {
    value: selectedTokenID,
    onValueChange: setSelectedTokenID,
    rememberScope: "viewer",
  });
  const scopeFailure = catalogScope.mode === "token"
    ? findCatalogScopeFailure([
      { error: servicePages.query.error, retry: servicePages.query.refetch },
      ...(serviceSearchActive ? [{ error: serviceCandidates.query.error, retry: serviceCandidates.query.refetch }] : []),
      ...(serviceID !== undefined ? [{ error: routePages.query.error, retry: routePages.query.refetch }] : []),
      ...(routeSearchActive ? [{ error: routeCandidates.query.error, retry: routeCandidates.query.refetch }] : []),
      ...(serviceID !== undefined ? [{ error: effective.error, retry: effective.refetch }] : []),
    ])
    : undefined;
  const [requestDraft, setRequestDraft] = useState<{ scopeKey: string; routeID: number; draft: RequestExampleDraft }>();
  if (selectedRoute && (requestDraft?.scopeKey !== scopeKey || requestDraft.routeID !== selectedRoute.id)) {
    setRequestDraft({ scopeKey, routeID: selectedRoute.id, draft: createRequestExampleDraft(selectedRoute) });
  } else if (!selectedRoute && requestDraft) {
    setRequestDraft(undefined);
  }
  const draft = selectedRoute
    ? requestDraft?.scopeKey === scopeKey && requestDraft.routeID === selectedRoute.id
      ? requestDraft.draft
      : createRequestExampleDraft(selectedRoute)
    : undefined;

  useEffect(() => {
    if (previousScopeKey.current === scopeKey) return;
    // behavior change: every local catalog artifact belongs to exactly one Token scope.
    setServiceSearchState({ scopeKey, value: "" });
    setRouteSearchState({ scopeKey, value: "" });
    setKnownService(undefined);
    setKnownRoutes(new Map());
    setRequestDraft(undefined);
  }, [scopeKey]);

  useEffect(() => {
    if (!servicePages.settled || !routePages.settled) return;
    if (requested.serviceID === serviceID && requested.routeID === routeID && requested.protocol === protocol) return;
    patchSearchParams({ service_id: serviceID, route_id: routeID, protocol });
  }, [patchSearchParams, protocol, requested.protocol, requested.routeID, requested.serviceID, routeID, routePages.settled, serviceID, servicePages.settled]);

  useEffect(() => {
    if (previousScopeKey.current === scopeKey) return;
    previousScopeKey.current = scopeKey;
    // behavior change: selections and request fields never carry into another Token catalog.
    patchSearchParams({ service_id: null, route_id: null, protocol: null });
  }, [patchSearchParams, scopeKey]);

  const selectService = (id: number) => {
    const candidate = serviceCandidates.items.find((service) => service.id === id);
    if (candidate) setKnownService({ scopeKey, item: candidate });
    setServiceSearchState({ scopeKey, value: "" });
    setRouteSearchState({ scopeKey, serviceID: id, value: "" });
    patchSearchParams({ service_id: id, route_id: null, protocol: null });
  };
  const selectRoute = (id: number) => {
    const nextRoute = routePages.items.find((route) => route.id === id)
      ?? routeCandidates.items.find((route) => route.id === id && route.api_service_id === serviceID);
    if (nextRoute && serviceID !== undefined) {
      setKnownRoutes((current) => new Map(current).set(serviceID, { scopeKey, item: nextRoute }));
    }
    setRouteSearchState({ scopeKey, serviceID, value: "" });
    patchSearchParams({ route_id: id, protocol: normalizeProtocol(requested.protocol, nextRoute) });
  };
  const selectProtocol = (nextProtocol: CatalogProtocol) => patchSearchParams({ protocol: nextProtocol });

  return {
    viewerUserID,
    services: servicePages.query,
    serviceItems: servicePages.items,
    serviceTotal: servicePages.total,
    serviceOptionItems,
    serviceOptionQuery,
    serviceOptionTotal,
    serviceOptionLoaded,
    serviceSearch,
    catalogScope,
    tokenUnavailableNotice,
    markTokenUnavailable: () => setTokenUnavailableNotice(true),
    serviceID,
    selectedService,
    routes: routePages.query,
    effective,
    scopeFailure,
    routeItems: routePages.items,
    routeTotal: routePages.total,
    routeOptionItems,
    routeOptionQuery,
    routeOptionTotal,
    routeOptionLoaded,
    routeSearch: committedRouteSearch,
    routeID,
    selectedRoute,
    protocol,
    draft,
    invocationToken,
    selectService,
    selectRoute,
    selectProtocol,
    setServiceSearch: (value: string) => setServiceSearchState({ scopeKey, value }),
    setRouteSearch: (value: string) => setRouteSearchState({ scopeKey, serviceID, value }),
    setDraft: (nextDraft: RequestExampleDraft) => {
      if (selectedRoute) setRequestDraft({ scopeKey, routeID: selectedRoute.id, draft: nextDraft });
    },
    loadMoreServices: serviceSearchActive ? serviceCandidates.loadMore : servicePages.loadMore,
    loadMoreRoutes: routeSearchActive ? routeCandidates.loadMore : routePages.loadMore,
    retryServices: () => void serviceOptionQuery.refetch(),
    retryRoutes: () => void routeOptionQuery.refetch(),
  };
}

function usePagedServices(viewerUserID: number, scope: CatalogAccessScope, scopeKey: string, requestedID: number | undefined, known?: APICatalogService) {
  const [servicePages, setServicePages] = useState<ScopedLoadedPage<APICatalogService>>(() => scopedPage(scopeKey));
  const scopeWitness = useScopeWitness(scopeKey);
  const current = servicePages.scopeKey === scopeKey ? servicePages : scopedPage<APICatalogService>(scopeKey);
  const query = useAPICatalogServices(viewerUserID, scope, { page: current.page, page_size: 50 });
  const loadedItems = mergeItems(current.items, query.data?.data ?? []);
  const items = mergeItems(loadedItems, known ? [known] : []);
  const total = query.data?.total ?? current.total;
  const exhausted = currentPageExhausted(query.data, loadedItems.length);
  const choice = pickCatalogID(requestedID, items, exhausted);
  const loadMore = useCallback(() => {
    if (!query.data) return;
    const expectedPage = current.page;
    setServicePages((saved) => {
      if (scopeWitness.current !== scopeKey) return saved;
      if (saved.scopeKey === scopeKey && saved.page !== expectedPage) return saved;
      return { scopeKey, page: expectedPage + 1, items: loadedItems, total };
    });
  }, [current, loadedItems, query.data, scopeKey, scopeWitness, total]);

  useEffect(() => {
    if (requestedID === undefined || !choice.pending || !query.data || exhausted) return;
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) loadMore();
    });
    return () => { cancelled = true; };
  }, [choice.pending, exhausted, loadMore, query.data, requestedID]);

  return {
    query,
    items,
    loadedCount: loadedItems.length,
    total,
    choice,
    settled: !choice.pending && (items.length > 0 || query.data !== undefined),
    loadMore,
  };
}

function usePagedRoutes(viewerUserID: number, scope: CatalogAccessScope, scopeKey: string, serviceID: number | undefined, requestedID: number | undefined, known?: APICatalogRoute) {
  const [routePages, setRoutePages] = useState<ScopedPageMap<number, APICatalogRoute>>(() => ({ scopeKey, pages: new Map() }));
  const scopeWitness = useScopeWitness(scopeKey);
  const pagesByService = routePages.scopeKey === scopeKey ? routePages.pages : new Map<number, ScopedLoadedPage<APICatalogRoute>>();
  const saved = serviceID === undefined ? undefined : pagesByService.get(serviceID);
  const current = serviceID === undefined || saved?.scopeKey !== scopeKey ? scopedPage<APICatalogRoute>(scopeKey) : saved;
  const query = useAPICatalogRoutes(viewerUserID, scope, serviceID ?? 0, { page: current.page, page_size: 50 }, { enabled: serviceID !== undefined });
  const loadedItems = mergeItems(current.items, query.data?.data ?? []);
  const items = mergeItems(loadedItems, known && known.api_service_id === serviceID ? [known] : []);
  const total = query.data?.total ?? current.total;
  const exhausted = currentPageExhausted(query.data, loadedItems.length);
  const choice = pickCatalogID(requestedID, items, exhausted);
  const loadMore = useCallback(() => {
    if (serviceID === undefined || !query.data) return;
    const expectedPage = current.page;
    setRoutePages((savedState) => {
      if (scopeWitness.current !== scopeKey) return savedState;
      const allPages = savedState.scopeKey === scopeKey ? savedState.pages : new Map<number, ScopedLoadedPage<APICatalogRoute>>();
      const saved = allPages.get(serviceID);
      if (saved?.scopeKey === scopeKey && saved.page !== expectedPage) return savedState;
      const next = new Map(allPages);
      next.set(serviceID, { scopeKey, page: expectedPage + 1, items: loadedItems, total });
      return { scopeKey, pages: next };
    });
  }, [current, loadedItems, query.data, scopeKey, scopeWitness, serviceID, total]);

  useEffect(() => {
    if (serviceID === undefined || requestedID === undefined || !choice.pending || !query.data || exhausted) return;
    let cancelled = false;
    queueMicrotask(() => {
      if (!cancelled) loadMore();
    });
    return () => { cancelled = true; };
  }, [choice.pending, exhausted, loadMore, query.data, requestedID, serviceID]);

  return {
    query,
    items,
    loadedCount: loadedItems.length,
    total,
    choice,
    settled: serviceID === undefined || (!choice.pending && (items.length > 0 || query.data !== undefined)),
    loadMore,
  };
}

function usePagedServiceSearch(viewerUserID: number, scope: CatalogAccessScope, scopeKey: string, search: string) {
  const [serviceSearchPages, setServiceSearchPages] = useState<ScopedPageMap<string, APICatalogService>>(() => ({ scopeKey, pages: new Map() }));
  const scopeWitness = useScopeWitness(scopeKey);
  const pagesBySearch = serviceSearchPages.scopeKey === scopeKey ? serviceSearchPages.pages : new Map<string, ScopedLoadedPage<APICatalogService>>();
  const saved = pagesBySearch.get(search);
  const current = saved?.scopeKey === scopeKey ? saved : scopedPage<APICatalogService>(scopeKey);
  const query = useAPICatalogServices(
    viewerUserID,
    scope,
    { page: current.page, page_size: 50, search },
    { enabled: search !== "" },
  );
  const items = mergeItems(current.items, query.data?.data ?? []);
  const total = query.data?.total ?? current.total;
  const loadMore = useCallback(() => {
    if (!search || !query.data) return;
    const expectedPage = current.page;
    setServiceSearchPages((savedState) => {
      if (scopeWitness.current !== scopeKey) return savedState;
      const allPages = savedState.scopeKey === scopeKey ? savedState.pages : new Map<string, ScopedLoadedPage<APICatalogService>>();
      const saved = allPages.get(search);
      if (saved?.scopeKey === scopeKey && saved.page !== expectedPage) return savedState;
      return { scopeKey, pages: new Map(allPages).set(search, { scopeKey, page: expectedPage + 1, items, total }) };
    });
  }, [current, items, query.data, scopeKey, scopeWitness, search, total]);
  return { query, items, total, loadMore };
}

function usePagedRouteSearch(viewerUserID: number, scope: CatalogAccessScope, scopeKey: string, serviceID: number | undefined, search: string) {
  const [routeSearchPages, setRouteSearchPages] = useState<ScopedPageMap<string, APICatalogRoute>>(() => ({ scopeKey, pages: new Map() }));
  const scopeWitness = useScopeWitness(scopeKey);
  const pagesBySearch = routeSearchPages.scopeKey === scopeKey ? routeSearchPages.pages : new Map<string, ScopedLoadedPage<APICatalogRoute>>();
  const key = `${serviceID ?? 0}:${search}`;
  const saved = pagesBySearch.get(key);
  const current = saved?.scopeKey === scopeKey ? saved : scopedPage<APICatalogRoute>(scopeKey);
  const enabled = serviceID !== undefined && search !== "";
  const query = useAPICatalogRoutes(
    viewerUserID,
    scope,
    serviceID ?? 0,
    { page: current.page, page_size: 50, search },
    { enabled },
  );
  const items = mergeItems(current.items, query.data?.data ?? [])
    .filter((route) => route.api_service_id === serviceID);
  const total = query.data?.total ?? current.total;
  const loadMore = useCallback(() => {
    if (!enabled || !query.data) return;
    const expectedPage = current.page;
    setRouteSearchPages((savedState) => {
      if (scopeWitness.current !== scopeKey) return savedState;
      const allPages = savedState.scopeKey === scopeKey ? savedState.pages : new Map<string, ScopedLoadedPage<APICatalogRoute>>();
      const saved = allPages.get(key);
      if (saved?.scopeKey === scopeKey && saved.page !== expectedPage) return savedState;
      return { scopeKey, pages: new Map(allPages).set(key, { scopeKey, page: expectedPage + 1, items, total }) };
    });
  }, [current, enabled, items, key, query.data, scopeKey, scopeWitness, total]);
  return { query, items, total, loadMore };
}

function CatalogWorkspace({ workspace }: { workspace: ReturnType<typeof useCatalogWorkspace> }) {
  const t = useTranslations("apiCatalog");
  const tc = useTranslations("common");
  const {
    services,
    serviceItems,
    serviceOptionItems,
    serviceOptionQuery,
    serviceOptionTotal,
    serviceOptionLoaded,
    serviceSearch,
    serviceID,
    selectedService,
    routeOptionItems,
    routeOptionQuery,
    routeOptionTotal,
    routeOptionLoaded,
    routeSearch,
    routeID,
    selectedRoute,
  } = workspace;
  const scopeRequired = workspace.catalogScope.mode === "required";
  const scopeHasToken = workspace.catalogScope.mode === "token";
  const tokenInvalidationGuard = useRef(initialCatalogTokenInvalidationGuard);
  const scopeIdentity = catalogScopeIdentity(workspace.viewerUserID, workspace.catalogScope);
  const scopeFailureKind = workspace.scopeFailure?.kind;
  const tokenID = workspace.invocationToken.tokenID;
  const clearToken = workspace.invocationToken.clearToken;
  const markTokenUnavailable = workspace.markTokenUnavailable;

  useEffect(() => {
    const transition = transitionCatalogTokenInvalidationGuard(
      tokenInvalidationGuard.current,
      scopeIdentity,
      scopeFailureKind === "token_not_available" && tokenID > 0,
    );
    tokenInvalidationGuard.current = transition.guard;
    if (!transition.shouldInvalidate) return;
    // behavior change: any active catalog request can invalidate the selected Token.
    clearToken();
    markTokenUnavailable();
  }, [clearToken, markTokenUnavailable, scopeFailureKind, scopeIdentity, tokenID]);

  return (
    <div className="flex min-w-0 flex-col gap-4 overflow-x-clip">
      <CatalogScopeBar workspace={workspace} />
      {scopeRequired ? (
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon"><BookOpen /></EmptyMedia>
            <EmptyTitle>{t("selectTokenForCatalog")}</EmptyTitle>
            <EmptyDescription>{t(workspace.tokenUnavailableNotice ? "tokenNotAvailable" : "selectTokenForCatalog")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : services.isLoading && serviceItems.length === 0 ? (
      <Skeleton className="h-64 w-full" />
    ) : workspace.scopeFailure ? (
      <Alert variant="destructive">
        <AlertTitle>{t(workspace.scopeFailure.kind === "access_unavailable" ? "catalogAccessUnavailable" : "loadFailed")}</AlertTitle>
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{t(workspace.scopeFailure.kind === "access_unavailable" ? "catalogAccessUnavailable" : "loadFailedDescription")}</span>
          <Button type="button" variant="outline" size="sm" onClick={workspace.scopeFailure.retry}>{tc("retry")}</Button>
        </AlertDescription>
      </Alert>
    ) : services.error && serviceItems.length === 0 ? (
      <Alert variant="destructive">
        <AlertTitle>{t(statusOf(services.error) === 401 ? "signInRequired" : statusOf(services.error) === 503 ? "catalogAccessUnavailable" : "loadFailed")}</AlertTitle>
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{t(statusOf(services.error) === 503 ? "catalogAccessUnavailable" : "loadFailedDescription")}</span>
          <Button type="button" variant="outline" size="sm" onClick={() => void services.refetch()}>{tc("retry")}</Button>
        </AlertDescription>
      </Alert>
      ) : serviceItems.length === 0 ? (
      <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon"><BookOpen /></EmptyMedia>
            <EmptyTitle>{t(scopeHasToken ? "tokenHasNoAPIs" : "emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t(scopeHasToken ? "tokenHasNoAPIs" : "emptyDescription")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <CatalogToolbar
          services={serviceOptionItems}
          routes={routeOptionItems}
          serviceID={serviceID}
          routeID={routeID}
          selectedServiceLabel={selectedService?.name}
          selectedRouteLabel={selectedRoute?.slug}
          serviceSearch={serviceSearch}
          routeSearch={routeSearch}
          servicesLoading={serviceOptionQuery.isFetching}
          servicesError={serviceOptionQuery.error}
          servicesHaveMore={serviceOptionLoaded < serviceOptionTotal}
          routesLoading={routeOptionQuery.isFetching}
          routesError={routeOptionQuery.error}
          routesHaveMore={routeOptionLoaded < routeOptionTotal}
          onServiceChange={workspace.selectService}
          onRouteChange={workspace.selectRoute}
          onServiceSearch={workspace.setServiceSearch}
          onRouteSearch={workspace.setRouteSearch}
          onLoadMoreServices={workspace.loadMoreServices}
          onLoadMoreRoutes={workspace.loadMoreRoutes}
          onRetryServices={workspace.retryServices}
          onRetryRoutes={workspace.retryRoutes}
          />
          <div className="grid min-w-0 gap-4 lg:grid-cols-[18rem_minmax(0,1fr)]">
          <div data-testid="catalog-desktop-service-navigation" className="hidden lg:block">
            <ServiceNavigator
              items={serviceOptionItems}
              selectedID={serviceID}
              search={serviceSearch}
              loading={serviceOptionQuery.isFetching}
              error={serviceOptionQuery.error}
              hasMore={serviceOptionLoaded < serviceOptionTotal}
              onSelect={workspace.selectService}
              onSearchChange={workspace.setServiceSearch}
              onLoadMore={workspace.loadMoreServices}
              onRetry={workspace.retryServices}
            />
          </div>
          <main data-testid="catalog-main" className="min-w-0 space-y-4" aria-label={selectedService?.name}>
            <ServiceSummary service={selectedService} />
            <section className="hidden min-w-0 flex-col gap-3 lg:flex">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-lg font-semibold tracking-tight">{t("routes")}</h2>
                <span className="text-xs tabular-nums text-muted-foreground">{routeOptionItems.length}</span>
              </div>
              <RouteNavigator
                items={routeOptionItems}
                selectedID={routeID}
                search={routeSearch}
                loading={routeOptionQuery.isFetching}
                error={routeOptionQuery.error}
                hasMore={routeOptionLoaded < routeOptionTotal}
                onSelect={workspace.selectRoute}
                onSearchChange={workspace.setRouteSearch}
                onLoadMore={workspace.loadMoreRoutes}
                onRetry={workspace.retryRoutes}
              />
            </section>
            {selectedService && selectedRoute && workspace.protocol && workspace.draft ? (
              <CatalogRouteWorkbench
                service={selectedService}
                route={selectedRoute}
                protocol={workspace.protocol}
                draft={workspace.draft}
                onProtocolChange={workspace.selectProtocol}
                onDraftChange={workspace.setDraft}
                invocationToken={workspace.invocationToken}
                effective={workspace.effective}
              />
            ) : null}
          </main>
          </div>
        </>
      )}
    </div>
  );
}

function CatalogScopeBar({ workspace }: { workspace: ReturnType<typeof useCatalogWorkspace> }) {
  const t = useTranslations("apiCatalog");
  const hint = workspace.catalogScope.mode === "admin-all"
    ? t("adminAllAPIs")
    : workspace.catalogScope.mode === "required"
      ? t("selectTokenForCatalog")
      : t("tokenLabel");

  return (
    <section data-testid="catalog-token-scope" className="flex min-w-0 flex-col gap-2 rounded-lg border border-border bg-card p-3 sm:flex-row sm:items-center sm:justify-between">
      <CatalogTokenPicker
        id="catalog-token-picker"
        label={t("tokenLabel")}
        tokenID={workspace.invocationToken.tokenID}
        onTokenChange={workspace.invocationToken.setTokenID}
        onTokenClear={workspace.invocationToken.clearToken}
      />
      <p className="text-sm text-muted-foreground">{hint}</p>
    </section>
  );
}

function ServiceSummary({ service }: { service?: APICatalogService }) {
  const t = useTranslations("apiCatalog");
  if (!service) return null;
  return (
    <header className="flex min-w-0 flex-col gap-1 border-b pb-4">
      <p className="font-mono text-xs text-muted-foreground">{service.slug}</p>
      <h2 className="text-xl font-semibold tracking-tight">{service.name}</h2>
      <p className="text-sm text-muted-foreground">{service.description || t("noDescription")}</p>
    </header>
  );
}

function CatalogRouteWorkbench({
  service,
  route,
  protocol,
  draft,
  onProtocolChange,
  onDraftChange,
  invocationToken,
  effective,
}: {
  service: APICatalogService;
  route: APICatalogRoute;
  protocol: CatalogProtocol;
  draft: RequestExampleDraft;
  onProtocolChange: (protocol: CatalogProtocol) => void;
  onDraftChange: (draft: RequestExampleDraft) => void;
  invocationToken: ReturnType<typeof useInvocationToken>;
  effective: ReturnType<typeof useAPICatalogEffective>;
}) {
  const origin = useBrowserOrigin();
  const effectiveAccess = effective.error || !effective.data
    ? "unknown"
    : effective.data.scope === "service" || effective.data.route_ids.includes(route.id)
      ? "granted"
      : "not_granted";
  const chooseToken = () => document.getElementById("catalog-token-picker")?.click();

  return (
    <Card>
      <CardContent className="flex min-w-0 flex-col gap-5 pt-6">
        <InvocationWorkbench
          origin={origin}
          service={service}
          route={route}
          protocol={protocol}
          draft={draft}
          tokenID={invocationToken.tokenID}
          token={invocationToken.token}
          tokenChecking={invocationToken.isChecking}
          tokenFailure={invocationToken.failure}
          effectiveAccess={effectiveAccess}
          effectiveUnavailable={effective.isError}
          onProtocolChange={onProtocolChange}
          onDraftChange={onDraftChange}
          onChooseToken={chooseToken}
          onTokenCommandCopied={invocationToken.rememberToken}
        />
      </CardContent>
    </Card>
  );
}

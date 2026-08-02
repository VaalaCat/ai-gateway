"use client";

import Link from "next/link";
import { notFound, useSearchParams } from "next/navigation";
import { Fragment, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, KeyRound, PackageSearch, RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";

import { ModelCard } from "@/components/model-marketplace/model-card";
import { ModelFilters } from "@/components/model-marketplace/model-filters";
import {
  findValidMarketplaceTokens,
  TokenPicker,
} from "@/components/model-marketplace/token-picker";
import { useTokenExpiryClock } from "@/components/model-marketplace/use-token-expiry-clock";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Separator } from "@/components/ui/separator";
import {
  useAdminModelMarketplaceList,
  useMarketplaceTokens,
  useModelMarketplaceList,
  type AdminModelMarketplaceListResponse,
  type MarketplaceFilters,
  type MarketplaceModel,
  type ModelMarketplaceKind,
  type ModelMarketplaceListParams,
  type ModelMarketplaceListResponse,
} from "@/lib/api/model-marketplace";
import {
  isModelMarketplaceVisible,
  useCapabilities,
} from "@/lib/api/capabilities";
import { ApiError } from "@/lib/api/client";
import { useAuth } from "@/lib/auth";
import { replaceCurrentPageSearch } from "@/lib/replace-current-page-search";
import type { Token } from "@/lib/types";

const LAST_TOKEN_KEY_PREFIX = "aigw:model-marketplace:last-token-id";
const EMPTY_TOKENS: Token[] = [];

interface OptimisticTokenSelection {
  viewerId: number;
  tokenId: number | undefined;
  sourceSearch: string;
  targetSearch: string;
}

function lastTokenStorageKey(userId: number | undefined) {
  return `${LAST_TOKEN_KEY_PREFIX}:${userId ?? "unknown"}`;
}

function parsePositiveTokenId(value: string | null) {
  if (value === null || !/^\d+$/.test(value)) return undefined;
  const tokenId = Number(value);
  return Number.isSafeInteger(tokenId) && tokenId > 0 ? tokenId : undefined;
}

function readRememberedTokenId(userId: number | undefined) {
  if (typeof window === "undefined") return undefined;
  try {
    return parsePositiveTokenId(window.localStorage.getItem(lastTokenStorageKey(userId)));
  } catch {
    return undefined;
  }
}

function rememberTokenId(userId: number | undefined, tokenId: number) {
  try {
    window.localStorage.setItem(lastTokenStorageKey(userId), String(tokenId));
  } catch {
    // Storage can be unavailable; URL state remains the source of truth.
  }
}

function forgetRememberedTokenId(userId: number | undefined, tokenId: number) {
  try {
    const key = lastTokenStorageKey(userId);
    if (window.localStorage.getItem(key) === String(tokenId)) {
      window.localStorage.removeItem(key);
    }
  } catch {
    // Storage can be unavailable; URL state remains the source of truth.
  }
}

function CatalogSkeleton() {
  const t = useTranslations("modelMarketplace");
  return (
    <div className="flex flex-col gap-4" aria-label={t("catalogLoading")} aria-busy="true">
      <Skeleton className="h-9 w-full" />
      <Card className="gap-0 py-0">
        {[0, 1, 2, 3].map((index) => (
          <Fragment key={index}>
            {index > 0 ? <Separator /> : null}
            <div
              data-testid="catalog-skeleton-row"
              className="grid min-w-0 gap-3 px-4 py-4 md:grid-cols-[minmax(0,1fr)_minmax(22rem,1.2fr)]"
            >
              <div data-testid="catalog-skeleton-identity" className="flex min-w-0 gap-3">
                <Skeleton className="size-9 shrink-0" />
                <div className="flex min-w-0 flex-1 flex-col gap-2">
                  <Skeleton className="h-4 w-2/5" />
                  <Skeleton className="h-3 w-3/5" />
                </div>
              </div>
              <Skeleton data-testid="catalog-skeleton-availability" className="h-6 w-40" />
              <div
                data-testid="catalog-skeleton-prices"
                className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4"
              >
                {[0, 1, 2, 3].map((priceIndex) => (
                  <div key={priceIndex} className="flex flex-col gap-2">
                    <Skeleton className="h-3 w-12" />
                    <Skeleton className="h-4 w-16" />
                  </div>
                ))}
              </div>
              <div
                data-testid="catalog-skeleton-channels"
                className="flex items-end justify-between gap-3"
              >
                <div className="flex flex-col gap-2">
                  <Skeleton className="h-3 w-24" />
                  <Skeleton className="h-5 w-36" />
                </div>
                <div className="flex -space-x-2">
                  {[0, 1, 2].map((avatarIndex) => (
                    <Skeleton key={avatarIndex} className="size-8 rounded-full" />
                  ))}
                </div>
              </div>
            </div>
          </Fragment>
        ))}
      </Card>
    </div>
  );
}

function NoTokenState() {
  const t = useTranslations("modelMarketplace");
  return (
    <Empty className="border">
      <EmptyHeader>
        <EmptyMedia variant="icon"><KeyRound aria-hidden="true" /></EmptyMedia>
        <EmptyTitle>{t("noTokenTitle")}</EmptyTitle>
        <EmptyDescription>{t("noTokenDescription")}</EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <Button asChild>
          <Link href="/tokens">{t("manageTokens")}</Link>
        </Button>
      </EmptyContent>
    </Empty>
  );
}

function ChooseTokenState() {
  const t = useTranslations("modelMarketplace");
  return (
    <Empty className="border">
      <EmptyHeader>
        <EmptyMedia variant="icon"><KeyRound aria-hidden="true" /></EmptyMedia>
        <EmptyTitle>{t("chooseTokenTitle")}</EmptyTitle>
        <EmptyDescription>{t("chooseTokenDescription")}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  );
}

interface CatalogViewProps {
  models: MarketplaceModel[];
  filters: MarketplaceFilters;
  isLoading: boolean;
  isError: boolean;
  refetch: () => unknown;
  params: ModelMarketplaceListParams;
  onSearchChange: (value: string) => void;
  onProviderChange: (value: string) => void;
  onKindChange: (value: ModelMarketplaceKind) => void;
}

function CatalogView({
  models,
  filters,
  isLoading,
  isError,
  refetch,
  params,
  onSearchChange,
  onProviderChange,
  onKindChange,
}: CatalogViewProps) {
  const t = useTranslations("modelMarketplace");

  return (
    <div className="flex flex-col gap-4">
      <ModelFilters
        search={params.search ?? ""}
        provider={params.provider ?? ""}
        kind={params.kind ?? ""}
        providers={filters.providers}
        disabled={isError}
        onSearchChange={onSearchChange}
        onProviderChange={onProviderChange}
        onKindChange={onKindChange}
      />
      {isLoading ? (
        <CatalogSkeleton />
      ) : isError ? (
        <Alert variant="destructive">
          <AlertCircle aria-hidden="true" />
          <AlertTitle>{t("loadErrorTitle")}</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-3">
            <p>{t("loadErrorDescription")}</p>
            <Button variant="outline" size="sm" onClick={() => refetch()}>
              <RefreshCw data-icon="inline-start" />
              {t("retry")}
            </Button>
          </AlertDescription>
        </Alert>
      ) : models.length === 0 ? (
        <Empty className="border">
          <EmptyHeader>
            <EmptyMedia variant="icon"><PackageSearch aria-hidden="true" /></EmptyMedia>
            <EmptyTitle>{t("emptyTitle")}</EmptyTitle>
            <EmptyDescription>{t("emptyDescription")}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <Card className="gap-0 py-0" data-testid="model-catalog-list">
          {models.map((model, index) => {
            const key = model.kind === "real" ? model.real.model_name : model.routing.model_name;
            return (
              <Fragment key={`${model.kind}:${key}`}>
                {index > 0 ? <Separator data-testid="model-catalog-separator" /> : null}
                <ModelCard model={model} detailTokenId={params.tokenId} />
              </Fragment>
            );
          })}
        </Card>
      )}
    </div>
  );
}

type CatalogControls = Pick<
  CatalogViewProps,
  "params" | "onSearchChange" | "onProviderChange" | "onKindChange"
>;

interface CatalogQueryProps extends CatalogControls {
  viewerId: number;
  onTokenUnavailable: (tokenId: number) => void;
}

const TOKEN_REJECTION_CODES = new Set([
  "marketplace_token_disabled",
  "marketplace_token_expired",
]);

function isTokenRejection(error: unknown) {
  return error instanceof ApiError &&
    error.status === 422 &&
    TOKEN_REJECTION_CODES.has(String(error.body?.code ?? ""));
}

function UserCatalog({ viewerId, onTokenUnavailable, ...props }: CatalogQueryProps) {
  const query = useModelMarketplaceList(props.params, viewerId, {
    retry: (failureCount, error) => !isTokenRejection(error) && failureCount < 1,
  });
  const response: ModelMarketplaceListResponse | undefined = query.data;
  useEffect(() => {
    if (props.params.tokenId && isTokenRejection(query.error)) {
      onTokenUnavailable(props.params.tokenId);
    }
  }, [onTokenUnavailable, props.params.tokenId, query.error]);
  return (
    <CatalogView
      {...props}
      models={response?.models ?? []}
      filters={response?.filters ?? { providers: [], input_modalities: [], output_modalities: [] }}
      isLoading={query.isLoading}
      isError={query.isError && !isTokenRejection(query.error)}
      refetch={query.refetch}
    />
  );
}

function AdminCatalog({ viewerId, onTokenUnavailable, ...props }: CatalogQueryProps) {
  const query = useAdminModelMarketplaceList(props.params, viewerId, {
    retry: (failureCount, error) => !isTokenRejection(error) && failureCount < 1,
  });
  const response: AdminModelMarketplaceListResponse | undefined = query.data;
  useEffect(() => {
    if (props.params.tokenId && isTokenRejection(query.error)) {
      onTokenUnavailable(props.params.tokenId);
    }
  }, [onTokenUnavailable, props.params.tokenId, query.error]);
  return (
    <CatalogView
      {...props}
      models={response?.models ?? []}
      filters={response?.filters ?? { providers: [], input_modalities: [], output_modalities: [] }}
      isLoading={query.isLoading}
      isError={query.isError && !isTokenRejection(query.error)}
      refetch={query.refetch}
    />
  );
}

function useMarketplaceTokenSelection(userId: number, isAdmin: boolean) {
  const searchParams = useSearchParams();
  const currentSearch = searchParams.toString();
  const requestedTokenId = parsePositiveTokenId(searchParams.get("token_id"));
  const [optimisticSelection, setOptimisticSelection] =
    useState<OptimisticTokenSelection>();
  const [selectionInitialized, setSelectionInitialized] = useState(false);
  const [rejectedTokenIds, setRejectedTokenIds] = useState<Set<number>>(() => new Set());
  const rejectedTokenIdsRef = useRef(new Set<number>());
  const [tokenUnavailable, setTokenUnavailable] = useState(false);

  const tokenQuery = useMarketplaceTokens(userId);
  const nowSeconds = useTokenExpiryClock(tokenQuery.data ?? EMPTY_TOKENS);
  const validTokens = useMemo(
    () => findValidMarketplaceTokens(tokenQuery.data ?? [], nowSeconds)
      .filter((token) => !rejectedTokenIds.has(token.id)),
    [nowSeconds, rejectedTokenIds, tokenQuery.data],
  );
  const validTokenIds = useMemo(
    () => new Set(validTokens.map((token) => token.id)),
    [validTokens],
  );
  const optimisticIsPending =
    optimisticSelection?.viewerId === userId &&
    optimisticSelection.sourceSearch === currentSearch;
  const optimisticTokenIsValid =
    optimisticSelection?.tokenId === undefined ||
    validTokenIds.has(optimisticSelection.tokenId);
  const selectedTokenId = optimisticIsPending && optimisticTokenIsValid
    ? optimisticSelection.tokenId
    : requestedTokenId && validTokenIds.has(requestedTokenId)
      ? requestedTokenId
      : undefined;

  const replaceTokenParam = useCallback((tokenId: number | undefined) => {
    const params = new URLSearchParams(currentSearch);
    if (tokenId === undefined) {
      params.delete("token_id");
    } else {
      params.set("token_id", String(tokenId));
    }
    const targetSearch = params.toString();
    setOptimisticSelection({
      viewerId: userId,
      tokenId,
      sourceSearch: currentSearch,
      targetSearch,
    });
    replaceCurrentPageSearch(targetSearch);
  }, [currentSearch, userId]);

  useEffect(() => {
    if (!optimisticSelection) return;
    if (
      optimisticSelection.tokenId !== undefined &&
      !validTokenIds.has(optimisticSelection.tokenId)
    ) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- an expired or disabled optimistic Token must stop owning catalog scope immediately
      setOptimisticSelection(undefined);
      return;
    }
    if (
      currentSearch === optimisticSelection.targetSearch ||
      currentSearch !== optimisticSelection.sourceSearch
    ) {
      setOptimisticSelection(undefined);
    }
  }, [currentSearch, optimisticSelection, validTokenIds]);

  useEffect(() => {
    if (tokenQuery.isLoading || tokenQuery.isError || selectionInitialized) return;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- resolve initial URL/localStorage selection once after the scoped Token query settles
    setSelectionInitialized(true);
    if (isAdmin || (requestedTokenId && validTokenIds.has(requestedTokenId))) return;

    const rememberedTokenId = readRememberedTokenId(userId);
    const initialTokenId = validTokens.length === 1
      ? validTokens[0].id
      : rememberedTokenId && validTokenIds.has(rememberedTokenId)
        ? rememberedTokenId
        : undefined;
    if (initialTokenId) replaceTokenParam(initialTokenId);
  }, [
    isAdmin,
    replaceTokenParam,
    requestedTokenId,
    selectionInitialized,
    tokenQuery.isError,
    tokenQuery.isLoading,
    userId,
    validTokenIds,
    validTokens,
  ]);

  useEffect(() => {
    if (
      selectionInitialized &&
      requestedTokenId &&
      !validTokenIds.has(requestedTokenId) &&
      !optimisticIsPending
    ) {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- remove a URL Token that expired or disappeared from the freshly loaded scoped list
      replaceTokenParam(undefined);
    }
  }, [
    optimisticIsPending,
    replaceTokenParam,
    requestedTokenId,
    selectionInitialized,
    validTokenIds,
  ]);

  const handleTokenChange = (tokenId: number | undefined) => {
    setTokenUnavailable(false);
    if (tokenId !== undefined && !isAdmin) rememberTokenId(userId, tokenId);
    replaceTokenParam(tokenId);
  };

  const refetchTokens = tokenQuery.refetch;
  const handleTokenUnavailable = useCallback((tokenId: number) => {
    if (rejectedTokenIdsRef.current.has(tokenId)) return;
    rejectedTokenIdsRef.current.add(tokenId);
    setRejectedTokenIds((current) => new Set(current).add(tokenId));
    setTokenUnavailable(true);
    forgetRememberedTokenId(userId, tokenId);
    replaceTokenParam(undefined);
    void refetchTokens();
  }, [
    refetchTokens,
    replaceTokenParam,
    setRejectedTokenIds,
    setTokenUnavailable,
    userId,
  ]);

  return {
    validTokens,
    selectedTokenId,
    isLoading: tokenQuery.isLoading,
    isError: tokenQuery.isError,
    refetch: tokenQuery.refetch,
    tokenUnavailable,
    handleTokenChange,
    handleTokenUnavailable,
  };
}

function TokenListError({ retry }: { retry: () => unknown }) {
  const t = useTranslations("modelMarketplace");
  return (
    <Alert variant="destructive">
      <AlertCircle aria-hidden="true" />
      <AlertTitle>{t("tokenLoadErrorTitle")}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-3">
        <p>{t("tokenLoadErrorDescription")}</p>
        <Button variant="outline" size="sm" onClick={() => retry()}>
          <RefreshCw data-icon="inline-start" />
          {t("retry")}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function TokenUnavailableAlert() {
  const t = useTranslations("modelMarketplace");
  return (
    <Alert variant="destructive">
      <AlertCircle aria-hidden="true" />
      <AlertTitle>{t("tokenUnavailableTitle")}</AlertTitle>
      <AlertDescription>{t("tokenUnavailableDescription")}</AlertDescription>
    </Alert>
  );
}

function ModelMarketplacePageContent({ userId, isAdmin }: { userId: number; isAdmin: boolean }) {
  const t = useTranslations("modelMarketplace");
  const tokenSelection = useMarketplaceTokenSelection(userId, isAdmin);
  const [search, setSearch] = useState("");
  const [provider, setProvider] = useState("");
  const [kind, setKind] = useState<ModelMarketplaceKind>("");

  const params = useMemo<ModelMarketplaceListParams>(() => ({
    tokenId: tokenSelection.selectedTokenId,
    search,
    provider,
    kind,
  }), [kind, provider, search, tokenSelection.selectedTokenId]);

  const controls: CatalogControls = {
    params,
    onSearchChange: setSearch,
    onProviderChange: setProvider,
    onKindChange: setKind,
  };

  const catalog = isAdmin ? (
    <AdminCatalog
      {...controls}
      viewerId={userId}
      onTokenUnavailable={tokenSelection.handleTokenUnavailable}
    />
  ) : tokenSelection.selectedTokenId ? (
    <UserCatalog
      {...controls}
      viewerId={userId}
      onTokenUnavailable={tokenSelection.handleTokenUnavailable}
    />
  ) : tokenSelection.validTokens.length > 0 ? (
    <ChooseTokenState />
  ) : (
    <NoTokenState />
  );

  return (
    <PageLayout
      title={t("title")}
      description={t("description")}
      maxWidth="full"
      actions={isAdmin && !tokenSelection.selectedTokenId ? <Badge variant="outline">{t("adminGlobalView")}</Badge> : undefined}
    >
      <div
        data-testid="marketplace-directory"
        className="flex min-w-0 flex-col gap-6 overflow-x-auto"
      >
        {tokenSelection.isError ? (
          <TokenListError retry={tokenSelection.refetch} />
        ) : (
          <>
            <TokenPicker
              tokens={tokenSelection.validTokens}
              selectedTokenId={tokenSelection.selectedTokenId}
              isLoading={tokenSelection.isLoading}
              allowGlobal={isAdmin}
              onChange={tokenSelection.handleTokenChange}
            />
            {tokenSelection.tokenUnavailable ? <TokenUnavailableAlert /> : null}
            {tokenSelection.isLoading ? <CatalogSkeleton /> : catalog}
          </>
        )}
      </div>
    </PageLayout>
  );
}

function OrdinaryUserMarketplaceGate({ userId }: { userId: number }) {
  const capabilities = useCapabilities(userId);
  if (capabilities.isLoading) return <CatalogSkeleton />;
  if (
    capabilities.isError ||
    !isModelMarketplaceVisible(capabilities.data, false)
  ) {
    return notFound();
  }
  return <ModelMarketplacePageContent userId={userId} isAdmin={false} />;
}

function ModelMarketplaceAccessBoundary() {
  const { user, loading, isAdmin } = useAuth();
  if (loading) return <CatalogSkeleton />;
  if (!user) return notFound();
  if (isAdmin) {
    return <ModelMarketplacePageContent key={`${user.user_id}:admin`} userId={user.user_id} isAdmin />;
  }
  return <OrdinaryUserMarketplaceGate key={`${user.user_id}:user`} userId={user.user_id} />;
}

export default function ModelMarketplacePage() {
  return (
    <Suspense fallback={<CatalogSkeleton />}>
      <ModelMarketplaceAccessBoundary />
    </Suspense>
  );
}

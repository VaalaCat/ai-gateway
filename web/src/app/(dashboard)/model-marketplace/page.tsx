"use client";

import Link from "next/link";
import { notFound } from "next/navigation";
import { Fragment, Suspense, useEffect, useMemo } from "react";
import { AlertCircle, KeyRound, PackageSearch, RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";

import { DataTablePagination } from "@/components/data-table/pagination";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec, FilterValues } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { PageLayout } from "@/components/layout/page-layout";
import { ModelCard } from "@/components/model-marketplace/model-card";
import {
  useMarketplaceTokenSelection,
  type MarketplaceTokenSelection,
} from "@/components/model-marketplace/use-marketplace-token-selection";
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
  useModelMarketplaceList,
  type AdminModelMarketplaceListResponse,
  type MarketplaceFilters,
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

const EMPTY_FILTERS: MarketplaceFilters = {
  providers: [],
  input_modalities: [],
  output_modalities: [],
};

const catalogFilterSpec = {
  token_id: { kind: "picker", entity: "usable-token" },
  search: { kind: "text", debounceMs: 300 },
  provider: { kind: "enum", options: [] },
  kind: { kind: "enum", options: [] },
} satisfies FilterSpec;

function CatalogSkeleton() {
  const t = useTranslations("modelMarketplace");
  return (
    <div className="flex flex-col gap-4" aria-label={t("catalogLoading")} aria-busy="true">
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

function CatalogPageSkeleton() {
  const t = useTranslations("modelMarketplace");
  return <PageLayout title={t("title")} description={t("description")} maxWidth="full"><CatalogSkeleton /></PageLayout>;
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

function TokenValidationError({ retry }: { retry: () => unknown }) {
  const t = useTranslations("modelMarketplace");
  return (
    <Alert variant="destructive">
      <AlertCircle aria-hidden="true" />
      <AlertTitle>{t("tokenValidationErrorTitle")}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-3">
        <p>{t("tokenValidationErrorDescription")}</p>
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

function parseToolbarTokenId(value: FilterValues[string]) {
  const tokenId = Number(value);
  return Number.isSafeInteger(tokenId) && tokenId > 0 ? tokenId : undefined;
}

type ScopeState = "no-token" | "choose-token";

interface CatalogViewProps {
  response?: ModelMarketplaceListResponse | AdminModelMarketplaceListResponse;
  isLoading: boolean;
  isFetching: boolean;
  isPlaceholderData: boolean;
  isError: boolean;
  refetch: () => unknown;
  params: ModelMarketplaceListParams;
  filterValues: FilterValues;
  setFilterValues: (next: Partial<FilterValues>) => void;
  setPagination: (page: number, pageSize: number) => void;
  tokenSelection: MarketplaceTokenSelection;
  isAdmin: boolean;
  scopeState?: ScopeState;
}

function CatalogView({
  response,
  isLoading,
  isFetching,
  isPlaceholderData,
  isError,
  refetch,
  params,
  filterValues,
  setFilterValues,
  setPagination,
  tokenSelection,
  isAdmin,
  scopeState,
}: CatalogViewProps) {
  const t = useTranslations("modelMarketplace");
  const filters = response?.filters ?? EMPTY_FILTERS;
  const toolbarSpec = useMemo<FilterSpec>(() => ({
    token_id: {
      kind: "picker",
      entity: "usable-token",
      label: t("tokenLabel"),
      placeholder: isAdmin ? t("adminGlobalOption") : t("tokenPlaceholder"),
    },
    search: {
      kind: "text",
      label: t("searchLabel"),
      placeholder: t("searchPlaceholder"),
      debounceMs: 300,
    },
    provider: {
      kind: "enum",
      label: t("providerLabel"),
      placeholder: t("allProviders"),
      options: filters.providers.map((value) => ({ value, label: value })),
    },
    kind: {
      kind: "enum",
      label: t("kindLabel"),
      placeholder: t("allKinds"),
      options: [
        { value: "real", label: t("kind.real") },
        { value: "routing", label: t("kind.routing") },
      ],
    },
  }), [filters.providers, isAdmin, t]);

  const handleToolbarChange = (next: Partial<FilterValues>) => {
    if (Object.hasOwn(next, "token_id")) {
      tokenSelection.handleTokenChange(parseToolbarTokenId(next.token_id));
      return;
    }
    setFilterValues(next);
  };

  let content;
  if (tokenSelection.validation.status === "validationError") {
    content = <TokenValidationError retry={tokenSelection.validation.retry} />;
  } else if (
    tokenSelection.validation.status === "initialPending" ||
    tokenSelection.validation.status === "rejected"
  ) {
    content = <CatalogSkeleton />;
  } else if (
    !isAdmin &&
    tokenSelection.candidateTokenId === undefined &&
    tokenSelection.ordinaryBootstrap.status === "error"
  ) {
    content = <TokenListError retry={tokenSelection.ordinaryBootstrap.retry} />;
  } else if (
    !isAdmin &&
    tokenSelection.candidateTokenId === undefined &&
    tokenSelection.ordinaryBootstrap.status === "initialPending"
  ) {
    content = <CatalogSkeleton />;
  } else if (scopeState === "no-token") {
    content = <NoTokenState />;
  } else if (scopeState === "choose-token") {
    content = <ChooseTokenState />;
  } else if (isLoading && response === undefined) {
    content = <CatalogSkeleton />;
  } else if (isError) {
    content = (
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
    );
  } else if ((response?.models.length ?? 0) === 0) {
    content = (
      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon"><PackageSearch aria-hidden="true" /></EmptyMedia>
          <EmptyTitle>{t("emptyTitle")}</EmptyTitle>
          <EmptyDescription>{t("emptyDescription")}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  } else {
    content = (
      <Card className="gap-0 py-0" data-testid="model-catalog-list">
        {response?.models.map((model, index) => {
          const key = model.kind === "real" ? model.real.model_name : model.routing.model_name;
          return (
            <Fragment key={`${model.kind}:${key}`}>
              {index > 0 ? <Separator data-testid="model-catalog-separator" /> : null}
              <ModelCard model={model} detailTokenId={params.tokenId} />
            </Fragment>
          );
        })}
      </Card>
    );
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <FilterableToolbar
        spec={toolbarSpec}
        value={{
          ...filterValues,
          token_id: tokenSelection.candidateTokenId ? String(tokenSelection.candidateTokenId) : "",
        }}
        onChange={handleToolbarChange}
      />
      {tokenSelection.tokenUnavailable ? <TokenUnavailableAlert /> : null}
      <div
        data-testid="model-catalog-results"
        className="flex min-w-0 flex-col gap-4"
        aria-busy={isFetching || tokenSelection.validation.status === "backgroundFetching"}
      >
        {content}
        {response ? (
          <DataTablePagination
            page={isPlaceholderData ? params.page : response.page}
            pageSize={isPlaceholderData ? params.pageSize : response.page_size}
            pageCount={Math.max(1, Math.ceil(
              response.total / (isPlaceholderData ? params.pageSize : response.page_size),
            ))}
            onPaginationChange={setPagination}
          />
        ) : null}
      </div>
    </div>
  );
}

interface CatalogQueryProps {
  viewerId: number;
  params: ModelMarketplaceListParams;
  filterValues: FilterValues;
  setFilterValues: (next: Partial<FilterValues>) => void;
  setPagination: (page: number, pageSize: number) => void;
  tokenSelection: MarketplaceTokenSelection;
  isAdmin: boolean;
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

function UserCatalog({ viewerId, ...props }: CatalogQueryProps) {
  const query = useModelMarketplaceList(props.params, viewerId, {
    retry: (failureCount, error) => !isTokenRejection(error) && failureCount < 1,
  });
  useEffect(() => {
    if (props.params.tokenId && isTokenRejection(query.error)) {
      props.tokenSelection.handleTokenUnavailable(props.params.tokenId);
    }
  }, [props.params.tokenId, props.tokenSelection, query.error]);
  return (
    <CatalogView
      {...props}
      response={query.data}
      isLoading={query.isLoading}
      isFetching={query.isFetching}
      isPlaceholderData={query.isPlaceholderData}
      isError={query.isError && !isTokenRejection(query.error)}
      refetch={query.refetch}
    />
  );
}

function AdminCatalog({ viewerId, ...props }: CatalogQueryProps) {
  const query = useAdminModelMarketplaceList(props.params, viewerId, {
    retry: (failureCount, error) => !isTokenRejection(error) && failureCount < 1,
  });
  useEffect(() => {
    if (props.params.tokenId && isTokenRejection(query.error)) {
      props.tokenSelection.handleTokenUnavailable(props.params.tokenId);
    }
  }, [props.params.tokenId, props.tokenSelection, query.error]);
  return (
    <CatalogView
      {...props}
      response={query.data}
      isLoading={query.isLoading}
      isFetching={query.isFetching}
      isPlaceholderData={query.isPlaceholderData}
      isError={query.isError && !isTokenRejection(query.error)}
      refetch={query.refetch}
    />
  );
}

function ModelMarketplacePageContent({ userId, isAdmin }: { userId: number; isAdmin: boolean }) {
  const t = useTranslations("modelMarketplace");
  const patchSearchParams = useSearchParamPatch();
  const [page, pageSize, setPagination] = usePaginationState(20, { patchSearchParams });
  const [filterValues, setFilterValues] = useFilterState(catalogFilterSpec, { patchSearchParams });
  const tokenSelection = useMarketplaceTokenSelection(userId, isAdmin, patchSearchParams);
  const params = useMemo<ModelMarketplaceListParams>(() => ({
    tokenId: tokenSelection.selectedTokenId,
    search: String(filterValues.search ?? ""),
    provider: String(filterValues.provider ?? ""),
    kind: String(filterValues.kind ?? "") as ModelMarketplaceKind,
    page,
    pageSize,
  }), [filterValues.kind, filterValues.provider, filterValues.search, page, pageSize, tokenSelection.selectedTokenId]);
  const sharedProps = {
    params,
    filterValues,
    setFilterValues,
    setPagination,
    tokenSelection,
    isAdmin,
  };

  let catalog;
  if (tokenSelection.selectedTokenId !== undefined && isAdmin) {
    catalog = <AdminCatalog {...sharedProps} viewerId={userId} />;
  } else if (tokenSelection.selectedTokenId !== undefined) {
    catalog = <UserCatalog {...sharedProps} viewerId={userId} />;
  } else if (tokenSelection.candidateTokenId !== undefined) {
    catalog = (
      <CatalogView
        {...sharedProps}
        isLoading={false}
        isFetching={false}
        isPlaceholderData={false}
        isError={false}
        refetch={() => undefined}
      />
    );
  } else if (isAdmin) {
    catalog = <AdminCatalog {...sharedProps} viewerId={userId} />;
  } else {
    catalog = (
      <CatalogView
        {...sharedProps}
        isLoading={false}
        isFetching={false}
        isPlaceholderData={false}
        isError={false}
        refetch={() => undefined}
        scopeState={tokenSelection.ordinaryBootstrap.totalUsableTokens > 0
          ? "choose-token"
          : "no-token"}
      />
    );
  }

  return (
    <PageLayout
      title={t("title")}
      description={t("description")}
      maxWidth="full"
      actions={isAdmin && tokenSelection.candidateTokenId === undefined
        ? <Badge variant="outline">{t("adminGlobalView")}</Badge>
        : undefined}
    >
      <div data-testid="marketplace-directory" className="flex min-w-0 flex-col gap-6">
        {catalog}
      </div>
    </PageLayout>
  );
}

function OrdinaryUserMarketplaceGate({ userId }: { userId: number }) {
  const capabilities = useCapabilities(userId);
  if (capabilities.isLoading) return <CatalogPageSkeleton />;
  if (capabilities.isError || !isModelMarketplaceVisible(capabilities.data, false)) {
    return notFound();
  }
  return <ModelMarketplacePageContent userId={userId} isAdmin={false} />;
}

function ModelMarketplaceAccessBoundary() {
  const { user, loading, isAdmin } = useAuth();
  if (loading) return <CatalogPageSkeleton />;
  if (!user) return notFound();
  if (isAdmin) {
    return <ModelMarketplacePageContent key={`${user.user_id}:admin`} userId={user.user_id} isAdmin />;
  }
  return <OrdinaryUserMarketplaceGate key={`${user.user_id}:user`} userId={user.user_id} />;
}

export default function ModelMarketplacePage() {
  return (
    <Suspense fallback={<CatalogPageSkeleton />}>
      <ModelMarketplaceAccessBoundary />
    </Suspense>
  );
}

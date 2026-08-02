"use client";

import Link from "next/link";
import { notFound, useSearchParams } from "next/navigation";
import { Suspense } from "react";
import { AlertCircle, ArrowLeft, RefreshCw, Route } from "lucide-react";
import { useTranslations } from "next-intl";

import { ModelName } from "@/components/business/model-name";
import { ModelProviderLogo } from "@/components/business/model-provider-logo";
import { TokensCell } from "@/components/business/tokens-cell";
import { PageLayout } from "@/components/layout/page-layout";
import {
  AdminRoutingDiagnostics,
  type AdminRoutingDefinitionDiagnosticsView,
} from "@/components/model-marketplace/admin-routing-diagnostics";
import {
  OfferComparison,
  type AdminOfferDiagnosticsView,
} from "@/components/model-marketplace/offer-comparison";
import { MARKETPLACE_STATUS_PRESENTATION } from "@/components/model-marketplace/marketplace-status";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { TooltipProvider } from "@/components/ui/tooltip";
import {
  useAdminModelMarketplaceDetail,
  useModelMarketplaceDetail,
  type AdminModelMarketplaceDetailResponse,
  type MarketplaceEndpoint,
  type MarketplaceHealthStatus,
  type MarketplaceModelOfferDetail,
  type MarketplaceUsageWindow,
  type ModelMarketplaceDetailParams,
  type ModelMarketplaceDetailResponse,
} from "@/lib/api/model-marketplace";
import {
  isModelMarketplaceVisible,
  useCapabilities,
} from "@/lib/api/capabilities";
import { useAuth } from "@/lib/auth";
import { marketplaceDetailHref } from "@/lib/model-marketplace-navigation";
import { replaceCurrentPageSearch } from "@/lib/replace-current-page-search";

const DETAIL_WINDOWS: MarketplaceUsageWindow[] = ["24h", "7d", "30d"];
const INVOCATION_ENDPOINTS: MarketplaceEndpoint[] = [
  "chat_completions",
  "responses",
  "messages",
];

function parsePositiveTokenId(value: string | null) {
  if (value === null || !/^\d+$/.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function parseWindow(value: string | null): MarketplaceUsageWindow {
  return DETAIL_WINDOWS.includes(value as MarketplaceUsageWindow)
    ? value as MarketplaceUsageWindow
    : "24h";
}

function DetailSkeleton() {
  const t = useTranslations("modelMarketplace.detail");
  return (
    <div className="flex min-w-0 flex-col gap-4" aria-label={t("loading")} aria-busy="true">
      <div data-testid="detail-skeleton-header" className="flex min-w-0 items-center gap-3">
        <Skeleton className="size-8 shrink-0" />
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <Skeleton className="h-6 w-64 max-w-full" />
          <Skeleton className="h-3 w-80 max-w-full" />
        </div>
      </div>
      <div
        data-testid="detail-skeleton-comparison"
        className="flex min-w-0 flex-col gap-3 rounded-xl border p-4"
      >
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-8 w-full" />
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {[0, 1, 2, 3].map((index) => (
            <Skeleton key={index} className="h-16 w-full" />
          ))}
        </div>
      </div>
      <div
        data-testid="detail-skeleton-status"
        className="flex min-w-0 flex-col gap-3 rounded-xl border p-4"
      >
        <Skeleton className="h-5 w-36" />
        {[0, 1, 2].map((index) => (
          <div key={index} className="flex min-w-0 items-center gap-3">
            <Skeleton className="h-4 w-28 shrink-0" />
            <Skeleton className="h-6 min-w-0 flex-1" />
          </div>
        ))}
      </div>
      <div
        data-testid="detail-skeleton-chart"
        className="flex min-w-0 flex-col gap-3 rounded-xl border p-4"
      >
        <div className="flex items-center justify-between gap-3">
          <Skeleton className="h-5 w-32" />
          <Skeleton className="h-8 w-40" />
        </div>
        <Skeleton className="h-64 w-full" />
      </div>
    </div>
  );
}

function WindowControl({ window, currentSearch }: {
  window: MarketplaceUsageWindow;
  currentSearch: string;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const changeWindow = (next: string) => {
    if (!DETAIL_WINDOWS.includes(next as MarketplaceUsageWindow)) return;
    const search = new URLSearchParams(currentSearch);
    search.set("window", next);
    replaceCurrentPageSearch(search.toString());
  };
  return (
    <ToggleGroup
      type="single"
      value={window}
      onValueChange={changeWindow}
      variant="outline"
      size="sm"
      aria-label={t("windowLabel")}
    >
      {DETAIL_WINDOWS.map((value) => (
        <ToggleGroupItem key={value} value={value} aria-label={t(`window.${value}`)}>
          {t(`window.${value}`)}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}

function DetailLoadError({ retry }: { retry: () => unknown }) {
  const t = useTranslations("modelMarketplace.detail");
  const root = useTranslations("modelMarketplace");
  return (
    <Alert variant="destructive">
      <AlertCircle aria-hidden="true" />
      <AlertTitle>{t("detailLoadErrorTitle")}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-3">
        <p>{t("detailLoadErrorDescription")}</p>
        <Button variant="outline" size="sm" onClick={() => retry()}>
          <RefreshCw data-icon="inline-start" />
          {root("retry")}
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function TokenRequiredState() {
  const t = useTranslations("modelMarketplace.detail");
  const root = useTranslations("modelMarketplace");
  return (
    <PageLayout title={root("title")} description={root("description")}>
      <Empty className="border">
        <EmptyHeader>
          <EmptyTitle>{t("detailTokenRequiredTitle")}</EmptyTitle>
          <EmptyDescription>{t("detailTokenRequiredDescription")}</EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button asChild variant="outline">
            <Link href="/model-marketplace">
              <ArrowLeft data-icon="inline-start" />
              {t("backToCatalog")}
            </Link>
          </Button>
        </EmptyContent>
      </Empty>
    </PageLayout>
  );
}

function DetailActions({
  window,
  currentSearch,
  adminGlobal,
}: {
  window: MarketplaceUsageWindow;
  currentSearch: string;
  adminGlobal: boolean;
}) {
  const root = useTranslations("modelMarketplace");
  return (
    <>
      {adminGlobal ? <Badge variant="outline">{root("adminGlobalView")}</Badge> : null}
      <WindowControl window={window} currentSearch={currentSearch} />
    </>
  );
}

function MarketplaceStatusBadge({ status }: { status: MarketplaceHealthStatus }) {
  const t = useTranslations("modelMarketplace");
  return (
    <Badge variant="outline" className={MARKETPLACE_STATUS_PRESENTATION[status].badge}>
      {t(`status.${status}`)}
    </Badge>
  );
}

function EndpointSummary({ offers }: { offers: readonly MarketplaceModelOfferDetail[] }) {
  const t = useTranslations("modelMarketplace");
  const endpoints = [
    ...INVOCATION_ENDPOINTS.filter((endpoint) =>
      offers.some((offer) => offer.supported_endpoints.includes(endpoint)),
    ),
    ...(offers.some((offer) => offer.supported_endpoints.includes("models"))
      ? ["models" as const]
      : []),
  ];

  return (
    <span className="inline-flex min-w-0 flex-wrap items-center gap-1">
      <span className="sr-only">{t("endpointsLabel")}</span>
      {endpoints.length > 0 ? endpoints.map((endpoint) => (
        <Badge key={endpoint} variant="outline">{t(`endpoint.${endpoint}`)}</Badge>
      )) : <span>—</span>}
    </span>
  );
}

function MarketplaceModelHeader({ model }: {
  model: Extract<DetailResponse["model"], { kind: "real" }>["real"];
}) {
  const t = useTranslations("modelMarketplace.detail");
  const displayName = model.metadata.display_name || model.model_name;

  return (
    <TooltipProvider delayDuration={100}>
      <div data-testid="marketplace-model-header" className="flex min-w-0 flex-col gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <ModelProviderLogo modelName={model.model_name} size={24} />
          <span className="truncate text-display">{displayName}</span>
          <MarketplaceStatusBadge status={model.aggregate_status} />
        </div>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-meta text-muted-foreground">
          <ModelName name={model.model_name} />
          <span className="inline-flex items-center gap-1">
            <span>{t("contextLengthLabel")}</span>
            <TokensCell tokens={model.metadata.context_length} />
          </span>
          <span className="inline-flex items-center gap-1">
            <span>{t("maxOutputTokensLabel")}</span>
            <TokensCell tokens={model.metadata.max_output_tokens} />
          </span>
          <EndpointSummary offers={model.offers} />
        </div>
      </div>
    </TooltipProvider>
  );
}

type DetailResponse =
  | ModelMarketplaceDetailResponse
  | AdminModelMarketplaceDetailResponse;

function projectAdminOfferDiagnostics(
  response: AdminModelMarketplaceDetailResponse,
): Record<string, AdminOfferDiagnosticsView | undefined> | undefined {
  if (response.model.kind !== "real") return undefined;
  return Object.fromEntries(response.model.real.offers.map((offer) => [
    offer.offer_ref,
    {
      publicDisplayName: offer.diagnostics.public_display_name,
      internalName: offer.diagnostics.internal_name,
      baseUrl: offer.diagnostics.base_url,
      channelId: offer.diagnostics.channel_id,
      privateChannelId: offer.diagnostics.private_channel_id,
      ownerId: offer.diagnostics.owner_id,
      endpointPaths: (offer.diagnostics.endpoint_paths ?? []).map(({ endpoint, path }) => ({
        endpoint,
        path,
      })),
      disabledReasons: [...(offer.diagnostics.disabled_reasons ?? [])],
      requestCount: offer.performance.request_count,
      successCount: offer.performance.success_count,
      failureCount: offer.performance.failure_count,
      streamRequestCount: offer.performance.stream_request_count,
      ttftSampleCount: offer.performance.ttft_sample_count,
      tpsSampleCount: offer.performance.tps_sample_count,
      durationSampleCount: offer.performance.duration_sample_count,
    },
  ]));
}

function projectAdminRoutingDiagnostics(
  response: AdminModelMarketplaceDetailResponse,
): AdminRoutingDefinitionDiagnosticsView[] | undefined {
  if (response.model.kind !== "routing") return undefined;
  const definitions = response.model.routing.diagnostics.definitions;
  return definitions.map((definition) => ({
    occurrenceId: definition.occurrence_id,
    path: definition.path.map((step) => ({
      ref: step.ref,
      routingId: step.routing_id,
    })),
    routingId: definition.routing_id,
    name: definition.name,
    scope: definition.scope,
    userId: definition.user_id,
    tokenId: definition.token_id,
    enabled: definition.enabled,
    members: definition.members.map((member) => ({
      type: member.kind,
      name: member.ref,
      modelName: member.model_name,
      routingId: member.routing_id,
      priority: member.priority,
      weight: member.weight,
    })),
  }));
}

function RealModelDetail({
  response,
  currentSearch,
  isAdmin,
}: {
  response: DetailResponse;
  currentSearch: string;
  isAdmin: boolean;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const root = useTranslations("modelMarketplace");
  if (response.model.kind !== "real") return null;
  const model = response.model.real;
  const offers: MarketplaceModelOfferDetail[] = model.offers;
  const adminGlobal = "view" in response && response.view.mode === "global";
  const adminDiagnostics = isAdmin && "view" in response
    ? projectAdminOfferDiagnostics(response)
    : undefined;

  return (
    <PageLayout
      title={root("title")}
      description={model.metadata.description || t("realModelDescription")}
      maxWidth="full"
      actions={
        <DetailActions
          window={response.window}
          currentSearch={currentSearch}
          adminGlobal={adminGlobal}
        />
      }
    >
      <div className="flex min-w-0 flex-col gap-6">
        <MarketplaceModelHeader model={model} />
        <OfferComparison
          offers={offers}
          window={response.window}
          usageStatus={response.usage_status}
          adminDiagnostics={adminDiagnostics}
        />
      </div>
    </PageLayout>
  );
}

function RoutingModelDetail({
  response,
  params,
  currentSearch,
  isAdmin,
}: {
  response: DetailResponse;
  params: ModelMarketplaceDetailParams;
  currentSearch: string;
  isAdmin: boolean;
}) {
  const t = useTranslations("modelMarketplace.detail");
  const root = useTranslations("modelMarketplace");
  if (response.model.kind !== "routing") return null;
  const model = response.model.routing;
  const adminGlobal = "view" in response && response.view.mode === "global";
  const adminDiagnostics = isAdmin && "view" in response
    ? projectAdminRoutingDiagnostics(response)
    : undefined;
  return (
    <PageLayout
      title={model.display_name || model.model_name}
      description={model.model_name}
      actions={
        <DetailActions
          window={response.window}
          currentSearch={currentSearch}
          adminGlobal={adminGlobal}
        />
      }
    >
      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Route aria-hidden="true" />
              {root("kind.routing")}
            </CardTitle>
            <CardDescription>{t("routingDetailGuidance")}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <Alert>
              <AlertTitle>{t("routingNoSyntheticFactsTitle")}</AlertTitle>
              <AlertDescription>{t("routingNoSyntheticFactsDescription")}</AlertDescription>
            </Alert>
            <div className="flex flex-wrap gap-2">
              {model.reachable_real_models.map((realModel) => (
                <Button key={realModel} asChild variant="outline" size="sm">
                  <Link href={marketplaceDetailHref({
                    model: realModel,
                    tokenId: params.tokenId,
                    window: response.window,
                  })}>
                    {realModel}
                  </Link>
                </Button>
              ))}
            </div>
            {model.reachable_real_models.length === 0 ? (
              <p className="text-sm text-muted-foreground">{root("routingNoReachableModels")}</p>
            ) : null}
          </CardContent>
        </Card>
        {adminDiagnostics ? (
          <AdminRoutingDiagnostics definitions={adminDiagnostics} />
        ) : null}
      </div>
    </PageLayout>
  );
}

function DetailSuccess({
  response,
  params,
  currentSearch,
  isAdmin,
}: {
  response: DetailResponse;
  params: ModelMarketplaceDetailParams;
  currentSearch: string;
  isAdmin: boolean;
}) {
  if (response.model.kind === "real") {
    return <RealModelDetail response={response} currentSearch={currentSearch} isAdmin={isAdmin} />;
  }
  return (
    <RoutingModelDetail
      response={response}
      params={params}
      currentSearch={currentSearch}
      isAdmin={isAdmin}
    />
  );
}

interface DetailQueryProps {
  params: ModelMarketplaceDetailParams;
  viewerId: number;
  currentSearch: string;
}

function UserDetailQuery({ params, viewerId, currentSearch }: DetailQueryProps) {
  const query = useModelMarketplaceDetail(params, viewerId);
  if (query.isLoading) return <DetailSkeleton />;
  if (query.isError) return <DetailLoadError retry={query.refetch} />;
  if (!query.data) return <DetailSkeleton />;
  return (
    <DetailSuccess
      response={query.data}
      params={params}
      currentSearch={currentSearch}
      isAdmin={false}
    />
  );
}

function AdminDetailQuery({ params, viewerId, currentSearch }: DetailQueryProps) {
  const query = useAdminModelMarketplaceDetail(params, viewerId);
  if (query.isLoading) return <DetailSkeleton />;
  if (query.isError) return <DetailLoadError retry={query.refetch} />;
  if (!query.data) return <DetailSkeleton />;
  return (
    <DetailSuccess
      response={query.data}
      params={params}
      currentSearch={currentSearch}
      isAdmin
    />
  );
}

function MarketplaceDetailContent({ viewerId, isAdmin }: {
  viewerId: number;
  isAdmin: boolean;
}) {
  const searchParams = useSearchParams();
  const currentSearch = searchParams.toString();
  const model = searchParams.get("model")?.trim() ?? "";
  if (!model) return notFound();
  const tokenId = parsePositiveTokenId(searchParams.get("token_id"));
  if (!isAdmin && tokenId === undefined) return <TokenRequiredState />;
  const params: ModelMarketplaceDetailParams = {
    model,
    tokenId,
    window: parseWindow(searchParams.get("window")),
    offerRef: searchParams.get("offer_ref")?.trim() || undefined,
  };
  return isAdmin
    ? <AdminDetailQuery params={params} viewerId={viewerId} currentSearch={currentSearch} />
    : <UserDetailQuery params={params} viewerId={viewerId} currentSearch={currentSearch} />;
}

function OrdinaryUserDetailGate({ viewerId }: { viewerId: number }) {
  const capabilities = useCapabilities(viewerId);
  if (capabilities.isLoading) return <DetailSkeleton />;
  if (capabilities.isError || !isModelMarketplaceVisible(capabilities.data, false)) {
    return notFound();
  }
  return <MarketplaceDetailContent viewerId={viewerId} isAdmin={false} />;
}

function MarketplaceDetailAccessBoundary() {
  const { user, loading, isAdmin } = useAuth();
  if (loading) return <DetailSkeleton />;
  if (!user) return notFound();
  return isAdmin
    ? <MarketplaceDetailContent key={`${user.user_id}:admin`} viewerId={user.user_id} isAdmin />
    : <OrdinaryUserDetailGate key={`${user.user_id}:user`} viewerId={user.user_id} />;
}

export default function ModelMarketplaceDetailPage() {
  return (
    <Suspense fallback={<DetailSkeleton />}>
      <MarketplaceDetailAccessBoundary />
    </Suspense>
  );
}

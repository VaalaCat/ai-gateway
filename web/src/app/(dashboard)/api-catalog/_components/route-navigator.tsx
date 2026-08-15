"use client";

import { useTranslations } from "next-intl";

import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { SearchableSelect } from "@/components/ui/searchable-select";
import { Skeleton } from "@/components/ui/skeleton";
import type { APICatalogRoute } from "@/lib/api/api-access";

export interface RouteNavigatorProps {
  items: APICatalogRoute[];
  selectedID?: number;
  search: string;
  loading: boolean;
  error?: unknown;
  hasMore: boolean;
  onSelect: (id: number) => void;
  onSearchChange: (search: string) => void;
  onLoadMore: () => void;
  onRetry: () => void;
}

export function RouteNavigator({
  items,
  selectedID,
  search,
  loading,
  error,
  hasMore,
  onSelect,
  onSearchChange,
  onLoadMore,
  onRetry,
}: RouteNavigatorProps) {
  const t = useTranslations("apiCatalog");
  const tc = useTranslations("common");

  if (loading && items.length === 0) return <Skeleton className="h-28 w-full" />;
  if (error && items.length === 0) return (
    <Alert variant="destructive">
      <AlertTitle>{t("routesLoadFailed")}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-2">
        <span>{t("loadFailedDescription")}</span>
        <Button type="button" variant="outline" size="sm" onClick={onRetry}>{tc("retry")}</Button>
      </AlertDescription>
    </Alert>
  );
  if (items.length === 0) return <Empty className="min-h-28"><EmptyHeader><EmptyTitle>{t("emptyRoutes")}</EmptyTitle></EmptyHeader></Empty>;

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="md:hidden">
        <SearchableSelect
          value={selectedID ? String(selectedID) : ""}
          onChange={(value) => onSelect(Number(value))}
          ariaLabel={t("routes")}
          placeholder={t("routes")}
          searchPlaceholder={t("routes")}
          emptyText={t("emptyRoutes")}
          items={items.map((route) => ({ value: String(route.id), label: route.slug }))}
          remoteSearch={{ value: search, onCommit: onSearchChange }}
          loading={loading}
        />
      </div>
      <div className="hidden md:block">
        <FilterableToolbar
          spec={{
            search: {
              kind: "text",
              label: t("routeLabel"),
              placeholder: t("searchPlaceholder"),
              debounceMs: 300,
            },
          }}
          value={{ search }}
          onChange={(next) => onSearchChange(typeof next.search === "string" ? next.search : "")}
        />
      </div>
      <nav className="hidden min-w-0 grid-cols-2 gap-2 md:grid xl:grid-cols-3" aria-label={t("routes")}>
        {items.map((route) => {
          const selected = route.id === selectedID;
          return (
            <Button
              key={route.id}
              type="button"
              variant={selected ? "secondary" : "outline"}
              aria-label={route.slug}
              aria-current={selected ? "true" : undefined}
              className="h-auto min-w-0 justify-start px-3 py-2 text-left"
              onClick={() => onSelect(route.id)}
            >
              <span className="flex min-w-0 flex-col items-start gap-1">
                <span className="w-full truncate font-mono font-medium">{route.slug}</span>
                <span className="flex flex-wrap gap-1">
                  {route.protocols.map((protocol) => <Badge key={protocol} variant="outline">{protocol}</Badge>)}
                </span>
              </span>
            </Button>
          );
        })}
      </nav>
      {loading ? <Skeleton className="h-9 w-full" /> : null}
      {error ? (
        <Alert variant="destructive">
          <AlertTitle>{t("routesLoadFailed")}</AlertTitle>
          <AlertDescription>{t("loadFailedDescription")}</AlertDescription>
        </Alert>
      ) : null}
      {hasMore ? (
        <Button type="button" variant="outline" size="sm" disabled={loading} onClick={error ? onRetry : onLoadMore}>
          {t("loadMoreRoutes")}
        </Button>
      ) : null}
    </div>
  );
}

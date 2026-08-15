"use client";

import { useTranslations } from "next-intl";

import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { SearchableSelect } from "@/components/ui/searchable-select";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { APICatalogService } from "@/lib/api/api-access";

export interface ServiceNavigatorProps {
  items: APICatalogService[];
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

export function ServiceNavigator({
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
}: ServiceNavigatorProps) {
  const t = useTranslations("apiCatalog");
  const tc = useTranslations("common");
  const appendedError = error && items.length > 0;

  return (
    <aside className="min-w-0 lg:border-r lg:pr-4" aria-label={t("serviceDetails")}>
      <div className="flex flex-col gap-2 lg:hidden">
        {loading && items.length === 0 ? (
          <Skeleton className="h-8 w-full" />
        ) : (
          <SearchableSelect
            value={selectedID ? String(selectedID) : ""}
            onChange={(value) => onSelect(Number(value))}
            ariaLabel={t("serviceDetails")}
            placeholder={t("serviceDetails")}
            searchPlaceholder={t("title")}
            emptyText={t("emptyTitle")}
            items={items.map((service) => ({ value: String(service.id), label: service.name }))}
            remoteSearch={{ value: search, onCommit: onSearchChange }}
            loading={loading}
          />
        )}
        {appendedError ? <AppendError title={t("loadFailed")} description={t("loadFailedDescription")} /> : null}
        {hasMore ? (
          <Button type="button" variant="outline" size="sm" disabled={loading} onClick={error ? onRetry : onLoadMore}>
            {t("loadMoreServices")}
          </Button>
        ) : null}
      </div>

      <div className="hidden flex-col gap-3 lg:flex">
        <FilterableToolbar
          spec={{
            search: {
              kind: "text",
              label: t("serviceLabel"),
              placeholder: t("searchPlaceholder"),
              debounceMs: 300,
            },
          }}
          value={{ search }}
          onChange={(next) => onSearchChange(typeof next.search === "string" ? next.search : "")}
        />
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold tracking-tight">{t("serviceDetails")}</h2>
          <span className="text-xs tabular-nums text-muted-foreground">{items.length}</span>
        </div>
        <nav className="flex flex-col gap-1" aria-label={t("serviceDetails")}>
          {items.map((service) => {
            const selected = service.id === selectedID;
            return (
              <Button
                key={service.id}
                type="button"
                variant={selected ? "secondary" : "ghost"}
                aria-label={service.name}
                aria-current={selected ? "true" : undefined}
                className="h-auto min-w-0 justify-start px-3 py-2 text-left"
                onClick={() => onSelect(service.id)}
              >
                <span className="flex min-w-0 flex-col items-start gap-0.5">
                  <span className="w-full truncate font-medium">{service.name}</span>
                  <span className="w-full truncate font-mono text-xs text-muted-foreground">{service.slug}</span>
                </span>
              </Button>
            );
          })}
        </nav>
        {loading && items.length > 0 ? <Skeleton className="h-9 w-full" /> : null}
        {appendedError ? <AppendError title={t("loadFailed")} description={t("loadFailedDescription")} /> : null}
        {hasMore ? (
          <Button type="button" variant="outline" size="sm" disabled={loading} onClick={error ? onRetry : onLoadMore}>
            {t("loadMoreServices")}
          </Button>
        ) : null}
        {error && items.length === 0 ? (
          <Button type="button" variant="outline" size="sm" onClick={onRetry}>{tc("retry")}</Button>
        ) : null}
      </div>
    </aside>
  );
}

function AppendError({ title, description }: { title: string; description: string }) {
  return (
    <Alert variant="destructive">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{description}</AlertDescription>
    </Alert>
  );
}

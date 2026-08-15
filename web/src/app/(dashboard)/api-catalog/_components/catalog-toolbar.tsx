"use client";

import { useTranslations } from "next-intl";

import { EntityPicker } from "@/components/business/entity-picker/entity-picker";
import { Button } from "@/components/ui/button";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { SearchableSelect } from "@/components/ui/searchable-select";
import type { APICatalogRoute, APICatalogService } from "@/lib/api/api-access";

interface TokenPickerProps {
  id: string;
  label: string;
  tokenID: number;
  onTokenChange: (tokenID: number) => void;
  onTokenClear: () => void;
}

export function CatalogTokenPicker({
  id,
  label,
  tokenID,
  onTokenChange,
  onTokenClear,
}: TokenPickerProps) {
  const t = useTranslations("apiCatalog");
  return (
    <Field className="w-full max-w-full sm:w-72">
      <FieldLabel htmlFor={id} className="sr-only">{label}</FieldLabel>
      <EntityPicker
        id={id}
        entity="usable-token"
        defaultAdminScope="all"
        value={tokenID ? String(tokenID) : ""}
        onChange={(value) => {
          if (value) onTokenChange(Number(value));
          else onTokenClear();
        }}
        placeholder={t("tokenPlaceholder")}
        size="sm"
      />
    </Field>
  );
}

export interface CatalogToolbarProps {
  services: APICatalogService[];
  routes: APICatalogRoute[];
  serviceID?: number;
  routeID?: number;
  selectedServiceLabel?: string;
  selectedRouteLabel?: string;
  serviceSearch: string;
  routeSearch: string;
  servicesLoading: boolean;
  servicesError?: unknown;
  servicesHaveMore: boolean;
  routesLoading: boolean;
  routesError?: unknown;
  routesHaveMore: boolean;
  onServiceChange: (serviceID: number) => void;
  onRouteChange: (routeID: number) => void;
  onServiceSearch: (search: string) => void;
  onRouteSearch: (search: string) => void;
  onLoadMoreServices: () => void;
  onLoadMoreRoutes: () => void;
  onRetryServices: () => void;
  onRetryRoutes: () => void;
}

export function CatalogToolbar({
  services,
  routes,
  serviceID,
  routeID,
  selectedServiceLabel,
  selectedRouteLabel,
  serviceSearch,
  routeSearch,
  servicesLoading,
  servicesError,
  servicesHaveMore,
  routesLoading,
  routesError,
  routesHaveMore,
  onServiceChange,
  onRouteChange,
  onServiceSearch,
  onRouteSearch,
  onLoadMoreServices,
  onLoadMoreRoutes,
  onRetryServices,
  onRetryRoutes,
}: CatalogToolbarProps) {
  const t = useTranslations("apiCatalog");

  return (
    <FieldGroup
      data-testid="catalog-mobile-toolbar"
      className="flex flex-col gap-3 rounded-lg border border-border bg-card p-3 lg:hidden"
    >
      <Field>
        <FieldLabel>{t("mobileServicePicker")}</FieldLabel>
        <SearchableSelect
          value={serviceID ? String(serviceID) : ""}
          onChange={(value) => onServiceChange(Number(value) || 0)}
          ariaLabel={t("mobileServicePicker")}
          placeholder={t("servicePlaceholder")}
          searchPlaceholder={t("searchPlaceholder")}
          emptyText={t("emptyState")}
          items={services.map((service) => ({ value: String(service.id), label: service.name }))}
          selectedLabel={selectedServiceLabel}
          loading={servicesLoading}
          remoteSearch={{ value: serviceSearch, onCommit: onServiceSearch }}
        />
        {servicesError ? <p className="text-sm text-destructive">{t("errorState")}</p> : null}
        {servicesHaveMore || servicesError ? (
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" disabled={servicesLoading} onClick={servicesError ? onRetryServices : onLoadMoreServices}>
              {t(servicesError ? "retry" : "loadMoreServices")}
            </Button>
          </div>
        ) : null}
      </Field>
      <Field>
        <FieldLabel>{t("mobileRoutePicker")}</FieldLabel>
        <SearchableSelect
          value={routeID ? String(routeID) : ""}
          onChange={(value) => onRouteChange(Number(value) || 0)}
          ariaLabel={t("mobileRoutePicker")}
          placeholder={t("routePlaceholder")}
          searchPlaceholder={t("searchPlaceholder")}
          emptyText={t("emptyRoutes")}
          items={routes.map((route) => ({ value: String(route.id), label: route.slug }))}
          selectedLabel={selectedRouteLabel}
          loading={routesLoading}
          remoteSearch={{ value: routeSearch, onCommit: onRouteSearch }}
        />
        {routesError ? <p className="text-sm text-destructive">{t("errorState")}</p> : null}
        {routesHaveMore || routesError ? (
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" disabled={routesLoading} onClick={routesError ? onRetryRoutes : onLoadMoreRoutes}>
              {t(routesError ? "retry" : "loadMoreRoutes")}
            </Button>
          </div>
        ) : null}
      </Field>
    </FieldGroup>
  );
}

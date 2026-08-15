"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { Plus } from "lucide-react";
import { toast } from "sonner";

import { BackgroundRefreshStatus } from "@/components/business/background-refresh-status";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { canCreateAPIService, canManageAPIService, useCapabilities } from "@/lib/api/capabilities";
import { useAPIServices, useDeleteAPIService, useUpdateAPIService, type APIService } from "@/lib/api/api-services";
import { useAuth } from "@/lib/auth";
import { PAGE_SIZES } from "@/lib/constants";

import { DeleteConfirmationDialog } from "./delete-confirmation-dialog";
import { useServiceColumns } from "./_components/service-columns";

function errorStatus(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error
    ? (error as { status?: number }).status
    : undefined;
}

function PageError({ error }: { error: unknown }) {
  const t = useTranslations("apiServices");
  const key = errorStatus(error) === 403 ? "permissionDenied" : "loadFailed";
  return <Alert variant="destructive"><AlertTitle>{t(key)}</AlertTitle><AlertDescription>{t(`${key}Description`)}</AlertDescription></Alert>;
}

function PageSkeleton() {
  return <div className="flex flex-col gap-4"><Skeleton className="h-8 w-48" /><Skeleton className="h-10 w-full" /><Skeleton className="h-64 w-full" /></div>;
}

export default function APIServicesPage() {
  const t = useTranslations("apiServices");
  const tc = useTranslations("common");
  const { user } = useAuth();
  const capability = useCapabilities(user?.user_id);
  const patchSearchParams = useSearchParamPatch();
  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.DEFAULT, { patchSearchParams });
  const filterSpec = useMemo(() => ({
    search: { kind: "text", label: t("nameOrSlug"), debounceMs: 300 },
    status: { kind: "enum", label: t("status"), options: [
      { value: "1", label: t("enabled") },
      { value: "0", label: t("disabled") },
    ] },
  }) satisfies FilterSpec, [t]);
  const [filters, setFilters] = useFilterState(filterSpec, { patchSearchParams });
  const rawStatus = filters.status === undefined ? "" : String(filters.status);
  const status = rawStatus === "0" ? 0 : rawStatus === "1" ? 1 : undefined;
  const invalidStatus = rawStatus !== "" && status === undefined;
  const servicesEnabled = capability.data?.generic_api?.services === true;
  const query = useAPIServices({
    page,
    page_size: pageSize,
    ...(filters.search ? { search: String(filters.search) } : {}),
    ...(status !== undefined ? { status } : {}),
  }, { enabled: servicesEnabled });
  const update = useUpdateAPIService();
  const remove = useDeleteAPIService();
  const [deleteTarget, setDeleteTarget] = useState<APIService | null>(null);
  const actions = capability.data?.generic_api?.service_actions;
  const hasManageActions = actions?.manage_all === true || (actions?.manage_ids?.length ?? 0) > 0;
  const columns = useServiceColumns({
    includeActions: hasManageActions,
    canManage: (service) => canManageAPIService(capability.data, service.id),
    onToggleStatus: async (service) => { await update.mutateAsync({ id: service.id, status: service.status === 1 ? 0 : 1 }); toast.success(tc("success")); },
    onDelete: setDeleteTarget,
  });
  const total = query.data?.total ?? 0;

  useEffect(() => {
    if (invalidStatus) patchSearchParams({ status: undefined, page: undefined });
  }, [invalidStatus, patchSearchParams]);

  useEffect(() => {
    if (query.data === undefined || query.isLoading || query.data.data.length > 0) return;
    const lastPage = Math.max(1, Math.ceil(total / pageSize));
    if (page > lastPage) setPagination(lastPage, pageSize);
  }, [page, pageSize, query.data, query.isLoading, setPagination, total]);

  return (
    <PageLayout title={t("title")} description={t("description")} maxWidth="full">
      {capability.isPending || capability.isLoading ? <PageSkeleton /> : capability.error ? (
        <PageError error={capability.error} />
      ) : !servicesEnabled ? (
        <Alert><AlertTitle>{t("unavailable")}</AlertTitle><AlertDescription>{t("permissionRequired")}</AlertDescription></Alert>
      ) : query.error ? (
        <PageError error={query.error} />
      ) : (
        <>
          <DataTable
            columns={columns}
            data={query.data?.data ?? []}
            loading={query.isLoading}
            total={total}
            page={page}
            pageSize={pageSize}
            pageCount={Math.max(1, Math.ceil(total / pageSize))}
            onPaginationChange={(nextPage, nextSize) => setPagination(nextSize === pageSize ? nextPage : 1, nextSize)}
            getRowId={(row) => String(row.id)}
            storageKey="api-services-columns"
            toolbar={
              <FilterableToolbar
                spec={filterSpec}
                value={filters}
                onChange={setFilters}
                secondaryContent={<BackgroundRefreshStatus refreshing={query.isFetching && !query.isLoading} label={t("refreshingServices")} />}
                primaryAction={canCreateAPIService(capability.data) ? (
                  <Button asChild size="sm"><Link href="/api-services/new"><Plus data-icon="inline-start" />{t("createService")}</Link></Button>
                ) : undefined}
              />
            }
          />
          {deleteTarget ? (
            <DeleteConfirmationDialog
              open
              onOpenChange={(open) => { if (!open) setDeleteTarget(null); }}
              subject={deleteTarget.name}
              onConfirm={async () => { await remove.mutateAsync(deleteTarget.id); setDeleteTarget(null); }}
            />
          ) : null}
        </>
      )}
    </PageLayout>
  );
}

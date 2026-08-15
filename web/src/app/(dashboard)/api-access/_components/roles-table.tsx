"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { Pencil, Plus, Trash2 } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";

import { PermissionScopeBadge } from "@/components/business/api-badges";
import { BackgroundRefreshStatus } from "@/components/business/background-refresh-status";
import { StatusBadge } from "@/components/business/status-badge";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAPIRoles, useDeleteAPIRole, type APIRole } from "@/lib/api/api-access";
import { PAGE_SIZES } from "@/lib/constants";

import { DeleteConfirmationDialog } from "./delete-confirmation-dialog";
import { isProtectedAPIRole } from "./role-protection";

function errorStatus(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined;
}
function formatTimestamp(value: number | undefined) {
  if (!value) return "-";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value * 1000));
}

function PermissionSummary({ role }: { role: APIRole }) {
  const t = useTranslations("apiAccess");
  if (role.permissions.length === 0) return <span className="text-sm text-muted-foreground">{t("noPermissions")}</span>;
  return <div className="flex flex-wrap gap-1">{role.permissions.map((permission) => <Badge key={`${permission.resource}:${permission.resource_id}:${permission.action}`} variant="outline">{t(`resourceOptions.${permission.resource}`)} · {t(`actionOptions.${permission.action}`)} <PermissionScopeBadge resourceId={permission.resource_id} /></Badge>)}</div>;
}

export function RolesTable({ enabled }: { enabled: boolean }) {
  const t = useTranslations("apiAccess");
  const patchSearchParams = useSearchParamPatch();
  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.DEFAULT, { patchSearchParams });
  const filterSpec = useMemo(() => ({
    search: { kind: "text", label: t("searchRoles"), debounceMs: 300 },
    status: { kind: "enum", label: t("status"), options: [{ value: "1", label: t("enabled") }, { value: "0", label: t("disabled") }] },
  }) satisfies FilterSpec, [t]);
  const [filters, setFilters] = useFilterState(filterSpec, { patchSearchParams });
  const status = filters.status === "0" ? 0 : filters.status === "1" ? 1 : undefined;
  const query = useAPIRoles({ page, page_size: pageSize, ...(filters.search ? { search: String(filters.search) } : {}), ...(status === undefined ? {} : { status }) }, { enabled });
  const remove = useDeleteAPIRole();
  const [deleteTarget, setDeleteTarget] = useState<APIRole>();
  const total = query.data?.total ?? 0;

  useEffect(() => {
    if (!enabled || query.isLoading || (query.data?.data.length ?? 0) > 0) return;
    const lastPage = Math.max(1, Math.ceil(total / pageSize));
    if (page > lastPage) setPagination(lastPage, pageSize);
  }, [enabled, page, pageSize, query.data?.data.length, query.isLoading, setPagination, total]);

  const columns = useMemo<ColumnDef<APIRole>[]>(() => [
    { accessorKey: "name", header: ({ column }) => <DataTableColumnHeader column={column} title={t("name")} />, cell: ({ row }) => <div className="flex min-w-44 flex-col gap-1"><span className="font-medium">{row.original.name}</span><span className="font-mono text-xs text-muted-foreground">{row.original.key}</span></div> },
    { accessorKey: "status", header: ({ column }) => <DataTableColumnHeader column={column} title={t("status")} />, cell: ({ row }) => <div className="flex flex-wrap gap-1"><StatusBadge status={row.original.status} />{isProtectedAPIRole(row.original) ? <Badge variant="secondary">{t("builtIn")}</Badge> : null}</div> },
    { id: "permissions", header: t("permissions"), cell: ({ row }) => <PermissionSummary role={row.original} /> },
    { accessorKey: "updated_at", header: ({ column }) => <DataTableColumnHeader column={column} title={t("updatedAt")} />, cell: ({ row }) => <span className="tabular-nums text-muted-foreground">{formatTimestamp(row.original.updated_at)}</span> },
    { id: "actions", header: () => <div className="text-right">{t("actions")}</div>, cell: ({ row }) => isProtectedAPIRole(row.original) ? null : <div className="flex justify-end gap-1"><Button asChild size="icon-sm" variant="ghost" aria-label={t("editRole")}><Link href={`/api-access/roles/edit?id=${row.original.id}`}><Pencil /></Link></Button><Button size="icon-sm" variant="ghost" aria-label={t("deleteRole")} onClick={() => setDeleteTarget(row.original)}><Trash2 /></Button></div> },
  ], [t]);

  if (query.error) {
    const key = errorStatus(query.error) === 403 ? "permissionDenied" : "loadFailed";
    return <div role="alert">{t(key)}</div>;
  }
  return <>
    <DataTable columns={columns} data={query.data?.data ?? []} loading={query.isLoading} total={total} page={page} pageSize={pageSize} pageCount={Math.max(1, Math.ceil(total / pageSize))} onPaginationChange={(nextPage, nextSize) => setPagination(nextSize === pageSize ? nextPage : 1, nextSize)} getRowId={(role) => String(role.id)} storageKey="api-access-roles-columns" toolbar={<FilterableToolbar spec={filterSpec} value={filters} onChange={setFilters} secondaryContent={<BackgroundRefreshStatus refreshing={query.isFetching && !query.isLoading} label={t("refreshingRoles")} />} primaryAction={<Button asChild size="sm"><Link href="/api-access/roles/new"><Plus data-icon="inline-start" />{t("createRole")}</Link></Button>} />} />
    {deleteTarget ? <DeleteConfirmationDialog open onOpenChange={(open) => { if (!open) setDeleteTarget(undefined); }} subject={deleteTarget.name} onConfirm={async () => { await remove.mutateAsync(deleteTarget.id); setDeleteTarget(undefined); }} /> : null}
  </>;
}

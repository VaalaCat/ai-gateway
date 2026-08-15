"use client";

import { useEffect, useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { Pencil, Plus, Trash2 } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";

import { BackgroundRefreshStatus } from "@/components/business/background-refresh-status";
import { EntityLabel } from "@/components/business/entity-label";
import type { EntityName } from "@/components/business/entity-picker/registry";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAPIRoleBindings, useDeleteAPIRoleBinding, type APIPrincipalType, type APIRoleBinding } from "@/lib/api/api-access";
import { PAGE_SIZES } from "@/lib/constants";

import { BindingDialog } from "./binding-dialog";
import { DeleteConfirmationDialog } from "./delete-confirmation-dialog";

const principalEntities: Record<APIPrincipalType, EntityName> = { user: "user", user_group: "user-group", token: "token" };
const principalTypes: APIPrincipalType[] = ["user", "user_group", "token"];

function errorStatus(error: unknown) {
  return typeof error === "object" && error !== null && "status" in error ? (error as { status?: number }).status : undefined;
}
function formatTimestamp(value: number | undefined) {
  if (!value) return "-";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value * 1000));
}

export function BindingsTable({ enabled }: { enabled: boolean }) {
  const t = useTranslations("apiAccess");
  const patchSearchParams = useSearchParamPatch();
  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.DEFAULT, { patchSearchParams });
  const [dialogBinding, setDialogBinding] = useState<APIRoleBinding | null | undefined>();
  const [deleteTarget, setDeleteTarget] = useState<APIRoleBinding>();
  const filterSpec = useMemo(() => ({
    principal_type: { kind: "enum", label: t("principalType"), options: principalTypes.map((value) => ({ value, label: t(`principalTypeOptions.${value}`) })) },
    principal_id: { kind: "picker", entity: "user" as EntityName, label: t("principal"), visible: (context) => Boolean(context.principalType), pickerQuery: () => ({}) },
    role_id: { kind: "picker", entity: "api-role" as EntityName, label: t("role"), advanced: true },
  }) satisfies FilterSpec, [t]);
  const [filters, setFilters] = useFilterState(filterSpec, { patchSearchParams });
  const selectedPrincipalType = principalTypes.includes(filters.principal_type as APIPrincipalType) ? filters.principal_type as APIPrincipalType : undefined;
  const bindingFilterSpec = useMemo(() => ({
    ...filterSpec,
    principal_id: { ...filterSpec.principal_id, entity: (selectedPrincipalType ? principalEntities[selectedPrincipalType] : "user") as EntityName, visible: () => Boolean(selectedPrincipalType) },
  }) satisfies FilterSpec, [filterSpec, selectedPrincipalType]);
  const onFiltersChange = (next: Record<string, string | number | undefined>) => {
    if (Object.hasOwn(next, "principal_type")) setFilters({ ...next, principal_id: undefined, page: undefined });
    else setFilters(next);
  };
  const principalID = Number(filters.principal_id);
  const roleID = Number(filters.role_id);
  const query = useAPIRoleBindings({ page, page_size: pageSize, ...(selectedPrincipalType ? { principal_type: selectedPrincipalType } : {}), ...(Number.isSafeInteger(principalID) && principalID > 0 ? { principal_id: principalID } : {}), ...(Number.isSafeInteger(roleID) && roleID > 0 ? { role_id: roleID } : {}) }, { enabled });
  const remove = useDeleteAPIRoleBinding();
  const total = query.data?.total ?? 0;

  useEffect(() => {
    if (!enabled || query.isLoading || (query.data?.data.length ?? 0) > 0) return;
    const lastPage = Math.max(1, Math.ceil(total / pageSize));
    if (page > lastPage) setPagination(lastPage, pageSize);
  }, [enabled, page, pageSize, query.data?.data.length, query.isLoading, setPagination, total]);

  const columns = useMemo<ColumnDef<APIRoleBinding>[]>(() => [
    { accessorKey: "principal_type", header: ({ column }) => <DataTableColumnHeader column={column} title={t("principal")} />, cell: ({ row }) => <div className="flex items-center gap-2"><Badge variant="outline">{t(`principalTypeOptions.${row.original.principal_type}`)}</Badge><EntityLabel entity={principalEntities[row.original.principal_type]} id={row.original.principal_id} /></div> },
    { accessorKey: "role_id", header: ({ column }) => <DataTableColumnHeader column={column} title={t("role")} />, cell: ({ row }) => <EntityLabel entity="api-role" id={row.original.role_id} /> },
    { accessorKey: "created_at", header: ({ column }) => <DataTableColumnHeader column={column} title={t("createdAt")} />, cell: ({ row }) => <span className="tabular-nums text-muted-foreground">{formatTimestamp(row.original.created_at)}</span> },
    { id: "actions", header: () => <div className="text-right">{t("actions")}</div>, cell: ({ row }) => <div className="flex justify-end gap-1"><Button size="icon-sm" variant="ghost" aria-label={t("editBinding")} onClick={() => setDialogBinding(row.original)}><Pencil /></Button><Button size="icon-sm" variant="ghost" aria-label={t("deleteBinding")} onClick={() => setDeleteTarget(row.original)}><Trash2 /></Button></div> },
  ], [t]);
  if (query.error) return <div role="alert">{t(errorStatus(query.error) === 403 ? "permissionDenied" : "loadFailed")}</div>;
  return <>
    <DataTable columns={columns} data={query.data?.data ?? []} loading={query.isLoading} total={total} page={page} pageSize={pageSize} pageCount={Math.max(1, Math.ceil(total / pageSize))} onPaginationChange={(nextPage, nextSize) => setPagination(nextSize === pageSize ? nextPage : 1, nextSize)} getRowId={(binding) => String(binding.id)} storageKey="api-access-bindings-columns" toolbar={<FilterableToolbar spec={bindingFilterSpec} value={filters} onChange={onFiltersChange} secondaryContent={<BackgroundRefreshStatus refreshing={query.isFetching && !query.isLoading} label={t("refreshingBindings")} />} primaryAction={<Button size="sm" onClick={() => setDialogBinding(null)}><Plus data-icon="inline-start" />{t("createBinding")}</Button>} />} />
    {dialogBinding !== undefined ? <BindingDialog open onOpenChange={(open) => { if (!open) setDialogBinding(undefined); }} binding={dialogBinding} /> : null}
    {deleteTarget ? <DeleteConfirmationDialog open onOpenChange={(open) => { if (!open) setDeleteTarget(undefined); }} subject={`${deleteTarget.principal_type}:${deleteTarget.principal_id}`} onConfirm={async () => { await remove.mutateAsync(deleteTarget.id); setDeleteTarget(undefined); }} /> : null}
  </>;
}

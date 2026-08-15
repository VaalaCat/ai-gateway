"use client";

import { useMemo, useState } from "react";
import { useTranslations } from "next-intl";
import { Pencil, Plus, Trash2 } from "lucide-react";
import type { ColumnDef } from "@tanstack/react-table";

import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { useSearchParamPatch } from "@/components/data-table/use-search-param-patch";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAPIAccessGrants, useDeleteAPIAccessGrant, type APIAccessGrant } from "@/lib/api/api-access";
import { PAGE_SIZES } from "@/lib/constants";

import { AccessGrantDialog } from "./access-grant-dialog";

export function AccessGrantsTable({ enabled }: { enabled: boolean }) {
  const t = useTranslations("apiAccess");
  const patchSearchParams = useSearchParamPatch();
  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.DEFAULT, { patchSearchParams });
  const filters = useMemo(() => ({ search: { kind: "text", label: t("searchGrants"), debounceMs: 300 } }) satisfies FilterSpec, [t]);
  const [filterValues, setFilterValues] = useFilterState(filters, { patchSearchParams });
  const grants = useAPIAccessGrants({ page, page_size: pageSize, ...(filterValues.search ? { search: String(filterValues.search) } : {}) }, { enabled });
  const remove = useDeleteAPIAccessGrant();
  const [editing, setEditing] = useState<APIAccessGrant | null | undefined>();
  const [deleting, setDeleting] = useState<APIAccessGrant>();
  const [deleteError, setDeleteError] = useState<string>();
  const confirmDelete = async () => {
    if (!deleting) return;
    setDeleteError(undefined);
    try {
      await remove.mutateAsync(deleting);
      setDeleting(undefined);
    } catch (reason) {
      setDeleteError(reason instanceof Error ? reason.message : t("deleteFailed"));
    }
  };
  const columns = useMemo<ColumnDef<APIAccessGrant>[]>(() => [
    { id: "principal", header: ({ column }) => <DataTableColumnHeader column={column} title={t("principal")} />, cell: ({ row }) => row.original.principal_label },
    { id: "service", header: ({ column }) => <DataTableColumnHeader column={column} title={t("service")} />, cell: ({ row }) => row.original.api_service_name },
    { id: "scope", header: t("scope"), cell: ({ row }) => <Badge variant="outline">{(row.original.configured ?? row.original.effective).scope === "service" ? t("serviceScope") : t("routeScope")}</Badge> },
    { id: "sources", header: t("sources"), cell: ({ row }) => <div className="flex flex-wrap gap-1">{row.original.sources.map((source) => <Badge key={source} variant="secondary">{t(`sourceOptions.${source}`)}</Badge>)}</div> },
    { id: "actions", header: () => <div className="text-right">{t("actions")}</div>, cell: ({ row }) => row.original.configured ? <div className="flex justify-end gap-1"><Button size="icon-sm" variant="ghost" aria-label={t("editGrant")} onClick={() => setEditing(row.original)}><Pencil /></Button><Button size="icon-sm" variant="ghost" aria-label={t("deleteGrant")} onClick={() => setDeleting(row.original)}><Trash2 /></Button></div> : null },
  ], [t]);
  if (grants.error) return <div role="alert">{t("loadFailed")}</div>;
  return <><DataTable columns={columns} data={grants.data?.data ?? []} loading={grants.isLoading} total={grants.data?.total ?? 0} page={page} pageSize={pageSize} pageCount={Math.max(1, Math.ceil((grants.data?.total ?? 0) / pageSize))} onPaginationChange={(nextPage, nextSize) => setPagination(nextSize === pageSize ? nextPage : 1, nextSize)} getRowId={(row) => `${row.principal_type}:${row.principal_id}:${row.api_service_id}`} storageKey="api-access-grants-columns" toolbar={<FilterableToolbar spec={filters} value={filterValues} onChange={setFilterValues} primaryAction={<Button size="sm" onClick={() => setEditing(null)}><Plus data-icon="inline-start" />{t("createGrant")}</Button>} />} />{editing !== undefined ? <AccessGrantDialog open onOpenChange={(open) => { if (!open) setEditing(undefined); }} grant={editing ?? undefined} /> : null}{deleting ? <AlertDialog open onOpenChange={(open) => { if (!open) { setDeleting(undefined); setDeleteError(undefined); } }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("deleteGrant")}</AlertDialogTitle><AlertDialogDescription>{t("deleteGrantDescription")}</AlertDialogDescription><p className="text-sm text-muted-foreground">{deleting.sources.filter((source) => source !== "managed").map((source) => t(`sourceOptions.${source}`)).join(", ") || t("noOtherSources")}</p></AlertDialogHeader>{deleteError ? <Alert variant="destructive"><AlertTitle>{t("deleteFailed")}</AlertTitle><AlertDescription>{deleteError}</AlertDescription></Alert> : null}<AlertDialogFooter><AlertDialogCancel disabled={remove.isPending}>{t("cancel")}</AlertDialogCancel><AlertDialogAction variant="destructive" disabled={remove.isPending} onClick={(event) => { event.preventDefault(); void confirmDelete(); }}>{t("confirmDelete")}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog> : null}</>;
}

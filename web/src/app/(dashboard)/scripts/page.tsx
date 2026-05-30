"use client";

import { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { FileCode, MoreHorizontal, Pencil, Trash2 } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";
import { ScopeBadge } from "@/components/script/scope-badge";
import { useScripts, useUpdateScript, useDeleteScript } from "@/lib/api/scripts";
import { formatErrorToast } from "@/lib/api/error-toast";
import { PAGE_SIZES } from "@/lib/constants";
import type { AdminScript } from "@/lib/types";

function detectHooks(code: string): string[] {
  const hooks: string[] = [];
  if (/(?:function\s+onRequest\s*\(|(?:const|let|var)\s+onRequest\s*=)/.test(code))
    hooks.push("onRequest");
  if (/(?:function\s+onUpstreamRequest\s*\(|(?:const|let|var)\s+onUpstreamRequest\s*=)/.test(code))
    hooks.push("onUpstreamRequest");
  return hooks;
}

export default function ScriptsPage() {
  const t = useTranslations("scripts");
  const tc = useTranslations("common");
  const router = useRouter();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(
    () => ({ search: { kind: "text", placeholder: t("searchName") } } satisfies FilterSpec),
    [t],
  );
  const [filterValues, setFilterValuesRaw] = useFilterState(filterSpec);
  const setFilterValues = (next: Parameters<typeof setFilterValuesRaw>[0]) => {
    setPage(1);
    setFilterValuesRaw(next);
  };
  const search = filterValues.search ? String(filterValues.search) : undefined;

  const { data, isLoading } = useScripts({ page, page_size: pageSize, ...(search ? { search } : {}) });
  const updateMut = useUpdateScript();
  const deleteMut = useDeleteScript();
  const [deleteItem, setDeleteItem] = useState<AdminScript | null>(null);

  const scripts = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const handleToggle = (row: AdminScript, enabled: boolean) => {
    updateMut.mutate(
      { id: row.id, enabled },
      { onError: (e) => toast.error(formatErrorToast(e, tc("error"))) },
    );
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMut.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    } finally {
      setDeleteItem(null);
    }
  };

  const columns: ColumnDef<AdminScript>[] = [
    {
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("name")} />,
      cell: ({ row }) => (
        <button
          className="text-left text-sm font-medium hover:underline"
          onClick={() => router.push(`/scripts/edit?id=${row.original.id}`)}
        >
          {row.original.name}
        </button>
      ),
    },
    { id: "scope", header: t("scope"), cell: ({ row }) => <ScopeBadge scope={row.original.scope} /> },
    {
      id: "hooks",
      header: t("hooks"),
      cell: ({ row }) => (
        <div className="flex flex-wrap gap-1">
          {detectHooks(row.original.code).map((h) => (
            <Badge key={h} variant="secondary" className="text-xs">{h}</Badge>
          ))}
        </div>
      ),
    },
    {
      accessorKey: "priority",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("priority")} />,
      cell: ({ row }) => <span className="text-sm tabular-nums text-muted-foreground">{row.original.priority}</span>,
    },
    {
      accessorKey: "enabled",
      header: t("status"),
      cell: ({ row }) => (
        <Switch
          checked={row.original.enabled}
          onCheckedChange={(v) => handleToggle(row.original, v)}
        />
      ),
    },
    {
      accessorKey: "updated_at",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("updated")} />,
      cell: ({ row }) => <DateCell timestamp={row.original.updated_at} relative />,
    },
    {
      id: "actions",
      header: tc("actions"),
      cell: ({ row }) => (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-8">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => router.push(`/scripts/edit?id=${row.original.id}`)}>
              <Pencil className="mr-2 size-4" />
              {t("edit")}
            </DropdownMenuItem>
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onClick={() => setDeleteItem(row.original)}
            >
              <Trash2 className="mr-2 size-4" />
              {t("delete")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ];

  const isEmpty = !isLoading && scripts.length === 0 && !search;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t("title")}</h1>
          <p className="mt-1 text-muted-foreground">{t("description")}</p>
        </div>
        <Button onClick={() => router.push("/scripts/new")}>{t("create")}</Button>
      </div>

      {isEmpty ? (
        <div className="flex flex-col items-center justify-center gap-4 py-24 text-center">
          <FileCode className="size-12 text-muted-foreground" />
          <div>
            <p className="text-lg font-semibold">{t("empty")}</p>
            <p className="mt-1 text-muted-foreground">{t("emptyHint")}</p>
          </div>
          <Button onClick={() => router.push("/scripts/new")}>{t("create")}</Button>
        </div>
      ) : (
        <DataTable
          columns={columns}
          data={scripts}
          loading={isLoading}
          total={total}
          page={page}
          pageSize={pageSize}
          pageCount={pageCount}
          onPaginationChange={handlePaginationChange}
          toolbar={<FilterableToolbar spec={filterSpec} value={filterValues} onChange={setFilterValues} />}
        />
      )}

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
        description={t("deleteConfirm")}
      />
    </div>
  );
}

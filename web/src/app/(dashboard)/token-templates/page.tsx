"use client";

import { useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { MoreHorizontal, Plus, RefreshCw } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { EntityChipList } from "@/components/business/entity-chip-list";
import { StatusBadge } from "@/components/business/status-badge";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";
import { TokenTemplateSyncDialog } from "@/components/business/token-template-sync-dialog";
import { TokenTemplateFormDialog, type TokenTemplateFormValues } from "@/components/business/token-template-form-dialog";
import { formatErrorToast } from "@/lib/api/error-toast";

import {
  useTokenTemplates,
  useCreateTokenTemplate,
  useUpdateTokenTemplate,
  useDeleteTokenTemplate,
} from "@/lib/api/token-templates";
import { useResponsiveColumnVisibility } from "@/hooks/use-responsive-column-visibility";
import { PAGE_SIZES } from "@/lib/constants";
import { parseModels } from "@/lib/parse-models";
import type { TokenTemplate } from "@/lib/types";

export default function TokenTemplatesPage() {
  const t = useTranslations("tokenTemplates");
  const tc = useTranslations("common");

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
    status: {
      kind: "enum",
      options: [
        { value: "1", label: t("statusEnabled") },
        { value: "0", label: t("statusDisabled") },
      ],
      placeholder: t("filterByStatus"),
    },
  } satisfies FilterSpec), [t, tc]);

  const visConfig = useMemo(
    () => ({ storageKey: "token-templates", hiddenOnMobile: ["allowed_groups", "models", "created_at"] as const }),
    [],
  );
  const [colVis, setColVis] = useResponsiveColumnVisibility(visConfig);

  const [filterValues, setFilterValuesRaw] = useFilterState(filterSpec);

  const setFilterValues = (next: Parameters<typeof setFilterValuesRaw>[0]) => {
    setPage(1);
    setFilterValuesRaw(next);
  };

  const { data, isLoading } = useTokenTemplates({
    page,
    pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.status ? { status: String(filterValues.status) } : {}),
  });

  const templates = data?.data ?? [];
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

  const createMutation = useCreateTokenTemplate();
  const updateMutation = useUpdateTokenTemplate();
  const deleteMutation = useDeleteTokenTemplate();

  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<TokenTemplate | null>(null);
  const [deleteItem, setDeleteItem] = useState<TokenTemplate | null>(null);
  const [syncItem, setSyncItem] = useState<TokenTemplate | null>(null);

  const handleCreate = async (values: TokenTemplateFormValues) => {
    try {
      await createMutation.mutateAsync({
        name: values.name,
        models: values.models,
        expiry_days: Number(values.expiry_days),
        status: Number(values.status),
        allowed_channel_ids: values.allowed_channel_ids.length > 0 ? values.allowed_channel_ids : undefined,
        allowed_group_ids: values.allowed_group_ids.length > 0 ? values.allowed_group_ids : undefined,
      });
      toast.success(tc("success"));
      setCreateOpen(false);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleEdit = async (values: TokenTemplateFormValues) => {
    if (!editItem) return;
    try {
      await updateMutation.mutateAsync({
        id: editItem.id,
        name: values.name,
        models: values.models,
        expiry_days: Number(values.expiry_days),
        status: Number(values.status),
        allowed_channel_ids: values.allowed_channel_ids,
        allowed_group_ids: values.allowed_group_ids,
      });
      toast.success(tc("success"));
      setEditItem(null);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMutation.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
      setDeleteItem(null);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const columns: ColumnDef<TokenTemplate>[] = [
    {
      accessorKey: "id",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("id")} />,
    },
    {
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("name")} />,
    },
    {
      accessorKey: "models",
      header: t("models"),
      cell: ({ row }) => {
        const models = parseModels(row.original.models);
        return (
          <span className="max-w-[300px] truncate block font-mono text-xs">
            {models.length > 0 ? models.join(", ") : "-"}
          </span>
        );
      },
    },
    {
      accessorKey: "expiry_days",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("expiryDays")} />,
      cell: ({ row }) =>
        row.original.expiry_days === -1 ? t("noExpiry") : `${row.original.expiry_days}`,
    },
    {
      id: "allowed_groups",
      header: t("allowedGroups"),
      cell: ({ row }) => (
        <EntityChipList
          entity="user-group"
          ids={row.original.allowed_group_ids ?? []}
          emptyLabel={t("allGroups")}
        />
      ),
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "created_at",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("createdAt")} />,
      cell: ({ row }) => <DateCell timestamp={row.original.created_at} />,
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
            <DropdownMenuItem onClick={() => setEditItem(row.original)}>
              {tc("edit")}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setSyncItem(row.original)}>
              <RefreshCw className="mr-2 size-4" />
              {t("sync.menu")}
            </DropdownMenuItem>
            <DropdownMenuItem
              className="text-destructive"
              onClick={() => setDeleteItem(row.original)}
            >
              {tc("delete")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground mt-1">{t("description")}</p>
      </div>

      <DataTable
        columns={columns}
        data={templates}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        columnVisibilityState={colVis}
        onColumnVisibilityChange={setColVis}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            primaryAction={
              <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus className="mr-2 size-4" />
                {t("create")}
              </Button>
            }
          />
        }
      />

      <TokenTemplateFormDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        onSubmit={handleCreate}
        pending={createMutation.isPending}
      />
      <TokenTemplateFormDialog
        open={!!editItem}
        onOpenChange={(o) => { if (!o) setEditItem(null); }}
        template={editItem}
        onSubmit={handleEdit}
        pending={updateMutation.isPending}
      />

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
      />

      <TokenTemplateSyncDialog
        template={syncItem}
        onOpenChange={(open) => { if (!open) setSyncItem(null); }}
      />
    </div>
  );
}

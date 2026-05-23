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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { TagInput } from "@/components/ui/tag-input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { StatusBadge } from "@/components/business/status-badge";
import { StatusSelect } from "@/components/business/status-select";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";
import { ChannelMultiSelect } from "@/components/business/channel-multi-select";
import { TokenTemplateSyncDialog } from "@/components/business/token-template-sync-dialog";
import { formatErrorToast } from "@/lib/api/error-toast";

import {
  useTokenTemplates,
  useCreateTokenTemplate,
  useUpdateTokenTemplate,
  useDeleteTokenTemplate,
} from "@/lib/api/token-templates";
import { PAGE_SIZES } from "@/lib/constants";
import { parseModels, serializeModels } from "@/lib/parse-models";
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

  const [createForm, setCreateForm] = useState({ name: "", models: "", expiry_days: "-1", status: "1", allowed_channel_ids: [] as number[] });
  const [editForm, setEditForm] = useState({ name: "", models: "", expiry_days: "-1", status: "1", allowed_channel_ids: [] as number[] });

  const handleCreate = async () => {
    try {
      await createMutation.mutateAsync({
        name: createForm.name,
        models: createForm.models,
        expiry_days: Number(createForm.expiry_days),
        status: Number(createForm.status),
        allowed_channel_ids: createForm.allowed_channel_ids.length > 0 ? createForm.allowed_channel_ids : undefined,
      });
      toast.success(tc("success"));
      setCreateOpen(false);
      setCreateForm({ name: "", models: "", expiry_days: "-1", status: "1", allowed_channel_ids: [] });
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleEdit = async () => {
    if (!editItem) return;
    try {
      await updateMutation.mutateAsync({
        id: editItem.id,
        name: editForm.name,
        models: editForm.models,
        expiry_days: Number(editForm.expiry_days),
        status: Number(editForm.status),
        allowed_channel_ids: editForm.allowed_channel_ids,
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

  const openEdit = (tpl: TokenTemplate) => {
    setEditForm({
      name: tpl.name,
      models: tpl.models ?? "",
      expiry_days: String(tpl.expiry_days),
      status: String(tpl.status),
      allowed_channel_ids: tpl.allowed_channel_ids ?? [],
    });
    setEditItem(tpl);
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
            <DropdownMenuItem onClick={() => openEdit(row.original)}>
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
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            primaryAction={
              <Button size="sm" onClick={() => {
                setCreateForm({ name: "", models: "", expiry_days: "-1", status: "1", allowed_channel_ids: [] });
                setCreateOpen(true);
              }}>
                <Plus className="mr-2 size-4" />
                {t("create")}
              </Button>
            }
          />
        }
      />

      {/* Create Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("create")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label>{t("name")}</Label>
              <Input
                value={createForm.name}
                onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("models")}</Label>
              <TagInput
                value={parseModels(createForm.models)}
                onChange={(tags) => setCreateForm({ ...createForm, models: serializeModels(tags) })}
                placeholder={t("modelsPlaceholder")}
              />
              <p className="text-xs text-muted-foreground">{t("modelsHint")}</p>
            </div>
            <div className="space-y-2">
              <Label>{t("allowedChannels")}</Label>
              <ChannelMultiSelect
                value={createForm.allowed_channel_ids}
                onChange={(ids) => setCreateForm({ ...createForm, allowed_channel_ids: ids })}
                placeholder={t("allowedChannelsPlaceholder")}
              />
              <p className="text-xs text-muted-foreground">{t("allowedChannelsEmptyHint")}</p>
            </div>
            <div className="space-y-2">
              <Label>{t("expiryDays")}</Label>
              <Input
                type="number"
                value={createForm.expiry_days}
                onChange={(e) => setCreateForm({ ...createForm, expiry_days: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">{t("expiryDaysHint")}</p>
            </div>
            <StatusSelect value={createForm.status} onChange={(v) => setCreateForm({ ...createForm, status: v })} />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>{tc("cancel")}</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending}>{tc("save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Dialog */}
      <Dialog open={!!editItem} onOpenChange={(open) => { if (!open) setEditItem(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("edit")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label>{t("name")}</Label>
              <Input
                value={editForm.name}
                onChange={(e) => setEditForm({ ...editForm, name: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("models")}</Label>
              <TagInput
                value={parseModels(editForm.models)}
                onChange={(tags) => setEditForm({ ...editForm, models: serializeModels(tags) })}
                placeholder={t("modelsPlaceholder")}
              />
              <p className="text-xs text-muted-foreground">{t("modelsHint")}</p>
            </div>
            <div className="space-y-2">
              <Label>{t("allowedChannels")}</Label>
              <ChannelMultiSelect
                value={editForm.allowed_channel_ids}
                onChange={(ids) => setEditForm({ ...editForm, allowed_channel_ids: ids })}
                placeholder={t("allowedChannelsPlaceholder")}
              />
              <p className="text-xs text-muted-foreground">{t("allowedChannelsEmptyHint")}</p>
            </div>
            <div className="space-y-2">
              <Label>{t("expiryDays")}</Label>
              <Input
                type="number"
                value={editForm.expiry_days}
                onChange={(e) => setEditForm({ ...editForm, expiry_days: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">{t("expiryDaysHint")}</p>
            </div>
            <StatusSelect value={editForm.status} onChange={(v) => setEditForm({ ...editForm, status: v })} />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditItem(null)}>{tc("cancel")}</Button>
            <Button onClick={handleEdit} disabled={updateMutation.isPending}>{tc("save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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

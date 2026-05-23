"use client";

import { useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { MoreHorizontal, RefreshCw, DollarSign, Copy } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import { PricingPreviewDialog } from "@/components/business/pricing-preview-dialog";

import { useIsMobile } from "@/hooks/use-mobile";
import {
  useModels,
  useUpdateModel,
  useDeleteModel,
  useSyncModels,
  useFetchPricing,
  useApplyPricing,
  type FetchPricingResponse,
} from "@/lib/api/models";
import { PAGE_SIZES } from "@/lib/constants";
import { formatPrice } from "@/lib/utils/format";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { ModelConfig } from "@/lib/types";

// --- Helpers ---

function PriceDisplay({ price }: { price: number }) {
  if (!price || price === 0) return <span className="text-muted-foreground">-</span>;
  return <span className="tabular-nums text-sm">{formatPrice(price)}</span>;
}

function ModelNameCell({ name }: { name: string }) {
  return (
    <div className="flex items-center gap-1 group max-w-[220px]">
      <span className="font-mono text-xs truncate" title={name}>{name}</span>
      <button
        className="opacity-0 group-hover:opacity-60 hover:!opacity-100 transition-opacity shrink-0"
        onClick={(e) => { e.stopPropagation(); navigator.clipboard.writeText(name); toast.success("Copied"); }}
      >
        <Copy className="size-3" />
      </button>
    </div>
  );
}

// --- Page ---

export default function ModelsPage() {
  const t = useTranslations("models");
  const tc = useTranslations("common");
  const isMobile = useIsMobile();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
    price_filter: {
      kind: "enum",
      options: [
        { value: "all", label: t("priceFilterAll") },
        { value: "no_price", label: t("priceFilterNone") },
        { value: "has_price", label: t("priceFilterSet") },
      ],
      includeAll: false,
      placeholder: t("priceFilterAll"),
    },
  } satisfies FilterSpec), [t, tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useModels({
    page,
    page_size: pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.price_filter && filterValues.price_filter !== "all"
      ? { price_filter: String(filterValues.price_filter) }
      : {}),
  });
  const models = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) { setPage(1); setPageSize(newPageSize); } else { setPage(newPage); }
  };

  const updateMutation = useUpdateModel();
  const deleteMutation = useDeleteModel();
  const syncMutation = useSyncModels();
  const fetchPricingMutation = useFetchPricing();
  const applyPricingMutation = useApplyPricing();

  const [editItem, setEditItem] = useState<ModelConfig | null>(null);
  const [deleteItem, setDeleteItem] = useState<ModelConfig | null>(null);
  const [pricingData, setPricingData] = useState<FetchPricingResponse | null>(null);

  const [editForm, setEditForm] = useState({
    model_name: "", input_price: "", output_price: "",
    cache_read_price: "", cache_write_price: "", status: "1",
  });

  const handleEdit = async () => {
    if (!editItem) return;
    try {
      await updateMutation.mutateAsync({
        id: editItem.id,
        model_name: editForm.model_name,
        input_price: Number(editForm.input_price),
        output_price: Number(editForm.output_price),
        cache_read_price: Number(editForm.cache_read_price),
        cache_write_price: Number(editForm.cache_write_price),
        status: Number(editForm.status),
      });
      toast.success(tc("success"));
      setEditItem(null);
    } catch (e) { toast.error(formatErrorToast(e, tc("error"))); }
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMutation.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
      setDeleteItem(null);
    } catch (e) { toast.error(formatErrorToast(e, tc("error"))); }
  };

  const openEdit = (model: ModelConfig) => {
    setEditForm({
      model_name: model.model_name,
      input_price: String(model.input_price),
      output_price: String(model.output_price),
      cache_read_price: String(model.cache_read_price ?? 0),
      cache_write_price: String(model.cache_write_price ?? 0),
      status: String(model.status),
    });
    setEditItem(model);
  };

  const handleFetchPricing = async (source?: string) => {
    try {
      const result = await fetchPricingMutation.mutateAsync(source ? { source } : undefined);
      if ((result.matches ?? []).length === 0) { toast.info(t("noMatches")); return; }
      setPricingData(result);
    } catch (e) { toast.error(formatErrorToast(e, t("fetchFailed"))); }
  };

  const handleApplyPricing = async (updates: Array<{
    model_id: number; input_price: number; output_price: number;
    cache_read_price: number; cache_write_price: number;
  }>) => {
    try {
      const result = await applyPricingMutation.mutateAsync({ updates });
      toast.success(t("pricingApplied", { count: result.updated }));
      setPricingData(null);
    } catch (e) { toast.error(formatErrorToast(e, tc("error"))); }
  };

  // --- Columns ---

  const columns: ColumnDef<ModelConfig>[] = [
    {
      accessorKey: "model_name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("modelName")} />,
      cell: ({ row }) => <ModelNameCell name={row.original.model_name} />,
    },
    {
      accessorKey: "input_price",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("inputPrice")} />,
      cell: ({ row }) => <PriceDisplay price={row.original.input_price} />,
    },
    {
      accessorKey: "output_price",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("outputPrice")} />,
      cell: ({ row }) => <PriceDisplay price={row.original.output_price} />,
    },
    {
      accessorKey: "cache_read_price",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("cacheReadPrice")} />,
      cell: ({ row }) => <PriceDisplay price={row.original.cache_read_price ?? 0} />,
    },
    {
      accessorKey: "cache_write_price",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("cacheWritePrice")} />,
      cell: ({ row }) => <PriceDisplay price={row.original.cache_write_price ?? 0} />,
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "updated_at",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("updatedAt")} />,
      cell: ({ row }) => <DateCell timestamp={row.original.updated_at} />,
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
            <DropdownMenuItem onClick={() => openEdit(row.original)}>{tc("edit")}</DropdownMenuItem>
            <DropdownMenuItem className="text-destructive" onClick={() => setDeleteItem(row.original)}>{tc("delete")}</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ];

  // Mobile: hide less important columns by default
  const defaultColumnVisibility = {
    cache_read_price: false,
    cache_write_price: false,
    status: !isMobile,
    updated_at: !isMobile,
  };

  // --- Toolbar ---

  const toolbarActions = (
    <div className="flex items-center gap-2">
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" disabled={fetchPricingMutation.isPending}>
            <DollarSign className={`mr-1.5 size-3.5 ${fetchPricingMutation.isPending ? "animate-pulse" : ""}`} />
            {fetchPricingMutation.isPending ? t("fetching") : t("fetchPricing")}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuItem onClick={() => handleFetchPricing()}>
            {t("filterAll")} (basellm + models.dev)
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => handleFetchPricing("basellm")}>basellm</DropdownMenuItem>
          <DropdownMenuItem onClick={() => handleFetchPricing("models.dev")}>models.dev</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Button
        variant="outline"
        size="sm"
        onClick={async () => {
          try {
            const result = await syncMutation.mutateAsync();
            toast.success(t("syncSuccess", { count: result.created }));
          } catch (e) { toast.error(formatErrorToast(e, tc("error"))); }
        }}
        disabled={syncMutation.isPending}
      >
        <RefreshCw className={`mr-1.5 size-3.5 ${syncMutation.isPending ? "animate-spin" : ""}`} />
        {t("syncFromChannels")}
      </Button>
    </div>
  );

  const toolbar = (
    <FilterableToolbar
      spec={filterSpec}
      value={filterValues}
      onChange={setFilterValues}
      primaryAction={toolbarActions}
    />
  );

  // --- Render ---

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground text-sm mt-0.5">{t("description")}</p>
      </div>

      <DataTable
        columns={columns}
        data={models}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        defaultColumnVisibility={defaultColumnVisibility}
        storageKey="models"
        toolbar={toolbar}
      />

      {/* Edit Dialog */}
      <Dialog open={!!editItem} onOpenChange={(open) => { if (!open) setEditItem(null); }}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{tc("edit")}: {editForm.model_name}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label className="text-xs">{t("inputPrice")} ({t("priceUnit")})</Label>
                <Input type="number" step="0.001" value={editForm.input_price} onChange={(e) => setEditForm({ ...editForm, input_price: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">{t("outputPrice")} ({t("priceUnit")})</Label>
                <Input type="number" step="0.001" value={editForm.output_price} onChange={(e) => setEditForm({ ...editForm, output_price: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">{t("cacheReadPrice")} ({t("priceUnit")})</Label>
                <Input type="number" step="0.001" value={editForm.cache_read_price} onChange={(e) => setEditForm({ ...editForm, cache_read_price: e.target.value })} />
              </div>
              <div className="space-y-1.5">
                <Label className="text-xs">{t("cacheWritePrice")} ({t("priceUnit")})</Label>
                <Input type="number" step="0.001" value={editForm.cache_write_price} onChange={(e) => setEditForm({ ...editForm, cache_write_price: e.target.value })} />
              </div>
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

      {pricingData && (
        <PricingPreviewDialog
          open={!!pricingData}
          onOpenChange={(open) => { if (!open) setPricingData(null); }}
          data={pricingData}
          onApply={handleApplyPricing}
          isApplying={applyPricingMutation.isPending}
        />
      )}
    </div>
  );
}

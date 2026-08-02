"use client";

import { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
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
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
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

import { useIsMobile } from "@/hooks/use-mobile";
import {
  useModels,
  useUpdateModel,
  useDeleteModel,
  useSyncModels,
} from "@/lib/api/models";
import { PAGE_SIZES } from "@/lib/constants";
import { formatPrice } from "@/lib/utils/format";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { formatErrorToast } from "@/lib/api/error-toast";
import type {
  ModelConfig,
  ModelMetadata,
  ModelMetadataOverride,
} from "@/lib/types";

// --- Helpers ---

function PriceDisplay({ price }: { price: number }) {
  if (!price || price === 0) return <span className="text-muted-foreground">-</span>;
  return <span className="tabular-nums text-sm">{formatPrice(price)}</span>;
}

function ModelNameCell({ name }: { name: string }) {
  const tc = useTranslations("common");
  return (
    <div className="flex items-center gap-1 group max-w-[220px]">
      <span className="font-mono text-xs truncate" title={name}>{name}</span>
      <button
        className="opacity-0 group-hover:opacity-60 hover:!opacity-100 transition-opacity shrink-0"
        onClick={(e) => {
          e.stopPropagation();
          copyTextWithFeedback(name, { success: tc("copied"), error: tc("copyFailed") });
        }}
      >
        <Copy className="size-3" />
      </button>
    </div>
  );
}

type MetadataFieldName = keyof ModelMetadata;
type MetadataFieldKind = "text" | "number" | "list" | "boolean";
type MetadataFormValue = string | boolean;
type MetadataFormValues = Record<MetadataFieldName, MetadataFormValue>;
type MetadataOverridePresence = Record<MetadataFieldName, boolean>;

interface MetadataFieldDefinition {
  key: MetadataFieldName;
  kind: MetadataFieldKind;
  labelKey: string;
  overrideLabelKey: string;
  controlLabelKey: string;
}

const metadataFieldDefinitions: MetadataFieldDefinition[] = [
  {
    key: "display_name",
    kind: "text",
    labelKey: "metadataDisplayName",
    overrideLabelKey: "overrideDisplayName",
    controlLabelKey: "metadataDisplayName",
  },
  {
    key: "description",
    kind: "text",
    labelKey: "metadataDescription",
    overrideLabelKey: "overrideDescription",
    controlLabelKey: "metadataDescription",
  },
  {
    key: "provider",
    kind: "text",
    labelKey: "metadataProvider",
    overrideLabelKey: "overrideProvider",
    controlLabelKey: "metadataProvider",
  },
  {
    key: "input_modalities",
    kind: "list",
    labelKey: "metadataInputModalities",
    overrideLabelKey: "overrideInputModalities",
    controlLabelKey: "metadataInputModalities",
  },
  {
    key: "output_modalities",
    kind: "list",
    labelKey: "metadataOutputModalities",
    overrideLabelKey: "overrideOutputModalities",
    controlLabelKey: "metadataOutputModalities",
  },
  {
    key: "context_length",
    kind: "number",
    labelKey: "metadataContextLength",
    overrideLabelKey: "overrideContextLength",
    controlLabelKey: "metadataContextLength",
  },
  {
    key: "max_output_tokens",
    kind: "number",
    labelKey: "metadataMaxOutputTokens",
    overrideLabelKey: "overrideMaxOutputTokens",
    controlLabelKey: "metadataMaxOutputTokens",
  },
  {
    key: "supported_parameters",
    kind: "list",
    labelKey: "metadataSupportedParameters",
    overrideLabelKey: "overrideSupportedParameters",
    controlLabelKey: "metadataSupportedParameters",
  },
  {
    key: "tool_calling",
    kind: "boolean",
    labelKey: "metadataToolCalling",
    overrideLabelKey: "overrideToolCalling",
    controlLabelKey: "metadataToolCalling",
  },
  {
    key: "structured_output",
    kind: "boolean",
    labelKey: "metadataStructuredOutput",
    overrideLabelKey: "overrideStructuredOutput",
    controlLabelKey: "metadataStructuredOutput",
  },
  {
    key: "reasoning",
    kind: "boolean",
    labelKey: "metadataReasoning",
    overrideLabelKey: "overrideReasoning",
    controlLabelKey: "metadataReasoning",
  },
  {
    key: "prompt_cache",
    kind: "boolean",
    labelKey: "metadataPromptCache",
    overrideLabelKey: "overridePromptCache",
    controlLabelKey: "metadataPromptCache",
  },
];

const emptyModelMetadata: ModelMetadata = {
  display_name: "",
  description: "",
  provider: "",
  input_modalities: [],
  output_modalities: [],
  context_length: 0,
  max_output_tokens: 0,
  supported_parameters: [],
  tool_calling: false,
  structured_output: false,
  reasoning: false,
  prompt_cache: false,
};

const metadataValueStrategies: Record<
  MetadataFieldKind,
  {
    format: (
      value: ModelMetadata[MetadataFieldName] | undefined,
    ) => MetadataFormValue;
    parse: (value: MetadataFormValue) => ModelMetadata[MetadataFieldName];
  }
> = {
  text: {
    format: (value) => String(value ?? ""),
    parse: (value) => String(value),
  },
  number: {
    format: (value) => String(value ?? 0),
    parse: (value) => Number(value),
  },
  list: {
    format: (value) => (Array.isArray(value) ? value.join(", ") : ""),
    parse: (value) =>
      String(value)
        .split(",")
        .map((item) => item.trim())
        .filter(Boolean),
  },
  boolean: {
    format: (value) => Boolean(value),
    parse: (value) => Boolean(value),
  },
};

interface MetadataControlProps {
  id: string;
  label: string;
  enabled: boolean;
  value: MetadataFormValue;
  onChange: (value: MetadataFormValue) => void;
}

const metadataControlRenderers: Record<
  MetadataFieldKind,
  (props: MetadataControlProps) => React.ReactNode
> = {
  text: ({ id, label, enabled, value, onChange }) => (
    <Input
      id={id}
      aria-label={label}
      disabled={!enabled}
      value={String(value)}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
  number: ({ id, label, enabled, value, onChange }) => (
    <Input
      id={id}
      aria-label={label}
      disabled={!enabled}
      type="number"
      step={1}
      value={String(value)}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
  list: ({ id, label, enabled, value, onChange }) => (
    <Input
      id={id}
      aria-label={label}
      disabled={!enabled}
      value={String(value)}
      onChange={(event) => onChange(event.target.value)}
    />
  ),
  boolean: ({ id, label, enabled, value, onChange }) => (
    <Switch
      id={id}
      aria-label={label}
      disabled={!enabled}
      checked={Boolean(value)}
      onCheckedChange={onChange}
    />
  ),
};

function createMetadataEditState(
  synced: ModelMetadata,
  override: ModelMetadataOverride,
): {
  values: MetadataFormValues;
  overrides: MetadataOverridePresence;
} {
  const values = Object.fromEntries(
    metadataFieldDefinitions.map((definition) => {
      const isOverridden = Object.prototype.hasOwnProperty.call(
        override,
        definition.key,
      );
      const value = isOverridden
        ? override[definition.key]
        : synced[definition.key];
      return [
        definition.key,
        metadataValueStrategies[definition.kind].format(value),
      ];
    }),
  ) as MetadataFormValues;
  const overrides = Object.fromEntries(
    metadataFieldDefinitions.map((definition) => [
      definition.key,
      Object.prototype.hasOwnProperty.call(override, definition.key),
    ]),
  ) as MetadataOverridePresence;
  return { values, overrides };
}

function buildMetadataOverride(
  values: MetadataFormValues,
  overrides: MetadataOverridePresence,
): ModelMetadataOverride {
  return Object.fromEntries(
    metadataFieldDefinitions
      .filter((definition) => overrides[definition.key])
      .map((definition) => [
        definition.key,
        metadataValueStrategies[definition.kind].parse(values[definition.key]),
      ]),
  ) as ModelMetadataOverride;
}

const emptyMetadataEditState = createMetadataEditState(emptyModelMetadata, {});

// --- Page ---

export default function ModelsPage() {
  const t = useTranslations("models");
  const tc = useTranslations("common");
  const isMobile = useIsMobile();
  const router = useRouter();

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

  const [editItem, setEditItem] = useState<ModelConfig | null>(null);
  const [deleteItem, setDeleteItem] = useState<ModelConfig | null>(null);

  const [editForm, setEditForm] = useState({
    model_name: "", input_price: "", output_price: "",
    cache_read_price: "", cache_write_price: "", status: "1",
  });
  const [metadataValues, setMetadataValues] = useState<MetadataFormValues>(
    emptyMetadataEditState.values,
  );
  const [metadataOverrides, setMetadataOverrides] =
    useState<MetadataOverridePresence>(emptyMetadataEditState.overrides);

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
        metadata_override: buildMetadataOverride(metadataValues, metadataOverrides),
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
    const metadataEdit = createMetadataEditState(
      model.synced_metadata ?? emptyModelMetadata,
      model.metadata_override ?? {},
    );
    setMetadataValues(metadataEdit.values);
    setMetadataOverrides(metadataEdit.overrides);
    setEditItem(model);
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
          <Button variant="outline" size="sm">
            <DollarSign className="mr-1.5 size-3.5" />
            {t("pricingSyncTitle")}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start">
          <DropdownMenuItem onClick={() => router.push("/models/pricing-sync")}>
            {t("filterAll")} (basellm + models.dev)
          </DropdownMenuItem>
          <DropdownMenuItem onClick={() => router.push("/models/pricing-sync?source=basellm")}>basellm</DropdownMenuItem>
          <DropdownMenuItem onClick={() => router.push("/models/pricing-sync?source=models.dev")}>models.dev</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Button
        variant="outline"
        size="sm"
        onClick={async () => {
          try {
            const result = await syncMutation.mutateAsync();
            if (result.metadata_source_error) {
              toast.error(t("syncMetadataFailed"), {
                description: result.metadata_source_error,
              });
              return;
            }
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
        <DialogContent className="max-h-[90vh] max-w-2xl overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{tc("edit")}: {editForm.model_name}</DialogTitle>
            <DialogDescription>{t("editDescription")}</DialogDescription>
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
            <FieldSet>
              <FieldLegend>{t("metadataOverrides")}</FieldLegend>
              <FieldDescription>{t("metadataOverridesDesc")}</FieldDescription>
              <FieldGroup className="gap-3">
                {metadataFieldDefinitions.map((definition) => {
                  const overrideID = `override-${definition.key}`;
                  const controlID = `metadata-${definition.key}`;
                  const enabled = metadataOverrides[definition.key];
                  const renderControl = metadataControlRenderers[definition.kind];
                  return (
                    <Field key={definition.key} orientation="responsive" className="rounded-lg border p-3">
                      <FieldContent>
                        <div className="flex items-center gap-2">
                          <Checkbox
                            id={overrideID}
                            aria-label={t(definition.overrideLabelKey)}
                            checked={enabled}
                            onCheckedChange={(checked) => setMetadataOverrides((current) => ({
                              ...current,
                              [definition.key]: checked === true,
                            }))}
                          />
                          <FieldLabel htmlFor={controlID}>
                            {t(definition.labelKey)}
                          </FieldLabel>
                        </div>
                        <FieldDescription>
                          {enabled ? t("metadataOverrideEnabled") : t("metadataSyncedValue")}
                        </FieldDescription>
                      </FieldContent>
                      <div className="w-full @md/field-group:w-64">
                        {renderControl({
                          id: controlID,
                          label: t(definition.controlLabelKey),
                          enabled,
                          value: enabled
                            ? metadataValues[definition.key]
                            : metadataValueStrategies[definition.kind].format(
                                (editItem?.synced_metadata ?? emptyModelMetadata)[definition.key],
                              ),
                          onChange: (value) => setMetadataValues((current) => ({
                            ...current,
                            [definition.key]: value,
                          })),
                        })}
                      </div>
                    </Field>
                  );
                })}
              </FieldGroup>
            </FieldSet>
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
    </div>
  );
}

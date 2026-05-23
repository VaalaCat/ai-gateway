"use client";

import { Suspense, useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef, Row } from "@tanstack/react-table";
import { toast } from "sonner";
import { ChevronRight, MoreHorizontal } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { StatusBadge } from "@/components/business/status-badge";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";
import { UsernameCell } from "@/components/business/username-cell";
import { ExpandedModelsView } from "@/components/business/expanded-models-view";
import { ModelName } from "@/components/business/model-name";

import { parseEndpoints } from "@/components/channel/channel-form/utils";
import { groupModelsByProvider, PAGE_SIZES } from "@/lib/constants";
import { formatErrorToast } from "@/lib/api/error-toast";
import {
  type BYOKChannelDetail,
  useAdminBYOKChannels,
  useBYOKSupportedTypes,
  useDisableBYOKChannel,
} from "@/lib/api/byok-channels";

function AdminBYOKPageInner() {
  const t = useTranslations("byok.admin");
  const tByok = useTranslations("byok");
  const tc = useTranslations("common");

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);
  const [confirmDisableId, setConfirmDisableId] = useState<number | null>(null);

  const { data: typesData } = useBYOKSupportedTypes();
  const byokTypes = typesData?.types ?? [];

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tByok("searchNameOrModel") },
    owner_id: { kind: "picker", entity: "user", placeholder: t("filterByOwner") },
    type: {
      kind: "enum",
      options: byokTypes.map((bt) => ({ value: String(bt.id), label: bt.name })),
      placeholder: tByok("filterByType"),
    },
    status: {
      kind: "enum",
      options: [
        { value: "1", label: tByok("statusEnabled") },
        { value: "0", label: tByok("statusDisabled") },
      ],
      placeholder: tByok("filterByStatus"),
    },
  } satisfies FilterSpec), [t, tByok, byokTypes]);

  const [filterValues, setFilterValuesRaw] = useFilterState(filterSpec);

  const setFilterValues = (next: Parameters<typeof setFilterValuesRaw>[0]) => {
    setPage(1);
    setFilterValuesRaw(next);
  };

  const { data, isLoading } = useAdminBYOKChannels({
    page,
    page_size: pageSize,
    search: filterValues.search ? String(filterValues.search) : undefined,
    owner_id: filterValues.owner_id ? String(filterValues.owner_id) : undefined,
    type: filterValues.type ? String(filterValues.type) : undefined,
    status: filterValues.status ? String(filterValues.status) : undefined,
  });

  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;
  const rows = data?.data ?? [];

  const disableMut = useDisableBYOKChannel();

  const handlePaginationChange = (nextPage: number, nextPageSize: number) => {
    if (nextPageSize !== pageSize) {
      setPage(1);
      setPageSize(nextPageSize);
    } else {
      setPage(nextPage);
    }
  };

  const handleDisable = async (id: number) => {
    try {
      await disableMut.mutateAsync(id);
      toast.success(t("disabledToast"));
    } catch (e) {
      toast.error(formatErrorToast(e, t("disableFailedToast")));
    }
  };

  const renderExpandedRow = (row: Row<BYOKChannelDetail>) => {
    const pc = row.original;
    const models = pc.models ?? [];
    const groups = groupModelsByProvider(models);
    const eps = parseEndpoints(pc.endpoints || "");
    const baseUrl = pc.base_url ? pc.base_url.replace(/\/+$/, "") : "";

    const mappings: [string, string][] =
      pc.model_mapping && Object.keys(pc.model_mapping).length > 0
        ? Object.entries(pc.model_mapping)
        : [];

    const allEndpoints = [
      { key: "chat_completions", label: tByok("endpointChat") },
      { key: "responses", label: tByok("endpointResp") },
      { key: "messages", label: tByok("endpointClaude") },
    ];

    return (
      <div className="space-y-4">
        {/* Endpoint Details */}
        {!pc.use_legacy_adaptor && (
          <div>
            <h4 className="text-heading mb-2">{tByok("expandedEndpoints")}</h4>
            <div className="space-y-1">
              {allEndpoints.map((ep) => {
                const path = (eps as Record<string, string | undefined>)[ep.key];
                const enabled = !!path;
                return (
                  <div key={ep.key} className="flex items-center gap-2 text-meta">
                    {enabled ? <span className="text-green-500">✓</span> : <span className="text-muted-foreground/40">✗</span>}
                    <span className={enabled ? "font-medium" : "text-muted-foreground/60"}>{ep.label}</span>
                    {enabled && (
                      <span className="font-mono text-muted-foreground break-all">→ {baseUrl}{path}</span>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {/* Models */}
        {models.length > 0 && (
          <ExpandedModelsView groups={groups} totalCount={models.length} />
        )}

        {/* Model Mapping */}
        {mappings.length > 0 && (
          <div>
            <h4 className="text-heading mb-2">{tByok("modelMapping")}</h4>
            <div className="space-y-1">
              {mappings.map(([from, to]) => (
                <div key={from} className="flex items-center gap-2 text-meta">
                  <ModelName name={from} />
                  <span className="text-muted-foreground">→</span>
                  <ModelName name={to} />
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Configuration Grid */}
        <div>
          <h4 className="text-heading mb-2">{tByok("expandedConfig")}</h4>
          <div className="grid grid-cols-2 gap-x-8 gap-y-1 text-meta md:grid-cols-3">
            <div><span className="text-muted-foreground">{tByok("configPassthrough")}: </span>{pc.passthrough_enabled ? tByok("yes") : tByok("no")}</div>
            <div><span className="text-muted-foreground">{tByok("baseUrl")}: </span><span className="font-mono break-all">{pc.base_url}</span></div>
            <div><span className="text-muted-foreground">{t("owner")}: </span><UsernameCell userId={pc.owner_id} /></div>
            <div><span className="text-muted-foreground">{tByok("createdAt")}: </span><DateCell timestamp={pc.created_at} /></div>
            {pc.test_model && <div><span className="text-muted-foreground">{tByok("testModel")}: </span><code className="text-meta">{pc.test_model}</code></div>}
            {pc.remark && <div><span className="text-muted-foreground">{tByok("remark")}: </span>{pc.remark}</div>}
            {pc.use_legacy_adaptor && <div><span className="text-muted-foreground">{tByok("modeLegacy")}: </span>{tByok("yes")}</div>}
          </div>
        </div>
      </div>
    );
  };

  const columns: ColumnDef<BYOKChannelDetail>[] = [
    {
      id: "expand",
      header: "",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="icon"
          className="size-6"
          onClick={(e) => {
            e.stopPropagation();
            row.toggleExpanded();
          }}
        >
          <ChevronRight
            className={`size-4 transition-transform ${row.getIsExpanded() ? "rotate-90" : ""}`}
          />
        </Button>
      ),
      enableHiding: false,
    },
    {
      accessorKey: "id",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("id")} />,
    },
    {
      accessorKey: "owner_id",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("owner")} />,
      cell: ({ row }) => <UsernameCell userId={row.original.owner_id} />,
    },
    {
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tByok("name")} />,
      cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
    },
    {
      accessorKey: "base_url",
      header: tByok("baseUrl"),
      cell: ({ row }) => {
        const url = row.original.base_url || "";
        let hostname = url;
        try {
          hostname = new URL(url).host;
        } catch {
          /* keep original */
        }
        return (
          <TooltipProvider delayDuration={200}>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="max-w-[160px] truncate block text-meta font-mono cursor-default">
                  {hostname}
                </span>
              </TooltipTrigger>
              <TooltipContent side="top" className="max-w-sm">
                <p className="text-meta font-mono break-all">{url}</p>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        );
      },
    },
    {
      accessorKey: "models",
      header: tByok("models"),
      cell: ({ row }) => {
        const models = row.original.models ?? [];
        if (models.length === 0) return <span className="text-muted-foreground">-</span>;
        const show = models.slice(0, 3);
        const rest = models.length - show.length;
        return (
          <TooltipProvider delayDuration={200}>
            <Tooltip>
              <TooltipTrigger asChild>
                <div className="flex items-center gap-1 max-w-[280px] overflow-hidden">
                  {show.map((m) => (
                    <Badge
                      key={m}
                      variant="secondary"
                      className="text-2xs font-mono px-1.5 py-0 shrink-0 truncate max-w-[100px]"
                    >
                      {m}
                    </Badge>
                  ))}
                  {rest > 0 && (
                    <span className="text-meta text-muted-foreground shrink-0">+{rest}</span>
                  )}
                </div>
              </TooltipTrigger>
              {models.length > 3 && (
                <TooltipContent side="bottom" className="max-w-sm">
                  <div className="text-meta font-mono space-y-0.5">
                    {models.map((m) => (
                      <div key={m}>{m}</div>
                    ))}
                  </div>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        );
      },
    },
    {
      accessorKey: "key_last4",
      header: tByok("keyLast4"),
      cell: ({ row }) => <code className="text-meta">****{row.original.key_last4}</code>,
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tByok("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "created_at",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("created")} />,
      cell: ({ row }) => <DateCell timestamp={row.original.created_at} />,
    },
    {
      id: "actions",
      header: tByok("actions"),
      cell: ({ row }) => {
        const pc = row.original;
        if (pc.status !== 1) {
          return (
            <span className="text-meta text-muted-foreground">{t("disabledLabel")}</span>
          );
        }
        return (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-8"
                onClick={(e) => e.stopPropagation()}
              >
                <MoreHorizontal className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem
                className="text-destructive"
                onClick={() => setConfirmDisableId(pc.id)}
              >
                {t("forceDisable")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        );
      },
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
        data={rows}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        renderExpandedRow={renderExpandedRow}
        defaultColumnVisibility={{ id: false, key_last4: false }}
        storageKey="byok-admin-columns"
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
          />
        }
      />

      <DeleteConfirm
        open={confirmDisableId !== null}
        onOpenChange={(o) => {
          if (!o) setConfirmDisableId(null);
        }}
        title={t("confirmTitle")}
        description={t("confirmDesc")}
        onConfirm={async () => {
          if (confirmDisableId !== null) {
            await handleDisable(confirmDisableId);
            setConfirmDisableId(null);
          }
        }}
      />
    </div>
  );
}

function AdminBYOKFallback() {
  const tc = useTranslations("common");
  return <div className="py-6 text-muted-foreground">{tc("loading")}</div>;
}

export default function AdminBYOKPage() {
  return (
    <Suspense fallback={<AdminBYOKFallback />}>
      <AdminBYOKPageInner />
    </Suspense>
  );
}

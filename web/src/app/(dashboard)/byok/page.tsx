"use client";

import { useState, useMemo, Suspense } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef, Row } from "@tanstack/react-table";
import { toast } from "sonner";
import {
  BarChart3,
  ChevronRight,
  Loader2,
  MoreHorizontal,
  Plus,
  Settings2,
} from "lucide-react";

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
import { ExpandedModelsView } from "@/components/business/expanded-models-view";
import { InlineEdit } from "@/components/business/inline-edit";
import { ChannelTestDialog } from "@/components/business/channel-test-dialog";
import { ModelName } from "@/components/business/model-name";

import { groupModelsByProvider, PAGE_SIZES } from "@/lib/constants";
import { formatErrorToast } from "@/lib/api/error-toast";
import {
  type BYOKChannelDetail,
  useBYOKChannels,
  useBYOKSupportedTypes,
  useDeleteBYOKChannel,
  useTestBYOKChannel,
  toChannelTestResponse,
  useUpdateBYOKChannel,
} from "@/lib/api/byok-channels";
import { parseEndpoints } from "@/components/channel/channel-form/utils";

function BYOKPageInner() {
  const t = useTranslations("byok");
  const tc = useTranslations("common");
  const router = useRouter();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);
  const [deleteTarget, setDeleteTarget] = useState<BYOKChannelDetail | null>(null);
  const [testingId, setTestingId] = useState<number | null>(null);
  const [testDialogChannel, setTestDialogChannel] = useState<BYOKChannelDetail | null>(null);

  const { data: typesData } = useBYOKSupportedTypes();
  const byokTypes = typesData?.types ?? [];

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: t("searchNameOrModel") },
    type: {
      kind: "enum",
      options: byokTypes.map((bt) => ({ value: String(bt.id), label: bt.name })),
      placeholder: t("filterByType"),
    },
    status: {
      kind: "enum",
      options: [
        { value: "1", label: t("statusEnabled") },
        { value: "0", label: t("statusDisabled") },
      ],
      placeholder: t("filterByStatus"),
    },
  } satisfies FilterSpec), [t, byokTypes]);

  const [filterValues, setFilterValuesRaw] = useFilterState(filterSpec);

  const setFilterValues = (next: Parameters<typeof setFilterValuesRaw>[0]) => {
    setPage(1);
    setFilterValuesRaw(next);
  };

  const { data, isLoading } = useBYOKChannels({
    page,
    page_size: pageSize,
    search: filterValues.search ? String(filterValues.search) : undefined,
    type: filterValues.type ? String(filterValues.type) : undefined,
    status: filterValues.status ? String(filterValues.status) : undefined,
  });

  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;
  const rows = data?.data ?? [];

  const deleteMut = useDeleteBYOKChannel();
  const testMut = useTestBYOKChannel();
  const updateMut = useUpdateBYOKChannel();

  const handlePaginationChange = (nextPage: number, nextPageSize: number) => {
    if (nextPageSize !== pageSize) {
      setPage(1);
      setPageSize(nextPageSize);
    } else {
      setPage(nextPage);
    }
  };

  const handleDeleteConfirm = async () => {
    if (!deleteTarget) return;
    try {
      await deleteMut.mutateAsync(deleteTarget.id);
      toast.success(t("deletedToast"));
      setDeleteTarget(null);
    } catch (e) {
      toast.error(formatErrorToast(e, t("deleteFailedToast")));
    }
  };

  const handleTest = async (pc: BYOKChannelDetail) => {
    setTestingId(pc.id);
    try {
      const raw = await testMut.mutateAsync({ id: pc.id });
      const result = toChannelTestResponse(raw);
      if (result.success) {
        toast.success(t("testSuccessWithTime", { time: result.time_cost.toFixed(2) }));
      } else {
        toast.error(t("testFailedWithError", { error: result.error || "Unknown error" }));
      }
    } catch (e) {
      toast.error(formatErrorToast(e, t("testFailedToast")));
    } finally {
      setTestingId(null);
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
        ? (Object.entries(pc.model_mapping) as [string, string][])
        : [];

    const allEndpoints = [
      { key: "chat_completions", label: t("endpointChat") },
      { key: "responses", label: t("endpointResp") },
      { key: "messages", label: t("endpointClaude") },
    ];

    return (
      <div className="space-y-4">
        {/* Endpoint Details */}
        {!pc.use_legacy_adaptor && (
          <div>
            <h4 className="text-heading mb-2">{t("expandedEndpoints")}</h4>
            <div className="space-y-1">
              {allEndpoints.map((ep) => {
                const path = (eps as Record<string, string | undefined>)[ep.key];
                const enabled = !!path;
                return (
                  <div key={ep.key} className="flex items-center gap-2 text-meta">
                    {enabled ? (
                      <span className="text-green-500">✓</span>
                    ) : (
                      <span className="text-muted-foreground/40">✗</span>
                    )}
                    <span className={enabled ? "font-medium" : "text-muted-foreground/60"}>
                      {ep.label}
                    </span>
                    {enabled && (
                      <span className="font-mono text-muted-foreground break-all">
                        → {baseUrl}{path}
                      </span>
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
            <h4 className="text-heading mb-2">{t("modelMapping")}</h4>
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
          <h4 className="text-heading mb-2">{t("expandedConfig")}</h4>
          <div className="grid grid-cols-2 gap-x-8 gap-y-1 text-meta md:grid-cols-3">
            <div><span className="text-muted-foreground">{t("configPassthrough")}: </span>{pc.passthrough_enabled ? t("yes") : t("no")}</div>
            <div><span className="text-muted-foreground">{t("baseUrl")}: </span><span className="font-mono break-all">{pc.base_url}</span></div>
            <div><span className="text-muted-foreground">{t("createdAt")}: </span><DateCell timestamp={pc.created_at} /></div>
            {pc.test_model && <div><span className="text-muted-foreground">{t("testModel")}: </span><code className="text-meta">{pc.test_model}</code></div>}
            {pc.remark && <div><span className="text-muted-foreground">{t("remark")}: </span>{pc.remark}</div>}
            {pc.use_legacy_adaptor && <div><span className="text-muted-foreground">{t("modeLegacy")}: </span>{t("yes")}</div>}
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
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("name")} />,
      cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
    },
    {
      accessorKey: "base_url",
      header: t("baseUrl"),
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
      header: t("models"),
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
      header: t("keyLast4"),
      cell: ({ row }) => <code className="text-meta">****{row.original.key_last4}</code>,
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: "lb",
      header: t("loadBalancing"),
      cell: ({ row }) => (
        <div className="flex items-center gap-2 text-meta">
          <span className="text-muted-foreground">W:</span>
          <InlineEdit
            value={row.original.weight}
            onSave={(v) => updateMut.mutate({ id: row.original.id, weight: v })}
            disabled={updateMut.isPending}
          />
          <span className="text-muted-foreground">P:</span>
          <InlineEdit
            value={row.original.priority}
            onSave={(v) => updateMut.mutate({ id: row.original.id, priority: v })}
            disabled={updateMut.isPending}
          />
        </div>
      ),
    },
    {
      id: "test",
      header: t("test"),
      cell: ({ row }) => {
        const pc = row.original;
        const isTesting = testingId === pc.id;
        const hasModels = (pc.models?.length ?? 0) > 0;
        return (
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-2 text-meta"
              onClick={(e) => {
                e.stopPropagation();
                handleTest(pc);
              }}
              disabled={isTesting || !hasModels}
            >
              {isTesting ? <Loader2 className="size-3 animate-spin" /> : t("test")}
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              onClick={(e) => {
                e.stopPropagation();
                setTestDialogChannel(pc);
              }}
              disabled={!hasModels}
            >
              <Settings2 className="size-3.5" />
            </Button>
          </div>
        );
      },
    },
    {
      id: "actions",
      header: t("actions"),
      cell: ({ row }) => {
        const pc = row.original;
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
              <DropdownMenuItem onClick={() => router.push(`/byok/edit?id=${pc.id}`)}>
                {t("edit")}
              </DropdownMenuItem>
              <DropdownMenuItem
                className="text-destructive"
                onClick={() => setDeleteTarget(pc)}
              >
                {t("delete")}
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
        storageKey="byok-user-columns"
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            secondaryActions={[
              {
                label: t("usageStats"),
                icon: <BarChart3 className="size-4" />,
                href: "/byok/stats",
                variant: "outline",
              },
            ]}
            primaryAction={
              <Button size="sm" onClick={() => router.push("/byok/new")}>
                <Plus className="mr-2 size-4" />
                {t("create")}
              </Button>
            }
          />
        }
      />

      {testDialogChannel && (
        <ChannelTestDialog
          channelLike={{
            id: testDialogChannel.id,
            name: testDialogChannel.name,
            models: (testDialogChannel.models ?? []).join(","),
            endpoints: testDialogChannel.endpoints ?? "",
            type: testDialogChannel.type,
          }}
          testFn={async ({ id, model, endpoint_type }) => {
            const raw = await testMut.mutateAsync({ id, model, endpoint_type });
            return toChannelTestResponse(raw);
          }}
          supportsStream={false}
          agentSourceType={null}
          open={!!testDialogChannel}
          onOpenChange={(open) => {
            if (!open) setTestDialogChannel(null);
          }}
        />
      )}

      <DeleteConfirm
        open={!!deleteTarget}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null);
        }}
        onConfirm={handleDeleteConfirm}
        title={t("deleteConfirm", { name: deleteTarget?.name ?? "" })}
        description={t("deleteDescription")}
      />
    </div>
  );
}

export default function BYOKPage() {
  return (
    <Suspense>
      <BYOKPageInner />
    </Suspense>
  );
}

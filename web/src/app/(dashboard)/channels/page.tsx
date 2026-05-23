"use client";

import { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef, Row } from "@tanstack/react-table";
import { toast } from "sonner";
import { MoreHorizontal, Plus, ChevronRight, Settings2, Loader2 } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { StatusBadge } from "@/components/business/status-badge";
import { DeleteConfirm } from "@/components/business/delete-confirm";

import { ExpandedModelsView } from "@/components/business/expanded-models-view";
import { ModelName } from "@/components/business/model-name";
import { InlineEdit } from "@/components/business/inline-edit";
import { groupModelsByProvider, PAGE_SIZES } from "@/lib/constants";
import { useChannels, useChannelTypes, useUpdateChannel, useDeleteChannel, useTestChannel } from "@/lib/api/channels";
import { formatErrorToast } from "@/lib/api/error-toast";
import { ChannelTestDialog } from "@/components/business/channel-test-dialog";
import type { Channel } from "@/lib/types";
import { parseEndpoints } from "@/components/channel/channel-form/utils";

export default function ChannelsPage() {
  const t = useTranslations("channels");
  const tc = useTranslations("common");
  const router = useRouter();

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const { data: channelTypesData = [] } = useChannelTypes();
  const channelTypes = channelTypesData;

  const formatTypeName = (name: string, i18nKey: string) => {
    if (i18nKey) {
      try {
        return t(i18nKey as never);
      } catch {
        // Fall back to backend-provided canonical name when i18n key is missing.
      }
    }
    return name;
  };

  const filterSpec = useMemo(() => ({
    search: { kind: "text" as const, placeholder: t("searchNameOrModel") },
    type: {
      kind: "enum" as const,
      options: channelTypes.map((ct) => ({
        value: String(ct.id),
        label: formatTypeName(ct.name, ct.i18n_key),
      })),
      placeholder: t("filterByType"),
    },
    status: {
      kind: "enum" as const,
      options: [
        { value: "1", label: t("enabled") },
        { value: "0", label: t("disabled") },
      ],
      placeholder: t("filterByStatus"),
    },
  } satisfies FilterSpec), [t, channelTypes]); // eslint-disable-line react-hooks/exhaustive-deps

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useChannels({
    page,
    page_size: pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.type ? { type: String(filterValues.type) } : {}),
    ...(filterValues.status ? { status: String(filterValues.status) } : {}),
  });

  const channels = data?.data ?? [];
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

  const updateMutation = useUpdateChannel();
  const deleteMutation = useDeleteChannel();
  const testMutation = useTestChannel();

  const [deleteItem, setDeleteItem] = useState<Channel | null>(null);
  const [testDialogChannel, setTestDialogChannel] = useState<Channel | null>(null);
  const [testingChannelId, setTestingChannelId] = useState<number | null>(null);

  const getTypeName = (type: number) => {
    const channelType = channelTypes.find((item) => item.id === type);
    if (!channelType) return `Unknown [${type}]`;
    return `${formatTypeName(channelType.name, channelType.i18n_key)} [${channelType.id}]`;
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

  const handleQuickTest = async (channel: Channel) => {
    setTestingChannelId(channel.id);
    try {
      const result = await testMutation.mutateAsync({ id: channel.id });
      if (result.success) {
        toast.success(t("testSuccessWithTime", { time: result.time_cost.toFixed(2) }));
      } else {
        toast.error(t("testFailedWithError", { error: result.error || "Unknown error" }));
      }
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    } finally {
      setTestingChannelId(null);
    }
  };

  const renderExpandedRow = (row: Row<Channel>) => {
    const channel = row.original;
    const models = channel.models
      ? channel.models.split(",").map((s) => s.trim()).filter(Boolean)
      : [];
    const groups = groupModelsByProvider(models);
    const eps = parseEndpoints(channel.endpoints || "");
    const baseUrl = channel.base_url ? channel.base_url.replace(/\/+$/, "") : "";

    let mappings: [string, string][] = [];
    if (channel.model_mapping) {
      try {
        const obj = JSON.parse(channel.model_mapping);
        mappings = Object.entries(obj) as [string, string][];
      } catch { /* ignore */ }
    }

    const allEndpoints = [
      { key: "chat_completions", label: "Chat Completions" },
      { key: "responses", label: "Responses API" },
      { key: "messages", label: "Claude Messages" },
      { key: "models", label: t("apiTypeModels") },
    ];

    return (
      <div className="space-y-4">
        {/* Endpoint Details */}
        {!channel.use_legacy_adaptor && (
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
            <div><span className="text-muted-foreground">{t("configPassthrough")}: </span>{channel.passthrough_enabled ? t("yes") : t("no")}</div>
            {channel.organization && <div><span className="text-muted-foreground">{t("configOrganization")}: </span>{channel.organization}</div>}
            {channel.api_version && <div><span className="text-muted-foreground">{t("configApiVersion")}: </span>{channel.api_version}</div>}
            {channel.proxy_url && <div><span className="text-muted-foreground">{t("configProxy")}: </span>{channel.proxy_url}</div>}
            {channel.tag && <div><span className="text-muted-foreground">{t("tag")}: </span>{channel.tag}</div>}
            {channel.remark && <div><span className="text-muted-foreground">{t("remark")}: </span>{channel.remark}</div>}
            {channel.use_legacy_adaptor && <div><span className="text-muted-foreground">{t("type")}: </span>{getTypeName(channel.type)}</div>}
          </div>
        </div>
      </div>
    );
  };

  const columns: ColumnDef<Channel>[] = [
    {
      id: "expand",
      header: "",
      cell: ({ row }) => (
        <Button
          variant="ghost"
          size="icon"
          className="size-6"
          onClick={() => row.toggleExpanded()}
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
      cell: ({ row }) => (
        <div className="flex items-center gap-1.5">
          <span className="font-medium">{row.original.name}</span>
          {row.original.use_legacy_adaptor ? (
            <Badge variant="outline" className="text-2xs px-1 py-0 border-yellow-500/50 text-yellow-600 dark:text-yellow-400">
              {t("modeLegacy")}
            </Badge>
          ) : (
            <Badge variant="outline" className="text-2xs px-1 py-0 border-blue-500/50 text-blue-600 dark:text-blue-400">
              {t("modeNative")}
            </Badge>
          )}
        </div>
      ),
    },
    {
      accessorKey: "base_url",
      header: t("baseUrl"),
      cell: ({ row }) => {
        const url = row.original.base_url || "";
        let hostname = url;
        try {
          hostname = new URL(url).host;
        } catch { /* keep original */ }
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
      id: "endpoints",
      header: t("sectionEndpoints"),
      cell: ({ row }) => {
        const ch = row.original;
        if (ch.use_legacy_adaptor) {
          return <span className="text-meta text-muted-foreground">{getTypeName(ch.type)}</span>;
        }
        const eps = parseEndpoints(ch.endpoints || "");
        const protocols = [
          { key: "chat_completions", label: t("endpointChat") },
          { key: "responses", label: t("endpointResp") },
          { key: "messages", label: t("endpointClaude") },
        ];
        return (
          <div className="flex gap-1">
            {protocols.map((p) => (
              <Badge
                key={p.key}
                variant={(eps as Record<string, string | undefined>)[p.key] ? "default" : "outline"}
                className={`text-2xs px-1.5 py-0 ${
                  (eps as Record<string, string | undefined>)[p.key]
                    ? ""
                    : "text-muted-foreground/40 border-muted-foreground/20"
                }`}
              >
                {p.label}
              </Badge>
            ))}
          </div>
        );
      },
    },
    {
      accessorKey: "models",
      header: t("models"),
      cell: ({ row }) => {
        const models = row.original.models
          ? row.original.models.split(",").map((s: string) => s.trim()).filter(Boolean)
          : [];
        if (models.length === 0) return <span className="text-muted-foreground">-</span>;
        const show = models.slice(0, 3);
        const rest = models.length - show.length;
        return (
          <TooltipProvider delayDuration={200}>
            <Tooltip>
              <TooltipTrigger asChild>
                <div className="flex items-center gap-1 max-w-[280px] overflow-hidden">
                  {show.map((m: string) => (
                    <Badge key={m} variant="secondary" className="text-2xs font-mono px-1.5 py-0 shrink-0 truncate max-w-[100px]">
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
                    {models.map((m: string) => <div key={m}>{m}</div>)}
                  </div>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        );
      },
    },
    {
      accessorKey: "tag",
      header: t("tag"),
      cell: ({ row }) => row.original.tag ? (
        <Badge variant="outline">{row.original.tag}</Badge>
      ) : null,
    },
    {
      id: "lb",
      header: t("loadBalancing"),
      cell: ({ row }) => (
        <div className="flex items-center gap-2 text-meta">
          <span className="text-muted-foreground">W:</span>
          <InlineEdit
            value={row.original.weight}
            onSave={(v) => updateMutation.mutate({ id: row.original.id, weight: v })}
            disabled={updateMutation.isPending}
          />
          <span className="text-muted-foreground">P:</span>
          <InlineEdit
            value={row.original.priority}
            onSave={(v) => updateMutation.mutate({ id: row.original.id, priority: v })}
            disabled={updateMutation.isPending}
          />
        </div>
      ),
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      id: "test",
      header: t("test"),
      cell: ({ row }) => {
        const channel = row.original;
        const isTesting = testingChannelId === channel.id;
        const hasModels = !!(channel.models && channel.models.trim());
        return (
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="sm"
              className="h-7 px-2 text-meta"
              onClick={() => handleQuickTest(channel)}
              disabled={isTesting || !hasModels}
            >
              {isTesting ? <Loader2 className="size-3 animate-spin" /> : t("test")}
            </Button>
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              onClick={() => setTestDialogChannel(channel)}
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
      header: tc("actions"),
      cell: ({ row }) => (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="size-8">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => router.push(`/channels/edit?id=${row.original.id}`)}>
              {tc("edit")}
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
        data={channels}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={handlePaginationChange}
        renderExpandedRow={renderExpandedRow}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            primaryAction={
              <Button size="sm" onClick={() => router.push("/channels/new")}>
                <Plus className="mr-2 size-4" />
                {t("createChannel")}
              </Button>
            }
          />
        }
      />

      {/* Delete Confirm */}
      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
      />

      {/* Test Dialog */}
      {testDialogChannel && (
        <ChannelTestDialog
          channelLike={{
            id: testDialogChannel.id,
            name: testDialogChannel.name,
            models: testDialogChannel.models ?? "",
            endpoints: testDialogChannel.endpoints ?? "",
            type: testDialogChannel.type,
          }}
          testFn={(args) => testMutation.mutateAsync(args)}
          supportsStream={true}
          agentSourceType="channel"
          open={!!testDialogChannel}
          onOpenChange={(open) => { if (!open) setTestDialogChannel(null); }}
        />
      )}
    </div>
  );
}

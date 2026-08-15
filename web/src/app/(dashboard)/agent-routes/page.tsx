"use client";

import { useMemo, useState } from "react";
import { ColumnDef } from "@tanstack/react-table";
import { MoreHorizontal, Pencil, Plus, Trash2 } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { AgentRouteFormDialog } from "@/components/agent-route-form-dialog";
import { DateCell } from "@/components/business/date-cell";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { DataTable } from "@/components/data-table/data-table";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import type { FilterSpec, FilterValues } from "@/components/data-table/filter-spec";
import { useFilterState } from "@/components/data-table/use-filter-state";
import { usePaginationState } from "@/components/data-table/use-pagination-state";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { PageLayout } from "@/components/layout/page-layout";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useAgentRoutesOverview, useDeleteAgentRoute } from "@/lib/api/agent-routes";
import { formatErrorToast } from "@/lib/api/error-toast";
import { PAGE_SIZES } from "@/lib/constants";
import type { AgentRouteOverviewItem } from "@/lib/types";

const SOURCE_TYPES = ["token", "channel", "api_service", "api_route"] as const;
type AgentRouteSourceFilterType = (typeof SOURCE_TYPES)[number];
const SOURCE_ENTITIES = {
  token: "token",
  channel: "channel",
  api_service: "api-service",
  api_route: "api-route",
} as const satisfies Record<
  AgentRouteSourceFilterType,
  "token" | "channel" | "api-service" | "api-route"
>;

function isSourceFilterType(value: string | null): value is AgentRouteSourceFilterType {
  return SOURCE_TYPES.some((type) => type === value);
}

function sourceTypeLabel(
  sourceType: string,
  t: ReturnType<typeof useTranslations>,
) {
  return {
    token: t("token"),
    channel: t("channel"),
    api_service: t("sourceAPIService"),
    api_route: t("sourceAPIRoute"),
  }[sourceType] ?? sourceType;
}

function PriorityBadge({ priority }: { priority: number }) {
  if (priority >= 100) {
    return <Badge className="bg-red-500 text-white hover:bg-red-500">{priority}</Badge>;
  }
  if (priority >= 90) {
    return <Badge className="bg-orange-500 text-white hover:bg-orange-500">{priority}</Badge>;
  }
  if (priority >= 80) {
    return <Badge className="bg-blue-500 text-white hover:bg-blue-500">{priority}</Badge>;
  }
  return <Badge variant="secondary">{priority}</Badge>;
}

export default function AgentRoutesPage() {
  const t = useTranslations("agentRoutes");
  const tc = useTranslations("common");
  const searchParams = useSearchParams();
  const sourceType = searchParams.get("source_type");
  const selectedSourceType = isSourceFilterType(sourceType) ? sourceType : undefined;
  const selectedSourceEntity = selectedSourceType
    ? SOURCE_ENTITIES[selectedSourceType]
    : "token";

  const filterSpec = useMemo(() => ({
    q: { kind: "text", placeholder: tc("search") },
    source_type: {
      kind: "enum",
      options: [
        { value: "token", label: t("sourceToken") },
        { value: "channel", label: t("sourceChannel") },
        { value: "api_service", label: t("sourceAPIService") },
        { value: "api_route", label: t("sourceAPIRoute") },
      ],
      placeholder: t("filterBySourceType"),
    },
    source_service_id: {
      kind: "picker",
      entity: "api-service",
      label: t("sourceAPIService"),
      visible: (context) => Boolean(context.isAPIRoute),
    },
    source_id: {
      kind: "picker",
      entity: selectedSourceEntity,
      label: t("source"),
      visible: (context) => Boolean(context.hasSourceType),
      ...(selectedSourceType === "api_route" ? {
        pickerQuery: (values) => {
          const apiServiceId = Number(values.source_service_id);
          const validServiceID = Number.isSafeInteger(apiServiceId) && apiServiceId > 0;
          return {
            apiServiceId: validServiceID ? apiServiceId : undefined,
            disabled: !validServiceID,
          };
        },
      } : {}),
    },
    model: {
      kind: "text",
      label: t("model"),
      placeholder: t("modelFilterPlaceholder"),
      advanced: true,
    },
    agent_id: {
      kind: "picker",
      entity: "agent",
      label: t("agentId"),
      advanced: true,
    },
  } satisfies FilterSpec), [selectedSourceEntity, selectedSourceType, t, tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);
  const [page, pageSize, setPagination] = usePaginationState(PAGE_SIZES.DEFAULT);

  const handleFilterChange = (next: Partial<FilterValues>) => {
    if (
      Object.hasOwn(next, "source_type")
      && next.source_type !== filterValues.source_type
    ) {
      setFilterValues({ ...next, source_id: "", source_service_id: "" });
      return;
    }
    if (
      Object.hasOwn(next, "source_service_id")
      && next.source_service_id !== filterValues.source_service_id
    ) {
      setFilterValues({ ...next, source_id: "" });
      return;
    }
    setFilterValues(next);
  };

  const sourceId = Number(filterValues.source_id);
  const { data, isLoading } = useAgentRoutesOverview({
    page,
    page_size: pageSize,
    ...(filterValues.q ? { q: String(filterValues.q) } : {}),
    ...(selectedSourceType ? { source_type: selectedSourceType } : {}),
    ...(selectedSourceType && Number.isSafeInteger(sourceId) && sourceId > 0
      ? { source_id: sourceId }
      : {}),
    ...(filterValues.model ? { model: String(filterValues.model) } : {}),
    ...(filterValues.agent_id ? { agent_id: String(filterValues.agent_id) } : {}),
  });

  const routes = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  const deleteMutation = useDeleteAgentRoute();
  const [deleteItem, setDeleteItem] = useState<AgentRouteOverviewItem | null>(null);
  const [formRoute, setFormRoute] = useState<AgentRouteOverviewItem | null>(null);
  const [formOpen, setFormOpen] = useState(false);

  const openCreate = () => {
    setFormRoute(null);
    setFormOpen(true);
  };

  const openEdit = (route: AgentRouteOverviewItem) => {
    setFormRoute(route);
    setFormOpen(true);
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMutation.mutateAsync(deleteItem.id);
      toast.success(t("ruleDeleted"));
      setDeleteItem(null);
    } catch (error) {
      toast.error(formatErrorToast(error, t("deleteFailed")));
    }
  };

  const columns: ColumnDef<AgentRouteOverviewItem>[] = [
    {
      accessorKey: "priority",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("priority")} />,
      cell: ({ row }) => <PriorityBadge priority={row.original.priority} />,
    },
    {
      id: "source",
      header: t("source"),
      cell: ({ row }) => (
        <div className="flex items-center gap-2">
          <Badge variant="outline" className="capitalize">
            {sourceTypeLabel(row.original.source_type, t)}
          </Badge>
          <span className="text-sm">{row.original.source_name}</span>
        </div>
      ),
    },
    {
      accessorKey: "model",
      header: t("model"),
      cell: ({ row }) => (
        <span className="text-sm">
          {row.original.model || <span className="text-muted-foreground">{t("default")}</span>}
        </span>
      ),
    },
    {
      id: "target",
      header: t("target"),
      cell: ({ row }) => (
        <span className="text-sm">
          {row.original.agent_name || row.original.agent_tag || row.original.agent_id}
        </span>
      ),
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
            <Button variant="ghost" size="icon-sm" aria-label={tc("actions")}>
              <MoreHorizontal data-icon="inline-start" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuGroup>
              <DropdownMenuItem onClick={() => openEdit(row.original)}>
                <Pencil data-icon="inline-start" />
                {tc("edit")}
              </DropdownMenuItem>
              <DropdownMenuItem variant="destructive" onClick={() => setDeleteItem(row.original)}>
                <Trash2 data-icon="inline-start" />
                {tc("delete")}
              </DropdownMenuItem>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ];

  return (
    <PageLayout
      title={t("title")}
      description={t("description")}
      maxWidth="full"
    >
      <div className="flex flex-col gap-4">

      <DataTable
        columns={columns}
        data={routes}
        loading={isLoading}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={pageCount}
        onPaginationChange={setPagination}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={handleFilterChange}
            context={{
              hasSourceType: Boolean(selectedSourceType),
              isAPIRoute: selectedSourceType === "api_route",
            }}
            primaryAction={
              <Button onClick={openCreate}>
                <Plus data-icon="inline-start" />
                {t("createRule")}
              </Button>
            }
          />
        }
      />

      <AgentRouteFormDialog
        open={formOpen}
        route={formRoute}
        onOpenChange={setFormOpen}
      />

      <DeleteConfirm
        open={Boolean(deleteItem)}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
      />
      </div>
    </PageLayout>
  );
}

"use client";

import { useState, useMemo } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";

import { useAgentRoutesOverview, useDeleteAgentRoute } from "@/lib/api/agent-routes";
import { formatErrorToast } from "@/lib/api/error-toast";
import { PAGE_SIZES } from "@/lib/constants";
import type { AgentRouteOverviewItem } from "@/lib/types";

function PriorityBadge({ priority }: { priority: number }) {
  if (priority >= 100) {
    return <Badge className="bg-red-500 hover:bg-red-500 text-white">{priority}</Badge>;
  }
  if (priority >= 90) {
    return <Badge className="bg-orange-500 hover:bg-orange-500 text-white">{priority}</Badge>;
  }
  if (priority >= 80) {
    return <Badge className="bg-blue-500 hover:bg-blue-500 text-white">{priority}</Badge>;
  }
  return <Badge variant="secondary">{priority}</Badge>;
}

export default function AgentRoutesPage() {
  const t = useTranslations("agentRoutes");
  const tc = useTranslations("common");

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
    source_type: {
      kind: "enum",
      options: [
        { value: "token", label: t("sourceToken") },
        { value: "channel", label: t("sourceChannel") },
      ],
      placeholder: t("filterBySourceType"),
    },
  } satisfies FilterSpec), [t, tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useAgentRoutesOverview({
    page,
    page_size: pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.source_type ? { source_type: String(filterValues.source_type) } : {}),
  });

  const routes = data?.data ?? [];
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

  const deleteMutation = useDeleteAgentRoute();
  const [deleteItem, setDeleteItem] = useState<AgentRouteOverviewItem | null>(null);

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
            {row.original.source_type === "token" ? t("token") : t("channel")}
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
        <Button
          variant="ghost"
          size="icon"
          className="size-8 text-destructive hover:text-destructive"
          onClick={() => setDeleteItem(row.original)}
        >
          <Trash2 className="size-4" />
        </Button>
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
        data={routes}
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
          />
        }
      />

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
      />
    </div>
  );
}

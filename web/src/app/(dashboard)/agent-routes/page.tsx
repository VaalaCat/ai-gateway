"use client";

import { useState, useEffect } from "react";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { DataTableToolbar } from "@/components/data-table/toolbar";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";

import { useDebounce } from "@/hooks/use-debounce";
import { useAgentRoutesOverview, useDeleteAgentRoute } from "@/lib/api/agent-routes";
import { PAGE_SIZES } from "@/lib/constants";
import type { AgentRouteOverviewItem } from "@/lib/types";

type SourceTypeFilter = "" | "token" | "channel";

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
  const [search, setSearch] = useState("");
  const [sourceTypeFilter, setSourceTypeFilter] = useState<SourceTypeFilter>("");
  const debouncedSearch = useDebounce(search, 300);

  const { data, isLoading } = useAgentRoutesOverview({
    page,
    page_size: pageSize,
    search: debouncedSearch,
    ...(sourceTypeFilter ? { source_type: sourceTypeFilter } : {}),
  });

  const routes = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  useEffect(() => { setPage(1); }, [debouncedSearch, sourceTypeFilter]);

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
    } catch {
      toast.error(tc("error"));
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

  const filterButtons: { label: string; value: SourceTypeFilter }[] = [
    { label: t("all"), value: "" },
    { label: t("tokenRules"), value: "token" },
    { label: t("channelRules"), value: "channel" },
  ];

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground mt-1">{t("description")}</p>
      </div>

      <div className="flex gap-2">
        {filterButtons.map((btn) => (
          <Button
            key={btn.value}
            variant={sourceTypeFilter === btn.value ? "default" : "outline"}
            size="sm"
            onClick={() => setSourceTypeFilter(btn.value)}
          >
            {btn.label}
          </Button>
        ))}
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
          <DataTableToolbar
            searchValue={search}
            searchPlaceholder={tc("search")}
            onSearchChange={setSearch}
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

"use client";

import { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { Network, MoreHorizontal, Pencil, Trash2 } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";
import { ScopeBadge } from "@/components/model-routing/scope-badge";

import {
  useModelRoutings,
  useUpdateModelRouting,
  useDeleteModelRouting,
} from "@/lib/api/model-routings";
import { ApiError } from "@/lib/api/client";
import { PAGE_SIZES } from "@/lib/constants";
import type { ModelRouting } from "@/lib/types";

export interface ModelRoutingsListPageProps {
  apiMode: "admin" | "user";
}

function MembersCell({ members }: { members: ModelRouting["members"] | string }) {
  const t = useTranslations("modelRoutings");
  // 后端 ModelRouting.members 在 JSON 响应里是字符串（GORM text 列），前端解析为数组
  const list: ModelRouting["members"] = (() => {
    if (Array.isArray(members)) return members;
    if (typeof members === "string") {
      try { return JSON.parse(members) as ModelRouting["members"]; } catch { return []; }
    }
    return [];
  })();
  const preview = list.slice(0, 5);
  const rest = list.length - preview.length;

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger asChild>
          <div className="flex items-center gap-1 flex-wrap">
            {preview.map((m) => (
              <Badge key={m.ref} variant="secondary" className="text-xs">
                {m.ref}
              </Badge>
            ))}
            {rest > 0 && (
              <Badge variant="outline" className="text-xs">
                +{rest}
              </Badge>
            )}
          </div>
        </TooltipTrigger>
        <TooltipContent>
          <p>{t("cols.membersCount", { count: list.length })}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function ModelRoutingsListPage({ apiMode }: ModelRoutingsListPageProps) {
  const t = useTranslations("modelRoutings");
  const tc = useTranslations("common");
  const router = useRouter();

  const baseHref = apiMode === "admin" ? "/model-routings" : "/profile/model-routings";
  const isAdmin = apiMode === "admin";

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: t("filters.searchPlaceholder") },
    ...(isAdmin ? {
      scope: {
        kind: "enum",
        options: [
          { value: "global", label: t("scope.global") },
          { value: "user", label: t("scope.user") },
        ],
        placeholder: t("filters.filterByScope"),
      },
      user_id: { kind: "picker", entity: "user", placeholder: t("filters.filterByUser") },
    } : {}),
  } satisfies FilterSpec), [t, isAdmin]);

  const [filterValues, setFilterValuesRaw] = useFilterState(filterSpec);

  const setFilterValues = (next: Parameters<typeof setFilterValuesRaw>[0]) => {
    setPage(1);
    setFilterValuesRaw(next);
  };

  const scopeFilter = filterValues.scope ? String(filterValues.scope) as "global" | "user" : undefined;
  const userIdFilter = filterValues.user_id ? String(filterValues.user_id) : "";

  const { data, isLoading } = useModelRoutings(
    {
      page,
      page_size: pageSize,
      search: filterValues.search ? String(filterValues.search) : undefined,
      ...(scopeFilter ? { scope: scopeFilter } : {}),
      ...(userIdFilter ? { user_id: Number(userIdFilter) } : {}),
    },
    apiMode
  );

  const routings = data?.data ?? [];
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

  const updateMut = useUpdateModelRouting(apiMode);
  const deleteMut = useDeleteModelRouting(apiMode);
  const [deleteItem, setDeleteItem] = useState<ModelRouting | null>(null);

  const handleToggleEnabled = (row: ModelRouting, newValue: boolean) => {
    updateMut.mutate(
      { id: row.id, enabled: newValue },
      {
        onError: () => {
          toast.error(tc("error"));
        },
      }
    );
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMut.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
      setDeleteItem(null);
    } catch (err) {
      if (err instanceof ApiError && err.body?.code === "referenced_by") {
        const refs = String(err.body.details ?? "");
        toast.error(t("errors.referencedBy", { refs }), { duration: 8000 });
      } else {
        toast.error(tc("error"));
      }
      setDeleteItem(null);
    }
  };

  const columns: ColumnDef<ModelRouting>[] = [
    {
      accessorKey: "name",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("cols.name")} />
      ),
      cell: ({ row }) => (
        <button
          className="font-medium text-sm hover:underline text-left"
          onClick={() =>
            router.push(`${baseHref}/edit?id=${row.original.id}`)
          }
        >
          {row.original.name}
        </button>
      ),
    },
    {
      accessorKey: "scope",
      header: t("cols.scope"),
      cell: ({ row }) => <ScopeBadge scope={row.original.scope} />,
    },
    {
      accessorKey: "user_id",
      header: t("cols.owner"),
      cell: ({ row }) => (
        <span className="text-sm text-muted-foreground">
          {row.original.user_id || "—"}
        </span>
      ),
    },
    {
      id: "members",
      header: t("cols.members"),
      cell: ({ row }) => <MembersCell members={row.original.members} />,
    },
    {
      accessorKey: "enabled",
      header: t("cols.enabled"),
      cell: ({ row }) => (
        <Switch
          checked={row.original.enabled}
          onCheckedChange={(v) => handleToggleEnabled(row.original, v)}
        />
      ),
    },
    {
      accessorKey: "updated_at",
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t("cols.updated")} />
      ),
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
            <DropdownMenuItem
              onClick={() =>
                router.push(`${baseHref}/edit?id=${row.original.id}`)
              }
            >
              <Pencil className="size-4 mr-2" />
              {t("actions.edit")}
            </DropdownMenuItem>
            <DropdownMenuItem
              className="text-destructive focus:text-destructive"
              onClick={() => setDeleteItem(row.original)}
            >
              <Trash2 className="size-4 mr-2" />
              {t("actions.delete")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ];

  const isEmpty =
    !isLoading &&
    routings.length === 0 &&
    !filterValues.search &&
    !filterValues.scope &&
    !userIdFilter;

  const pageTitle = apiMode === "admin" ? t("title") : t("myTitle");
  const pageSubtitle = apiMode === "admin" ? t("subtitle") : t("filtersUserHint");

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">{pageTitle}</h1>
          <p className="text-muted-foreground mt-1">{pageSubtitle}</p>
        </div>
        <Button onClick={() => router.push(`${baseHref}/new`)}>
          {t("create")}
        </Button>
      </div>

      {isEmpty ? (
        <div className="flex flex-col items-center justify-center py-24 gap-4 text-center">
          <Network className="size-12 text-muted-foreground" />
          <div>
            <p className="font-semibold text-lg">{t("empty.title")}</p>
            <p className="text-muted-foreground mt-1">{t("empty.desc")}</p>
          </div>
          <Button onClick={() => router.push(`${baseHref}/new`)}>
            {t("empty.cta")}
          </Button>
        </div>
      ) : (
        <DataTable
          columns={columns}
          data={routings}
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
      )}

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => {
          if (!open) setDeleteItem(null);
        }}
        onConfirm={handleDelete}
      />
    </div>
  );
}

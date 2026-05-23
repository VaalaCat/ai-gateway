"use client";

import { useState, useEffect, useMemo } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { MoreHorizontal, Plus } from "lucide-react";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip";

import { StatusBadge } from "@/components/business/status-badge";
import { StatusSelect } from "@/components/business/status-select";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { DateCell } from "@/components/business/date-cell";

import { ApiError } from "@/lib/api/client";
import { formatErrorToast } from "@/lib/api/error-toast";
import {
  useUserGroups,
  useDeleteUserGroup,
  useCreateUserGroup,
  DEFAULT_GROUP_ID,
} from "@/lib/api/user-groups";
import { PAGE_SIZES } from "@/lib/constants";
import { parseModels } from "@/lib/parse-models";
import type { UserGroup } from "@/lib/types";

export default function UserGroupsPage() {
  const t = useTranslations("userGroups");
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

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useUserGroups({
    page,
    pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.status ? { status: String(filterValues.status) } : {}),
  });

  const groups = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  useEffect(() => { setPage(1); }, [filterValues]);

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const router = useRouter();
  const createMutation = useCreateUserGroup();
  const [createOpen, setCreateOpen] = useState(false);
  const [createForm, setCreateForm] = useState({ name: "", description: "", status: "1" });

  const handleCreate = async () => {
    try {
      const result = await createMutation.mutateAsync({
        name: createForm.name,
        description: createForm.description,
        status: Number(createForm.status),
      });
      toast.success(t("createSuccess"));
      setCreateOpen(false);
      setCreateForm({ name: "", description: "", status: "1" });
      router.push(`/groups/detail?id=${result.id}`);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        toast.error(t("nameConflict"));
      } else {
        toast.error(tc("error"));
      }
    }
  };

  const deleteMutation = useDeleteUserGroup();
  const [deleteItem, setDeleteItem] = useState<UserGroup | null>(null);

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await deleteMutation.mutateAsync(deleteItem.id);
      toast.success(t("deleteSuccess"));
      setDeleteItem(null);
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const isDefault = (g: UserGroup) => g.id === DEFAULT_GROUP_ID;

  const columns: ColumnDef<UserGroup>[] = [
    {
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("name")} />,
      cell: ({ row }) => (
        <span className="inline-flex items-center gap-2">
          <Link href={`/groups/detail?id=${row.original.id}`} className="font-medium hover:underline">
            {row.original.name}
          </Link>
          {isDefault(row.original) && <Badge variant="outline">{t("default")}</Badge>}
        </span>
      ),
    },
    {
      accessorKey: "description",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("description")} />,
      cell: ({ row }) => (
        <span className="hidden md:inline-block max-w-[300px] truncate text-muted-foreground">
          {row.original.description || "-"}
        </span>
      ),
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "user_count",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("userCount")} />,
      cell: ({ row }) => <span className="tabular-nums">{row.original.user_count ?? 0}</span>,
    },
    {
      accessorKey: "allowed_channel_ids",
      header: t("channels"),
      cell: ({ row }) => (
        <span className="hidden md:inline-block tabular-nums">
          {row.original.allowed_channel_ids?.length ?? 0}
        </span>
      ),
    },
    {
      accessorKey: "models",
      header: t("models"),
      cell: ({ row }) => (
        <span className="hidden md:inline-block tabular-nums">
          {parseModels(row.original.models).length}
        </span>
      ),
    },
    {
      accessorKey: "created_at",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("createdAt")} />,
      cell: ({ row }) => (
        <span className="hidden md:inline-block">
          <DateCell timestamp={row.original.created_at} />
        </span>
      ),
    },
    {
      id: "actions",
      header: tc("actions"),
      cell: ({ row }) => {
        const g = row.original;
        const protectedGroup = isDefault(g);
        return (
          <TooltipProvider>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="size-8">
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem asChild>
                  <Link href={`/groups/detail?id=${g.id}`}>{tc("edit")}</Link>
                </DropdownMenuItem>
                {protectedGroup ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span className="block">
                        <DropdownMenuItem disabled className="text-destructive">
                          {tc("delete")}
                        </DropdownMenuItem>
                      </span>
                    </TooltipTrigger>
                    <TooltipContent side="left">{t("protectedTip")}</TooltipContent>
                  </Tooltip>
                ) : (
                  <DropdownMenuItem
                    className="text-destructive"
                    onClick={() => setDeleteItem(g)}
                  >
                    {tc("delete")}
                  </DropdownMenuItem>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          </TooltipProvider>
        );
      },
    },
  ];

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold">{t("title")}</h1>
        <p className="text-muted-foreground mt-1">{t("subtitle")}</p>
      </div>

      <DataTable
        columns={columns}
        data={groups}
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
                setCreateForm({ name: "", description: "", status: "1" });
                setCreateOpen(true);
              }}>
                <Plus className="mr-2 size-4" />
                {t("createButton")}
              </Button>
            }
          />
        }
      />

      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("createButton")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label>{t("name")}</Label>
              <Input
                value={createForm.name}
                onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })}
                maxLength={64}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("description")}</Label>
              <Textarea
                value={createForm.description}
                onChange={(e) => setCreateForm({ ...createForm, description: e.target.value })}
                maxLength={255}
                rows={2}
              />
            </div>
            <StatusSelect
              value={createForm.status}
              onChange={(v) => setCreateForm({ ...createForm, status: v })}
            />
            <p className="text-xs text-muted-foreground">{t("createHint")}</p>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>{tc("cancel")}</Button>
            <Button
              onClick={handleCreate}
              disabled={createMutation.isPending || !createForm.name.trim()}
            >
              {tc("save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(open) => { if (!open) setDeleteItem(null); }}
        onConfirm={handleDelete}
        title={t("deleteConfirmTitle")}
        description={t("deleteConfirmDesc")}
      />
    </div>
  );
}

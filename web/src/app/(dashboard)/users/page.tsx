"use client";

import { Suspense, useState, useEffect, useMemo } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { ColumnDef } from "@tanstack/react-table";
import { toast } from "sonner";
import { MoreHorizontal, Plus } from "lucide-react";
import Link from "next/link";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PasswordInput } from "@/components/business/password-input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { StatusBadge, RoleBadge } from "@/components/business/status-badge";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { ProfileFormDialog } from "@/components/business/profile-form-dialog";
import { DateCell } from "@/components/business/date-cell";
import { GroupSelect } from "@/components/business/group-select";

import { useUsers, useCreateUser, useDeleteUser, useUpdateQuota } from "@/lib/api/users";
import { formatErrorToast } from "@/lib/api/error-toast";
import { PAGE_SIZES } from "@/lib/constants";
import type { User } from "@/lib/types";

export default function UsersPage() {
  return (
    <Suspense fallback={<div className="flex items-center justify-center py-12 text-muted-foreground">Loading...</div>}>
      <UsersPageContent />
    </Suspense>
  );
}

function UsersPageContent() {
  const t = useTranslations("users");
  const tc = useTranslations("common");

  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState<number>(PAGE_SIZES.DEFAULT);

  const filterSpec = useMemo(() => ({
    search: { kind: "text", placeholder: tc("search") },
    role: {
      kind: "enum",
      options: [
        { value: "1", label: t("roleUser") },
        { value: "2", label: t("roleAdmin") },
      ],
      placeholder: t("filterByRole"),
    },
    group_id: {
      kind: "picker",
      entity: "user-group",
    },
  } satisfies FilterSpec), [t, tc]);

  const [filterValues, setFilterValues] = useFilterState(filterSpec);

  const { data, isLoading } = useUsers({
    page,
    page_size: pageSize,
    ...(filterValues.search ? { search: String(filterValues.search) } : {}),
    ...(filterValues.role ? { role: String(filterValues.role) } : {}),
    ...(filterValues.group_id ? { group_id: Number(filterValues.group_id) } : {}),
  });

  const users = data?.data ?? [];
  const total = data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  useEffect(() => { setPage(1); }, [filterValues]);

  const searchParams = useSearchParams();
  const targetId = searchParams.get("id");
  useEffect(() => {
    if (!targetId || !data?.data) return;
    const target = data.data.find((u) => String(u.id) === targetId);
    if (target) {
      setEditItem(target);
    } else {
      toast.message(t("targetUserNotInPage"));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targetId, data]);

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const createMutation = useCreateUser();
  const deleteMutation = useDeleteUser();
  const quotaMutation = useUpdateQuota();

  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<User | null>(null);
  const [deleteItem, setDeleteItem] = useState<User | null>(null);
  const [quotaItem, setQuotaItem] = useState<User | null>(null);

  // Create form state
  const [createForm, setCreateForm] = useState<{
    username: string; password: string; role: string; group_id: number | undefined;
  }>({ username: "", password: "", role: "1", group_id: undefined });
  // Quota form state
  const [quotaDelta, setQuotaDelta] = useState("");

  const handleCreate = async () => {
    try {
      await createMutation.mutateAsync({
        username: createForm.username,
        password: createForm.password,
        role: Number(createForm.role),
        ...(createForm.group_id !== undefined ? { group_id: createForm.group_id } : {}),
      });
      toast.success(tc("success"));
      setCreateOpen(false);
      setCreateForm({ username: "", password: "", role: "1", group_id: undefined });
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
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

  const handleQuota = async () => {
    if (!quotaItem) return;
    try {
      await quotaMutation.mutateAsync({ id: quotaItem.id, delta: Number(quotaDelta) });
      toast.success(tc("success"));
      setQuotaItem(null);
      setQuotaDelta("");
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const columns: ColumnDef<User>[] = [
    {
      accessorKey: "id",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("id")} />,
    },
    {
      id: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("name")} />,
      cell: ({ row }) => {
        const u = row.original;
        const dn = u.display_name?.trim();
        const primary = dn || u.username;
        const showHandle = !!dn && dn !== u.username;
        return (
          <div className="flex flex-col">
            <span className="font-medium">{primary}</span>
            {showHandle && <span className="text-xs text-muted-foreground">@{u.username}</span>}
          </div>
        );
      },
    },
    {
      accessorKey: "email",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("email")} />,
      cell: ({ row }) => <span className="text-sm">{row.original.email || "-"}</span>,
    },
    {
      accessorKey: "role",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("role")} />,
      cell: ({ row }) => <RoleBadge role={row.original.role} />,
    },
    {
      accessorKey: "group_name",
      header: ({ column }) => (
        <div className="hidden md:block">
          <DataTableColumnHeader column={column} title={t("groupColumn")} />
        </div>
      ),
      cell: ({ row }) => <span className="hidden md:table-cell">{row.original.group_name ?? "-"}</span>,
    },
    {
      accessorKey: "status",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("status")} />,
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    {
      accessorKey: "quota",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("quota")} />,
      cell: ({ row }) => <span className="tabular-nums">$ {(row.original.quota / 100000).toFixed(2)}</span>,
    },
    {
      accessorKey: "used_quota",
      header: ({ column }) => <DataTableColumnHeader column={column} title={t("usedQuota")} />,
      cell: ({ row }) => <span className="tabular-nums">$ {(row.original.used_quota / 100000).toFixed(2)}</span>,
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
            <Button variant="ghost" size="icon" className="size-8">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onClick={() => setEditItem(row.original)}>
              {tc("edit")}
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { setQuotaItem(row.original); setQuotaDelta(""); }}>
              {t("adjustQuota")}
            </DropdownMenuItem>
            <DropdownMenuItem asChild>
              <Link href={`/admin/byok?owner_id=${row.original.id}`}>
                View BYOK Channels
              </Link>
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
        data={users}
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
                setCreateForm({ username: "", password: "", role: "1", group_id: undefined });
                setCreateOpen(true);
              }}>
                <Plus className="mr-2 size-4" />
                {t("createUser")}
              </Button>
            }
          />
        }
      />

      {/* Create Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent
          onPointerDownOutside={(e) => {
            const target = e.target as HTMLElement | null;
            if (target?.closest("[data-radix-popper-content-wrapper]")) {
              e.preventDefault();
            }
          }}
          onInteractOutside={(e) => {
            const target = e.target as HTMLElement | null;
            if (target?.closest("[data-radix-popper-content-wrapper]")) {
              e.preventDefault();
            }
          }}
          onFocusOutside={(e) => {
            const target = e.target as HTMLElement | null;
            if (target?.closest("[data-radix-popper-content-wrapper]")) {
              e.preventDefault();
            }
          }}
        >
          <DialogHeader>
            <DialogTitle>{t("createUser")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label>{t("username")}</Label>
              <Input
                value={createForm.username}
                onChange={(e) => setCreateForm({ ...createForm, username: e.target.value })}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("password")}</Label>
              <PasswordInput
                value={createForm.password}
                onChange={(v) => setCreateForm({ ...createForm, password: v })}
              />
            </div>
            <div className="space-y-2">
              <Label>{t("role")}</Label>
              <Select value={createForm.role} onValueChange={(v) => setCreateForm({ ...createForm, role: v })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="1">{t("roleUser")}</SelectItem>
                  <SelectItem value="2">{t("roleAdmin")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-2">
              <Label>{t("group")}</Label>
              <GroupSelect
                value={createForm.group_id}
                onChange={(id) => setCreateForm({ ...createForm, group_id: id })}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreateOpen(false)}>{tc("cancel")}</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending}>{tc("save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Dialog */}
      {editItem && (
        <ProfileFormDialog
          mode="admin"
          open={!!editItem}
          onOpenChange={(o) => !o && setEditItem(null)}
          user={editItem}
        />
      )}

      {/* Quota Dialog */}
      <Dialog open={!!quotaItem} onOpenChange={(open) => { if (!open) setQuotaItem(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("adjustQuota")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label>{t("quotaDelta")}</Label>
              <Input
                type="number"
                value={quotaDelta}
                onChange={(e) => setQuotaDelta(e.target.value)}
              />
              {quotaDelta && Number(quotaDelta) !== 0 && (
                <p className="text-sm text-muted-foreground tabular-nums">
                  ≈ $ {(Number(quotaDelta) / 100000).toFixed(4)} USD
                </p>
              )}
              <p className="text-sm text-muted-foreground">{t("quotaDeltaHint")}</p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setQuotaItem(null)}>{tc("cancel")}</Button>
            <Button onClick={handleQuota} disabled={quotaMutation.isPending}>{tc("save")}</Button>
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

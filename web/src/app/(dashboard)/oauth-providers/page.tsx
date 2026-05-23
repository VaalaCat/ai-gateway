"use client";

import { useMemo, useState } from "react";
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
import { Switch } from "@/components/ui/switch";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

import { DeleteConfirm } from "@/components/business/delete-confirm";
import { OAuthProviderBadge } from "@/components/business/oauth-provider-badge";

import {
  useOAuthProviders,
  useUpdateOAuthProvider,
  useDeleteOAuthProvider,
} from "@/lib/api/oauth";
import { formatErrorToast } from "@/lib/api/error-toast";
import type { OAuthProvider } from "@/lib/types-oauth";

import { ProviderFormDialog } from "./_form-dialog";

export default function OAuthProvidersPage() {
  const t = useTranslations("oauth.providers");
  const tc = useTranslations("common");

  // 后端 List 接口暂不支持 search，filterSpec 为空（仅承载 primaryAction）
  const filterSpec = useMemo(() => ({} satisfies FilterSpec), []);
  const [filterValues, setFilterValues] = useFilterState(filterSpec);
  void filterValues;

  const { data: providers = [], isLoading } = useOAuthProviders();
  const update = useUpdateOAuthProvider();
  const del = useDeleteOAuthProvider();

  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<OAuthProvider | null>(null);
  const [deleteItem, setDeleteItem] = useState<OAuthProvider | null>(null);

  const handleToggleEnabled = async (p: OAuthProvider, next: boolean) => {
    try {
      await update.mutateAsync({ id: p.id, enabled: next });
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  const handleDelete = async () => {
    if (!deleteItem) return;
    try {
      await del.mutateAsync(deleteItem.id);
      toast.success(tc("success"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    } finally {
      setDeleteItem(null);
    }
  };

  const columns: ColumnDef<OAuthProvider>[] = [
    {
      accessorKey: "name",
      header: ({ column }) => <DataTableColumnHeader column={column} title={tc("name")} />,
      cell: ({ row }) => (
        <div className="flex items-center gap-2">
          <OAuthProviderBadge
            displayName={row.original.display_name}
            iconUrl={row.original.icon_url}
            size="md"
          />
          <span className="font-mono text-xs">{row.original.name}</span>
        </div>
      ),
    },
    {
      accessorKey: "display_name",
      header: t("displayName"),
    },
    {
      accessorKey: "protocol",
      header: "协议",
      cell: ({ row }) => (
        <code className="rounded bg-muted px-1.5 py-0.5 text-xs">
          {row.original.protocol ?? "oidc"}
        </code>
      ),
    },
    {
      accessorKey: "client_id",
      header: "Client ID",
      cell: ({ row }) => <code className="text-xs">{row.original.client_id}</code>,
    },
    {
      accessorKey: "enabled",
      header: tc("enabled"),
      cell: ({ row }) => (
        <Switch
          checked={row.original.enabled}
          onCheckedChange={(v) => handleToggleEnabled(row.original, v)}
        />
      ),
    },
    {
      id: "actions",
      header: () => <div className="text-right">{tc("actions")}</div>,
      cell: ({ row }) => (
        <div className="text-right">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon">
                <MoreHorizontal className="size-4" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => setEditing(row.original)}>
                {tc("edit")}
              </DropdownMenuItem>
              <DropdownMenuItem
                className="text-destructive"
                onSelect={() => setDeleteItem(row.original)}
              >
                {tc("delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">{t("title")}</h1>
      <DataTable
        columns={columns}
        data={providers}
        loading={isLoading}
        toolbar={
          <FilterableToolbar
            spec={filterSpec}
            value={filterValues}
            onChange={setFilterValues}
            primaryAction={
              <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus className="mr-2 size-4" />
                {t("addNew")}
              </Button>
            }
          />
        }
      />
      <ProviderFormDialog
        mode="create"
        open={createOpen}
        onOpenChange={setCreateOpen}
      />
      <ProviderFormDialog
        mode="edit"
        open={!!editing}
        onOpenChange={(o) => !o && setEditing(null)}
        initial={editing ?? undefined}
      />
      <DeleteConfirm
        open={!!deleteItem}
        onOpenChange={(o) => !o && setDeleteItem(null)}
        title={tc("delete")}
        description={
          deleteItem
            ? `${tc("deleteConfirm")} (${deleteItem.display_name})`
            : ""
        }
        onConfirm={handleDelete}
      />
    </div>
  );
}

"use client";

import { Suspense, type ReactNode, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { MoreHorizontal, Plus } from "lucide-react";
import { ColumnDef } from "@tanstack/react-table";

import { DataTable } from "@/components/data-table/data-table";
import { DataTableColumnHeader } from "@/components/data-table/column-header";
import { FilterableToolbar } from "@/components/data-table/filterable-toolbar";
import { useFilterState } from "@/components/data-table/use-filter-state";
import type { FilterSpec } from "@/components/data-table/filter-spec";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { PageLayout } from "@/components/layout/page-layout";
import { PageLayoutSkeleton } from "@/components/layout/page-layout-skeleton";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { DateTimePicker } from "@/components/business/date-picker/date-time-picker";
import { CopyableText } from "@/components/business/copyable-text";
import { EntityLabel } from "@/components/business/entity-label";
import { DateCell } from "@/components/business/date-cell";
import { DeleteConfirm } from "@/components/business/delete-confirm";
import { PAGE_SIZES } from "@/lib/constants";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { usePublicConfig } from "@/lib/api/system";
import { useAuth } from "@/lib/auth";
import {
  useInviteCodes,
  useCreateInviteCode,
  useDeleteInviteCode,
  useAdminInviteCodes,
  useAdminDeleteInviteCode,
} from "@/lib/api/invite-codes";
import type { InviteCodeRow } from "@/lib/types";

function InvitesPageLayout({ children }: { children: ReactNode }) {
  const t = useTranslations("invites");

  return (
    <PageLayout title={t("title")} description={t("description")} maxWidth="full">
      {children}
    </PageLayout>
  );
}

function InvitesPageSkeleton() {
  const t = useTranslations("invites");

  return (
    <PageLayoutSkeleton
      title={t("title")}
      description={t("description")}
      maxWidth="full"
    />
  );
}

function CreateInviteDialog() {
  const t = useTranslations("invites");
  const create = useCreateInviteCode();
  const [open, setOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [note, setNote] = useState("");
  const [maxUses, setMaxUses] = useState(1);
  const [expiresAt, setExpiresAt] = useState<number | null>(null);

  const reset = () => {
    setNote("");
    setMaxUses(1);
    setExpiresAt(null);
  };

  const submit = async () => {
    setSubmitting(true);
    try {
      await create.mutateAsync({ note, max_uses: maxUses, expires_at: expiresAt ?? 0 });
      toast.success(t("createSuccess"));
      setOpen(false);
      reset();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t("createFailed"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) reset();
        setOpen(v);
      }}
    >
      <DialogTrigger asChild>
        <Button size="sm">
          <Plus className="size-4" /> {t("create")}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("create")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label>{t("note")}</Label>
            <Input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder={t("notePlaceholder")}
            />
          </div>
          <div className="space-y-2">
            <Label>{t("maxUses")}</Label>
            <Input
              type="number"
              min={1}
              value={maxUses}
              onChange={(e) => setMaxUses(Math.max(1, Number(e.target.value) || 1))}
            />
          </div>
          <div className="space-y-2">
            <Label>{t("expiresAt")}</Label>
            <DateTimePicker
              value={expiresAt}
              onChange={setExpiresAt}
              placeholder={t("expiresAtPlaceholder")}
            />
            <p className="text-xs text-muted-foreground">{t("expiresAtHint")}</p>
          </div>
        </div>
        <DialogFooter>
          <Button onClick={submit} disabled={submitting}>
            {t("submit")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function InvitesPageInner() {
  const t = useTranslations("invites");
  const tc = useTranslations("common");
  const { isAdmin } = useAuth();
  const { data: config } = usePublicConfig();
  const searchParams = useSearchParams();

  const [page, setPage] = useState(() => Number(searchParams.get("page")) || 1);
  const [pageSize, setPageSize] = useState<number>(
    () => Number(searchParams.get("page_size")) || PAGE_SIZES.DEFAULT,
  );

  const filterSpec = useMemo(
    () =>
      ({
        search: { kind: "text", placeholder: tc("search") },
        creator: {
          kind: "picker",
          entity: "user",
          visible: (ctx: { isAdmin: boolean }) => ctx.isAdmin,
          placeholder: t("filterByCreator"),
        },
      }) satisfies FilterSpec,
    [t, tc],
  );

  const [filterValues, setFilterValues] = useFilterState(filterSpec);
  const search = filterValues.search ? String(filterValues.search) : undefined;
  const creatorId = filterValues.creator ? Number(filterValues.creator) : undefined;

  const mineQ = useInviteCodes({ page, pageSize, search, enabled: !isAdmin });
  const adminQ = useAdminInviteCodes({ page, pageSize, search, creatorId, enabled: isAdmin });
  const active = isAdmin ? adminQ : mineQ;

  const del = useDeleteInviteCode();
  const adminDel = useAdminDeleteInviteCode();
  const [deleteItem, setDeleteItem] = useState<InviteCodeRow | null>(null);

  const handlePaginationChange = (newPage: number, newPageSize: number) => {
    if (newPageSize !== pageSize) {
      setPage(1);
      setPageSize(newPageSize);
    } else {
      setPage(newPage);
    }
  };

  const confirmDelete = () => {
    if (!deleteItem) return;
    const m = isAdmin ? adminDel : del;
    m.mutate(deleteItem.id, { onSuccess: () => toast.success(t("deleteSuccess")) });
    setDeleteItem(null);
  };

  const copyLink = (code: string) => {
    copyTextWithFeedback(`${window.location.origin}/register?invite=${code}`, {
      success: t("copiedLink"),
      error: t("copyFailed"),
    });
  };

  const columns = useMemo<ColumnDef<InviteCodeRow>[]>(() => {
    const cols: ColumnDef<InviteCodeRow>[] = [
      {
        accessorKey: "code",
        header: ({ column }) => <DataTableColumnHeader column={column} title={t("code")} />,
        cell: ({ row }) => <CopyableText text={row.original.code} />,
      },
      {
        accessorKey: "note",
        header: t("note"),
        cell: ({ row }) => row.original.note || "—",
      },
    ];
    if (isAdmin) {
      cols.push({
        accessorKey: "creator_id",
        header: t("creator"),
        cell: ({ row }) => <EntityLabel entity="user" id={row.original.creator_id} />,
      });
    }
    cols.push(
      {
        id: "usage",
        header: t("usage"),
        cell: ({ row }) => `${row.original.used_count} / ${row.original.max_uses}`,
      },
      {
        accessorKey: "expires_at",
        header: ({ column }) => <DataTableColumnHeader column={column} title={t("expiresAt")} />,
        cell: ({ row }) => <DateCell timestamp={row.original.expires_at} />,
      },
      {
        accessorKey: "created_at",
        header: ({ column }) => <DataTableColumnHeader column={column} title={t("createdAt")} />,
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
              <DropdownMenuItem onClick={() => copyLink(row.original.code)}>
                {t("copyLink")}
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
    );
    return cols;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin, t, tc]);

  if (!config) return <InvitesPageSkeleton />;

  if (!config.invite_enabled) {
    return (
      <InvitesPageLayout>
        <Card>
          <CardContent className="pt-6 text-center text-muted-foreground">
            {t("disabled")}
          </CardContent>
        </Card>
      </InvitesPageLayout>
    );
  }

  const canCreate = isAdmin || (config.invite_user_max_codes ?? 0) > 0;
  if (!canCreate) {
    return (
      <InvitesPageLayout>
        <Card>
          <CardContent className="pt-6 text-center text-muted-foreground">
            {t("unavailable")}
          </CardContent>
        </Card>
      </InvitesPageLayout>
    );
  }

  const rows = active.data?.data ?? [];
  const total = active.data?.total ?? 0;
  const pageCount = Math.ceil(total / pageSize) || 1;

  return (
    <InvitesPageLayout>
      <div className="space-y-4">
      <DataTable
        columns={columns}
        data={rows}
        loading={active.isLoading}
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
            primaryAction={<CreateInviteDialog />}
          />
        }
      />
      <DeleteConfirm
        open={deleteItem !== null}
        onOpenChange={(o) => {
          if (!o) setDeleteItem(null);
        }}
        onConfirm={confirmDelete}
      />
      </div>
    </InvitesPageLayout>
  );
}

export default function InvitesPage() {
  return (
    <Suspense fallback={<InvitesPageSkeleton />}>
      <InvitesPageInner />
    </Suspense>
  );
}

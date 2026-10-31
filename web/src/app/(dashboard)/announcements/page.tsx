"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import type { ColumnDef } from "@tanstack/react-table";
import { Loader2, Plus } from "lucide-react";
import { toast } from "sonner";

import { DateCell } from "@/components/business/date-cell";
import { EntityLabel } from "@/components/business/entity-label";
import { DataTable } from "@/components/data-table/data-table";
import { PageLayout } from "@/components/layout/page-layout";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { type AnnouncementSummary, useAdminAnnouncements, useAnnouncementDetail, useDeleteAnnouncement } from "@/lib/api/announcements";
import { formatErrorToast } from "@/lib/api/error-toast";
import { useAuth } from "@/lib/auth";
import { AnnouncementFormDialog } from "./_form-dialog";

function AnnouncementManagement() {
  const t = useTranslations("announcements");
  const tc = useTranslations("common");
  const { user } = useAuth();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const query = useAdminAnnouncements(page, pageSize, user?.user_id);
  const total = query.data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  if (!query.isPlaceholderData && query.data?.page === page && query.data.page_size === pageSize && page > totalPages) {
    setPage(totalPages);
  }
  const handlePaginationChange = (nextPage: number, nextPageSize: number) => {
    setPage(nextPageSize === pageSize ? nextPage : 1);
    setPageSize(nextPageSize);
  };
  const remove = useDeleteAnnouncement();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const [deleting, setDeleting] = useState<AnnouncementSummary | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const handleDelete = async () => {
    if (!deleting || remove.isPending) return;
    setDeleteError(null);
    try {
      await remove.mutateAsync(deleting.id);
      toast.success(tc("success"));
      setDeleting(null);
    } catch (error) {
      setDeleteError(formatErrorToast(error, tc("error")));
    }
  };

  const columns: ColumnDef<AnnouncementSummary>[] = [
    {
      accessorKey: "title",
      header: t("announcementTitle"),
      cell: ({ row }) => (
        <div className="flex max-w-xs items-start gap-2">
          {row.original.pinned && <Badge variant="secondary">{t("pinned")}</Badge>}
          <span className="whitespace-normal break-words font-medium">{row.original.title}</span>
        </div>
      ),
    },
    {
      id: "groups",
      header: t("groups"),
      cell: ({ row }) => row.original.group_ids.length === 0 ? t("allLoggedIn") : (
        <div className="flex max-w-xs flex-wrap gap-2">
          {row.original.group_ids.map((id) => <EntityLabel key={id} entity="user-group" scope="all" id={id} />)}
        </div>
      ),
    },
    { accessorKey: "show_for_guests", header: t("showForGuests"), cell: ({ row }) => row.original.show_for_guests ? tc("enabled") : tc("disabled") },
    { accessorKey: "created_at", header: tc("createdAt"), cell: ({ row }) => <DateCell timestamp={row.original.created_at} /> },
    { accessorKey: "updated_at", header: tc("updatedAt"), cell: ({ row }) => <DateCell timestamp={row.original.updated_at} /> },
    {
      id: "actions",
      header: tc("actions"),
      cell: ({ row }) => (
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => setEditing(row.original.id)}>{tc("edit")}</Button>
          <Button variant="destructive" size="sm" onClick={() => { setDeleteError(null); setDeleting(row.original); }}>{tc("delete")}</Button>
        </div>
      ),
    },
  ];

  return (
    <PageLayout title={t("title")} description={t("description")} maxWidth="full" actions={<Button size="sm" onClick={() => setCreateOpen(true)}><Plus className="mr-2 size-4" />{t("create")}</Button>}>
      {query.isError ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle>{t("loadFailed")}</AlertTitle>
          <AlertDescription className="space-y-2">
            <p>{formatErrorToast(query.error, tc("error"))}</p>
            <Button variant="outline" size="sm" disabled={query.isFetching} onClick={() => { void query.refetch(); }}>{tc("retry")}</Button>
          </AlertDescription>
        </Alert>
      ) : null}
      <DataTable
        columns={columns}
        data={query.data?.items ?? []}
        loading={query.isLoading && !query.data}
        total={total}
        page={page}
        pageSize={pageSize}
        pageCount={totalPages}
        paginationDisabled={query.isFetching}
        onPaginationChange={handlePaginationChange}
      />
      {createOpen && <AnnouncementFormDialog open onOpenChange={setCreateOpen} />}
      {editing !== null && <AnnouncementEditDialog key={editing} id={editing} userId={user?.user_id} onClose={() => setEditing(null)} />}
      <Dialog open={deleting !== null} onOpenChange={(open) => { if (!open && !remove.isPending) setDeleting(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("delete")}</DialogTitle>
            <DialogDescription>{t("deleteConfirm", { title: deleting?.title ?? "" })}</DialogDescription>
          </DialogHeader>
          {deleteError && <Alert variant="destructive" role="alert"><AlertDescription>{deleteError}</AlertDescription></Alert>}
          <DialogFooter>
            <Button variant="outline" disabled={remove.isPending} onClick={() => setDeleting(null)}>{tc("cancel")}</Button>
            <Button variant="destructive" disabled={remove.isPending} onClick={() => void handleDelete()}>{remove.isPending && <Loader2 className="mr-2 size-4 animate-spin" />}{tc("confirm")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageLayout>
  );
}

function AnnouncementEditDialog({ id, userId, onClose }: { id: number; userId?: number; onClose: () => void }) {
  const t = useTranslations("announcements");
  const tc = useTranslations("common");
  const detail = useAnnouncementDetail(id, userId, true);
  if (detail.data && !detail.isFetching && !detail.isError) {
    return <AnnouncementFormDialog open initial={detail.data} onOpenChange={(open) => { if (!open) onClose(); }} />;
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t("edit")}</DialogTitle><DialogDescription>{t("editHint")}</DialogDescription></DialogHeader>
        {detail.isError ? (
          <Alert variant="destructive" role="alert">
            <AlertDescription className="space-y-2">
              <p>{formatErrorToast(detail.error, tc("error"))}</p>
              <Button variant="outline" disabled={detail.isFetching} onClick={() => { void detail.refetch(); }}>{tc("retry")}</Button>
            </AlertDescription>
          </Alert>
        ) : <p role="status">{tc("loading")}</p>}
        <DialogFooter><Button variant="outline" onClick={onClose}>{tc("cancel")}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function AnnouncementsPage() {
  const t = useTranslations("announcements");
  const tc = useTranslations("common");
  const { user, isAdmin, loading } = useAuth();
  if (loading) return <PageLayout title={t("title")}><p role="status">{tc("loading")}</p></PageLayout>;
  if (!isAdmin) {
    return <PageLayout title={t("title")}><Alert variant="destructive"><AlertTitle>{t("permissionDenied")}</AlertTitle></Alert></PageLayout>;
  }
  return <AnnouncementManagement key={user?.user_id} />;
}

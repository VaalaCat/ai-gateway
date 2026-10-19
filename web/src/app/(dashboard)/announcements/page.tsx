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
import { type Announcement, useAdminAnnouncements, useDeleteAnnouncement } from "@/lib/api/announcements";
import { formatErrorToast } from "@/lib/api/error-toast";
import { useAuth } from "@/lib/auth";
import { AnnouncementFormDialog } from "./_form-dialog";

function AnnouncementManagement() {
  const t = useTranslations("announcements");
  const tc = useTranslations("common");
  const query = useAdminAnnouncements();
  const remove = useDeleteAnnouncement();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<Announcement | null>(null);
  const [deleting, setDeleting] = useState<Announcement | null>(null);
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

  const columns: ColumnDef<Announcement>[] = [
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
    { accessorKey: "content", header: t("content"), cell: ({ row }) => <p className="line-clamp-3 max-w-md whitespace-pre-wrap break-words">{row.original.content}</p> },
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
          <Button variant="outline" size="sm" onClick={() => setEditing(row.original)}>{tc("edit")}</Button>
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
            <Button variant="outline" size="sm" disabled={query.isFetching} onClick={() => void query.refetch()}>{tc("retry")}</Button>
          </AlertDescription>
        </Alert>
      ) : (
        <DataTable columns={columns} data={query.data ?? []} loading={query.isLoading} showFooter={false} />
      )}
      {createOpen && <AnnouncementFormDialog open onOpenChange={setCreateOpen} />}
      {editing && <AnnouncementFormDialog key={editing.id} open initial={editing} onOpenChange={(open) => { if (!open) setEditing(null); }} />}
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

export default function AnnouncementsPage() {
  const t = useTranslations("announcements");
  const tc = useTranslations("common");
  const { isAdmin, loading } = useAuth();
  if (loading) return <PageLayout title={t("title")}><p role="status">{tc("loading")}</p></PageLayout>;
  if (!isAdmin) {
    return <PageLayout title={t("title")}><Alert variant="destructive"><AlertTitle>{t("permissionDenied")}</AlertTitle></Alert></PageLayout>;
  }
  return <AnnouncementManagement />;
}

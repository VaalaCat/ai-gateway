"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { EntityMultiPicker } from "@/components/business/entity-picker/entity-multi-picker";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { type Announcement, useCreateAnnouncement, useUpdateAnnouncement } from "@/lib/api/announcements";
import { formatErrorToast } from "@/lib/api/error-toast";

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  initial?: Announcement;
}

export function AnnouncementFormDialog({ open, onOpenChange, initial }: Props) {
  const t = useTranslations("announcements");
  const tc = useTranslations("common");
  const create = useCreateAnnouncement();
  const update = useUpdateAnnouncement();
  const [title, setTitle] = useState(initial?.title ?? "");
  const [content, setContent] = useState(initial?.content ?? "");
  const [groupIds, setGroupIds] = useState(initial?.group_ids.map(String) ?? []);
  const [showForGuests, setShowForGuests] = useState(initial?.show_for_guests ?? true);
  const [pinned, setPinned] = useState(initial?.pinned ?? false);
  const [reannounce, setReannounce] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = create.isPending || update.isPending;

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;
    const body = { title: title.trim(), content: content.trim(), group_ids: groupIds.map(Number), show_for_guests: showForGuests, pinned };
    if (!body.title || !body.content) {
      setError(t("required"));
      return;
    }
    if (Array.from(body.title).length > 200 || Array.from(body.content).length > 20000) {
      setError(t("tooLong"));
      return;
    }
    setError(null);
    try {
      if (initial) await update.mutateAsync({ id: initial.id, ...body, reannounce });
      else await create.mutateAsync(body);
      toast.success(tc("success"));
      onOpenChange(false);
    } catch (cause) {
      setError(formatErrorToast(cause, tc("error")));
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!pending) onOpenChange(next); }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{initial ? t("edit") : t("create")}</DialogTitle>
          <DialogDescription>{t("editHint")}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate className="space-y-4">
          {error && <Alert variant="destructive" role="alert"><AlertDescription>{error}</AlertDescription></Alert>}
          <fieldset disabled={pending} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="announcement-title">{t("announcementTitle")}</Label>
              <Input id="announcement-title" value={title} onChange={(event) => setTitle(event.target.value)} required aria-describedby="announcement-title-hint" />
              <p id="announcement-title-hint" className="text-xs text-muted-foreground">{t("titleHint")}</p>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="announcement-content">{t("content")}</Label>
              <Textarea id="announcement-content" value={content} onChange={(event) => setContent(event.target.value)} rows={8} required aria-describedby="announcement-content-hint" />
              <p id="announcement-content-hint" className="text-xs text-muted-foreground">{t("contentHint")}</p>
            </div>
            <div className="space-y-1.5">
              <Label>{t("groups")}</Label>
              <EntityMultiPicker entity="user-group" scope="all" value={groupIds} onChange={setGroupIds} disabled={pending} />
              <p className="text-xs text-muted-foreground">{t("groupsHint")}</p>
            </div>
            <div className="space-y-1.5">
              <div className="flex items-center gap-2">
                <Switch id="announcement-guests" checked={showForGuests} onCheckedChange={setShowForGuests} disabled={pending} aria-describedby="announcement-guests-hint" />
                <Label htmlFor="announcement-guests">{t("showForGuests")}</Label>
              </div>
              <p id="announcement-guests-hint" className="text-xs text-muted-foreground">{t("guestsHint")}</p>
            </div>
            <div className="flex items-center gap-2">
              <Switch id="announcement-pinned" checked={pinned} onCheckedChange={setPinned} disabled={pending} />
              <Label htmlFor="announcement-pinned">{t("pinned")}</Label>
            </div>
            {initial && (
              <div className="space-y-1.5">
                <div className="flex items-center gap-2">
                  <Switch id="announcement-reannounce" checked={reannounce} onCheckedChange={setReannounce} disabled={pending} aria-describedby="announcement-reannounce-hint" />
                  <Label htmlFor="announcement-reannounce">{t("reannounce")}</Label>
                </div>
                <p id="announcement-reannounce-hint" className="text-xs text-muted-foreground">{t("reannounceHint")}</p>
              </div>
            )}
          </fieldset>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={pending} onClick={() => onOpenChange(false)}>{tc("cancel")}</Button>
            <Button type="submit" disabled={pending}>{pending && <Loader2 className="mr-2 size-4 animate-spin" />}{tc("save")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

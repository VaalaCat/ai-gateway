"use client";

import { useState, useEffect } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { useRouter } from "next/navigation";
import { Trash2 } from "lucide-react";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { StatusSelect } from "@/components/business/status-select";
import { TagInput } from "@/components/ui/tag-input";
import { ChannelMultiSelect } from "@/components/business/channel-multi-select";
import { DeleteConfirm } from "@/components/business/delete-confirm";

import { ApiError } from "@/lib/api/client";
import { formatErrorToast } from "@/lib/api/error-toast";
import { useUpdateUserGroup, useDeleteUserGroup } from "@/lib/api/user-groups";
import { parseModels, serializeModels } from "@/lib/parse-models";
import type { UserGroup } from "@/lib/types";
import { BYOKCard } from "./byok-card";

export function OverviewTab({ group, isDefault }: { group: UserGroup; isDefault: boolean }) {
  return (
    <div className="space-y-4">
      <BasicInfoCard group={group} isDefault={isDefault} />
      <PermissionsCard group={group} />
      <BYOKCard group={group} />
    </div>
  );
}

function BasicInfoCard({ group, isDefault }: { group: UserGroup; isDefault: boolean }) {
  const t = useTranslations("userGroups");
  const tc = useTranslations("common");
  const updateMutation = useUpdateUserGroup();

  const [form, setForm] = useState({
    name: group.name,
    description: group.description ?? "",
    status: String(group.status),
  });

  // Reset only when navigating to a different group (not on react-query refetch
  // of the same group, which would clobber unsaved edits made in the sibling
  // PermissionsCard).
  useEffect(() => {
    setForm({
      name: group.name,
      description: group.description ?? "",
      status: String(group.status),
    });
    // Reset on group change only — see comment above.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [group.id]);

  const dirty =
    form.name !== group.name ||
    form.description !== (group.description ?? "") ||
    form.status !== String(group.status);

  const handleSave = async () => {
    try {
      await updateMutation.mutateAsync({
        id: group.id,
        name: form.name,
        description: form.description,
        status: Number(form.status),
      });
      toast.success(t("updateSuccess"));
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        toast.error(t("nameConflict"));
      } else {
        toast.error(tc("error"));
      }
    }
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <CardTitle className="text-base">{t("basicInfo")}</CardTitle>
        {!isDefault && <DeleteEntry id={group.id} />}
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label>{t("name")}</Label>
          <Input
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            disabled={isDefault}
            maxLength={64}
          />
          {isDefault && (
            <p className="text-xs text-muted-foreground">{t("nameLockedTip")}</p>
          )}
        </div>
        <div className="space-y-2">
          <Label>{t("description")}</Label>
          <Textarea
            value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}
            maxLength={255}
            rows={2}
          />
        </div>
        <div className="space-y-2">
          <Label>{t("status")}</Label>
          <StatusSelect
            value={form.status}
            onChange={(v) => setForm({ ...form, status: v })}
            showLabel={false}
            disabled={isDefault}
          />
          {isDefault && (
            <p className="text-xs text-muted-foreground">{t("statusLockedTip")}</p>
          )}
        </div>
        <div className="flex justify-end">
          <Button
            onClick={handleSave}
            disabled={!dirty || updateMutation.isPending || !form.name.trim()}
          >
            {tc("save")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function PermissionsCard({ group }: { group: UserGroup }) {
  const t = useTranslations("userGroups");
  const tc = useTranslations("common");
  const updateMutation = useUpdateUserGroup();

  const [channels, setChannels] = useState<number[]>(group.allowed_channel_ids ?? []);
  const [models, setModels] = useState<string[]>(parseModels(group.models));

  useEffect(() => {
    setChannels(group.allowed_channel_ids ?? []);
    setModels(parseModels(group.models));
    // Reset on group change only — avoid wiping unsaved edits when sibling card saves.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [group.id]);

  const channelsDirty =
    JSON.stringify([...channels].sort()) !==
    JSON.stringify([...(group.allowed_channel_ids ?? [])].sort());
  const modelsDirty =
    JSON.stringify([...models].sort()) !==
    JSON.stringify([...parseModels(group.models)].sort());
  const dirty = channelsDirty || modelsDirty;

  const handleSave = async () => {
    try {
      await updateMutation.mutateAsync({
        id: group.id,
        allowed_channel_ids: channels,
        models: serializeModels(models),
      });
      toast.success(t("updateSuccess"));
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("permissions")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label>{t("channels")}</Label>
          <ChannelMultiSelect value={channels} onChange={setChannels} />
        </div>
        <div className="space-y-2">
          <Label>{t("models")}</Label>
          <TagInput value={models} onChange={setModels} />
          <p className="text-xs text-muted-foreground">{t("modelsHint")}</p>
        </div>
        <div className="flex justify-end">
          <Button
            onClick={handleSave}
            disabled={!dirty || updateMutation.isPending}
          >
            {tc("save")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function DeleteEntry({ id }: { id: number }) {
  const t = useTranslations("userGroups");
  const tc = useTranslations("common");
  const router = useRouter();
  const deleteMutation = useDeleteUserGroup();
  const [open, setOpen] = useState(false);

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(id);
      toast.success(t("deleteSuccess"));
      setOpen(false);
      router.push("/groups");
    } catch (e) {
      toast.error(formatErrorToast(e, tc("error")));
    }
  };

  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className="size-8 text-destructive"
        onClick={() => setOpen(true)}
      >
        <Trash2 className="size-4" />
      </Button>
      <DeleteConfirm
        open={open}
        onOpenChange={setOpen}
        onConfirm={handleDelete}
        title={t("deleteConfirmTitle")}
        description={t("deleteConfirmDesc")}
      />
    </>
  );
}

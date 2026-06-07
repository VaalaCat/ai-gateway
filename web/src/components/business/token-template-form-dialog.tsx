"use client";

import { useEffect, useState } from "react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { TagInput } from "@/components/ui/tag-input";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { StatusSelect } from "@/components/business/status-select";
import { EntityMultiPicker } from "@/components/business/entity-picker/entity-multi-picker";
import { parseModels, serializeModels } from "@/lib/parse-models";
import type { TokenTemplate } from "@/lib/types";

export interface TokenTemplateFormValues {
  name: string;
  models: string;
  expiry_days: string;
  status: string;
  allowed_channel_ids: number[];
  allowed_group_ids: number[];
}

const EMPTY: TokenTemplateFormValues = {
  name: "",
  models: "",
  expiry_days: "-1",
  status: "1",
  allowed_channel_ids: [],
  allowed_group_ids: [],
};

function fromTemplate(tpl: TokenTemplate): TokenTemplateFormValues {
  return {
    name: tpl.name,
    models: tpl.models ?? "",
    expiry_days: String(tpl.expiry_days),
    status: String(tpl.status),
    allowed_channel_ids: tpl.allowed_channel_ids ?? [],
    allowed_group_ids: tpl.allowed_group_ids ?? [],
  };
}

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** 有值=编辑，null/undefined=新建 */
  template?: TokenTemplate | null;
  onSubmit: (values: TokenTemplateFormValues) => Promise<void>;
  pending?: boolean;
}

export function TokenTemplateFormDialog({ open, onOpenChange, template, onSubmit, pending }: Props) {
  const t = useTranslations("tokenTemplates");
  const tc = useTranslations("common");
  const [form, setForm] = useState<TokenTemplateFormValues>(EMPTY);

  useEffect(() => {
    if (open) setForm(template ? fromTemplate(template) : EMPTY);
  }, [open, template]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{template ? t("edit") : t("create")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="space-y-2">
            <Label>{t("name")}</Label>
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          <div className="space-y-2">
            <Label>{t("models")}</Label>
            <TagInput
              value={parseModels(form.models)}
              onChange={(tags) => setForm({ ...form, models: serializeModels(tags) })}
              placeholder={t("modelsPlaceholder")}
            />
            <p className="text-xs text-muted-foreground">{t("modelsHint")}</p>
          </div>
          <div className="space-y-2">
            <Label>{t("allowedChannels")}</Label>
            <EntityMultiPicker
              entity="channel"
              value={form.allowed_channel_ids.map(String)}
              onChange={(vals) => setForm({ ...form, allowed_channel_ids: vals.map(Number) })}
              placeholder={t("allowedChannelsPlaceholder")}
            />
            <p className="text-xs text-muted-foreground">{t("allowedChannelsEmptyHint")}</p>
          </div>
          <div className="space-y-2">
            <Label>{t("allowedGroups")}</Label>
            <EntityMultiPicker
              entity="user-group"
              value={form.allowed_group_ids.map(String)}
              onChange={(vals) => setForm({ ...form, allowed_group_ids: vals.map(Number) })}
              placeholder={t("allowedGroupsPlaceholder")}
            />
            <p className="text-xs text-muted-foreground">{t("allowedGroupsEmptyHint")}</p>
          </div>
          <div className="space-y-2">
            <Label>{t("expiryDays")}</Label>
            <Input
              type="number"
              value={form.expiry_days}
              onChange={(e) => setForm({ ...form, expiry_days: e.target.value })}
            />
            <p className="text-xs text-muted-foreground">{t("expiryDaysHint")}</p>
          </div>
          <StatusSelect value={form.status} onChange={(v) => setForm({ ...form, status: v })} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>{tc("cancel")}</Button>
          <Button onClick={() => onSubmit(form)} disabled={pending}>{tc("save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

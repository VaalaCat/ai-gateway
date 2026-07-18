"use client";

import { useEffect, useEffectEvent, useState } from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { SyncPreviewItem, SyncPreviewResponse, TokenTemplate } from "@/lib/types";
import {
  usePreviewSyncTokenTemplate,
  useSyncTokenTemplate,
} from "@/lib/api/token-templates";
import { formatErrorToast } from "@/lib/api/error-toast";
import { EntityLabel } from "@/components/business/entity-label";
import type { EntityName } from "@/components/business/entity-picker/registry";

interface Props {
  template: TokenTemplate | null;
  onOpenChange: (open: boolean) => void;
}

const DEFAULT_FIELDS = ["models", "channels"];

function parseModelsArr(s: string): string[] {
  if (!s) return [];
  try {
    const v = JSON.parse(s);
    return Array.isArray(v) ? v.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function diff<T>(after: T[], before: T[]): { added: T[]; removed: T[] } {
  const beforeSet = new Set(before);
  const afterSet = new Set(after);
  return {
    added: after.filter((x) => !beforeSet.has(x)),
    removed: before.filter((x) => !afterSet.has(x)),
  };
}

function DiffCell({
  beforeArr,
  afterArr,
  entity,
}: {
  beforeArr: (string | number)[];
  afterArr: (string | number)[];
  entity?: EntityName;
}) {
  const { added, removed } = diff(afterArr, beforeArr);
  if (added.length === 0 && removed.length === 0) {
    return <span className="text-xs text-muted-foreground">-</span>;
  }
  return (
    <span className="text-xs font-mono tabular-nums">
      {added.map((m) => (
        <span key={`a-${m}`} className="text-green-600 font-medium mr-1">
          +{entity ? <EntityLabel entity={entity} id={Number(m)} showId={false} /> : m}
        </span>
      ))}
      {removed.map((m) => (
        <span key={`r-${m}`} className="text-destructive font-medium mr-1">
          -{entity ? <EntityLabel entity={entity} id={Number(m)} showId={false} /> : m}
        </span>
      ))}
    </span>
  );
}

function DiffRow({ item, showByokOnly }: { item: SyncPreviewItem; showByokOnly: boolean }) {
  const before = parseModelsArr(item.models_before);
  const after = parseModelsArr(item.models_after);
  return (
    <TableRow>
      <TableCell className="font-medium">{item.token_name}</TableCell>
      <TableCell><DiffCell beforeArr={before} afterArr={after} /></TableCell>
      <TableCell>
        <DiffCell beforeArr={item.channels_before ?? []} afterArr={item.channels_after ?? []} entity="channel" />
      </TableCell>
      {showByokOnly && (
        <TableCell>
          <DiffCell
            beforeArr={item.byok_only_before !== undefined ? [String(item.byok_only_before)] : []}
            afterArr={item.byok_only_after !== undefined ? [String(item.byok_only_after)] : []}
          />
        </TableCell>
      )}
    </TableRow>
  );
}

export function TokenTemplateSyncDialog({ template, onOpenChange }: Props) {
  const t = useTranslations("tokenTemplates");
  const tc = useTranslations("common");

  const previewMut = usePreviewSyncTokenTemplate();
  const previewAsync = previewMut.mutateAsync;
  const syncMut = useSyncTokenTemplate();

  const [fields, setFields] = useState<string[]>(DEFAULT_FIELDS);
  const [preview, setPreview] = useState<SyncPreviewResponse | null>(null);
  const handlePreviewFailure = useEffectEvent(() => {
    toast.error(t("sync.previewFailed"));
    onOpenChange(false);
  });
  useEffect(() => {
    if (!template) return;
    previewAsync({ id: template.id, fields })
      .then(setPreview)
      .catch(handlePreviewFailure);
  }, [fields, previewAsync, template]);

  const handleConfirm = async () => {
    if (!template) return;
    try {
      const r = await syncMut.mutateAsync({ id: template.id, fields });
      toast.success(t("sync.success", { count: r.synced }));
      onOpenChange(false);
    } catch (e) {
      toast.error(formatErrorToast(e, t("sync.syncFailed")));
    }
  };

  const loading = previewMut.isPending && !preview;
  const changed = preview?.changed ?? 0;
  const showByokOnly = fields.includes("byok_only");

  return (
    <Dialog open={!!template} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {template ? t("sync.title", { name: template.name }) : ""}
          </DialogTitle>
        </DialogHeader>

        <div className="flex items-center gap-4">
          {[
            { key: "models", label: t("syncFieldModels") },
            { key: "channels", label: t("syncFieldChannels") },
            { key: "byok_only", label: t("syncFieldBYOKOnly") },
          ].map((it) => (
            <label key={it.key} className="flex items-center gap-2">
              <Checkbox
                checked={fields.includes(it.key)}
                onCheckedChange={(c) =>
                  setFields((prev) => (c ? [...prev, it.key] : prev.filter((k) => k !== it.key)))
                }
              />
              <span>{it.label}</span>
            </label>
          ))}
        </div>

        {loading && (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="size-6 animate-spin text-muted-foreground" />
          </div>
        )}

        {!loading && preview && (
          <>
            <p className="text-sm text-muted-foreground">
              {t("sync.summary", { total: preview.total, changed: preview.changed })}
            </p>
            {changed === 0 ? (
              <p className="text-sm">{t("sync.noChanges")}</p>
            ) : (
              <>
                <div className="max-h-[60vh] overflow-auto rounded-md border">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t("sync.tokenName")}</TableHead>
                        <TableHead>{t("sync.modelsChange")}</TableHead>
                        <TableHead>{t("sync.channelsChange")}</TableHead>
                        {showByokOnly && <TableHead>{t("syncFieldBYOKOnly")}</TableHead>}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {preview.items.map((it) => (
                        <DiffRow key={it.token_id} item={it} showByokOnly={showByokOnly} />
                      ))}
                    </TableBody>
                  </Table>
                </div>
                <p className="text-sm text-destructive mt-2">{t("sync.warning")}</p>
              </>
            )}
          </>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {tc("cancel")}
          </Button>
          {changed > 0 && (
            <Button onClick={handleConfirm} disabled={syncMut.isPending}>
              {syncMut.isPending && <Loader2 className="mr-2 size-4 animate-spin" />}
              {t("sync.confirm", { count: changed })}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

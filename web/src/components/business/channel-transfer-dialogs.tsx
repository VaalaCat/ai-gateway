"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { AlertTriangle, Download, Loader2, Upload } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import {
  commitChannelImport,
  downloadChannelExport,
  previewChannelImport,
  type ChannelExportMode,
  type ChannelImportPreview,
  type ChannelTransferFilter,
} from "@/lib/api/channel-transfer";
import { formatErrorToast } from "@/lib/api/error-toast";

const MAX_FILE_BYTES = 16 * 1024 * 1024;

interface ChannelImportDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  path: string;
  onImported: () => void | Promise<void>;
}

export function ChannelImportDialog({ open, onOpenChange, path, onImported }: ChannelImportDialogProps) {
  const t = useTranslations("channelTransfer");
  const tc = useTranslations("common");
  const [rawFile, setRawFile] = useState("");
  const [fileName, setFileName] = useState("");
  const [preview, setPreview] = useState<ChannelImportPreview | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [importing, setImporting] = useState(false);

  const reset = () => {
    setRawFile("");
    setFileName("");
    setPreview(null);
    setPreviewing(false);
    setImporting(false);
  };

  const handleOpenChange = (next: boolean) => {
    if (!next && (previewing || importing)) return;
    if (!next) reset();
    onOpenChange(next);
  };

  const handleFile = async (file: File | undefined) => {
    reset();
    if (!file) return;
    if (file.size > MAX_FILE_BYTES) {
      toast.error(t("fileTooLarge"));
      return;
    }
    setFileName(file.name);
    setPreviewing(true);
    try {
      const raw = await file.text();
      const next = await previewChannelImport(path, raw);
      setRawFile(raw);
      setPreview(next);
    } catch (error) {
      toast.error(formatErrorToast(error, t("previewFailed")));
    } finally {
      setPreviewing(false);
    }
  };

  const handleImport = async () => {
    if (!rawFile || !preview || preview.failed > 0 || preview.total === 0) return;
    setImporting(true);
    try {
      const result = await commitChannelImport(path, rawFile);
      await onImported();
      toast.success(t("imported", { count: result.created }));
      reset();
      onOpenChange(false);
    } catch (error) {
      toast.error(formatErrorToast(error, t("importFailed")));
    } finally {
      setImporting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("importTitle")}</DialogTitle>
          <DialogDescription>{t("importDescription")}</DialogDescription>
        </DialogHeader>

        <FieldGroup className="gap-4">
          <Alert>
            <AlertTriangle />
            <AlertTitle>{t("plaintextTitle")}</AlertTitle>
            <AlertDescription>{t("plaintextImportDescription")}</AlertDescription>
          </Alert>

          <Field>
            <FieldLabel htmlFor="channel-import-file">{t("file")}</FieldLabel>
            <Input
              id="channel-import-file"
              type="file"
              accept="application/json,.json"
              disabled={previewing || importing}
              onChange={(event) => {
                const file = event.currentTarget.files?.[0];
                event.currentTarget.value = "";
                void handleFile(file);
              }}
            />
            <FieldDescription>{fileName || t("fileLimit")}</FieldDescription>
          </Field>

          {previewing && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="animate-spin" />
              {t("previewing")}
            </div>
          )}

          {preview && <ImportPreview preview={preview} />}
        </FieldGroup>

        <DialogFooter>
          <Button variant="outline" onClick={() => handleOpenChange(false)} disabled={previewing || importing}>
            {tc("cancel")}
          </Button>
          <Button
            onClick={handleImport}
            disabled={!preview || preview.failed > 0 || preview.total === 0 || importing}
          >
            {importing ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Upload data-icon="inline-start" />}
            {importing ? t("importing") : t("confirmImport")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ImportPreview({ preview }: { preview: ChannelImportPreview }) {
  const t = useTranslations("channelTransfer");
  return (
    <div className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline">{t("total", { count: preview.total })}</Badge>
        <Badge variant="secondary">{t("ready", { count: preview.ready })}</Badge>
        {preview.failed > 0 && <Badge variant="destructive">{t("failed", { count: preview.failed })}</Badge>}
      </div>
      <ScrollArea className="h-64 rounded-md border">
        <div className="flex flex-col">
          {preview.items.map((item, index) => (
            <div key={`${item.index}-${item.source_name}`}>
              <div className="flex min-w-0 flex-col gap-1.5 p-3 text-sm sm:flex-row sm:items-start sm:justify-between">
                <div className="min-w-0">
                  <div className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-medium" title={item.source_name}>{item.source_name}</span>
                    {item.final_name && item.final_name !== item.source_name && (
                      <>
                        <span className="text-muted-foreground">-&gt;</span>
                        <span className="truncate font-medium" title={item.final_name}>{item.final_name}</span>
                      </>
                    )}
                  </div>
                  {item.error && <p className="mt-1 text-xs text-destructive">{item.error.message}</p>}
                </div>
                <Badge variant={item.error ? "destructive" : item.warnings.length > 0 ? "outline" : "secondary"}>
                  {item.error ? t("itemFailed") : item.warnings.length > 0 ? t("renamed") : t("itemReady")}
                </Badge>
              </div>
              {index < preview.items.length - 1 && <Separator />}
            </div>
          ))}
        </div>
      </ScrollArea>
    </div>
  );
}

interface ChannelExportDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  path: string;
  selectedIds: number[];
  filter: ChannelTransferFilter;
}

export function ChannelExportDialog({ open, onOpenChange, path, selectedIds, filter }: ChannelExportDialogProps) {
  const hasSelection = selectedIds.length > 0;
  return (
    <ChannelExportDialogSession
      key={`${open}:${hasSelection}`}
      open={open}
      onOpenChange={onOpenChange}
      path={path}
      selectedIds={selectedIds}
      filter={filter}
    />
  );
}

function ChannelExportDialogSession({ open, onOpenChange, path, selectedIds, filter }: ChannelExportDialogProps) {
  const t = useTranslations("channelTransfer");
  const tc = useTranslations("common");
  const [mode, setMode] = useState<ChannelExportMode>(selectedIds.length > 0 ? "ids" : "filter");
  const [exporting, setExporting] = useState(false);

  const handleExport = async () => {
    setExporting(true);
    try {
      await downloadChannelExport(path, mode === "ids" ? { mode, ids: selectedIds } : { mode, filter });
      toast.success(t("exported"));
      onOpenChange(false);
    } catch (error) {
      toast.error(formatErrorToast(error, t("exportFailed")));
    } finally {
      setExporting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !exporting && onOpenChange(next)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("exportTitle")}</DialogTitle>
          <DialogDescription>{t("exportDescription")}</DialogDescription>
        </DialogHeader>

        <FieldGroup className="gap-4">
          <Alert>
            <AlertTriangle />
            <AlertTitle>{t("plaintextTitle")}</AlertTitle>
            <AlertDescription>{t("plaintextExportDescription")}</AlertDescription>
          </Alert>

          <FieldSet>
            <FieldLegend variant="label">{t("exportScope")}</FieldLegend>
            <RadioGroup value={mode} onValueChange={(value) => setMode(value as ChannelExportMode)}>
              <Field orientation="horizontal" data-disabled={selectedIds.length === 0}>
                <RadioGroupItem id="channel-export-selected" value="ids" disabled={selectedIds.length === 0} />
                <FieldContent>
                  <FieldLabel htmlFor="channel-export-selected">{t("selected", { count: selectedIds.length })}</FieldLabel>
                  <FieldDescription>{t("selectedDescription")}</FieldDescription>
                </FieldContent>
              </Field>
              <Field orientation="horizontal">
                <RadioGroupItem id="channel-export-filter" value="filter" />
                <FieldContent>
                  <FieldLabel htmlFor="channel-export-filter">{t("filtered")}</FieldLabel>
                  <FieldDescription>{t("filteredDescription")}</FieldDescription>
                </FieldContent>
              </Field>
            </RadioGroup>
          </FieldSet>
        </FieldGroup>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={exporting}>
            {tc("cancel")}
          </Button>
          <Button onClick={handleExport} disabled={exporting || (mode === "ids" && selectedIds.length === 0)}>
            {exporting ? <Loader2 data-icon="inline-start" className="animate-spin" /> : <Download data-icon="inline-start" />}
            {exporting ? t("exporting") : t("confirmExport")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

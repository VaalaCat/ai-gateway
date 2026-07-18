import { api } from "./client";

export type ChannelTransferKind = "admin_channels" | "byok_channels";
export type ChannelExportMode = "ids" | "filter";

export interface ChannelTransferFilter {
  search?: string;
  type?: string;
  status?: string;
}
export interface ChannelExportSelection {
  mode: ChannelExportMode;
  ids?: number[];
  filter?: ChannelTransferFilter;
}

export interface ChannelImportIssue {
  code: string;
  field?: string;
  message: string;
}

export interface ChannelImportPreviewItem {
  index: number;
  source_name: string;
  final_name: string;
  warnings: ChannelImportIssue[];
  error?: ChannelImportIssue;
}

export interface ChannelImportPreview {
  kind: ChannelTransferKind;
  total: number;
  ready: number;
  failed: number;
  items: ChannelImportPreviewItem[];
}

export interface ChannelImportResultItem {
  id: number;
  source_name: string;
  final_name: string;
}

export interface ChannelImportResult {
  kind: ChannelTransferKind;
  created: number;
  items: ChannelImportResultItem[];
}

export function previewChannelImport(path: string, rawFile: string) {
  return api.postRawJSON<ChannelImportPreview>(`${path}?dry_run=true`, rawFile);
}

export function commitChannelImport(path: string, rawFile: string) {
  return api.postRawJSON<ChannelImportResult>(`${path}?dry_run=false`, rawFile);
}

export async function downloadChannelExport(path: string, selection: ChannelExportSelection) {
  const result = await api.download(path, { selection });
  const href = URL.createObjectURL(result.blob);
  try {
    const anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = result.filename ?? "ai-gateway-channels.json";
    anchor.click();
  } finally {
    URL.revokeObjectURL(href);
  }
}

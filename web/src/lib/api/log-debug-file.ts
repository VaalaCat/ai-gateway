import { api } from "@/lib/api/client";
import type { UsageLog, UsageLogTrace } from "@/lib/types";

interface LogDebugFile {
  format: "ai-gateway-log-debug-v1";
  log: UsageLog;
  traces: UsageLogTrace[];
}


function debugFileRequestID(requestID: string): string {
  return requestID.replace(/[^A-Za-z0-9._-]+/g, "_").replace(/^_+|_+$/g, "") || "request";
}
export async function downloadLogDebugFile(log: UsageLog): Promise<void> {
  const traces = await api.get<UsageLogTrace[]>(`/logs/${encodeURIComponent(log.request_id)}/trace`);
  const debugFile: LogDebugFile = {
    format: "ai-gateway-log-debug-v1",
    log,
    traces,
  };
  const blob = new Blob([JSON.stringify(debugFile, null, 2)], { type: "application/json" });
  const href = URL.createObjectURL(blob);
  let anchor: HTMLAnchorElement | undefined;
  try {
    anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = `ai-gateway-log-${debugFileRequestID(log.request_id)}.debug.json`;
    anchor.click();
  } finally {
    try {
      anchor?.remove();
    } finally {
      URL.revokeObjectURL(href);
    }
  }
}

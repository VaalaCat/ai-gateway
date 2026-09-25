import {
  getAPIRequestTrace,
  type APIRequestLog,
  type APIRequestLogScope,
} from "./api-logs";

interface APIRequestLogDebugFile {
  format: "ai-gateway-api-log-debug-v1";
  log: APIRequestLog;
  trace: Awaited<ReturnType<typeof getAPIRequestTrace>>;
}

function debugFileRequestID(requestID: string): string {
  return requestID.replace(/[^A-Za-z0-9._-]+/g, "_").replace(/^_+|_+$/g, "") || "request";
}

export async function downloadAPIRequestLogDebugFile(
  log: APIRequestLog,
  scope: APIRequestLogScope,
): Promise<void> {
  const trace = await getAPIRequestTrace(log.request_id, scope);
  const debugFile: APIRequestLogDebugFile = {
    format: "ai-gateway-api-log-debug-v1",
    log,
    trace,
  };
  const blob = new Blob([JSON.stringify(debugFile, null, 2)], { type: "application/json" });
  const href = URL.createObjectURL(blob);
  let anchor: HTMLAnchorElement | undefined;
  try {
    anchor = document.createElement("a");
    anchor.href = href;
    anchor.download = `ai-gateway-api-log-${debugFileRequestID(log.request_id)}.debug.json`;
    anchor.click();
  } finally {
    try {
      anchor?.remove();
    } finally {
      URL.revokeObjectURL(href);
    }
  }
}

export const OPENAPI_RESPONSE_BYTE_LIMIT = 256 * 1024;

export interface BoundedOpenAPIResponse {
  body: string;
  truncated: boolean;
}

function abortError() {
  return new DOMException("The operation was aborted", "AbortError");
}

export async function readBoundedOpenAPIResponse(
  response: Response,
  signal?: AbortSignal,
): Promise<BoundedOpenAPIResponse> {
  if (!response.body) return { body: "", truncated: false };
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let body = "";
  let bytes = 0;
  let aborted = signal?.aborted ?? false;
  const onAbort = () => {
    aborted = true;
    void reader.cancel();
  };
  signal?.addEventListener("abort", onAbort, { once: true });

  try {
    if (aborted) {
      await reader.cancel();
      throw abortError();
    }
    while (true) {
      const result = await reader.read();
      if (aborted) throw abortError();
      if (result.done) return { body: body + decoder.decode(), truncated: false };
      const remaining = OPENAPI_RESPONSE_BYTE_LIMIT - bytes;
      if (result.value.byteLength > remaining) {
        body += decoder.decode(result.value.subarray(0, remaining), { stream: true });
        await reader.cancel();
        return { body: body + decoder.decode(), truncated: true };
      }
      bytes += result.value.byteLength;
      body += decoder.decode(result.value, { stream: true });
    }
  } finally {
    signal?.removeEventListener("abort", onAbort);
    reader.releaseLock();
  }
}

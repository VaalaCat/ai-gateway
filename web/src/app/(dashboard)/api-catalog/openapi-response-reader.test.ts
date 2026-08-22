import { describe, expect, it, vi } from "vitest";

import {
  OPENAPI_RESPONSE_BYTE_LIMIT,
  readBoundedOpenAPIResponse,
} from "./_components/openapi-response-reader";

function responseFromChunks(chunks: Uint8Array[], onCancel = vi.fn()) {
  let index = 0;
  return {
    response: new Response(new ReadableStream<Uint8Array>({
      pull(controller) {
        const chunk = chunks[index++];
        if (chunk) controller.enqueue(chunk);
        else controller.close();
      },
      cancel: onCancel,
    }, { highWaterMark: 0 })),
    onCancel,
  };
}

describe("readBoundedOpenAPIResponse", () => {
  it("reads and decodes a normal response without marking it truncated", async () => {
    const response = responseFromChunks([new TextEncoder().encode('{"ok":true}')]).response;
    await expect(readBoundedOpenAPIResponse(response)).resolves.toEqual({ body: '{"ok":true}', truncated: false });
    expect(response.body?.locked).toBe(false);
  });

  it("accepts the exact byte boundary but truncates and cancels once one more byte arrives", async () => {
    const exact = responseFromChunks([new Uint8Array(OPENAPI_RESPONSE_BYTE_LIMIT).fill(97)]);
    const overflow = responseFromChunks([
      new Uint8Array(OPENAPI_RESPONSE_BYTE_LIMIT).fill(97),
      new Uint8Array([98]),
    ]);

    await expect(readBoundedOpenAPIResponse(exact.response)).resolves.toMatchObject({
      body: "a".repeat(OPENAPI_RESPONSE_BYTE_LIMIT), truncated: false,
    });
    await expect(readBoundedOpenAPIResponse(overflow.response)).resolves.toMatchObject({
      body: "a".repeat(OPENAPI_RESPONSE_BYTE_LIMIT), truncated: true,
    });
    expect(overflow.onCancel).toHaveBeenCalledOnce();
    expect(exact.response.body?.locked).toBe(false);
    expect(overflow.response.body?.locked).toBe(false);
  });

  it("cancels an open stream and rejects with AbortError when the request scope aborts", async () => {
    const onCancel = vi.fn();
    const response = new Response(new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode("event: ready\n\n"));
      },
      cancel: onCancel,
    }));
    const controller = new AbortController();
    const reading = readBoundedOpenAPIResponse(response, controller.signal);

    controller.abort();

    await expect(reading).rejects.toMatchObject({ name: "AbortError" });
    expect(onCancel).toHaveBeenCalledOnce();
    expect(response.body?.locked).toBe(false);
  });
});

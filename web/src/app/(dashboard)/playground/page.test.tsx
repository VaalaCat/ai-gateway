import { act, render, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import PlaygroundPage from "./page";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("@/hooks/use-mobile", () => ({ useIsMobile: () => false }));
vi.mock("@/hooks/use-user-pref", () => ({
  useUserPref: (name: string) => name === "playground-token-id" ? ["1", vi.fn()] : ["", vi.fn()],
}));
vi.mock("@/lib/api/tokens", () => ({ useToken: () => ({ data: { key: "sk-test" } }) }));
vi.mock("@/components/playground/message-list", () => ({ MessageList: () => null }));
vi.mock("@/components/playground/sse-viewer", () => ({ SSEViewer: () => null }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({ EntityPicker: () => null }));
vi.mock("@/components/ui/searchable-select", () => ({ SearchableSelect: () => null }));

function deferredResponse() {
  let resolve!: (value: { ok: boolean; json: () => Promise<{ data: { id: string }[] }> }) => void;
  const promise = new Promise<{ ok: boolean; json: () => Promise<{ data: { id: string }[] }> }>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

afterEach(() => vi.restoreAllMocks());

it("aborts the available-model request when the playground unmounts", async () => {
  const pending = deferredResponse();
  let requestSignal: AbortSignal | undefined;
  vi.spyOn(globalThis, "fetch").mockImplementation((_input, init) => {
    requestSignal = init?.signal as AbortSignal | undefined;
    return pending.promise as Promise<Response>;
  });

  const { unmount } = render(<PlaygroundPage />);
  await waitFor(() => expect(fetch).toHaveBeenCalledOnce());
  unmount();
  pending.resolve({ ok: true, json: async () => ({ data: [{ id: "stale-model" }] }) });
  await act(async () => { await pending.promise; });

  expect(requestSignal).toBeInstanceOf(AbortSignal);
  expect(requestSignal?.aborted).toBe(true);
});

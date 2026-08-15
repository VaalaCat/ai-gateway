import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";

import { clearAPIAuthorityDerivedCache } from "./clear-api-authority-derived-cache";

describe("clearAPIAuthorityDerivedCache", () => {
  it("immediately clears catalog and real-key validation data without touching LLM data", () => {
    const client = new QueryClient();
    client.setQueryData(["api-catalog", "services", 1], { data: [{ id: 7 }] });
    client.setQueryData(["tokens", "usable-for-api-route", 1, 7, 9, 5], { id: 5 });
    client.setQueryData(["available-models"], ["gpt-5"]);

    void clearAPIAuthorityDerivedCache(client);

    expect(client.getQueryData(["api-catalog", "services", 1])).toBeUndefined();
    expect(client.getQueryData(["tokens", "usable-for-api-route", 1, 7, 9, 5])).toBeUndefined();
    expect(client.getQueryData(["available-models"])).toEqual(["gpt-5"]);
  });

  it("refetches active catalog consumers after clearing their old data", async () => {
    const client = new QueryClient();
    const key = ["api-catalog", "services", 1];
    const queryFn = vi.fn().mockResolvedValue({ data: [{ id: 8 }] });
    client.setQueryData(key, { data: [{ id: 7 }] });
    const observer = new QueryObserver(client, { queryKey: key, queryFn, staleTime: Infinity });
    const unsubscribe = observer.subscribe(() => {});

    clearAPIAuthorityDerivedCache(client);

    expect(client.getQueryData(key)).toBeUndefined();
    await vi.waitFor(() => expect(queryFn).toHaveBeenCalledOnce());
    await vi.waitFor(() => expect(client.getQueryData(key)).toEqual({ data: [{ id: 8 }] }));
    unsubscribe();
  });
});

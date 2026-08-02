import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import { useAgentRoutesOverview } from "./agent-routes";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return { ...actual, api: { ...actual.api, get: apiGet } };
});

function wrapper({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={createTestQueryClient()}>
      {children}
    </QueryClientProvider>
  );
}

describe("agent route overview API", () => {
  beforeEach(() => apiGet.mockReset());

  it("passes every management filter and pagination value to the overview endpoint", async () => {
    apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 3, page_size: 20 });
    const { result } = renderHook(
      () => useAgentRoutesOverview({
        page: 3,
        page_size: 20,
        q: "smart",
        source_type: "token",
        source_id: 17,
        model: "gpt-4.1",
        agent_id: "agent-east",
      }),
      { wrapper },
    );

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/admin/agent-routes/overview?page=3&page_size=20&q=smart&source_type=token&source_id=17&model=gpt-4.1&agent_id=agent-east",
    );
  });
});

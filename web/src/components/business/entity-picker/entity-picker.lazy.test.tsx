import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient } from "@/test/render";
import { EntityPicker } from "./entity-picker";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/auth", () => ({
  useAuth: () => ({ isAdmin: false, user: { user_id: 7 } }),
}));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { get: apiGet } };
});

function renderPicker() {
  const queryClient = createTestQueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return render(
    <EntityPicker entity="usable-token" value="" onChange={() => {}} />,
    { wrapper },
  );
}

function tokenListRequests() {
  return apiGet.mock.calls
    .map(([path]) => String(path))
    .filter((path) => path.startsWith("/tokens?"));
}

describe("EntityPicker lazy list query", () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiGet.mockResolvedValue({ data: [], total: 0, page: 1, page_size: 50 });
  });
  afterEach(() => vi.useRealTimers());

  it("does not request the 50-row Token list while closed", async () => {
    renderPicker();
    await act(() => Promise.resolve());

    expect(tokenListRequests()).toEqual([]);
  });

  it("does not request the Token list when disabled", async () => {
    const queryClient = createTestQueryClient();
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    );
    render(
      <EntityPicker entity="usable-token" value="" onChange={() => {}} disabled />,
      { wrapper },
    );

    await act(() => Promise.resolve());

    expect(tokenListRequests()).toEqual([]);
  });

  it("requests the Token list only after opening", async () => {
    renderPicker();

    fireEvent.click(screen.getByRole("combobox"));
    await act(() => Promise.resolve());

    expect(tokenListRequests()).toHaveLength(1);
    expect(new URLSearchParams(tokenListRequests()[0].split("?")[1]).get("page_size"))
      .toBe("50");
  });

  it("keeps searched Token requests capped at 50 rows", async () => {
    vi.useFakeTimers();
    renderPicker();
    fireEvent.click(screen.getByRole("combobox"));
    await act(() => Promise.resolve());

    fireEvent.change(screen.getByPlaceholderText("searchPlaceholder"), {
      target: { value: "prod" },
    });
    await act(() => vi.advanceTimersByTimeAsync(300));

    const requests = tokenListRequests();
    expect(requests).toHaveLength(2);
    expect(requests.at(-1)).toContain("search=prod");
    for (const request of requests) {
      expect(new URLSearchParams(request.split("?")[1]).get("page_size")).toBe("50");
    }
  });
});

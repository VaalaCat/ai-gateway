import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient, queryClientWrapper } from "@/test/render";

import APIServiceDetailPage from "./detail/page";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const navigation = vi.hoisted(() => ({ replace: vi.fn() }));
const requests = vi.hoisted(() => ({ delete: vi.fn(), get: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ usePathname: () => "/api-services/detail", useRouter: () => ({ replace: navigation.replace, push: vi.fn() }), useSearchParams: () => new URLSearchParams("id=7") }));
vi.mock("@/lib/api/capabilities", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/capabilities")>()),
  useCapabilities: () => ({
    data: { generic_api: { services: true, service_actions: { create: true, manage_all: true, manage_ids: [] } } },
  }),
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 } }) }));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { ...actual.api, delete: requests.delete, get: requests.get } };
});

const service = { id: 7, slug: "weather", name: "Weather", description: "Current weather", price_per_call: 12, status: 1 };

function initialResponse(path: string) {
  if (path === "/admin/api-services/7") return Promise.resolve(service);
  if (path === "/admin/api-routes?api_service_id=7&page=1&page_size=20") return Promise.resolve({ data: [], total: 0, page: 1, page_size: 20 });
  if (path === "/admin/api-upstreams?api_service_id=7&page=1&page_size=20") return Promise.resolve({ data: [], total: 0, page: 1, page_size: 20 });
  return Promise.reject(new Error(`unexpected GET ${path}`));
}

describe("API service delete navigation", () => {
  beforeEach(() => {
    navigation.replace.mockReset();
    requests.delete.mockReset();
    requests.get.mockReset();
  });

  it("navigates and removes controls before a post-delete detail refetch settles", async () => {
    let detailReads = 0;
    requests.get.mockImplementation((path: string) => {
      if (path === "/admin/api-services/7" && ++detailReads > 1) return new Promise(() => {});
      return initialResponse(path);
    });
    requests.delete.mockResolvedValueOnce(undefined);
    const user = userEvent.setup();
    render(<APIServiceDetailPage />, { wrapper: queryClientWrapper(createTestQueryClient()) });
    await screen.findByRole("button", { name: "serviceActions" });

    await user.click(screen.getByRole("button", { name: "serviceActions" }));
    await user.click(screen.getByRole("menuitem", { name: "deleteService" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    await waitFor(() => expect(navigation.replace).toHaveBeenCalledWith("/api-services"));
    expect(screen.queryByRole("button", { name: "serviceActions" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "createRoute" })).not.toBeInTheDocument();
    expect(detailReads).toBe(2);
  });

  it("does not navigate when DELETE fails", async () => {
    requests.get.mockImplementation(initialResponse);
    requests.delete.mockRejectedValueOnce(new Error("delete rejected"));
    const user = userEvent.setup();
    render(<APIServiceDetailPage />, { wrapper: queryClientWrapper(createTestQueryClient()) });
    await screen.findByRole("button", { name: "serviceActions" });

    await user.click(screen.getByRole("button", { name: "serviceActions" }));
    await user.click(screen.getByRole("menuitem", { name: "deleteService" }));
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));

    await screen.findByText("delete rejected");
    expect(navigation.replace).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "serviceActions", hidden: true })).toBeInTheDocument();
  });
});

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ColumnDef } from "@tanstack/react-table";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { createTestQueryClient, queryClientWrapper } from "@/test/render";
import type { APIService } from "@/lib/api/api-services";

import APIServicesPage from "./page";

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

const requests = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
const toast = vi.hoisted(() => ({ error: vi.fn(), success: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({
  usePathname: () => "/api-services",
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ user: { user_id: 1 }, isAdmin: true }) }));
vi.mock("@/lib/api/capabilities", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/capabilities")>()),
  useCapabilities: () => ({ data: { generic_api: { services: true, service_actions: { create: true, manage_all: true, manage_ids: [] } } } }),
}));
vi.mock("@/lib/api/client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/api/client")>();
  return { ...actual, api: { ...actual.api, get: requests.get, put: requests.put } };
});
vi.mock("sonner", () => ({ toast }));
vi.mock("@/components/business/background-refresh-status", () => ({ BackgroundRefreshStatus: () => null }));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ columns, data }: { columns: ColumnDef<APIService>[]; data: APIService[] }) => {
    const action = columns.find((column) => column.id === "actions")?.cell;
    return <div>{data.map((service) => <div key={service.id}><span>{`status:${service.status}`}</span>{typeof action === "function" ? action({ row: { original: service } } as never) : null}</div>)}</div>;
  },
}));

const service = { id: 7, slug: "weather", name: "Weather", description: "", price_per_call: 1, status: 1 };
const page = (status: number) => ({ data: [{ ...service, status }], total: 1, page: 1, page_size: 20 });

describe("API service quick toggle", () => {
  beforeEach(() => {
    requests.get.mockReset();
    requests.put.mockReset();
    toast.error.mockReset();
    toast.success.mockReset();
  });

  it("sends only status and renders the refetched server state after success", async () => {
    requests.get.mockResolvedValueOnce(page(1)).mockResolvedValue(page(0));
    requests.put.mockResolvedValue({ ...service, status: 0 });
    const user = userEvent.setup();
    render(<APIServicesPage />, { wrapper: queryClientWrapper(createTestQueryClient()) });
    await screen.findByText("status:1");

    await user.click(screen.getByRole("button", { name: "actions" }));
    await user.click(screen.getByRole("menuitem", { name: "disableService" }));

    await waitFor(() => expect(requests.put).toHaveBeenCalledWith("/admin/api-services/7", { status: 0 }));
    expect(toast.success).toHaveBeenCalledWith("success");
    expect(await screen.findByText("status:0")).toBeInTheDocument();
    expect(requests.get).toHaveBeenCalledTimes(2);
  });

  it("reports failure and leaves the server row unchanged", async () => {
    requests.get.mockResolvedValue(page(1));
    requests.put.mockRejectedValueOnce(new Error("toggle rejected"));
    const user = userEvent.setup();
    render(<APIServicesPage />, { wrapper: queryClientWrapper(createTestQueryClient()) });
    await screen.findByText("status:1");

    await user.click(screen.getByRole("button", { name: "actions" }));
    await user.click(screen.getByRole("menuitem", { name: "disableService" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("toggle rejected"));
    expect(toast.success).not.toHaveBeenCalled();
    expect(screen.getByText("status:1")).toBeInTheDocument();
    expect(screen.queryByText("status:0")).not.toBeInTheDocument();
    expect(requests.get).toHaveBeenCalledTimes(1);
  });
});

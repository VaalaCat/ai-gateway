import type { ReactNode } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ModelRouting } from "@/lib/types";
import { ModelRoutingsListPage } from "./list-page";

const state = vi.hoisted(() => ({
  routings: [] as ModelRouting[],
  isLoading: false,
  routerPush: vi.fn(),
  setPagination: vi.fn(),
}));

const routing: ModelRouting = {
  id: 7,
  name: "production-route",
  scope: "global",
  user_id: 0,
  token_id: 0,
  members: [],
  enabled: true,
  remark: "",
  created_at: 1,
  updated_at: 1,
};

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: state.routerPush }) }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));
vi.mock("@/lib/api/model-routings", () => ({
  useModelRoutings: () => ({ data: { data: state.routings, total: state.routings.length }, isLoading: state.isLoading }),
  useUpdateModelRouting: () => ({ mutate: vi.fn() }),
  useDeleteModelRouting: () => ({ mutateAsync: vi.fn() }),
}));
vi.mock("@/components/data-table/use-filter-state", () => ({ useFilterState: () => [{}, vi.fn()] }));
vi.mock("@/components/data-table/use-pagination-state", () => ({
  usePaginationState: () => [1, 20, state.setPagination],
}));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({
    data,
    toolbar,
    total,
    page,
    pageSize,
    onPaginationChange,
  }: {
    data: ModelRouting[];
    toolbar: ReactNode;
    total: number;
    page: number;
    pageSize: number;
    onPaginationChange: (page: number, pageSize: number) => void;
  }) => (
    <section
      data-testid="model-routing-table"
      data-page={page}
      data-page-size={pageSize}
      data-total={total}
    >
      <output data-testid="model-routing-rows">
        {data.map(({ name }) => name).join(",")}
      </output>
      <div data-testid="model-routing-toolbar">{toolbar}</div>
      <button type="button" onClick={() => onPaginationChange(2, pageSize)}>
        next-page
      </button>
    </section>
  ),
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: () => <div data-testid="filterable-toolbar" />,
}));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/date-cell", () => ({ DateCell: () => null }));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: () => null }));
vi.mock("@/components/model-routing/scope-badge", () => ({ ScopeBadge: () => null }));

beforeEach(() => {
  state.routings = [];
  state.isLoading = false;
  state.routerPush.mockReset();
  state.setPagination.mockReset();
});

describe("ModelRoutingsListPage page header", () => {
  it("keeps the admin header create action wired to the admin new route", async () => {
    render(<ModelRoutingsListPage apiMode="admin" />);

    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("title");
    expect(screen.getByTestId("page-header-actions")).toContainElement(
      screen.getByRole("button", { name: "create" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "create" }));
    expect(state.routerPush).toHaveBeenCalledWith("/model-routings/new");
  });

  it("renders the profile title through the shared page header", () => {
    render(<ModelRoutingsListPage apiMode="user" />);

    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("myTitle");
    expect(screen.getByTestId("page-header")).toHaveTextContent("filtersUserHint");
  });

  it("keeps the empty boundary and CTA wired beneath the shared header", async () => {
    render(<ModelRoutingsListPage apiMode="admin" />);

    expect(screen.getByText("empty.title")).toBeInTheDocument();
    expect(screen.getByTestId("page-layout-content")).toContainElement(screen.getByText("empty.title"));
    await userEvent.click(screen.getByRole("button", { name: "empty.cta" }));
    expect(state.routerPush).toHaveBeenCalledWith("/model-routings/new");
  });

  it("passes non-empty routings, pagination, and toolbar to DataTable", async () => {
    state.routings = [routing];
    render(<ModelRoutingsListPage apiMode="admin" />);

    const table = screen.getByTestId("model-routing-table");
    expect(table).toHaveAttribute("data-page", "1");
    expect(table).toHaveAttribute("data-page-size", "20");
    expect(table).toHaveAttribute("data-total", "1");
    expect(screen.getByTestId("model-routing-rows")).toHaveTextContent("production-route");
    expect(screen.getByTestId("model-routing-toolbar")).toContainElement(
      screen.getByTestId("filterable-toolbar"),
    );
    expect(screen.queryByText("empty.title")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "next-page" }));
    expect(state.setPagination).toHaveBeenCalledWith(2, 20);
  });
});

import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, expect, it, vi } from "vitest";

import AgentRoutesPage from "./page";

const state = vi.hoisted(() => ({
  overviewParams: null as Record<string, unknown> | null,
  toolbar: null as Record<string, unknown> | null,
  setFilterValues: vi.fn(),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams("source_type=api_route") }));
vi.mock("@/lib/api/agent-routes", () => ({
  useAgentRoutesOverview: (params: Record<string, unknown>) => {
    state.overviewParams = params;
    return { data: { data: [], total: 0 }, isLoading: false };
  },
  useDeleteAgentRoute: () => ({ mutateAsync: vi.fn() }),
}));
vi.mock("@/components/data-table/use-filter-state", () => ({
  useFilterState: () => [
    { source_type: "api_route", source_service_id: "7", source_id: "9" },
    state.setFilterValues,
  ],
}));
vi.mock("@/components/data-table/use-pagination-state", () => ({
  usePaginationState: () => [1, 20, vi.fn()],
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: (props: Record<string, unknown>) => {
    state.toolbar = props;
    return null;
  },
}));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ toolbar }: { toolbar: ReactNode }) => <>{toolbar}</>,
}));
vi.mock("@/components/agent-route-form-dialog", () => ({ AgentRouteFormDialog: () => null }));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));

beforeEach(() => {
  state.overviewParams = null;
  state.toolbar = null;
  state.setFilterValues.mockReset();
});

it("uses the API route picker with a service-only candidate parent and omits that auxiliary parent from overview API filters", () => {
  render(<AgentRoutesPage />);

  const spec = state.toolbar?.spec as Record<string, {
    entity?: string;
    pickerQuery?: (values: Record<string, string>) => unknown;
  }>;
  expect(spec.source_id?.entity).toBe("api-route");
  expect(spec.source_id?.pickerQuery?.({ source_service_id: "7" })).toEqual({
    apiServiceId: 7,
    disabled: false,
  });
  expect(state.toolbar?.context).toMatchObject({ hasSourceType: true, isAPIRoute: true });
  expect(state.overviewParams).toMatchObject({ source_type: "api_route", source_id: 9 });
  expect(state.overviewParams).not.toHaveProperty("source_service_id");
});

it("clears route and auxiliary service filters atomically when source type changes", () => {
  render(<AgentRoutesPage />);

  const onChange = state.toolbar?.onChange as (next: Record<string, string>) => void;
  onChange({ source_type: "api_service" });

  expect(state.setFilterValues).toHaveBeenCalledWith({
    source_type: "api_service",
    source_id: "",
    source_service_id: "",
  });
});

it("clears only the child route filter when its auxiliary service parent changes", () => {
  render(<AgentRoutesPage />);

  const onChange = state.toolbar?.onChange as (next: Record<string, string>) => void;
  onChange({ source_service_id: "8" });

  expect(state.setFilterValues).toHaveBeenCalledWith({
    source_service_id: "8",
    source_id: "",
  });
});

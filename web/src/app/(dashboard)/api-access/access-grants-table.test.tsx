import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { AccessGrantsTable } from "./_components/access-grants-table";

const entityLabels = vi.hoisted(() => vi.fn(() => <span data-testid="entity-label" />));
const deleteGrant = vi.hoisted(() => vi.fn());

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ usePathname: () => "/api-access", useRouter: () => ({ replace: vi.fn() }), useSearchParams: () => new URLSearchParams() }));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: entityLabels }));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: () => null }));
vi.mock("./_components/access-grant-dialog", () => ({ AccessGrantDialog: () => null }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ columns, data }: { columns: Array<{ id?: string; cell?: (context: { row: { original: Record<string, unknown> } }) => React.ReactNode }>; data: Array<Record<string, unknown>> }) => <div>{data.flatMap((row, index) => columns.filter((column) => column.id === "principal" || column.id === "service" || column.id === "scope" || column.id === "actions").map((column) => <div key={`${index}-${column.id}`}>{column.cell?.({ row: { original: row } })}</div>))}</div>,
}));
vi.mock("@/lib/api/api-access", () => ({
  useAPIAccessGrants: () => ({ data: { data: [
    { principal_type: "user", principal_id: 1, principal_label: "one", api_service_id: 9, api_service_name: "Weather", configured: { scope: "service", route_ids: [] }, effective: { scope: "service", route_ids: [] }, sources: [] },
    { principal_type: "token", principal_id: 2, principal_label: "two", api_service_id: 9, api_service_name: "Weather", configured: { scope: "service", route_ids: [] }, effective: { scope: "service", route_ids: [] }, sources: [] },
    { principal_type: "user_group", principal_id: 3, principal_label: "three", api_service_id: 9, api_service_name: "Weather", effective: { scope: "service", route_ids: [] }, sources: ["custom_role"] },
  ], total: 3, page: 1, page_size: 20 }, error: null, isLoading: false }),
  useDeleteAPIAccessGrant: () => ({ mutateAsync: deleteGrant, isPending: false }),
}));

describe("AccessGrantsTable labels", () => {
  it("renders labels returned by the list response without row-by-row EntityLabel requests", () => {
    render(<AccessGrantsTable enabled />);

    expect(screen.getAllByText("Weather")).toHaveLength(3);
    expect(screen.getByText("one")).toBeInTheDocument();
    expect(screen.getByText("two")).toBeInTheDocument();
    expect(screen.getByText("three")).toBeInTheDocument();
    expect(screen.getAllByText("serviceScope")).toHaveLength(3);
    expect(entityLabels).not.toHaveBeenCalled();
  });

  it("keeps the delete dialog open with an accessible error, then retries after a rejected mutation", async () => {
    const { default: userEvent } = await import("@testing-library/user-event");
    const user = userEvent.setup();
    deleteGrant.mockReset();
    deleteGrant.mockRejectedValueOnce(new Error("network unavailable")).mockResolvedValueOnce(undefined);
    render(<AccessGrantsTable enabled />);

    await user.click(screen.getAllByRole("button", { name: "deleteGrant" })[0]);
    await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("network unavailable");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "confirmDelete" }));
    expect(deleteGrant).toHaveBeenCalledTimes(2);
  });
});

import type { ReactNode } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import UsersPage from "./page";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams() }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/api/users", () => ({
  useUsers: () => ({ data: { data: [], total: 0 }, isLoading: false }),
  useCreateUser: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteUser: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateQuota: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/components/data-table/use-filter-state", () => ({ useFilterState: () => [{}, vi.fn()] }));
vi.mock("@/components/data-table/use-pagination-state", () => ({ usePaginationState: () => [1, 20, vi.fn()] }));
vi.mock("@/components/data-table/data-table", () => ({ DataTable: ({ toolbar }: { toolbar: ReactNode }) => <>{toolbar}</> }));
vi.mock("@/components/data-table/filterable-toolbar", () => ({
  FilterableToolbar: ({ primaryAction }: { primaryAction: ReactNode }) => <>{primaryAction}</>,
}));
vi.mock("@/components/business/status-badge", () => ({ StatusBadge: () => null, RoleBadge: () => null }));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/profile-form-dialog", () => ({ ProfileFormDialog: () => null }));
vi.mock("@/components/business/date-cell", () => ({ DateCell: () => null }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({ EntityPicker: () => null }));
vi.mock("@/components/business/password-input", () => ({ PasswordInput: () => null }));

it("opens the original create-user dialog from the shared header action", async () => {
  render(<UsersPage />);

  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(screen.getByRole("heading", { level: 1 })).toHaveClass("tracking-tight");
  expect(screen.getByTestId("page-header-actions")).toContainElement(
    screen.getByRole("button", { name: "createUser" }),
  );

  await userEvent.click(screen.getByRole("button", { name: "createUser" }));

  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByRole("heading", { name: "createUser" })).toBeInTheDocument();
  expect(within(dialog).getByText("username")).toBeInTheDocument();
  expect(within(dialog).getByText("password")).toBeInTheDocument();
});

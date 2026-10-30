import type { ReactNode } from "react";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import type { InviteCodeRow } from "@/lib/types";
import InvitesPage from "./page";

const mocks = vi.hoisted(() => ({ isAdmin: true, create: vi.fn(), success: vi.fn(), error: vi.fn() }));
const rows: InviteCodeRow[] = [
  { id: 1, code: "DEFAULT", creator_id: 1, group_id: 0, note: "", max_uses: 1, used_count: 0, expires_at: 0, created_at: 1, updated_at: 1 },
  { id: 2, code: "SELECTED", creator_id: 1, group_id: 8, note: "", max_uses: 1, used_count: 0, expires_at: 0, created_at: 1, updated_at: 1 },
];
vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams() }));
vi.mock("sonner", () => ({ toast: { success: mocks.success, error: mocks.error } }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: mocks.isAdmin }) }));
vi.mock("@/lib/api/system", () => ({ usePublicConfig: () => ({ data: { invite_enabled: true, invite_user_max_codes: 5 } }) }));
vi.mock("@/lib/api/invite-codes", () => ({
  useInviteCodes: () => ({ data: { data: rows, total: 2 }, isLoading: false }),
  useAdminInviteCodes: () => ({ data: { data: rows, total: 2 }, isLoading: false }),
  useCreateInviteCode: () => ({ mutateAsync: mocks.create }),
  useDeleteInviteCode: () => ({ mutate: vi.fn() }),
  useAdminDeleteInviteCode: () => ({ mutate: vi.fn() }),
}));
vi.mock("@/components/data-table/use-filter-state", () => ({ useFilterState: () => [{}, vi.fn()] }));
vi.mock("@/components/data-table/data-table", () => ({
  DataTable: ({ toolbar, columns, data }: { toolbar: ReactNode; columns: { accessorKey?: string; cell?: (context: { row: { original: InviteCodeRow } }) => ReactNode }[]; data: InviteCodeRow[] }) => {
    const group = columns.find((column) => column.accessorKey === "group_id");
    return <>{toolbar}<div data-testid="groups">{data.map((row) => <div key={row.id}>{group?.cell?.({ row: { original: row } })}</div>)}</div></>;
  },
}));
vi.mock("@/components/data-table/filterable-toolbar", () => ({ FilterableToolbar: ({ primaryAction }: { primaryAction: ReactNode }) => <>{primaryAction}</> }));
vi.mock("@/components/business/delete-confirm", () => ({ DeleteConfirm: () => null }));
vi.mock("@/components/business/entity-label", () => ({ EntityLabel: ({ entity, id }: { entity: string; id: number }) => <span>{entity}:{id}</span> }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, entity, value, onChange }: { id: string; entity: string; value: string; onChange: (value: string) => void }) => <select id={id} data-entity={entity} value={value} onChange={(event) => onChange(event.target.value)}><option value="">defaultGroup</option><option value="8">selected</option></select>,
}));
vi.mock("@/components/business/date-picker/date-time-picker", () => ({ DateTimePicker: () => null }));

beforeEach(() => {
  mocks.isAdmin = true;
  mocks.create.mockReset().mockResolvedValue(rows[0]);
  mocks.success.mockReset();
  mocks.error.mockReset();
});

it("lets administrators select a registration group with the existing picker", async () => {
  const user = userEvent.setup();
  render(<InvitesPage />);
  await user.click(screen.getByRole("button", { name: "create" }));
  const picker = screen.getByRole("combobox", { name: "group" });
  expect(picker).toHaveAttribute("data-entity", "user-group");
  await user.selectOptions(picker, "8");
  await user.click(screen.getByRole("button", { name: "submit" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledWith({ note: "", max_uses: 1, expires_at: 0, group_id: 8 }));
  await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  await user.click(screen.getByRole("button", { name: "create" }));
  expect(screen.getByRole("combobox", { name: "group" })).toHaveValue("");
});

it("uses an unspecified group when administrators leave the picker empty", async () => {
  const user = userEvent.setup();
  render(<InvitesPage />);
  await user.click(screen.getByRole("button", { name: "create" }));
  await user.click(screen.getByRole("button", { name: "submit" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledWith({ note: "", max_uses: 1, expires_at: 0, group_id: 0 }));
});

it("omits group_id entirely for ordinary users", async () => {
  mocks.isAdmin = false;
  const user = userEvent.setup();
  render(<InvitesPage />);
  await user.click(screen.getByRole("button", { name: "create" }));
  expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "submit" }));
  await waitFor(() => expect(mocks.create).toHaveBeenCalledWith({ note: "", max_uses: 1, expires_at: 0 }));
  expect(mocks.create.mock.calls[0][0]).not.toHaveProperty("group_id");
});

it("shows selected groups and the unspecified default in the list", () => {
  render(<InvitesPage />);
  const groups = within(screen.getByTestId("groups"));
  expect(groups.getByText("defaultGroup")).toBeInTheDocument();
  expect(groups.getByText("user-group:8")).toBeInTheDocument();
});

it("keeps the selected group when creation fails", async () => {
  mocks.create.mockRejectedValue(new Error("invite user group unavailable"));
  const user = userEvent.setup();
  render(<InvitesPage />);
  await user.click(screen.getByRole("button", { name: "create" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "group" }), "8");
  await user.click(screen.getByRole("button", { name: "submit" }));
  await waitFor(() => expect(mocks.error).toHaveBeenCalledWith("invite user group unavailable"));
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "group" })).toHaveValue("8");
});

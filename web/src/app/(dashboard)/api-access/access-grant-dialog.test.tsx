import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AccessGrantDialog } from "./_components/access-grant-dialog";

const mutation = vi.hoisted(() => vi.fn());

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/api-access", () => ({ useReplaceAPIAccessGrant: () => ({ mutateAsync: mutation, isPending: false }) }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, value, onChange, disabled }: { id: string; value: string; onChange: (value: string) => void; disabled?: boolean }) => <select id={id} aria-label={id} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}><option value="" /><option value="7">seven</option><option value="8">eight</option></select>,
}));
vi.mock("@/components/business/entity-picker/entity-multi-picker", () => ({
  EntityMultiPicker: ({ value, onChange, disabled }: { value: string[]; onChange: (value: string[]) => void; disabled?: boolean }) => <select multiple aria-label="api-grant-routes" value={value} disabled={disabled} onChange={(event) => onChange(Array.from(event.currentTarget.selectedOptions, (option) => option.value))}><option value="7">route-7</option><option value="9">route-9</option></select>,
}));

describe("AccessGrantDialog", () => {
  beforeEach(() => { mutation.mockReset(); mutation.mockResolvedValue(undefined); });

  it("submits a set of routes for one service", async () => {
    const user = userEvent.setup();
    render(<AccessGrantDialog open onOpenChange={() => {}} />);
    await user.selectOptions(screen.getByLabelText("api-grant-principal"), "7");
    await user.selectOptions(screen.getByLabelText("api-grant-service"), "8");
    await user.click(screen.getByRole("radio", { name: "routeScope" }));
    await user.selectOptions(screen.getByLabelText("api-grant-routes"), ["7", "9"]);
    await user.click(screen.getByRole("button", { name: "save" }));

    expect(mutation).toHaveBeenCalledWith({ principal_type: "user", principal_id: 7, api_service_id: 8, scope: "routes", route_ids: [7, 9] });
  });

  it("keeps the grant identity locked while editing its route set", async () => {
    const user = userEvent.setup();
    render(<AccessGrantDialog open onOpenChange={() => {}} grant={{ principal_type: "user", principal_id: 7, principal_label: "Ada", api_service_id: 8, api_service_name: "Weather", configured: { scope: "routes", route_ids: [7] }, effective: { scope: "routes", route_ids: [7] }, sources: ["managed"] }} />);

    expect(screen.getByRole("combobox", { name: "principalType" })).toBeDisabled();
    expect(screen.getByLabelText("api-grant-principal")).toBeDisabled();
    expect(screen.getByLabelText("api-grant-service")).toBeDisabled();
    expect(screen.getByLabelText("api-grant-routes")).toBeEnabled();
    await user.selectOptions(screen.getByLabelText("api-grant-routes"), ["7", "9"]);
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(mutation).toHaveBeenCalledWith(expect.objectContaining({ principal_type: "user", principal_id: 7, api_service_id: 8, route_ids: [7, 9] }));
  });

  it("rejects an empty route set without mutating", async () => {
    const user = userEvent.setup();
    render(<AccessGrantDialog open onOpenChange={() => {}} grant={{ principal_type: "user", principal_id: 7, principal_label: "Ada", api_service_id: 8, api_service_name: "Weather", configured: { scope: "routes", route_ids: [] }, effective: { scope: "routes", route_ids: [] }, sources: ["managed"] }} />);
    await user.click(screen.getByRole("button", { name: "save" }));
    expect(screen.getByRole("alert")).toHaveTextContent("invalidGrant");
    expect(mutation).not.toHaveBeenCalled();
  });
});

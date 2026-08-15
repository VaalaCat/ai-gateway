import { useState } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { PermissionEditor, type PermissionDraft } from "./_components/permission-editor";

const detail = vi.hoisted(() => ({
  routes: {} as Record<number, { data?: { api_service_id: number }; error: unknown }>,
}));

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  Element.prototype.scrollIntoView ??= () => {};
});

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ id, entity, apiServiceId, value, onChange, disabled }: { id: string; entity: string; apiServiceId?: number; value: string; onChange: (value: string) => void; disabled?: boolean }) => <select id={id} data-testid={id} data-entity={entity} data-api-service-id={apiServiceId} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)}><option value="" /><option value="8">candidate-8</option><option value="9">candidate-9</option><option value="17">candidate-17</option><option value="18">candidate-18</option></select>,
}));
vi.mock("@/lib/api/api-services", () => ({
  useAPIRoute: (id: number) => detail.routes[id] ?? { data: undefined, error: null },
}));

const routePermission: PermissionDraft = { rowKey: 1, resource: "api_route", resource_id: 17, action: "invoke", scope: "specific" };

function ControlledEditor({ initialRows, resolvedServiceIDs = { current: new Map<number, number>() } }: { initialRows: PermissionDraft[]; resolvedServiceIDs?: { current: Map<number, number> } }) {
  const [rows, setRows] = useState(initialRows);
  return <><PermissionEditor rows={rows} onChange={setRows} onAdd={() => setRows((current) => [...current, { rowKey: Math.max(0, ...current.map((row) => row.rowKey)) + 1, resource: "api_service", resource_id: 0, action: "invoke", scope: "all" }])} resolvedServiceIDs={resolvedServiceIDs} /><output data-testid="rows">{JSON.stringify(rows)}</output></>;
}

describe("PermissionEditor", () => {
  beforeEach(() => { detail.routes = {}; });

  it("offers only service and route invoke grants without an action selector", async () => {
    const user = userEvent.setup();
    render(<ControlledEditor initialRows={[{ rowKey: 1, resource: "api_service", resource_id: 0, action: "invoke", scope: "all" }]} />);
    expect(screen.queryByRole("combobox", { name: "action" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("combobox", { name: "resource" }));
    expect(await screen.findByRole("option", { name: "resourceOptions.api_service" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "resourceOptions.api_route" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "resourceOptions.api_upstream" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "resourceOptions.api_request_log" })).not.toBeInTheDocument();
  });

  it("rehydrates the parent Service selector for an existing route permission", async () => {
    detail.routes[17] = { data: { api_service_id: 9 }, error: null };
    const resolvedServiceIDs = { current: new Map<number, number>() };
    render(<ControlledEditor initialRows={[routePermission]} resolvedServiceIDs={resolvedServiceIDs} />);

    expect(screen.getByTestId("api-permission-service-1")).toHaveValue("9");
    expect(screen.getByTestId("api-permission-target-1")).toHaveAttribute("data-api-service-id", "9");
    await waitFor(() => expect(resolvedServiceIDs.current).toEqual(new Map([[1, 9]])));
  });

  it("shows a clear failure when an existing route parent cannot load", () => {
    detail.routes[17] = { data: undefined, error: new Error("route failed") };
    render(<ControlledEditor initialRows={[routePermission]} />);
    expect(screen.getByRole("alert")).toHaveTextContent("resourceLoadFailed");
  });

  it("keeps routes specifically scoped and clears the target when its parent service changes", async () => {
    detail.routes[17] = { data: { api_service_id: 9 }, error: null };
    const user = userEvent.setup();
    render(<ControlledEditor initialRows={[routePermission]} />);

    expect(screen.queryByRole("radio", { name: "allResources" })).not.toBeInTheDocument();

    await user.selectOptions(screen.getByTestId("api-permission-service-1"), "8");
    expect(screen.getByTestId("rows")).toHaveTextContent('"resource_id":0');
    expect(screen.getByTestId("api-permission-target-1")).toHaveValue("");

    await user.click(screen.getByRole("combobox", { name: "resource" }));
    await user.click(await screen.findByRole("option", { name: "resourceOptions.api_service" }));
    expect(screen.getByTestId("rows")).toHaveTextContent('"resource":"api_service"');
    expect(screen.getByTestId("rows")).toHaveTextContent('"resource_id":0');
    expect(screen.getByTestId("rows")).toHaveTextContent('"scope":"all"');
  });

  it("keeps row keys stable across add and remove while clearing an unmounted parent", async () => {
    detail.routes[17] = { data: { api_service_id: 9 }, error: null };
    const user = userEvent.setup();
    const resolvedServiceIDs = { current: new Map<number, number>() };
    render(<ControlledEditor initialRows={[routePermission]} resolvedServiceIDs={resolvedServiceIDs} />);
    await waitFor(() => expect(resolvedServiceIDs.current.get(1)).toBe(9));
    await user.click(screen.getByRole("button", { name: "addPermission" }));
    await user.click(screen.getAllByRole("button", { name: "removePermission" })[0]);
    await waitFor(() => expect(resolvedServiceIDs.current.has(1)).toBe(false));
    await user.click(screen.getByRole("button", { name: "addPermission" }));
    expect(screen.getByTestId("rows")).toHaveTextContent('"rowKey":2');
    expect(screen.getByTestId("rows")).toHaveTextContent('"rowKey":3');
  });
});

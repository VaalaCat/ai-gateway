import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { AgentRouteFormDialog } from "./agent-route-form-dialog";

const state = vi.hoisted(() => ({
  pickerProps: [] as Array<Record<string, unknown>>,
  serviceIDs: [] as string[],
  create: vi.fn(),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/api/agent-routes", () => ({
  useCreateAgentRoute: () => ({ mutateAsync: state.create, isPending: false }),
  useUpdateAgentRoute: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: (props: { entity: string; disabled?: boolean; apiServiceId?: number; onChange: (value: string) => void }) => {
    state.pickerProps.push(props);
    return (
      <button
        type="button"
        data-testid={`picker-${props.entity}`}
        disabled={props.disabled}
        onClick={() => props.onChange(
          props.entity === "api-service" ? state.serviceIDs.shift() ?? "7" : "9",
        )}
      >
        {props.entity}
      </button>
    );
  },
}));

beforeEach(() => {
  state.pickerProps = [];
  state.serviceIDs = ["7"];
  state.create.mockReset().mockResolvedValue({});
  Element.prototype.hasPointerCapture ??= () => false;
  Element.prototype.setPointerCapture ??= () => {};
  Element.prototype.releasePointerCapture ??= () => {};
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

async function chooseSourceType(value: "api_service" | "api_route") {
  const user = userEvent.setup();
  await user.click(screen.getByLabelText("sourceType"));
  await user.click(await screen.findByRole("option", { name: value === "api_service" ? "sourceAPIService" : "sourceAPIRoute" }));
  return user;
}

describe("AgentRouteFormDialog Generic API sources", () => {
  it("uses the API service EntityPicker instead of a numeric source id input", async () => {
    render(<AgentRouteFormDialog open route={null} onOpenChange={() => {}} />);

    await chooseSourceType("api_service");

    expect(screen.getByTestId("picker-api-service")).toBeInTheDocument();
    expect(screen.queryByRole("spinbutton")).not.toBeInTheDocument();
  });

  it("requires a service parent before allowing an API route source and passes it to the route picker", async () => {
    render(<AgentRouteFormDialog open route={null} onOpenChange={() => {}} />);

    const user = await chooseSourceType("api_route");
    expect(screen.getByTestId("picker-api-service")).toBeInTheDocument();
    expect(screen.getByTestId("picker-api-route")).toBeDisabled();

    await user.click(screen.getByTestId("picker-api-service"));

    expect(screen.getByTestId("picker-api-route")).not.toBeDisabled();
    expect(state.pickerProps.findLast((props) => props.entity === "api-route")).toMatchObject({
      entity: "api-route",
      apiServiceId: 7,
    });
  });

  it("clears a selected API route when its service parent changes", async () => {
    state.serviceIDs = ["7", "8"];
    render(<AgentRouteFormDialog open route={null} onOpenChange={() => {}} />);
    const user = await chooseSourceType("api_route");

    await user.click(screen.getByTestId("picker-api-service"));
    await user.click(screen.getByTestId("picker-api-route"));
    expect(state.pickerProps.findLast((props) => props.entity === "api-route")).toMatchObject({
      value: "9",
      apiServiceId: 7,
    });

    await user.click(screen.getByTestId("picker-api-service"));
    expect(state.pickerProps.findLast((props) => props.entity === "api-route")).toMatchObject({
      value: "",
      apiServiceId: 8,
    });
  });

  it("clears an API route source when changing source type", async () => {
    render(<AgentRouteFormDialog open route={null} onOpenChange={() => {}} />);
    const user = await chooseSourceType("api_route");
    await user.click(screen.getByTestId("picker-api-service"));
    await user.click(screen.getByTestId("picker-api-route"));

    await chooseSourceType("api_service");
    expect(state.pickerProps.findLast((props) => props.entity === "api-service")).toMatchObject({
      value: "",
    });
  });

  it("submits only the route source id and never sends the auxiliary service parent", async () => {
    render(<AgentRouteFormDialog open route={null} onOpenChange={() => {}} />);
    const user = await chooseSourceType("api_route");
    await user.click(screen.getByTestId("picker-api-service"));
    await user.click(screen.getByTestId("picker-api-route"));
    await user.click(screen.getByTestId("picker-agent"));

    await user.click(screen.getByRole("button", { name: "create" }));
    expect(state.create).toHaveBeenCalledWith({
      source_type: "api_route",
      source_id: 9,
      model: "",
      agent_id: "9",
      agent_tag: "",
    });
    expect(state.create.mock.calls[0]?.[0]).not.toHaveProperty("api_service_id");
  });
});

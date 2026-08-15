import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import { AgentRouteEditor } from "./agent-route-editor";

const state = vi.hoisted(() => ({
  create: vi.fn(),
  query: vi.fn(),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("@/lib/api/agent-routes", () => ({
  useAgentRoutes: (query: unknown) => {
    state.query(query);
    return { data: { data: [] }, isLoading: false };
  },
  useCreateAgentRoute: () => ({ mutateAsync: state.create, isPending: false }),
  useDeleteAgentRoute: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/components/business/entity-picker/entity-picker", () => ({
  EntityPicker: ({ onChange }: { onChange: (value: string) => void }) => <button type="button" onClick={() => onChange("agent-east")}>pick-agent</button>,
}));

beforeEach(() => {
  state.create.mockReset().mockResolvedValue({});
  state.query.mockReset();
});

it("creates an API service route with the same agent payload shape as token routes", async () => {
  render(<AgentRouteEditor sourceType="api_service" sourceId={19} />);

  expect(state.query).toHaveBeenCalledWith({ source_type: "api_service", source_id: 19 });
  await userEvent.click(screen.getByRole("button", { name: "addRule" }));
  await userEvent.click(screen.getByRole("button", { name: "pick-agent" }));
  await userEvent.click(screen.getByRole("button", { name: "add" }));

  await waitFor(() => expect(state.create).toHaveBeenCalledWith({
    source_type: "api_service",
    source_id: 19,
    model: "",
    agent_id: "agent-east",
  }));
});

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";

import type { Channel } from "@/lib/types";

import { ChannelForm } from "./index";
import { adminChannelAdapter } from "./adapters/admin";
import type { UseChannelFormResult } from "./use-channel-form";
import { emptyForm, type ChannelForm as ChannelFormValues } from "./types";

const state = vi.hoisted(() => ({
  fetch: vi.fn(),
  formState: {} as UseChannelFormResult<Channel>,
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("./use-channel-form", () => ({ useChannelForm: () => state.formState }));
vi.mock("@/components/agent-route-editor", () => ({ AgentRouteEditor: () => null }));
vi.mock("@/components/business/provider-avatar", () => ({ ProviderAvatar: () => null }));
vi.mock("@/lib/api/channels", () => ({
  useFetchUpstreamModels: () => ({ mutateAsync: state.fetch, isPending: false }),
  useChannelTypes: () => ({ data: [] }),
  useChannel: () => ({ data: undefined }),
  useCreateChannel: () => ({ mutateAsync: vi.fn() }),
  useUpdateChannel: () => ({ mutateAsync: vi.fn() }),
}));

function loadedForm(form: Partial<ChannelFormValues>): void {
  state.formState = {
    form: { ...emptyForm, key: "sk-upstream", ...form },
    setForm: vi.fn(),
    initial: emptyForm,
    entity: null,
    isDirty: false,
    dirtyFieldCount: 0,
    isLoading: false,
    notFound: false,
    saving: false,
    submit: vi.fn(),
    cancel: vi.fn(),
  };
}

async function fetchModels(user: ReturnType<typeof userEvent.setup>, form: Partial<ChannelFormValues>) {
  loadedForm(form);
  render(
    <ChannelForm
      mode={{ kind: "edit", id: 7 }}
      adapter={adminChannelAdapter}
      agentId="agent-east"
      agentTag="edge"
    />,
  );
  if (!form.use_legacy_adaptor) {
    await user.click(screen.getAllByRole("button", { name: "stageRouting" })[0]);
  }
  await user.click(screen.getByRole("button", { name: "fetchModels" }));
  await waitFor(() => expect(state.fetch).toHaveBeenCalledTimes(1));
  return state.fetch.mock.calls[0][0];
}

beforeEach(() => {
  state.fetch.mockReset().mockResolvedValue({ models: [] });
});

it("sends the form's agent_tag when fetching models", async () => {
  const body = await fetchModels(userEvent.setup(), {});

  expect(body).toMatchObject({ agent_id: "agent-east", agent_tag: "edge" });
});

it("sends the form's agent_tag when fetching models from the legacy form", async () => {
  const body = await fetchModels(userEvent.setup(), { use_legacy_adaptor: true });

  expect(body).toMatchObject({ agent_id: "agent-east", agent_tag: "edge" });
});

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { beforeAll, expect, it, vi } from "vitest";

import { emptyForm, type ChannelForm } from "../types";
import { ConnectionSection } from "./connection";
import { adminChannelAdapter } from "../adapters/admin";
import { useChannelForm } from "../use-channel-form";

const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn(), error: vi.fn() }));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: mutations.error } }));

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
  HTMLElement.prototype.hasPointerCapture ??= () => false;
  HTMLElement.prototype.setPointerCapture ??= () => {};
  HTMLElement.prototype.releasePointerCapture ??= () => {};
});

function renderSection(setForm = vi.fn()) {
  const form = {
    ...emptyForm,
    endpoints: JSON.stringify({
      chat_completions: "/v1/chat/completions",
      responses: "/v1/responses",
      messages: "/v1/messages",
    }),
    other_settings: JSON.stringify({ custom_setting: "keep" }),
  };
  render(<ConnectionSection form={form} setForm={setForm} />);
  return { form, setForm };
}

it("groups upstream request settings by provider, field policy, and network", () => {
  renderSection();

  expect(screen.getByRole("group", { name: "upstreamProviderParameters" })).toBeInTheDocument();
  expect(screen.getByRole("group", { name: "requestFieldPolicy" })).toBeInTheDocument();
  expect(screen.getByRole("group", { name: "networkConnection" })).toBeInTheDocument();
});

it("updates request field policy without replacing unknown other settings", async () => {
  const user = userEvent.setup();
  const { setForm } = renderSection();

  await user.click(screen.getByRole("switch", { name: "allowServiceTier" }));

  const next = setForm.mock.calls.at(-1)?.[0];
  expect(JSON.parse(next.other_settings)).toEqual({
    custom_setting: "keep",
    allow_service_tier: true,
  });
});

it("keeps Claude beta query in provider parameters", async () => {
  const user = userEvent.setup();
  const { setForm } = renderSection();

  await user.click(screen.getByRole("switch", { name: "claudeBetaQuery" }));

  const next = setForm.mock.calls.at(-1)?.[0];
  expect(JSON.parse(next.other_settings)).toEqual({
    custom_setting: "keep",
    claude_beta_query: true,
  });
});

function EditableSection({ initial = emptyForm, onChange = vi.fn() }: { initial?: ChannelForm; onChange?: (form: ChannelForm) => void }) {
  const [form, setForm] = useState(initial);
  return <ConnectionSection form={form} setForm={(next) => { setForm(next); onChange(next); }} />;
}

it("edits overall authentication and keeps the channel key reference verbatim", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<EditableSection onChange={onChange} />);

  await user.click(screen.getByRole("combobox", { name: "upstreamAuthDefault" }));
  await user.click(screen.getByRole("option", { name: "upstreamAuthCustom" }));
  await user.type(screen.getByLabelText("Header · upstreamAuthDefault"), "Authorization");
  await user.type(screen.getByLabelText("Value · upstreamAuthDefault"), "Bearer ${{CHANNEL_KEY}");

  expect(JSON.parse(onChange.mock.calls.at(-1)?.[0].auth)).toEqual({
    default: { header: "Authorization", value: "Bearer ${CHANNEL_KEY}" },
  });
  expect(screen.queryByText("upstreamAuthIncomplete")).not.toBeInTheDocument();
});

it.each(["Chat Completions", "Responses", "Messages", "Gemini"])("removes the %s override when returning to inherit", async (label) => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const auth = {
    default: { header: "Authorization", value: "Bearer ${CHANNEL_KEY}" },
    protocols: {
      chat_completions: { header: "X-Chat-Key", value: "${CHANNEL_KEY}" },
      responses: { header: "X-Responses-Key", value: "${CHANNEL_KEY}" },
      messages: { header: "X-Api-Key", value: "${CHANNEL_KEY}" },
      generate_content: { header: "x-goog-api-key", value: "${CHANNEL_KEY}" },
    },
  };
  render(<EditableSection initial={{ ...emptyForm, auth: JSON.stringify(auth) }} onChange={onChange} />);

  await user.click(screen.getByRole("combobox", { name: label }));
  await user.click(screen.getByRole("option", { name: "upstreamAuthInherit" }));

  const protocol = { "Chat Completions": "chat_completions", Responses: "responses", Messages: "messages", Gemini: "generate_content" }[label]!;
  const expected = structuredClone(auth);
  delete expected.protocols[protocol as keyof typeof expected.protocols];
  expect(JSON.parse(onChange.mock.calls.at(-1)?.[0].auth)).toEqual(expected);
  expect(screen.queryByLabelText(`Header · ${label}`)).not.toBeInTheDocument();
});

it("adds a Messages override without replacing the overall authentication", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const defaultAuth = { header: "Authorization", value: "Bearer ${CHANNEL_KEY}" };
  render(<EditableSection initial={{ ...emptyForm, auth: JSON.stringify({ default: defaultAuth }) }} onChange={onChange} />);
  await user.click(screen.getByRole("combobox", { name: "Messages" }));
  await user.click(screen.getByRole("option", { name: "upstreamAuthCustom" }));
  await user.type(screen.getByLabelText("Header · Messages"), "X-Api-Key");
  await user.type(screen.getByLabelText("Value · Messages"), "${{CHANNEL_KEY}");
  expect(JSON.parse(onChange.mock.calls.at(-1)?.[0].auth)).toEqual({
    default: defaultAuth,
    protocols: { messages: { header: "X-Api-Key", value: "${CHANNEL_KEY}" } },
  });
});

it("clears all custom authentication when the overall setting returns to default", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<EditableSection initial={{ ...emptyForm, auth: JSON.stringify({ default: { header: "Authorization", value: "Bearer ${CHANNEL_KEY}" } }) }} onChange={onChange} />);
  await user.click(screen.getByRole("combobox", { name: "upstreamAuthDefault" }));
  await user.click(screen.getByRole("option", { name: "upstreamAuthUseDefault" }));
  expect(onChange.mock.calls.at(-1)?.[0].auth).toBe("");
});

function SubmitSection() {
  const state = useChannelForm({ kind: "create" }, {
    ...adminChannelAdapter,
    useEntity: () => ({ data: undefined, isLoading: false, isError: false }),
    useCreate: () => ({ mutateAsync: mutations.create, isPending: false }),
    useUpdate: () => ({ mutateAsync: mutations.update, isPending: false }),
  });
  return <><ConnectionSection form={state.form} setForm={state.setForm} /><output aria-label="dirty-fields">{state.dirtyFieldCount}</output><button onClick={state.submit}>save</button></>;
}

it("rejects incomplete authentication before saving and saves the completed raw value", async () => {
  const user = userEvent.setup();
  mutations.create.mockResolvedValue({});
  render(<SubmitSection />);
  await user.click(screen.getByRole("combobox", { name: "upstreamAuthDefault" }));
  await user.click(screen.getByRole("option", { name: "upstreamAuthCustom" }));
  await user.type(screen.getByLabelText("Header · upstreamAuthDefault"), "Authorization");
  expect(screen.getByLabelText("dirty-fields")).toHaveTextContent("1");
  await user.click(screen.getByRole("button", { name: "save" }));
  expect(mutations.create).not.toHaveBeenCalled();
  expect(mutations.error).toHaveBeenCalledWith("upstreamAuthIncomplete");
  await user.type(screen.getByLabelText("Value · upstreamAuthDefault"), "Bearer ${{CHANNEL_KEY}");
  await user.click(screen.getByRole("button", { name: "save" }));
  expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({
    auth: { default: { header: "Authorization", value: "Bearer ${CHANNEL_KEY}" } },
  }));
});

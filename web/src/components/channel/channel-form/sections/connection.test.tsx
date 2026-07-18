import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { emptyForm } from "../types";
import { ConnectionSection } from "./connection";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

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

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, expect, it, vi } from "vitest";

import { EncodeConfigSection } from "./encode-config";
import { emptyForm } from "../types";
import { ProtocolRuleCard } from "../protocol-rule-card";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
  HTMLElement.prototype.hasPointerCapture ??= () => false;
  HTMLElement.prototype.setPointerCapture ??= () => {};
  HTMLElement.prototype.releasePointerCapture ??= () => {};
});

it("offers Gemini as an outbound override for existing client protocols", async () => {
  const user = userEvent.setup();
  const setForm = vi.fn();
  render(<EncodeConfigSection form={{ ...emptyForm, endpoints: JSON.stringify({ chat_completions: "/v1/chat/completions", generate_content: "/v1beta/models/{model}:{action}" }) }} setForm={setForm} />);
  await user.click(screen.getByRole("button", { name: /protocolOverride.*encodeSummaryOverrides/ }));
  await user.click(screen.getAllByRole("combobox")[0]);
  await user.click(screen.getByRole("option", { name: "Gemini" }));
  expect(JSON.parse(setForm.mock.calls.at(-1)![0].other_settings)).toEqual({ protocol_override: { openai_chat: "gemini" } });
});

it("keeps per-model inbound choices independent of the upstream endpoint and excludes Gemini inbound conversion", async () => {
  const user = userEvent.setup();
  render(<ProtocolRuleCard rule={{ model: "custom-.*", overrides: { "*": "gemini" } }} enabledOutbounds={[{ value: "gemini", label: "Gemini" }]} onChange={vi.fn()} onDelete={vi.fn()} />);
  await user.click(screen.getAllByRole("combobox")[0]);
  expect(screen.getByRole("option", { name: "openai_chat" })).toBeInTheDocument();
  expect(screen.getByRole("option", { name: "openai_responses" })).toBeInTheDocument();
  expect(screen.getByRole("option", { name: "claude" })).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: /gemini/i })).not.toBeInTheDocument();
});

import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { ChannelEndpointsEditor } from "./channel-endpoints-editor";
import { ENDPOINT_DEFAULTS } from "./types";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

function EditableEndpoints({ initial = "", onChange = vi.fn() }) {
  const [endpoints, setEndpoints] = useState(initial);
  return <ChannelEndpointsEditor endpoints={endpoints} baseURL="https://generativelanguage.googleapis.com/" onEndpointsChange={(next) => {
    setEndpoints(next);
    onChange(next);
  }} />;
}

it("enables Gemini with the default template and previews unresolved placeholders", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  render(<EditableEndpoints onChange={onChange} />);
  await user.click(screen.getByRole("checkbox", { name: "apiTypeGemini" }));
  expect(JSON.parse(onChange.mock.calls.at(-1)![0])).toEqual({ generate_content: ENDPOINT_DEFAULTS.generate_content });
  expect(screen.getByRole("textbox", { name: "endpointPath" })).toHaveValue("/v1beta/models/{model}:{action}");
  expect(screen.getByText("→ https://generativelanguage.googleapis.com/v1beta/models/{model}:{action}")).toBeInTheDocument();
  expect(screen.getByText("geminiEndpointHint")).toBeInTheDocument();
});

it("keeps a custom Gemini path when reopening and removes only Gemini when unchecked", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const initial = JSON.stringify({ generate_content: "/custom/{model}:{action}?extra=1", messages: "/v1/messages" });
  render(<EditableEndpoints initial={initial} onChange={onChange} />);
  expect(screen.getByDisplayValue("/custom/{model}:{action}?extra=1")).toBeInTheDocument();
  await user.click(screen.getByRole("checkbox", { name: "apiTypeGemini" }));
  expect(JSON.parse(onChange.mock.calls.at(-1)![0])).toEqual({ messages: "/v1/messages" });
});

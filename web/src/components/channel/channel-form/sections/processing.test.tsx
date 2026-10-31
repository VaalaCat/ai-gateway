import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, expect, it, vi } from "vitest";

import { emptyForm } from "../types";
import { buildAdminCreatePayload } from "../adapters/admin";
import { ProcessingSection } from "./processing";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("@/lib/api/channels", () => ({
  useChannelDataFlow: () => ({ data: undefined }),
}));

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
  HTMLElement.prototype.hasPointerCapture ??= () => false;
  HTMLElement.prototype.setPointerCapture ??= () => {};
  HTMLElement.prototype.releasePointerCapture ??= () => {};
});

function ToolSchemaForm() {
  const [form, setForm] = useState({
    ...emptyForm,
    other_settings: JSON.stringify({ custom_setting: "keep" }),
  });
  const [saved, setSaved] = useState("");
  return (
    <>
      <ProcessingSection form={form} setForm={setForm} />
      <button type="button" onClick={() => setSaved(buildAdminCreatePayload(form).other_settings ?? "")}>
        Save channel
      </button>
      <output data-testid="saved-settings">{saved}</output>
    </>
  );
}

it("edits inline_image_url from the inline image dataflow step", async () => {
  const user = userEvent.setup();
  const setForm = vi.fn();
  const form = {
    ...emptyForm,
    other_settings: JSON.stringify({ custom_setting: "keep" }),
  };

  render(<ProcessingSection form={form} setForm={setForm} />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.inline_image" }));
  await user.click(screen.getByRole("switch", { name: "inlineImageUrl" }));

  expect(setForm).toHaveBeenCalledTimes(1);
  const next = setForm.mock.calls[0][0];
  expect(JSON.parse(next.other_settings)).toEqual({
    custom_setting: "keep",
    inline_image_url: true,
  });
});

it("adds and saves tool schema rules from a step alongside thinking passthrough", async () => {
  const user = userEvent.setup();
  render(<ToolSchemaForm />);

  // 编码设置收起后，工具定义兼容仍可独立编辑。
  await user.click(screen.getByRole("button", { name: "dataflowStep.encode*" }));
  const thinkingStep = screen.getByRole("button", { name: "dataflowStep.thinking_passthrough" });
  const compatibilityStep = screen.getByRole("button", { name: "dataflowStep.tool_schema_compatibility" });
  expect(thinkingStep).toHaveAttribute("aria-expanded", "false");
  await user.click(compatibilityStep);
  await user.click(screen.getByRole("button", { name: "+ toolSchemaCompatibilityAddRule" }));
  await user.type(screen.getByRole("textbox", { name: "toolSchemaCompatibilityModelLabel" }), "^gemini-.*$");
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "gemini_compat" }],
  });

  await user.click(screen.getByRole("combobox", { name: "toolSchemaCompatibilityProfileLabel" }));
  await user.click(screen.getByRole("option", { name: "toolSchemaCompatibilityNone" }));
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "none" }],
  });
});

it("removes the last tool schema rule without clearing other channel settings", async () => {
  const user = userEvent.setup();
  render(<ToolSchemaForm />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_schema_compatibility" }));
  await user.click(screen.getByRole("button", { name: "+ toolSchemaCompatibilityAddRule" }));
  await user.type(screen.getByRole("textbox", { name: "toolSchemaCompatibilityModelLabel" }), "^gemini-.*$");
  await user.click(screen.getByRole("button", { name: "delete" }));
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(screen.queryByRole("textbox", { name: "toolSchemaCompatibilityModelLabel" })).not.toBeInTheDocument();
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({ custom_setting: "keep" });
});

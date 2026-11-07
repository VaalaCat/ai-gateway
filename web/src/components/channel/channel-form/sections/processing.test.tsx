import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
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

function ToolCompatibilityForm({ initialSettings = JSON.stringify({ custom_setting: "keep" }) }: { initialSettings?: string }) {
  const [form, setForm] = useState({
    ...emptyForm,
    other_settings: initialSettings,
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

// 规则卡片只有模型模式输入框、两个下拉（兼容档案、签名兜底）和删除按钮，没有别的控件。
function expectCompatibilityCardControls() {
  const card = screen.getByRole("textbox", { name: "toolCompatibilityModelLabel" }).closest<HTMLElement>("[data-slot='field-group']");
  expect(card).not.toBeNull();
  expect(within(card!).getAllByRole("textbox")).toHaveLength(1);
  expect(within(card!).getAllByRole("combobox")).toHaveLength(2);
  expect(within(card!).getByRole("combobox", { name: "toolCompatibilityProfileLabel" })).toBeEnabled();
  expect(within(card!).getByRole("combobox", { name: "toolCompatibilitySignatureLabel" })).toBeEnabled();
  expect(within(card!).getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual(["delete"]);
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

it("adds and saves independent tool compatibility choices from a step alongside thinking passthrough", async () => {
  const user = userEvent.setup();
  render(<ToolCompatibilityForm />);

  // 编码设置收起后，工具兼容仍可独立编辑。
  await user.click(screen.getByRole("button", { name: "dataflowStep.encode*" }));
  const thinkingStep = screen.getByRole("button", { name: "dataflowStep.thinking_passthrough" });
  const compatibilityStep = screen.getByRole("button", { name: "dataflowStep.tool_compatibility" });
  expect(thinkingStep).toHaveAttribute("aria-expanded", "false");
  await user.click(compatibilityStep);
  await user.click(screen.getByRole("button", { name: "+ toolCompatibilityAddRule" }));
  expect(screen.getByRole("combobox", { name: "toolCompatibilitySignatureLabel" })).toHaveTextContent("toolCompatibilitySignaturePreserve");
  await user.type(screen.getByRole("textbox", { name: "toolCompatibilityModelLabel" }), "^gemini-.*$");
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "gemini_compat", signature_fallback: "none" }],
  });

  await user.click(screen.getByRole("combobox", { name: "toolCompatibilityProfileLabel" }));
  await user.click(screen.getByRole("option", { name: "toolCompatibilityNone" }));
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "none", signature_fallback: "none" }],
  });
});

it("loads existing schema rules with signature fallback off and keeps their saved settings", async () => {
  const user = userEvent.setup();
  const settings = {
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "gemini_compat" }],
  };
  render(<ToolCompatibilityForm initialSettings={JSON.stringify(settings)} />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_compatibility" }));
  expect(screen.getByRole("combobox", { name: "toolCompatibilitySignatureLabel" })).toHaveTextContent("toolCompatibilitySignaturePreserve");
  expect(screen.getByRole("combobox", { name: "toolCompatibilityProfileLabel" })).toHaveTextContent("toolCompatibilityGemini");
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual(settings);
});

it("saves and reloads combined compatibility and signature-only rules", async () => {
  const user = userEvent.setup();
  const { unmount } = render(<ToolCompatibilityForm />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_compatibility" }));
  await user.click(screen.getByRole("button", { name: "+ toolCompatibilityAddRule" }));
  await user.type(screen.getByRole("textbox", { name: "toolCompatibilityModelLabel" }), "^gemini-.*$");
  await user.click(screen.getByRole("combobox", { name: "toolCompatibilitySignatureLabel" }));
  await user.click(screen.getByRole("option", { name: "toolCompatibilitySignatureFallback" }));
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "gemini_compat", signature_fallback: "gemini" }],
  });

  await user.click(screen.getByRole("combobox", { name: "toolCompatibilityProfileLabel" }));
  await user.click(screen.getByRole("option", { name: "toolCompatibilityNone" }));
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  const savedSettings = screen.getByTestId("saved-settings").textContent!;
  expect(JSON.parse(savedSettings)).toEqual({
    custom_setting: "keep",
    model_tool_schema_compatibility: [{ model_pattern: "^gemini-.*$", profile: "none", signature_fallback: "gemini" }],
  });

  unmount();
  render(<ToolCompatibilityForm initialSettings={savedSettings} />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_compatibility" }));
  expect(screen.getByRole("combobox", { name: "toolCompatibilityProfileLabel" })).toHaveTextContent("toolCompatibilityNone");
  expect(screen.getByRole("combobox", { name: "toolCompatibilitySignatureLabel" })).toHaveTextContent("toolCompatibilitySignatureFallback");
});

it("removes the last tool compatibility rule without clearing other channel settings", async () => {
  const user = userEvent.setup();
  render(<ToolCompatibilityForm />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_compatibility" }));
  await user.click(screen.getByRole("button", { name: "+ toolCompatibilityAddRule" }));
  await user.type(screen.getByRole("textbox", { name: "toolCompatibilityModelLabel" }), "^gemini-.*$");
  await user.click(screen.getByRole("button", { name: "delete" }));
  await user.click(screen.getByRole("button", { name: "Save channel" }));
  expect(screen.queryByRole("textbox", { name: "toolCompatibilityModelLabel" })).not.toBeInTheDocument();
  expect(JSON.parse(screen.getByTestId("saved-settings").textContent!)).toEqual({ custom_setting: "keep" });
});

it("shows only the base rule controls and keeps the gemini signature fallback after save and reopen", async () => {
  const user = userEvent.setup();
  const { unmount } = render(<ToolCompatibilityForm />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_compatibility" }));
  await user.click(screen.getByRole("button", { name: "+ toolCompatibilityAddRule" }));
  await user.type(screen.getByRole("textbox", { name: "toolCompatibilityModelLabel" }), "^gemini-");
  await user.click(screen.getByRole("combobox", { name: "toolCompatibilitySignatureLabel" }));
  await user.click(screen.getByRole("option", { name: "toolCompatibilitySignatureFallback" }));

  expectCompatibilityCardControls();

  await user.click(screen.getByRole("button", { name: "Save channel" }));
  const saved = screen.getByTestId("saved-settings").textContent!;
  expect(JSON.parse(saved).model_tool_schema_compatibility).toEqual([
    { model_pattern: "^gemini-", profile: "gemini_compat", signature_fallback: "gemini" },
  ]);

  unmount();
  render(<ToolCompatibilityForm initialSettings={saved} />);
  await user.click(screen.getByRole("button", { name: "dataflowStep.tool_compatibility" }));
  expectCompatibilityCardControls();
  expect(screen.getByRole("combobox", { name: "toolCompatibilitySignatureLabel" })).toHaveTextContent("toolCompatibilitySignatureFallback");
});

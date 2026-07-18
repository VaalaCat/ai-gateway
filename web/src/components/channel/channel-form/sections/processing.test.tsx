import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { emptyForm } from "../types";
import { ProcessingSection } from "./processing";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

vi.mock("@/lib/api/channels", () => ({
  useChannelDataFlow: () => ({ data: undefined }),
}));

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

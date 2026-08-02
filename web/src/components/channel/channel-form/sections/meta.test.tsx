import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { expect, it, vi } from "vitest";

import { emptyForm } from "../types";
import { MetaSection } from "./meta";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => {
    if (key === "publicDisplayName") return "公开名称";
    if (key === "publicDisplayNameAutoPreview") return "留空将恢复安全自动名称";
    return key;
  },
}));

function MetaSectionHarness() {
  const [form, setForm] = useState({ ...emptyForm, name: "private-internal-name" });
  return <MetaSection form={form} setForm={setForm} channelTypes={[]} />;
}

it("explains the safe automatic-name behavior without copying the internal name", () => {
  render(<MetaSectionHarness />);

  const explanation = screen.getByText("留空将恢复安全自动名称");
  expect(explanation).toBeVisible();
  expect(explanation).not.toHaveTextContent("private-internal-name");
});

it("accepts a public display name containing exactly 64 emoji", async () => {
  const user = userEvent.setup();
  render(<MetaSectionHarness />);
  const input = screen.getByLabelText("公开名称");
  const maximumName = "😀".repeat(64);

  await user.type(input, maximumName);
  expect(input).toHaveValue(maximumName);
});

it("does not update a 64-code-point public display name with a 65th emoji", async () => {
  const user = userEvent.setup();
  render(<MetaSectionHarness />);
  const input = screen.getByLabelText("公开名称");
  const maximumName = "😀".repeat(64);

  await user.type(input, maximumName);
  await user.type(input, "😀");
  expect(input).toHaveValue(maximumName);
});

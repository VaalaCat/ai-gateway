import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import { AgentAddressEditor } from "./agent-address-editor";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

it("discards a local address draft immediately when the authoritative value changes", async () => {
  const user = userEvent.setup();
  const onChange = vi.fn();
  const first = JSON.stringify([{ url: "http://old.example", tag: "old" }]);
  const replacement = JSON.stringify([{ url: "http://server.example", tag: "server" }]);
  const { rerender } = render(<AgentAddressEditor value={first} onChange={onChange} />);

  const url = screen.getByPlaceholderText("addressUrlPlaceholder");
  await user.clear(url);
  await user.type(url, "http://draft.example");
  rerender(<AgentAddressEditor value={replacement} onChange={onChange} />);

  expect(screen.getByPlaceholderText("addressUrlPlaceholder")).toHaveValue("http://server.example");
  expect(screen.getByPlaceholderText("addressTagPlaceholder")).toHaveValue("server");
});

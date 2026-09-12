import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { TraceContentBlock } from "./trace-content-block";

describe("TraceContentBlock", () => {
  it("renders valid JSON as an expanded hierarchy", () => {
    render(<TraceContentBlock content={'{"request":{"model":"gpt-test"}}'} />);

    expect(screen.getByRole("button", { name: /request/ })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByText('"model"')).toBeInTheDocument();
    expect(screen.getByText('"gpt-test"')).toBeInTheDocument();
  });

  it("keeps messages open and collapses each long message item with a 30-character preview", () => {
    const messages = [
      { role: "system", content: "You are a careful coding assistant with a long prompt. ".repeat(4) },
      { role: "user", content: "Inspect the trace and explain what happened." },
    ];
    const { container } = render(
      <TraceContentBlock content={JSON.stringify({ messages, model: "deepseek-reasoner" })} />,
    );

    expect(screen.getByRole("button", { name: "messages" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByText("[2 items]")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "messages item 1" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(container.querySelectorAll('[data-slot="json-preview"]')[0]).toHaveTextContent(
      '{"role":"system","content":"Yo…',
    );
    expect(screen.queryByText(/careful coding assistant/)).not.toBeInTheDocument();
  });

  it("expands a long message item independently on click", async () => {
    const user = userEvent.setup();
    const messages = [
      { role: "system", content: "You are a careful coding assistant with a long prompt. ".repeat(4) },
      { role: "user", content: "Inspect the trace and explain what happened." },
    ];
    render(<TraceContentBlock content={JSON.stringify({ messages })} />);

    await user.click(screen.getByRole("button", { name: "messages item 1" }));

    expect(screen.getByRole("button", { name: "messages item 1" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByRole("button", { name: "messages item 2" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(
      screen.getByText(`"${"You are a careful coding assistant with a long prompt. ".repeat(4)}"`),
    ).toBeInTheDocument();
  });

  it("keeps a short message item on one line without an ellipsis", () => {
    const { container } = render(
      <TraceContentBlock
        content={JSON.stringify({ messages: [{ role: "assistant", content: "短消息" }] })}
      />,
    );

    expect(screen.getByRole("button", { name: "messages" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
    expect(screen.getByRole("button", { name: "messages item 1" })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(container.querySelector('[data-slot="json-preview"]')).toHaveTextContent(
      '{"role":"assistant","content":"短消息"}',
    );
    expect(container.querySelector('[data-slot="json-preview"]')).not.toHaveTextContent("…");
  });

  it("keeps non-JSON content unchanged", () => {
    const content = "event: message\ndata: plain-text";
    render(<TraceContentBlock content={content} />);

    expect(screen.getByText(/event: message/).textContent).toBe(content);
  });

  it("uses bounded two-axis overflow without wrapping long lines", () => {
    render(<TraceContentBlock content={"x".repeat(2_000)} />);

    const block = screen.getByText("x".repeat(2_000));
    expect(block).toHaveClass("max-h-60", "overflow-auto", "whitespace-pre");
    expect(block).not.toHaveClass("whitespace-pre-wrap", "break-all");
  });

  it("bounds the JSON tree overflow inside the trace block", () => {
    const { container } = render(
      <TraceContentBlock content={JSON.stringify({ value: "x".repeat(2_000) })} />,
    );

    expect(container.querySelector('[data-slot="trace-json-tree"]')).toHaveClass(
      "w-full",
      "min-w-0",
      "max-w-full",
      "overflow-auto",
    );
  });
});

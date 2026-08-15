import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TraceContentBlock } from "./trace-content-block";

describe("TraceContentBlock", () => {
  it("pretty-prints valid JSON", () => {
    render(<TraceContentBlock content={'{"request":{"model":"gpt-test"}}'} />);

    expect(screen.getByText(/"request"/).textContent).toBe(
      '{\n  "request": {\n    "model": "gpt-test"\n  }\n}',
    );
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
});

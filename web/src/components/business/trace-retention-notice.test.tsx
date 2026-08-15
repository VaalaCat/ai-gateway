import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";

import { TraceRetentionNotice } from "./trace-retention-notice";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

it.each([
  "headers_only",
  "body_truncated",
  "body_trimmed",
  "trace_stripped",
  "billing_only",
  "disabled",
] as const)("shows the %s retention reason", (status) => {
  render(<TraceRetentionNotice status={status} />);
  expect(screen.getByText(`${status}.label`)).toBeInTheDocument();
  expect(screen.getByText(`${status}.description`)).toBeInTheDocument();
});

it("renders nothing for legacy rows without a status", () => {
  const { container } = render(<TraceRetentionNotice />);
  expect(container).toBeEmptyDOMElement();
});

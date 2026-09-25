import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  HTTPStatusBadge,
  PermissionScopeBadge,
  ProtocolBadge,
  TraceCaptureBadge,
} from "./api-badges";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

describe("ProtocolBadge", () => {
  it.each([
    ["http", "protocol.http", "http"],
    ["websocket", "protocol.websocket", "websocket"],
    ["smtp", "protocol.unknown", "unknown"],
  ])("uses the localized %s protocol state", (protocol, label, state) => {
    render(<ProtocolBadge protocol={protocol} />);

    expect(screen.getByText(label)).toHaveAttribute("data-slot", "api-protocol-badge");
    expect(screen.getByText(label)).toHaveAttribute("data-state", state);
  });
});

describe("HTTPStatusBadge", () => {
  it.each([
    [200, "success", "statusSuccess 200", "default"],
    [399, "success", "statusSuccess 399", "default"],
    [400, "failed", "statusFailed 400", "destructive"],
    [599, "failed", "statusFailed 599", "destructive"],
    [0, "failed", "noResponse", "destructive"],
  ])("presents HTTP status %i with the usage-log %s treatment", (statusCode, state, label, variant) => {
    render(<HTTPStatusBadge statusCode={statusCode} />);

    const badge = screen.getByText(label);
    expect(badge).toHaveAttribute("data-slot", "api-http-status-badge");
    expect(badge).toHaveAttribute("data-state", state);
    expect(badge).toHaveAttribute("data-variant", variant);
  });
});

describe("PermissionScopeBadge", () => {
  it.each([
    [0, "permission.global", "global"],
    [42, "permission.scoped", "scoped"],
  ])("labels resource ID %i as a localized %s permission", (resourceId, label, state) => {
    render(<PermissionScopeBadge resourceId={resourceId} />);

    expect(screen.getByText(label)).toHaveAttribute("data-slot", "api-permission-scope-badge");
    expect(screen.getByText(label)).toHaveAttribute("data-state", state);
  });
});

describe("TraceCaptureBadge", () => {
  it.each([
    ["captured", false, "trace.captured", "captured"],
    ["captured", true, "trace.truncated", "truncated"],
    ["empty", false, "trace.empty", "empty"],
    ["skipped", false, "trace.skipped", "skipped"],
    ["unavailable", false, "trace.unavailable", "unavailable"],
    ["unexpected", false, "trace.unavailable", "unavailable"],
  ])("presents %s capture data as %s", (status, truncated, label, state) => {
    render(<TraceCaptureBadge status={status} reason="content_encoding" truncated={truncated} />);

    expect(screen.getByText(label)).toHaveAttribute("data-slot", "api-trace-capture-badge");
    expect(screen.getByText(label)).toHaveAttribute("data-state", state);
  });
});

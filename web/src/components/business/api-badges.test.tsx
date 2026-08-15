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
    [200, "success"],
    [299, "success"],
    [300, "redirect"],
    [399, "redirect"],
    [400, "client-error"],
    [499, "client-error"],
    [500, "server-error"],
    [599, "server-error"],
    [0, "unavailable"],
  ])("classifies HTTP status %i as %s", (statusCode, state) => {
    render(<HTTPStatusBadge statusCode={statusCode} />);

    expect(screen.getByText(String(statusCode))).toHaveAttribute("data-slot", "api-http-status-badge");
    expect(screen.getByText(String(statusCode))).toHaveAttribute("data-state", state);
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

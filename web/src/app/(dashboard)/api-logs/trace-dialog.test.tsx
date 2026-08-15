import { useState } from "react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/client";
import type { APIRequestLog } from "@/lib/api/api-logs";
import { TraceDialog } from "./_components/trace-dialog";

const state = vi.hoisted(() => ({ trace: {} as Record<string, unknown> }));
const clipboard = vi.hoisted(() => ({ copy: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/api-logs", async (importOriginal) => ({ ...(await importOriginal<typeof import("@/lib/api/api-logs")>()), useAPIRequestTrace: () => state.trace }));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: clipboard.copy }));

function body(overrides: Record<string, unknown> = {}) {
  return {
    captured: true,
    status: "captured",
    skip_reason: "",
    data: '{"ok":true}',
    captured_bytes: 11,
    total_bytes: 11,
    truncated: false,
    ...overrides,
  };
}

function trace(overrides: Record<string, unknown> = {}) {
  return {
    id: 31,
    request_id: "req-1",
    source_request_headers: { "x-source": ["source-header"] },
    source_request_trailers: {},
    source_request_headers_truncated: false,
    source_request_trailers_truncated: false,
    source_request_body: body(),
    request_headers: {},
    request_trailers: {},
    request_headers_truncated: false,
    request_trailers_truncated: false,
    request_body: body(),
    response_headers: {},
    response_trailers: {},
    response_headers_truncated: false,
    response_trailers_truncated: false,
    response_body: body(),
    created_at: 1_001,
    ...overrides,
  };
}

function request(overrides: Partial<APIRequestLog> = {}): APIRequestLog {
  return {
    id: 1,
    request_id: " req-1 ",
    user_id: 2,
    client_ip: "203.0.113.8",
    api_service_id: 7,
    api_service_name: "Weather",
    api_route_id: 9,
    api_route_name: "Forecast",
    api_upstream_id: 11,
    api_upstream_name: "Primary",
    token_id: 13,
    token_name: "production",
    protocol: "http",
    method: "POST",
    subpath: "",
    source_agent_id: "",
    execution_agent_id: "",
    agent_route_id: 0,
    agent_route_path: "",
    status_code: 201,
    duration_ms: 42,
    first_byte_ms: 0,
    request_bytes: 0,
    response_bytes: 0,
    websocket_close_code: null,
    provider_dispatch_known: false,
    provider_dispatched: false,
    quota_gate_decision: "",
    error_stage: "",
    error_code: "",
    service_missing_at_settlement: false,
    rate_limit_decision: "",
    rate_limit_wait_ms: 0,
    rate_limit_reason: "",
    rate_limit_hits: [],
    unit_price: 0,
    total_cost: 0,
    created_at: 1_001,
    ...overrides,
  };
}

function renderDialog(selectedRequest = request()) {
  return render(<TraceDialog request={selectedRequest} onOpenChange={() => {}} />);
}

describe("API request Trace dialog", () => {
  beforeEach(() => {
    state.trace = { data: trace(), error: null, isLoading: false };
    clipboard.copy.mockReset();
  });

  it("keeps a fixed header and footer around an independently scrolling loading body", () => {
    state.trace = { data: undefined, error: null, isLoading: true };

    renderDialog();

    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveClass("flex", "h-[90dvh]", "flex-col", "overflow-hidden");
    expect(within(dialog).getByText("trace")).toBeInTheDocument();
    expect(dialog.querySelector('[data-slot="dialog-footer"]')).toBeInTheDocument();
    expect(dialog.querySelector('[data-slot="trace-dialog-body"]')).toHaveClass("min-h-0", "flex-1");
    expect(dialog.querySelector('[data-slot="skeleton"]')).toBeInTheDocument();
  });

  it.each([
    [404, undefined, "traceNotFound"],
    [503, "LogDatabaseUnavailable", "logUnavailable"],
    [503, "OtherUnavailable", "traceLoadFailed"],
    [500, undefined, "traceLoadFailed"],
  ])("maps trace error %s/%s to %s", (status, code, message) => {
    state.trace = { data: undefined, error: new ApiError(status, "trace failed", code ? { code } : undefined), isLoading: false };

    renderDialog();

    expect(screen.getByRole("alert")).toHaveTextContent(message);
    expect(screen.queryByText("sourceRequestCapture")).not.toBeInTheDocument();
  });

  it("shows the selected request context and copies the opaque request id", async () => {
    const user = userEvent.setup();
    renderDialog();
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("req-1")).toBeInTheDocument();
    expect(dialog.querySelector('[data-slot="api-protocol-badge"]')).toHaveTextContent("protocol.http");
    expect(dialog.querySelector('[data-slot="api-http-status-badge"]')).toHaveTextContent("201");
    expect(within(dialog).getByText("42ms")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "copy" }));
    expect(clipboard.copy).toHaveBeenCalledWith(" req-1 ", { success: "copied", error: "copyFailed" });
  });

  it("updates header context when the selected log identity changes", () => {
    const view = renderDialog(request());
    view.rerender(<TraceDialog request={request({ request_id: "req-2", protocol: "websocket", status_code: 503, duration_ms: 99 })} onOpenChange={() => {}} />);
    expect(screen.getByText("req-2")).toBeInTheDocument();
    expect(screen.queryByText(" req-1 ")).not.toBeInTheDocument();
    expect(document.querySelector('[data-slot="api-protocol-badge"]')).toHaveTextContent("protocol.websocket");
  });

  it("exposes only the localized footer close action", () => {
    renderDialog();
    expect(screen.getAllByRole("button", { name: /close/i })).toHaveLength(1);
  });

  it("does not render empty JSON blocks for missing or empty headers and trailers", () => {
    state.trace = {
      data: trace({
        source_request_headers: undefined,
        source_request_trailers: {},
        request_headers: {},
        request_trailers: undefined,
        response_headers: {},
        response_trailers: {},
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    expect(screen.queryByText("headers")).not.toBeInTheDocument();
    expect(screen.queryByText("trailers")).not.toBeInTheDocument();
    expect(screen.getAllByText("body")).toHaveLength(3);
  });

  it("shows header and trailer truncation only where the backend reported it", () => {
    state.trace = {
      data: trace({
        source_request_headers_truncated: true,
        source_request_trailers_truncated: true,
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    expect(screen.getByText("headersTruncated")).toBeInTheDocument();
    expect(screen.getByText("trailersTruncated")).toBeInTheDocument();
  });

  it("derives captured, empty, and truncated body badges from captured facts", () => {
    state.trace = {
      data: trace({
        source_request_body: body({ captured_bytes: 1, total_bytes: 1 }),
        request_body: body({ captured_bytes: 0, total_bytes: 0, data: "" }),
        response_body: body({ captured_bytes: 5, total_bytes: 12, truncated: true }),
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    const badges = document.querySelectorAll('[data-slot="api-trace-capture-badge"]');
    expect(Array.from(badges).map((badge) => badge.getAttribute("data-state"))).toEqual([
      "captured",
      "empty",
      "truncated",
    ]);
    expect(screen.getByText("1 / 1")).toBeInTheDocument();
    expect(screen.getByText("0 / 0")).toBeInTheDocument();
    expect(screen.getByText("5 / 12")).toBeInTheDocument();
  });

  it.each([
    ["content_encoded", "skipReason.contentEncoded"],
    ["content_encoding", "skipReason.contentEncoded"],
    ["websocket", "skipReason.websocket"],
  ])("localizes skipped body reason %s without fabricating empty content", (reason, label) => {
    state.trace = {
      data: trace({
        source_request_body: body({
          captured: false,
          status: "skipped",
          skip_reason: reason,
          data: "",
          captured_bytes: 0,
          total_bytes: 12,
        }),
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    const source = screen.getByText("sourceRequestCapture").closest("section")!;
    expect(source.querySelector('[data-state="skipped"]')).toBeInTheDocument();
    expect(within(source).getByText(label)).toBeInTheDocument();
    expect(within(source).queryByText("empty", { exact: false })).not.toBeInTheDocument();
    const bodyCapture = source.querySelector('[data-slot="trace-capture-body"]');
    expect(bodyCapture).toBeInTheDocument();
    expect(bodyCapture!.querySelector("pre")).toBeNull();
  });

  it("shows unavailable bodies without inventing byte counts", () => {
    state.trace = {
      data: trace({
        source_request_body: {
          captured: false,
          status: "unavailable",
          skip_reason: "",
          data: "",
          truncated: false,
        },
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    const source = screen.getByText("sourceRequestCapture").closest("section")!;
    expect(source.querySelector('[data-state="unavailable"]')).toBeInTheDocument();
    expect(source.textContent).not.toContain("0 / 0");
  });

  it("escapes captured header and body text inside pre elements", () => {
    const unsafe = '</pre><script data-unsafe="yes">alert(1)</script>';
    state.trace = {
      data: trace({
        source_request_headers: { unsafe: [unsafe] },
        source_request_body: body({ data: unsafe, captured_bytes: unsafe.length, total_bytes: unsafe.length }),
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    const source = screen.getByText("sourceRequestCapture").closest("section")!;
    const headerBlock = source.querySelector('[data-slot="trace-capture-json"] pre');
    const bodyBlock = source.querySelector('[data-slot="trace-capture-body"] pre');
    expect(headerBlock).toHaveTextContent("</pre><script");
    expect(bodyBlock).toHaveTextContent(unsafe);
    expect(document.querySelector('script[data-unsafe="yes"]')).toBeNull();
    expect(headerBlock?.closest("pre")).toBe(headerBlock);
    expect(bodyBlock?.closest("pre")).toBe(bodyBlock);
  });

  it("pretty-prints JSON and scrolls long captures without wrapping", () => {
    state.trace = {
      data: trace({
        source_request_headers: { long: ["x".repeat(2_000)] },
        source_request_body: body({
          data: JSON.stringify({ payload: "y".repeat(2_000) }),
          captured_bytes: 2_014,
          total_bytes: 2_014,
        }),
      }),
      error: null,
      isLoading: false,
    };

    renderDialog();

    const source = screen.getByText("sourceRequestCapture").closest("section")!;
    for (const block of [
      source.querySelector('[data-slot="trace-capture-json"] pre'),
      source.querySelector('[data-slot="trace-capture-body"] pre'),
    ]) {
      expect(block).toHaveClass("max-h-60", "overflow-auto", "whitespace-pre");
      expect(block).not.toHaveClass("whitespace-pre-wrap", "break-all");
    }
    expect(source.querySelector('[data-slot="trace-capture-body"] pre')).toHaveTextContent(
      /^\{\s+"payload": "y+/,
    );
  });

  it("clears the selected request and restores focus to the opener when closed", async () => {
    const user = userEvent.setup();

    function Harness() {
      const [selectedRequest, setSelectedRequest] = useState<APIRequestLog | null>(null);
      return (
        <>
          <button type="button" onClick={() => setSelectedRequest(request())}>open trace</button>
          <TraceDialog
            request={selectedRequest}
            onOpenChange={(open) => { if (!open) setSelectedRequest(null); }}
          />
        </>
      );
    }

    render(<Harness />);
    const opener = screen.getByRole("button", { name: "open trace" });
    await user.click(opener);
    await user.click(screen.getByRole("button", { name: "close" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(opener).toHaveFocus();
  });
});

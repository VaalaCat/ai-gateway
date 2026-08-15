import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIRequestLog } from "@/lib/api/api-logs";
import { copyTextWithFeedback } from "@/lib/utils/clipboard";
import { APIRequestDetails } from "./request-details";

const state = vi.hoisted(() => ({
  traceRequestIDs: [] as Array<string | null>,
  trace: {} as Record<string, unknown>,
  user: undefined as { id: number; username: string } | undefined,
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/api-logs", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/api-logs")>()),
  useAPIRequestTrace: (requestID: string | null) => {
    state.traceRequestIDs.push(requestID);
    return state.trace;
  },
}));
vi.mock("@/components/business/date-cell", () => ({ DateCell: ({ timestamp }: { timestamp: number }) => <span>{timestamp}</span> }));
vi.mock("@/components/business/duration-cell", () => ({ DurationCell: ({ ms }: { ms: number }) => <span>{ms}ms</span> }));
vi.mock("@/lib/api/users", () => ({
  useUser: () => ({ data: state.user }),
  useUsers: () => ({ data: undefined }),
}));
vi.mock("@/components/business/entity-hover/entity-hover-card", () => ({
  EntityHoverCard: () => null,
  hasEntityHoverBody: () => false,
}));
vi.mock("@/lib/utils/clipboard", () => ({
  copyTextWithFeedback: vi.fn().mockResolvedValue(true),
}));

function request(overrides: Partial<APIRequestLog> = {}): APIRequestLog {
  return {
    id: 1,
    request_id: "req-1",
    user_id: 2,
    token_id: 3,
    token_name: "production",
    client_ip: "203.0.113.8",
    api_service_id: 7,
    api_service_name: "Weather",
    api_route_id: 9,
    api_route_name: "Forecast",
    api_upstream_id: 11,
    api_upstream_name: "Primary",
    protocol: "http",
    method: "POST",
    subpath: "/daily",
    source_agent_id: "agent-source",
    execution_agent_id: "agent-exec",
    agent_route_id: 13,
    agent_route_path: "/egress/weather",
    status_code: 201,
    duration_ms: 42,
    first_byte_ms: 12,
    request_bytes: 128,
    response_bytes: 512,
    websocket_close_code: null,
    provider_dispatch_known: true,
    provider_dispatched: true,
    quota_gate_decision: "allow",
    error_stage: "",
    error_code: "",
    service_missing_at_settlement: false,
    rate_limit_decision: "allow",
    rate_limit_wait_ms: 0,
    rate_limit_reason: "",
    rate_limit_hits: [],
    unit_price: 100,
    total_cost: 100,
    created_at: 1_000,
    ...overrides,
  };
}

describe("APIRequestDetails", () => {
  beforeEach(() => {
    state.traceRequestIDs = [];
    state.trace = { data: undefined, error: null, isLoading: false };
    state.user = undefined;
    vi.mocked(copyTextWithFeedback).mockClear();
  });

  it("shows the current username with its stable user ID", () => {
    state.user = { id: 2, username: "alice" };

    render(<APIRequestDetails request={request()} />);

    const userID = screen.getByText("#2");
    expect(userID.parentElement).toHaveTextContent("alice#2");
  });

  it("falls back to the stable user ID when the user no longer resolves", () => {
    render(<APIRequestDetails request={request()} />);

    expect(screen.getByText("#2")).toBeInTheDocument();
    expect(screen.queryByText("alice")).not.toBeInTheDocument();
  });

  it("keeps a zero user ID explicit instead of rendering an empty value", () => {
    render(<APIRequestDetails request={request({ user_id: 0 })} />);

    expect(screen.getByText("#0")).toBeInTheDocument();
  });

  it("shows routing, execution, response, gateway, and billing facts", () => {
    render(<APIRequestDetails request={request()} />);

    for (const value of [
      "req-1", "production", "203.0.113.8", "Weather", "Forecast", "Primary", "/daily",
      "agent-source", "agent-exec", "/egress/weather", "201", "42ms", "12ms",
      "128 B", "512 B", "allow", "unitPrice", "totalCost",
    ]) {
      expect(screen.getAllByText(value).length).toBeGreaterThan(0);
    }
  });

  it("puts the request result and route chain before the compact facts", () => {
    render(<APIRequestDetails request={request()} />);

    expect(screen.getByTestId("api-log-result-summary")).toHaveTextContent("201");
    expect(screen.getByTestId("api-log-result-summary")).toHaveTextContent("POST");
    expect(screen.getByTestId("api-log-result-summary")).toHaveTextContent("42ms");
    expect(screen.getByTestId("api-log-route-chain")).toHaveTextContent("Weather");
    expect(screen.getByTestId("api-log-route-chain")).toHaveTextContent("Forecast");
    expect(screen.getByTestId("api-log-route-chain")).toHaveTextContent("Primary");
  });

  it("hides upstream and Agent facts in the ordinary-user projection", () => {
    render(<APIRequestDetails request={request()} showInternal={false} />);

    expect(screen.getByTestId("api-log-route-chain")).toHaveTextContent("Weather");
    expect(screen.getByTestId("api-log-route-chain")).toHaveTextContent("Forecast");
    for (const hidden of ["Primary", "agent-source", "agent-exec", "/egress/weather", "203.0.113.8"]) {
      expect(screen.queryByText(hidden)).not.toBeInTheDocument();
    }
  });

  it("truncates a long request ID, reveals it on hover, and copies the full value", async () => {
    const user = userEvent.setup();
    const requestID = "req-576426fa-6fd2-4583-825a-0a86ccdf653f";
    render(<APIRequestDetails request={request({ request_id: requestID })} />);

    const requestIDButton = screen.getByRole("button", { name: new RegExp(requestID) });
    expect(requestIDButton).toHaveClass("truncate");

    await user.hover(requestIDButton);
    expect(await screen.findByRole("tooltip")).toHaveTextContent(requestID);

    await user.click(requestIDButton);
    expect(copyTextWithFeedback).toHaveBeenCalledWith(requestID, {
      success: "copied",
      error: "copyFailed",
    });
  });

  it("loads Trace only after the administrator asks for it", () => {
    state.trace = {
      data: {
        request_id: "req-1",
        source_request_headers: { "x-source": ["captured"] },
        source_request_body: { captured: true, status: "captured", data: "source-body", captured_bytes: 11, total_bytes: 11 },
      },
      error: null,
      isLoading: false,
    };

    render(<APIRequestDetails request={request()} />);
    expect(state.traceRequestIDs.at(-1)).toBeNull();
    expect(screen.queryByText("sourceRequestCapture")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "loadTrace" }));

    expect(state.traceRequestIDs.at(-1)).toBe("req-1");
    expect(screen.getByText("sourceRequestCapture")).toBeInTheDocument();
    expect(screen.getByText("source-body")).toBeInTheDocument();
  });

  it("lets loaded Trace captures fill the expanded row", () => {
    state.trace = {
      data: {
        request_id: "req-1",
        source_request_headers: { "x-source": ["captured"] },
        source_request_body: { captured: true, status: "captured", data: "source-body", captured_bytes: 11, total_bytes: 11 },
      },
      error: null,
      isLoading: false,
    };

    render(<APIRequestDetails request={request()} />);
    fireEvent.click(screen.getByRole("button", { name: "loadTrace" }));

    const traceSection = screen.getByRole("heading", { name: "trace" }).closest("section");
    const capture = screen.getByText("sourceRequestCapture").closest("section");
    expect(traceSection).toHaveClass("min-w-0");
    expect(traceSection).not.toHaveClass("items-start");
    expect(capture).toHaveClass("w-full", "min-w-0");
  });

  it("keeps absent optional facts readable and reports a missing Trace", () => {
    state.trace = { data: undefined, error: new Error("missing"), isLoading: false };
    render(<APIRequestDetails request={request({ source_agent_id: "", execution_agent_id: "", subpath: "" })} />);

    expect(screen.getAllByText("-").length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "loadTrace" }));
    expect(screen.getByRole("alert")).toHaveTextContent("traceLoadFailed");
  });
});

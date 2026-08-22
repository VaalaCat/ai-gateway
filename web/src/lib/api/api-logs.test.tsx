import type { ReactNode } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "./client";
import { createTestQueryClient } from "@/test/render";
import { useAPIRequestLogs, useAPIRequestTrace } from "./api-logs";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return { ...actual, api: { ...actual.api, get: apiGet } };
});

function wrapper({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={createTestQueryClient()}>{children}</QueryClientProvider>;
}

describe("generic API log hooks", () => {
  beforeEach(() => apiGet.mockReset());

  it("sends selected entity, status, time, and server pagination filters to the log endpoint", async () => {
    apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 1, page_size: 20 });
    const { result } = renderHook(() => useAPIRequestLogs({ page: 3, page_size: 50, api_service_id: 7, api_route_id: 9, api_upstream_id: 11, token_id: 12, status_code: 502, request_id: "req-1", start: 1_000, end: 2_000 }), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/admin/api-request-logs?page=3&page_size=50&api_service_id=7&api_route_id=9&api_upstream_id=11&token_id=12&status_code=502&request_id=req-1&start=1000&end=2000");
  });

  it("preserves status zero and a zero start boundary in the log query", async () => {
    apiGet.mockResolvedValueOnce({ data: [], total: 0, page: 1, page_size: 20 });
    const { result } = renderHook(() => useAPIRequestLogs({ status_code: 0, start: 0, end: 1 }), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith("/admin/api-request-logs?status_code=0&start=0&end=1");
  });

  it("does not request logs while the caller disables the capability gate", () => {
    renderHook(() => useAPIRequestLogs({}, "admin", { enabled: false }), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it("does not request a trace until a request id is selected", () => {
    renderHook(() => useAPIRequestTrace(null), { wrapper });
    expect(apiGet).not.toHaveBeenCalled();
  });

  it.each([
    ["req-1", "/admin/api-request-traces?request_id=req-1"],
    ["\u00a0edge\u00a0", "/admin/api-request-traces?request_id=%C2%A0edge%C2%A0"],
    ["question?hash#slash/percent%", "/admin/api-request-traces?request_id=question%3Fhash%23slash%2Fpercent%25"],
  ])("requests an opaque trace ID %s through the query endpoint", async (requestID, endpoint) => {
    apiGet.mockResolvedValueOnce({ id: 31, request_id: requestID, created_at: 1_001 });
    const { result } = renderHook(() => useAPIRequestTrace(requestID), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(endpoint);
  });

  it("uses current-user endpoints for portal logs and traces", async () => {
    apiGet
      .mockResolvedValueOnce({ data: [], total: 0, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ id: 31, request_id: "mine", created_at: 1_001 });
    const logs = renderHook(() => useAPIRequestLogs({ request_id: "mine" }, "portal"), { wrapper });
    await waitFor(() => expect(logs.result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenNthCalledWith(1, "/api-request-logs?request_id=mine");

    const trace = renderHook(() => useAPIRequestTrace("mine", "portal"), { wrapper });
    await waitFor(() => expect(trace.result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenNthCalledWith(2, "/api-request-traces?request_id=mine");
  });

  it("consumes an administrator error message from the log payload", async () => {
    apiGet.mockResolvedValueOnce({
      data: [{ request_id: "req-failed", error_message: "connection refused" }],
      total: 1,
      page: 1,
      page_size: 20,
    });

    const { result } = renderHook(() => useAPIRequestLogs({}, "admin"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data?.data[0]?.error_message).toBe("connection refused");
  });

  it("accepts a Portal log payload without an internal error message", async () => {
    apiGet.mockResolvedValueOnce({
      data: [{ request_id: "req-portal" }],
      total: 1,
      page: 1,
      page_size: 20,
    });

    const { result } = renderHook(() => useAPIRequestLogs({}, "portal"), { wrapper });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(result.current.data?.data[0]?.error_message).toBeUndefined();
  });

  it("exposes a 503 log-store error instead of converting it to an empty result", async () => {
    apiGet.mockRejectedValueOnce(new ApiError(503, "log database is temporarily unavailable"));
    const { result } = renderHook(() => useAPIRequestLogs({}), { wrapper });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toMatchObject({ status: 503 });
  });
});

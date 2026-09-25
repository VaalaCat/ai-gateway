import { beforeEach, describe, expect, it, vi } from "vitest";

import type { APIRequestLog, APIRequestTrace } from "./api-logs";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("./client", () => ({ api: { get }, buildQuery: (values: Record<string, unknown>) => {
  const query = new URLSearchParams(values as Record<string, string>).toString();
  return query ? `?${query}` : "";
} }));

import { downloadAPIRequestLogDebugFile } from "./api-log-debug-file";

const log = {
  id: 7,
  request_id: "req-api-debug",
  status_code: 502,
  has_trace: true,
} as APIRequestLog;

const trace = {
  id: 11,
  request_id: log.request_id,
  response_body: { captured: true, status: "captured", data: "upstream failed" },
  created_at: 1_000,
} as APIRequestTrace;

describe("API request debug file download", () => {
  beforeEach(() => {
    get.mockReset();
    vi.restoreAllMocks();
  });

  it.each([
    ["admin", "/admin/api-request-traces?request_id=req-api-debug"],
    ["portal", "/api-request-traces?request_id=req-api-debug"],
  ] as const)("downloads one correlated %s API log and trace file", async (scope, endpoint) => {
    let blob: Blob | undefined;
    let filename = "";
    vi.spyOn(URL, "createObjectURL").mockImplementation((value) => {
      if (value instanceof Blob) blob = value;
      return "blob:api-log-debug";
    });
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function () {
      filename = this.download;
    });
    get.mockResolvedValueOnce(trace);

    await downloadAPIRequestLogDebugFile(log, scope);

    expect(get).toHaveBeenCalledWith(endpoint);
    expect(JSON.parse(await blob!.text())).toEqual({
      format: "ai-gateway-api-log-debug-v1",
      log,
      trace,
    });
    expect(filename).toBe(`ai-gateway-api-log-${log.request_id}.debug.json`);
    expect(revoke).toHaveBeenCalledWith("blob:api-log-debug");
  });

  it("sanitizes the local filename without rewriting the opaque request ID", async () => {
    const requestID = "question?hash#slash/percent%\nline";
    let filename = "";
    get.mockResolvedValueOnce({ ...trace, request_id: requestID });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:opaque-api-id");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function () {
      filename = this.download;
    });

    await downloadAPIRequestLogDebugFile({ ...log, request_id: requestID }, "admin");

    expect(get).toHaveBeenCalledWith("/admin/api-request-traces?request_id=question%3Fhash%23slash%2Fpercent%25%0Aline");
    expect(filename).toBe("ai-gateway-api-log-question_hash_slash_percent_line.debug.json");
  });

  it("does not create a partial file when the trace is unavailable", async () => {
    const createObjectURL = vi.spyOn(URL, "createObjectURL");
    get.mockRejectedValueOnce(new Error("trace unavailable"));

    await expect(downloadAPIRequestLogDebugFile(log, "admin")).rejects.toThrow("trace unavailable");

    expect(createObjectURL).not.toHaveBeenCalled();
  });
});

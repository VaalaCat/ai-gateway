import { beforeEach, describe, expect, it, vi } from "vitest";

import type { UsageLog, UsageLogTrace } from "@/lib/types";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("@/lib/api/client", () => ({ api: { get } }));

import { downloadLogDebugFile } from "./log-debug-file";

const log = {
  id: 7,
  request_id: "req-a32f42e1-bf53-48ff-892f-552369643097",
  model_name: "gpt-5.6-terra",
  status: 0,
  error_message: "encode response: overloaded",
  has_trace: true,
} as UsageLog;

const traces = [
  {
    id: 11,
    request_id: log.request_id,
    attempt_index: 0,
    response_body: "event: error",
    client_response_body: "event: error",
  },
  {
    id: 12,
    request_id: log.request_id,
    attempt_index: 1,
    response_body: "event: response.completed",
    client_response_body: "event: response.completed",
  },
] as UsageLogTrace[];

describe("log debug file download", () => {
  beforeEach(() => {
    get.mockReset();
    vi.restoreAllMocks();
  });

  it("downloads one formatted correlated log and trace file", async () => {
    let blob: Blob | undefined;
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(URL, "createObjectURL").mockImplementation((value) => {
      if (value instanceof Blob) blob = value;
      return "blob:log-debug";
    });
    get.mockResolvedValueOnce(traces);

    await downloadLogDebugFile(log);

    expect(get).toHaveBeenCalledWith(`/logs/${log.request_id}/trace`);
    expect(blob?.type).toBe("application/json");
    expect(JSON.parse(await blob!.text())).toEqual({
      format: "ai-gateway-log-debug-v1",
      log,
      traces,
    });
    expect(document.querySelector(`a[download="ai-gateway-log-${log.request_id}.debug.json"]`)).not.toBeInTheDocument();
    expect(click).toHaveBeenCalledOnce();
    expect(revoke).toHaveBeenCalledWith("blob:log-debug");
  });

  it("keeps an opaque request ID in the API path but sanitizes the local filename", async () => {
    let filename = "";
    const requestId = "question?hash#slash/percent%\nline";
    get.mockResolvedValueOnce([]);
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:opaque-id");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function () {
      filename = this.download;
    });

    await downloadLogDebugFile({ ...log, request_id: requestId });

    expect(get).toHaveBeenCalledWith("/logs/question%3Fhash%23slash%2Fpercent%25%0Aline/trace");
    expect(filename).toBe("ai-gateway-log-question_hash_slash_percent_line.debug.json");
  });

  it("does not create a partial file when loading traces fails", async () => {
    const createObjectURL = vi.spyOn(URL, "createObjectURL");
    get.mockRejectedValueOnce(new Error("trace unavailable"));

    await expect(downloadLogDebugFile(log)).rejects.toThrow("trace unavailable");

    expect(createObjectURL).not.toHaveBeenCalled();
  });
});

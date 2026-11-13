import { describe, expect, it } from "vitest";
import type { GlobalInflightRow } from "@/lib/types";
import { inflightChannel, reconcileInflightRequests } from "./inflight";

const request = (agent_id: number, id: number): GlobalInflightRow => ({ agent_id, id, agent_name: `Node ${agent_id}`, req_id: "same-client-id", stage: "client_encode", elapsed_ms: 120000, queued_ms: 0, queued_reason: "", view: { user_id: 7, channel_id: 0, model_name: "model", is_stream: true } });

describe("in-flight snapshots", () => {
  it("updates selected progress, keeps unknown nodes, and only confirms departure after a successful node refresh", () => {
    const initial = reconcileInflightRequests([], [request(2, 1), request(1, 1), request(1, 2)], [1, 2], null, 1000);
    expect(initial.map(({ row }) => [row.agent_id, row.id])).toEqual([[1, 1], [1, 2], [2, 1]]);
    const newer = { ...request(1, 1), progress: { client_body_bytes: 4096, client_idle_ms: 200, upstream_body_bytes: null, upstream_idle_ms: null } };
    const partial = reconcileInflightRequests(initial, [newer], [1], "1:1", 2000);
    expect(partial[0]).toMatchObject({ row: { progress: { client_body_bytes: 4096 } }, status: "current", observedAt: 2000 });
    expect(partial[1]).toMatchObject({ row: { agent_id: 2 }, status: "stale", observedAt: 1000 });
    const unknown = reconcileInflightRequests(partial, [], [], "1:1", 3000);
    expect(unknown[0].status).toBe("stale");
    const departed = reconcileInflightRequests(unknown, [], [1, 2], "1:1", 4000);
    expect(departed).toHaveLength(1);
    expect(departed[0]).toMatchObject({ status: "departed", observedAt: 2000 });
  });

  it.each([
    { view: { channel_id: 0 }, attempt: undefined, expected: { id: null, name: "", private: false } },
    { view: { channel_id: 0, private_channel_id: 7 }, attempt: undefined, expected: { id: 7, name: "", private: true } },
    { view: { channel_id: 11 }, attempt: { seq: 2, channel_id: 7, channel_name: "BYOK current", source: "private", real_model: "upstream" }, expected: { id: 7, name: "BYOK current", private: true } },
  ])("resolves channel identity without #0: $expected", ({ view, attempt, expected }) => {
    const row = request(1, 1);
    expect(inflightChannel({ ...row, view: { ...row.view, ...view }, current_attempt: attempt })).toEqual(expected);
  });

});

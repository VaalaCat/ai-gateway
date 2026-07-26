import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useBillingInsights } from "./billing-insights";
import { useMarketShare, useMetricTrend } from "./dashboard";
import { useModelDistribution } from "./stats";
import { createTestQueryClient, queryClientWrapper } from "@/test/render";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
  return { ...actual, api: { ...actual.api, get: apiGet } };
});

const metricResponse = (metric: "ttft" | "tps", stat: "avg" | "p95" | "p5") => ({
  metric,
  stat,
  unit: metric === "ttft" ? "ms" : "tokens/s",
  estimated: stat !== "avg",
  buckets: [],
  series_order: [],
});

beforeEach(() => apiGet.mockReset());

describe("chart API query contracts", () => {
  it("sends billing token and top n using the backend query names", async () => {
    apiGet.mockResolvedValueOnce({ trend: [], cost_trend_stacked: { buckets: [], series_order: [] }, cache_saving: {} });
    const client = createTestQueryClient();
    const { result } = renderHook(() => useBillingInsights({
      from: 100,
      to: 200,
      gran: "hour",
      model: "gpt-5",
      user_id: 42,
      token_id: 9,
      top_n: 20,
    }), { wrapper: queryClientWrapper(client) });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/billing/insights?start=100&end=200&gran=hour&model=gpt-5&user_id=42&token_id=9&top_n=20",
    );
  });

  it("includes metric stat, model, user and top n in both URL and query identity", async () => {
    apiGet.mockResolvedValueOnce(metricResponse("ttft", "p95"));
    const client = createTestQueryClient();
    const { result } = renderHook(() => useMetricTrend("ttft", "model", 100, 200, {
      gran: "hour",
      stat: "p95",
      model: "gpt-5",
      user_id: 42,
      top_n: 10,
    }), { wrapper: queryClientWrapper(client) });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/stats/metric-trend?metric=ttft&dim=model&start=100&end=200&gran=hour&stat=p95&top_n=10&model=gpt-5&user_id=42",
    );
    expect(client.getQueryCache().getAll()[0]?.queryKey).toEqual([
      "metric-trend", "ttft", "model", 100, 200, "hour", "p95", 10, "gpt-5", 42,
    ]);
  });

  it("does not retain the previous metric response while the next query is pending", async () => {
    let finishTPS: ((value: ReturnType<typeof metricResponse>) => void) | undefined;
    apiGet
      .mockResolvedValueOnce(metricResponse("ttft", "avg"))
      .mockImplementationOnce(() => new Promise((resolve) => { finishTPS = resolve; }));
    const client = createTestQueryClient();
    const { result, rerender } = renderHook(
      ({ metric }) => useMetricTrend(metric, "model", 100, 200, { stat: "avg", top_n: 5 }),
      { initialProps: { metric: "ttft" as "ttft" | "tps" }, wrapper: queryClientWrapper(client) },
    );
    await waitFor(() => expect(result.current.data?.metric).toBe("ttft"));

    rerender({ metric: "tps" });
    expect(result.current.data).toBeUndefined();

    await act(async () => finishTPS?.(metricResponse("tps", "avg")));
    await waitFor(() => expect(result.current.data?.metric).toBe("tps"));
  });

  it("sends top n through both independent distribution endpoints", async () => {
    apiGet.mockResolvedValue({ buckets: [], series_order: [] });
    const client = createTestQueryClient();
    const market = renderHook(() => useMarketShare("model", 100, 200, { top_n: 20 }), {
      wrapper: queryClientWrapper(client),
    });
    const distribution = renderHook(() => useModelDistribution({
      start: 100, end: 200, gran: "day", user_id: 42, top_n: 20,
    }), { wrapper: queryClientWrapper(client) });

    await waitFor(() => expect(market.result.current.isSuccess).toBe(true));
    await waitFor(() => expect(distribution.result.current.isSuccess).toBe(true));
    expect(apiGet).toHaveBeenCalledWith(
      "/stats/market-share?dim=model&start=100&end=200&gran=day&top_n=20",
    );
    expect(apiGet).toHaveBeenCalledWith(
      "/stats/model-distribution?start=100&end=200&gran=day&user_id=42&top_n=20",
    );
  });
});

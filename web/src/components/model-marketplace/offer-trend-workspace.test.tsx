import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type {
  MarketplaceModelOfferDetail,
  MarketplacePerformanceTrendPoint,
} from "@/lib/api/model-marketplace";
import { CHART_LINE_ACTIVE_DOT, chartColorForSeries } from "@/lib/chart-colors";
import {
  OfferTrendWorkspace,
  TREND_METRIC_FORMATTERS,
} from "./offer-trend-workspace";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key === "trendMetricSla" ? "SLA" : key,
}));

vi.mock("@/components/business/chart-option-select", () => ({
  ChartOptionSelect: ({
    label,
    onValueChange,
    options,
    value,
  }: {
    label: string;
    onValueChange: (value: string) => void;
    options: Array<{ label: string; value: string }>;
    value: string;
  }) => (
    <select aria-label={label} value={value} onChange={(event) => onValueChange(event.target.value)}>
      {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>
  ),
}));

vi.mock("recharts", () => ({
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  LineChart: ({
    accessibilityLayer,
    children,
    data,
  }: {
    accessibilityLayer?: boolean;
    children: React.ReactNode;
    data: unknown;
  }) => (
    <div
      data-testid="line-chart"
      data-accessibility-layer={String(accessibilityLayer)}
      data-chart-data={JSON.stringify(data, (_key, value) =>
        typeof value === "number" && !Number.isFinite(value) ? String(value) : value
      )}
    >
      {children}
    </div>
  ),
  CartesianGrid: () => null,
  XAxis: () => null,
  YAxis: ({ tickFormatter }: { tickFormatter: (value: number) => string }) => (
    <div
      data-testid="y-axis"
      data-format-1000={tickFormatter(1_000)}
      data-format-percent={tickFormatter(99.98)}
    />
  ),
  Tooltip: () => null,
  Legend: () => null,
  Line: ({
    activeDot,
    connectNulls,
    dataKey,
    dot,
    hide,
    stroke,
    strokeDasharray,
    strokeWidth,
  }: {
    activeDot: { r: number; strokeWidth: number };
    connectNulls?: boolean;
    dataKey: string;
    dot: boolean;
    hide: boolean;
    stroke: string;
    strokeDasharray?: string;
    strokeWidth: number;
  }) => (
    <div
      data-testid="offer-line"
      data-active-dot={`${activeDot.r}:${activeDot.strokeWidth}`}
      data-connect-nulls={String(connectNulls)}
      data-dot={String(dot)}
      data-hide={String(hide)}
      data-key={dataKey}
      data-stroke={stroke}
      data-stroke-dasharray={strokeDasharray}
      data-stroke-width={strokeWidth}
    />
  ),
}));

function makePoint(
  startedAt: number,
  overrides: Partial<MarketplacePerformanceTrendPoint> = {},
): MarketplacePerformanceTrendPoint {
  return {
    started_at: startedAt,
    ended_at: startedAt + 3_600,
    status: "operational",
    in_progress: false,
    success_rate: 99.98,
    ttft_avg_ms: 1_000,
    tps_avg: 40,
    token_units: {
      total: 100,
      input: 10,
      cache_read: 30,
      output: 20,
      cache_write: 40,
    },
    ...overrides,
  };
}

function makeOffer(
  ref: string,
  trendSeries: MarketplacePerformanceTrendPoint[] = [
    makePoint(1_752_494_400),
    makePoint(1_752_498_000, { tps_avg: 41 }),
  ],
): MarketplaceModelOfferDetail {
  return {
    offer_ref: ref,
    kind: "platform",
    display_name: ref.toUpperCase(),
    ownership: "platform",
    available: true,
    supported_endpoints: ["responses"],
    pricing: {
      reference_price: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      gateway_charge: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      estimated_total: { input: 1, output: 2, cache_read: 3, cache_write: 4 },
      accuracy: "exact",
    },
    performance_status: "available",
    performance: {
      status: "operational",
      success_rate: 99.98,
      ttft_avg_ms: 1_000,
      ttft_p95_ms: 1_200,
      tps_avg: 40,
      tps_p5: 20,
      duration_p95_ms: 2_000,
      token_units: { input: 10, output: 20, cache_read: 30, cache_write: 40, total: 100 },
    },
    status_history: [],
    trend_series: trendSeries,
    usage_references: [],
  };
}

describe("marketplace offer trend workspace", () => {
  const primaryMetricSelect = () => screen.getByRole("combobox", { name: "trendPrimaryMetricLabel" });
  const tokenMetricSelect = () => screen.getByRole("combobox", { name: "trendTokenMetricLabel" });

  it("starts with one TTFT combobox and reveals a token submetric only for Token", () => {
    render(<OfferTrendWorkspace offers={[makeOffer("alpha")]} />);

    expect(primaryMetricSelect()).toHaveValue("ttft_avg_ms");
    expect(screen.queryByRole("combobox", { name: "trendTokenMetricLabel" })).not.toBeInTheDocument();

    fireEvent.change(primaryMetricSelect(), { target: { value: "token" } });

    expect(tokenMetricSelect()).toHaveValue("total");
    fireEvent.change(primaryMetricSelect(), { target: { value: "success_rate" } });
    expect(screen.queryByRole("combobox", { name: "trendTokenMetricLabel" })).not.toBeInTheDocument();
  });

  it("keeps one chart and the same offer context while switching primary and token metrics", () => {
    render(<OfferTrendWorkspace offers={[makeOffer("alpha"), makeOffer("beta")]} />);

    expect(screen.getAllByTestId("line-chart")).toHaveLength(1);
    expect(primaryMetricSelect()).toHaveValue("ttft_avg_ms");
    expect(screen.getAllByTestId("offer-line")).toHaveLength(2);

    fireEvent.change(primaryMetricSelect(), { target: { value: "tps_avg" } });
    expect(screen.getAllByTestId("line-chart")).toHaveLength(1);
    expect(JSON.parse(screen.getByTestId("line-chart").dataset.chartData!)[0].alpha).toBe(40);

    fireEvent.change(primaryMetricSelect(), { target: { value: "token" } });
    expect(tokenMetricSelect()).toHaveValue("total");
    fireEvent.change(tokenMetricSelect(), { target: { value: "cache_read" } });
    expect(JSON.parse(screen.getByTestId("line-chart").dataset.chartData!)[0].alpha).toBe(30);
  });

  it("uses the complete shared formatter registry without changing metric units", () => {
    expect(Object.keys(TREND_METRIC_FORMATTERS)).toEqual([
      "ttft_avg_ms",
      "tps_avg",
      "success_rate",
      "total",
      "input",
      "cache_read",
      "output",
      "cache_write",
    ]);
    expect(TREND_METRIC_FORMATTERS.ttft_avg_ms.axis(1_000)).toBe("1.0s");
    expect(TREND_METRIC_FORMATTERS.ttft_avg_ms.tooltip(1_000)).toBe("1.0s");
    expect(TREND_METRIC_FORMATTERS.tps_avg.axis(40)).toBe("40");
    expect(TREND_METRIC_FORMATTERS.tps_avg.tooltip(40)).toBe("40.0 tok/s");
    expect(TREND_METRIC_FORMATTERS.success_rate.axis(99.98)).toBe("100%");
    expect(TREND_METRIC_FORMATTERS.success_rate.tooltip(99.98)).toBe("99.98%");
    for (const metric of ["total", "input", "cache_read", "output", "cache_write"] as const) {
      expect(TREND_METRIC_FORMATTERS[metric].axis(1_000)).toBe("1.00K");
      expect(TREND_METRIC_FORMATTERS[metric].tooltip(1_000)).toBe("1,000");
    }
  });

  it("keeps TPS units in the shared formatter without a static explanatory subtitle", () => {
    render(<OfferTrendWorkspace offers={[makeOffer("alpha")]} />);

    expect(screen.queryByText(`trend${"Description"}`)).not.toBeInTheDocument();
    expect(screen.queryByText(`trend${"TpsUnit"}`)).not.toBeInTheDocument();
    fireEvent.change(primaryMetricSelect(), { target: { value: "tps_avg" } });

    expect(screen.getByTestId("y-axis")).toHaveAttribute("data-format-1000", "1000");
    expect(TREND_METRIC_FORMATTERS.tps_avg.tooltip(82.44)).toBe("82.4 tok/s");
  });

  it("sorts rows by started_at and keeps missing or non-finite samples null", () => {
    render(<OfferTrendWorkspace offers={[
      makeOffer("alpha", [
        makePoint(200, { ttft_avg_ms: Number.NaN }),
        makePoint(100, { ttft_avg_ms: null }),
      ]),
      makeOffer("beta", [
        makePoint(200, { ttft_avg_ms: Number.POSITIVE_INFINITY }),
        makePoint(100, { ttft_avg_ms: 80 }),
      ]),
    ]} />);

    const rows = JSON.parse(screen.getByTestId("line-chart").dataset.chartData!);
    expect(rows.map((row: { startedAt: number }) => row.startedAt)).toEqual([100, 200]);
    expect(rows[0]).toMatchObject({ alpha: null, beta: 80 });
    expect(rows[1]).toMatchObject({ alpha: null, beta: null });
  });

  it("drops malformed timestamps instead of rendering 1970 or throwing RangeError", () => {
    const invalidTimes = [
      null,
      0,
      -1,
      Number.NaN,
      Number.POSITIVE_INFINITY,
      Number.MAX_VALUE,
    ] as unknown as number[];
    render(<OfferTrendWorkspace offers={[makeOffer("alpha", [
      ...invalidTimes.map((startedAt) => makePoint(startedAt)),
      makePoint(1_752_494_400, { ttft_avg_ms: 0 }),
    ])]} />);

    const rows = JSON.parse(screen.getByTestId("line-chart").dataset.chartData!);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ startedAt: 1_752_494_400, alpha: 0 });
    expect(rows[0].label).not.toContain("1970");
  });

  it("keeps zero and valid metric boundaries while removing negative and out-of-range samples", () => {
    render(<OfferTrendWorkspace offers={[makeOffer("alpha", [
      makePoint(100, {
        ttft_avg_ms: -1,
        tps_avg: -1,
        success_rate: -0.01,
        token_units: { total: -1, input: -1, cache_read: -1, output: -1, cache_write: -1 },
      }),
      makePoint(200, {
        ttft_avg_ms: 0,
        tps_avg: 0,
        success_rate: 0,
        token_units: { total: 0, input: 0, cache_read: 0, output: 0, cache_write: 0 },
      }),
      makePoint(300, {
        ttft_avg_ms: 25,
        tps_avg: 40,
        success_rate: 100,
        token_units: { total: 100, input: 10, cache_read: 20, output: 30, cache_write: 40 },
      }),
      makePoint(400, { success_rate: 100.01 }),
    ])]} />);
    const values = () => JSON.parse(screen.getByTestId("line-chart").dataset.chartData!)
      .map((row: Record<string, unknown>) => row.alpha);

    expect(values()).toEqual([null, 0, 25, 1_000]);
    fireEvent.change(primaryMetricSelect(), { target: { value: "tps_avg" } });
    expect(values()).toEqual([null, 0, 40, 40]);
    fireEvent.change(primaryMetricSelect(), { target: { value: "success_rate" } });
    expect(values()).toEqual([null, 0, 100, null]);
    fireEvent.change(primaryMetricSelect(), { target: { value: "token" } });
    expect(values()).toEqual([null, 0, 100, 100]);
  });

  it("caps the workspace at five solid color-hash lines with shared active dots and one hidden state", () => {
    const offers = Array.from({ length: 6 }, (_, index) => makeOffer(`offer-${index}`));
    render(<OfferTrendWorkspace offers={offers} />);

    const lines = screen.getAllByTestId("offer-line");
    expect(lines).toHaveLength(5);
    for (const line of lines) {
      const ref = line.dataset.key!;
      expect(line).toHaveAttribute("data-stroke", chartColorForSeries(ref));
      expect(line).toHaveAttribute("data-stroke-width", "2");
      expect(line).toHaveAttribute("data-dot", "false");
      expect(line).toHaveAttribute(
        "data-active-dot",
        `${CHART_LINE_ACTIVE_DOT.r}:${CHART_LINE_ACTIVE_DOT.strokeWidth}`,
      );
      expect(line).toHaveAttribute("data-connect-nulls", "false");
      expect(line).not.toHaveAttribute("data-stroke-dasharray");
    }
    expect(screen.queryByRole("button", { name: "OFFER-5" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "OFFER-0" }));
    expect(screen.getAllByTestId("offer-line")[0]).toHaveAttribute("data-hide", "true");
    fireEvent.change(primaryMetricSelect(), { target: { value: "tps_avg" } });
    expect(screen.getAllByTestId("offer-line")[0]).toHaveAttribute("data-hide", "true");
  });

  it("shows a local empty state when selected offers have no samples", () => {
    render(<OfferTrendWorkspace offers={[makeOffer("empty", [])]} />);

    expect(screen.getByText("trendEmpty")).toBeInTheDocument();
    expect(screen.queryByTestId("line-chart")).not.toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "trendLegendLabel" })).not.toBeInTheDocument();
  });
});

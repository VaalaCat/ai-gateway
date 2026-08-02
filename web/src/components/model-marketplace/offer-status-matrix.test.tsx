import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { TooltipProvider } from "@/components/ui/tooltip";
import { MARKETPLACE_STATUS_PRESENTATION } from "./marketplace-status";
import type {
  MarketplaceHealthStatus,
  MarketplaceModelOfferDetail,
  MarketplaceUsageWindow,
} from "@/lib/api/model-marketplace";
import {
  normalizeStatusBuckets,
  OfferStatusMatrix,
} from "./offer-status-matrix";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, string | number>) =>
    values ? `${key} ${Object.values(values).join(" ")}` : key,
}));

const WINDOW_SHAPE: Record<MarketplaceUsageWindow, { points: number; seconds: number }> = {
  "24h": { points: 24, seconds: 3_600 },
  "7d": { points: 28, seconds: 21_600 },
  "30d": { points: 30, seconds: 86_400 },
};

function makeOffer(
  ref: string,
  window: MarketplaceUsageWindow = "24h",
  performanceStatus: MarketplaceModelOfferDetail["performance_status"] = "available",
): MarketplaceModelOfferDetail {
  const { points, seconds } = WINDOW_SHAPE[window];
  const startedAt = 1_752_494_400;
  const status_history = Array.from({ length: points }, (_, index) => ({
    started_at: startedAt + index * seconds,
    ended_at: startedAt + (index + 1) * seconds,
    status: index === 1 ? "degraded" as const : "operational" as const,
    in_progress: index === points - 1,
  }));
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
    performance_status: performanceStatus,
    performance: {
      status: "operational",
      success_rate: 98.75,
      ttft_avg_ms: 100,
      ttft_p95_ms: 120,
      tps_avg: 40,
      tps_p5: 20,
      duration_p95_ms: 1_000,
      token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
    },
    status_history,
    trend_series: status_history.map((bucket) => ({
      ...bucket,
      success_rate: 98.75,
      ttft_avg_ms: 100,
      tps_avg: 40,
      token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
    })),
    usage_references: [],
  };
}

function renderMatrix(
  offers: MarketplaceModelOfferDetail[],
  window: MarketplaceUsageWindow = "24h",
) {
  return render(
    <TooltipProvider>
      <OfferStatusMatrix offers={offers} window={window} />
    </TooltipProvider>,
  );
}

function statusBlocks() {
  return screen.getAllByRole("img");
}

describe("normalize marketplace offer status buckets", () => {
  it("keeps the final 24 chronological observations from an overlong server snapshot", () => {
    const offer = makeOffer("overlong");
    const inputStatuses: MarketplaceHealthStatus[] = [
      "outage", "degraded",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
    ];
    offer.status_history = inputStatuses.map((status, index) => ({
      started_at: 101 + index,
      ended_at: 102 + index,
      status,
      in_progress: index === 25,
    }));
    offer.trend_series = [];

    const normalized = normalizeStatusBuckets(offer, "24h");

    expect(normalized.map((bucket) => bucket.started_at)).toEqual([
      103, 104, 105, 106, 107, 108, 109, 110,
      111, 112, 113, 114, 115, 116, 117, 118,
      119, 120, 121, 122, 123, 124, 125, 126,
    ]);
    expect(normalized.map((bucket) => bucket.status)).toEqual([
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
      "operational", "unknown", "degraded", "outage",
    ]);
    expect(normalized.at(-1)).toMatchObject({ ended_at: 127, in_progress: true });
  });

  it("left-pads a short snapshot with unknown buckets without moving observations", () => {
    const offer = makeOffer("short");
    offer.status_history = [
      { started_at: 201, ended_at: 202, status: "degraded", in_progress: false },
      { started_at: 202, ended_at: 203, status: "outage", in_progress: true },
    ];
    offer.trend_series = [];

    const normalized = normalizeStatusBuckets(offer, "24h");

    expect(normalized.slice(0, 22)).toEqual(Array.from({ length: 22 }, () => ({
      started_at: null,
      ended_at: null,
      status: "unknown",
      baseState: "unknown",
      in_progress: false,
      hasObservation: false,
      successRate: undefined,
    })));
    expect(normalized.slice(22)).toEqual([
      {
        started_at: 201,
        ended_at: 202,
        status: "degraded",
        baseState: "degraded",
        in_progress: false,
        hasObservation: true,
        successRate: undefined,
      },
      {
        started_at: 202,
        ended_at: 203,
        status: "outage",
        baseState: "outage",
        in_progress: true,
        hasObservation: true,
        successRate: undefined,
      },
    ]);
  });

  it("preserves reverse and disordered server input instead of silently sorting it", () => {
    const offer = makeOffer("server-order");
    offer.status_history = [
      { started_at: 300, ended_at: 301, status: "outage", in_progress: false },
      { started_at: 100, ended_at: 101, status: "operational", in_progress: false },
      { started_at: 200, ended_at: 201, status: "degraded", in_progress: true },
    ];
    offer.trend_series = [];

    const observations = normalizeStatusBuckets(offer, "24h").slice(-3);

    expect(observations.map((bucket) => bucket.started_at)).toEqual([300, 100, 200]);
    expect(observations.map((bucket) => bucket.status)).toEqual([
      "outage",
      "operational",
      "degraded",
    ]);
  });

  it.each([
    { name: "zero start", startedAt: 0, endedAt: 10, wantStart: null, wantEnd: 10 },
    { name: "zero end", startedAt: 10, endedAt: 0, wantStart: 10, wantEnd: null },
    { name: "NaN start", startedAt: Number.NaN, endedAt: 20, wantStart: null, wantEnd: 20 },
    { name: "NaN end", startedAt: 20, endedAt: Number.NaN, wantStart: 20, wantEnd: null },
    { name: "infinite start", startedAt: Number.POSITIVE_INFINITY, endedAt: 30, wantStart: null, wantEnd: 30 },
    { name: "infinite end", startedAt: 30, endedAt: Number.NEGATIVE_INFINITY, wantStart: 30, wantEnd: null },
    { name: "extreme start", startedAt: Number.MAX_VALUE, endedAt: 30, wantStart: null, wantEnd: 30 },
  ])("does not expose a fabricated timestamp for $name", ({
    startedAt,
    endedAt,
    wantStart,
    wantEnd,
  }) => {
    const offer = makeOffer("invalid-time");
    offer.status_history = [{
      started_at: startedAt,
      ended_at: endedAt,
      status: "degraded",
      in_progress: false,
    }];
    offer.trend_series = [];

    expect(normalizeStatusBuckets(offer, "24h").at(-1)).toMatchObject({
      started_at: wantStart,
      ended_at: wantEnd,
      hasObservation: true,
    });
  });

  it("joins SLA by the same bucket raw started_at without borrowing from zero or placeholders", async () => {
    const user = userEvent.setup();
    const offer = makeOffer("raw-sla");
    offer.status_history = [{
      started_at: Number.NaN,
      ended_at: Number.POSITIVE_INFINITY,
      status: "degraded",
      in_progress: false,
    }];
    offer.trend_series = [
      {
        ...offer.status_history[0],
        started_at: 0,
        success_rate: 11,
        ttft_avg_ms: 100,
        tps_avg: 40,
        token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
      },
      {
        ...offer.status_history[0],
        success_rate: 88,
        ttft_avg_ms: 100,
        tps_avg: 40,
        token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
      },
    ];

    const normalized = normalizeStatusBuckets(offer, "24h");
    expect(normalized[0]).toHaveProperty("successRate", undefined);
    expect(normalized.at(-1)).toHaveProperty("successRate", 88);

    renderMatrix([offer]);
    const observed = statusBlocks().at(-1);
    await user.hover(observed!);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("observedSla 88.00%");
    expect(tooltip).not.toHaveTextContent("observedSla 11.00%");
    expect(tooltip).not.toHaveTextContent("UTC");
  });
});

describe("marketplace offer status matrix", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("aligns two offer rows to one shared timeline without a persistent status legend", () => {
    const { container } = renderMatrix([makeOffer("alpha"), makeOffer("beta")]);

    expect(screen.getAllByTestId("offer-status-row")).toHaveLength(2);
    expect(statusBlocks()).toHaveLength(48);
    expect(container.querySelectorAll("[data-slot='tracker-block']")).toHaveLength(48);
    expect(screen.getByRole("group", { name: "statusTimelineLabel" })).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: `status${"LegendLabel"}` })).not.toBeInTheDocument();
    expect(container.querySelector("[data-lucide='check']")).toBeNull();
    expect(container.querySelector("[data-lucide='triangle-alert']")).toBeNull();
    expect(container.querySelector("[data-lucide='x']")).toBeNull();
    expect(screen.getByText("ALPHA")).toBeInTheDocument();
    expect(screen.getByText("BETA")).toBeInTheDocument();
    expect(container.querySelectorAll("[data-slot='card']")).toHaveLength(1);
  });

  it.each([
    ["24h", 24],
    ["7d", 28],
    ["30d", 30],
  ] as const)("renders %s as exactly %i aligned cells per offer", (window, expected) => {
    const { container } = renderMatrix([makeOffer("alpha", window)], window);

    expect(statusBlocks()).toHaveLength(expected);
    expect(document.querySelectorAll("[data-slot='tracker-block']")).toHaveLength(expected);
    expect(screen.getByTestId("offer-status-timeline")).toHaveAttribute(
      "data-points",
      String(expected),
    );
    expect(screen.getByText(`statusTimelineStart window.${window}`))
      .toHaveClass("whitespace-nowrap");
    expect(screen.getByText("statusTimelineEnd")).toHaveClass("whitespace-nowrap");
    const tracker = container.querySelector("[data-slot='tracker']");
    expect(tracker).toHaveClass("w-fit", "gap-0.5");
    for (const block of container.querySelectorAll("[data-slot='tracker-block']")) {
      expect(block).toHaveClass("h-4", "w-[5px]", "shrink-0", "rounded-[1px]");
      expect(block).not.toHaveClass("flex-1", "h-6");
    }
    expect(container.querySelector("[data-testid='offer-status-row']"))
      .not.toHaveClass("grid-cols-[9rem_minmax(30rem,1fr)]");
  });

  it("fills an empty unavailable snapshot without inventing a current bucket or UTC time", () => {
    const offer = makeOffer("offline", "24h", "unavailable");
    offer.status_history = [];
    offer.trend_series = [];

    renderMatrix([offer]);

    const cells = statusBlocks();
    expect(cells).toHaveLength(24);
    for (const cell of cells) {
      expect(cell).toHaveAttribute("data-state", "unavailable");
      expect(cell).toHaveAttribute("data-in-progress", "false");
      expect(cell).toHaveAttribute("tabindex", "0");
      expect(cell).not.toHaveAccessibleName(/UTC/);
    }
  });

  it("keeps stale as the base state while retaining the final in-progress flag", () => {
    renderMatrix([makeOffer("stale", "24h", "stale")]);

    const current = statusBlocks().at(-1);
    expect(current).toHaveAttribute("data-state", "stale");
    expect(current).toHaveAttribute("data-in-progress", "true");
    expect(current).toHaveAccessibleName(/statusState\.stale.*statusState\.in_progress/);
  });

  it("expresses unknown health and in-progress as simultaneous facts", () => {
    const offer = makeOffer("unknown");
    const last = offer.status_history.length - 1;
    offer.status_history[last] = {
      ...offer.status_history[last],
      status: "unknown",
      in_progress: true,
    };
    offer.trend_series[last] = {
      ...offer.trend_series[last],
      status: "unknown",
      in_progress: true,
      success_rate: null,
    };

    renderMatrix([offer]);

    const current = statusBlocks().at(-1);
    expect(current).toHaveAttribute("data-state", "unknown");
    expect(current).toHaveAttribute("data-in-progress", "true");
    expect(current).toHaveAccessibleName(/statusState\.unknown.*statusState\.in_progress/);
  });

  it("shows real observation UTC, observed SLA, base health, and in-progress in one tooltip", async () => {
    const user = userEvent.setup();
    renderMatrix([makeOffer("observed")]);
    const current = statusBlocks().at(-1);
    expect(current).toBeDefined();

    await user.hover(current!);

    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("statusState.operational");
    expect(tooltip).toHaveTextContent("UTC");
    expect(tooltip).toHaveTextContent("observedSla 98.75%");
    expect(tooltip).toHaveTextContent("statusState.in_progress");
  });

  it.each([
    { value: -0.01, expected: "observedSla —" },
    { value: 0, expected: "observedSla 0.00%" },
    { value: 100, expected: "observedSla 100.00%" },
    { value: 100.01, expected: "observedSla —" },
  ])("formats observed SLA boundary $value through the shared percent contract", async ({
    value,
    expected,
  }) => {
    const user = userEvent.setup();
    const offer = makeOffer(`sla-${value}`);
    offer.status_history = [offer.status_history[0]];
    offer.trend_series = [{ ...offer.trend_series[0], success_rate: value }];
    renderMatrix([offer]);

    await user.hover(statusBlocks().at(-1)!);

    expect(await screen.findByRole("tooltip")).toHaveTextContent(expected);
  });

  it("keeps an observed status and SLA when its timestamp is missing without fabricating UTC", async () => {
    const user = userEvent.setup();
    const offer = makeOffer("no-time");
    offer.status_history = [{
      started_at: 0,
      ended_at: 0,
      status: "degraded",
      in_progress: false,
    }];
    offer.trend_series = [{
      ...offer.status_history[0],
      success_rate: 87.5,
      ttft_avg_ms: 100,
      tps_avg: 40,
      token_units: { input: 1, output: 2, cache_read: 3, cache_write: 4, total: 10 },
    }];

    renderMatrix([offer]);
    const cells = statusBlocks();
    expect(cells[0]).toHaveAttribute("data-state", "unknown");
    expect(cells[0]).not.toHaveAccessibleName(/UTC/);
    const observed = cells.at(-1);
    expect(observed).toHaveAttribute("data-state", "degraded");
    expect(observed).not.toHaveAccessibleName(/UTC/);

    await user.hover(observed!);
    const tooltip = await screen.findByRole("tooltip");
    expect(tooltip).toHaveTextContent("observedSla 87.50%");
    expect(tooltip).not.toHaveTextContent("UTC");
  });

  it("retains complete state information when reduced motion is requested", () => {
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));

    renderMatrix([makeOffer("reduced")]);

    const current = statusBlocks().at(-1);
    expect(current).toHaveAttribute("data-state", "operational");
    expect(current).toHaveAttribute("data-in-progress", "true");
    expect(current).toHaveAccessibleName(/statusState\.operational.*statusState\.in_progress/);
    expect(current).toHaveClass("ring-amber-500/80");
  });

  it("uses the registry in-progress indicator on a collecting status block", () => {
    const offer = makeOffer("collecting");
    offer.status_history = [{
      started_at: 1_752_494_400,
      ended_at: 1_752_498_000,
      status: "operational",
      in_progress: true,
    }];
    offer.trend_series = [];
    renderMatrix([offer]);

    const block = statusBlocks().at(-1)!;
    for (const className of MARKETPLACE_STATUS_PRESENTATION.in_progress.tracker.split(" ")) {
      expect(block).toHaveClass(className);
    }
  });

  it.each([
    ["operational", "available", "bg-emerald-600"],
    ["degraded", "available", "bg-amber-500"],
    ["outage", "available", "bg-red-600"],
    ["unknown", "available", "bg-gray-400"],
    ["operational", "stale", "bg-gray-400"],
    ["operational", "unavailable", "bg-red-600"],
  ] as const)("shows %s/%s with the shared semantic tracker color", (health, performanceStatus, color) => {
    const offer = makeOffer(`${health}-${performanceStatus}`, "24h", performanceStatus);
    offer.status_history = [{
      started_at: 1_752_494_400,
      ended_at: 1_752_498_000,
      status: health,
      in_progress: health === "degraded",
    }];
    offer.trend_series = [];
    renderMatrix([offer]);

    const current = statusBlocks().at(-1)!;
    expect(current).toHaveClass(color);
    if (health === "degraded") expect(current).toHaveClass("ring-amber-500/80");
  });

  it("derives row identity only from the public display name", () => {
    const offer = Object.assign(makeOffer("public channel"), {
      channel_id: 71,
      internal_name: "secret relay",
      base_url: "https://internal.example.test",
    });

    renderMatrix([offer]);

    expect(screen.getByText("PC")).toBeInTheDocument();
    expect(screen.getByText("PUBLIC CHANNEL")).toBeInTheDocument();
    expect(screen.queryByText(/secret|internal|channel_id|base_url/i)).not.toBeInTheDocument();
  });
});

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { RouteLivePreview } from "./route-live-preview";

const draft = (slug: string) => ({
  api_service_id: 7,
  slug,
  upstream_path: slug,
  forward_subpath: false,
  sample: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
  target: { mode: "existing" as const, backend_id: 12 },
});

afterEach(() => vi.useRealTimers());

describe("RouteLivePreview", () => {
  it("does not restart a stable draft preview after its own query rerenders", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
      endpoints: [{ upstream_id: 2, upstream_name: "primary", status: 1, priority: 0, weight: 1, final_url: "https://edge.test/v1" }],
      diagnostics: [],
    }), { headers: { "content-type": "application/json" } }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    const t = (key: string) => key;
    const view = render(<QueryClientProvider client={client}><RouteLivePreview draft={draft("forecast")} t={t} /></QueryClientProvider>);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByText("https://edge.test/v1")).toBeInTheDocument();

    view.rerender(<QueryClientProvider client={client}><RouteLivePreview draft={draft("forecast")} t={t} /></QueryClientProvider>);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    fetchMock.mockRestore();
  });

  it("aborts the previous real preview fetch when a newer draft replaces it", async () => {
    vi.useFakeTimers();
    const signals: AbortSignal[] = [];
    const resolveFetch: Array<(response: Response) => void> = [];
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation((_input, init) => {
      signals.push(init?.signal as AbortSignal);
      return new Promise<Response>((resolve) => resolveFetch.push(resolve));
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const t = (key: string) => key;
    const view = render(<QueryClientProvider client={client}><RouteLivePreview draft={draft("first")} t={t} /></QueryClientProvider>);

    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(signals).toHaveLength(1);
    view.rerender(<QueryClientProvider client={client}><RouteLivePreview draft={draft("second")} t={t} /></QueryClientProvider>);

    expect(signals[0]?.aborted).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    expect(signals).toHaveLength(2);
    await act(async () => {
      resolveFetch[1]?.(new Response(JSON.stringify({ endpoints: [{ upstream_id: 2, upstream_name: "second", priority: 0, weight: 1, final_url: "https://second.test" }], diagnostics: [] }), { headers: { "content-type": "application/json" } }));
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByText("https://second.test")).toBeInTheDocument();
    await act(async () => {
      resolveFetch[0]?.(new Response(JSON.stringify({ endpoints: [{ upstream_id: 1, upstream_name: "first", priority: 0, weight: 1, final_url: "https://first.test" }], diagnostics: [] }), { headers: { "content-type": "application/json" } }));
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(screen.getByText("https://second.test")).toBeInTheDocument();
    expect(screen.queryByText("https://first.test")).not.toBeInTheDocument();
    fetchMock.mockRestore();
  });

  it("localizes unknown endpoint diagnostics without exposing the server value", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
      endpoints: [{ upstream_id: 2, upstream_name: "primary", priority: 0, weight: 1, final_url: "https://edge.test/v1" }],
      diagnostics: ["future_diagnostic"],
    }), { headers: { "content-type": "application/json" } }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    render(<QueryClientProvider client={client}><RouteLivePreview draft={draft("forecast")} t={(key) => key} /></QueryClientProvider>);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(screen.getByText("https://edge.test/v1")).toBeInTheDocument();
    expect(screen.getByText("routingPreviewUnknownDiagnostic")).toBeInTheDocument();
    expect(screen.queryByText("future_diagnostic")).not.toBeInTheDocument();
    fetchMock.mockRestore();
  });

  it("shows mixed enabled and disabled candidates without implying a retry or fallback", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
      endpoints: [
        { upstream_id: 2, upstream_name: "primary", status: 1, priority: 0, weight: 1, final_url: "https://primary.test/v1" },
        { upstream_id: 3, upstream_name: "standby", status: 0, priority: 1, weight: 1, final_url: "https://standby.test/v1" },
      ],
      diagnostics: [],
    }), { headers: { "content-type": "application/json" } }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    render(<QueryClientProvider client={client}><RouteLivePreview draft={draft("forecast")} t={(key) => key} /></QueryClientProvider>);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(screen.getByText("https://primary.test/v1")).toBeInTheDocument();
    expect(screen.getByText(/standby.*endpointDisabled/)).toBeInTheDocument();
    expect(screen.getByText("singleEndpointNoFallback")).toBeInTheDocument();
    expect(screen.queryByText("routingPreviewStaticOnlyDisabled")).not.toBeInTheDocument();
    fetchMock.mockRestore();
  });

  it("warns that an all-disabled static preview would return 503", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({
      endpoints: [{ upstream_id: 2, upstream_name: "standby", status: 0, priority: 0, weight: 1, final_url: "https://standby.test/v1" }],
      diagnostics: ["no_available_upstream"],
    }), { headers: { "content-type": "application/json" } }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    render(<QueryClientProvider client={client}><RouteLivePreview draft={draft("forecast")} t={(key) => key} /></QueryClientProvider>);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await vi.advanceTimersByTimeAsync(0);
    });

    expect(screen.getByText("endpointUnavailable503")).toBeInTheDocument();
    expect(screen.getByText("routingPreviewStaticOnlyDisabled")).toBeInTheDocument();
    expect(screen.queryByText("noAvailableUpstream")).not.toBeInTheDocument();
    fetchMock.mockRestore();
  });
});

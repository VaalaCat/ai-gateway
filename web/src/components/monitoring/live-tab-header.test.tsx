import React from "react";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";
import { LiveTabHeader } from "./live-tab-header";

const messages = { monitoring: { refresh: {
  loading: "Loading", refreshing: "Refreshing", live: "Auto-refreshing", paused: "Paused", stale: "Stale data", partialStatus: "Partial data", partial: "Incomplete cluster totals", error: "Refresh failed", noData: "No data available", retained: "Showing previous data", lastUpdated: "Captured: {time}", neverUpdated: "Never fetched", refresh: "Refresh", pause: "Pause", resume: "Resume", failedAgents: "Failed nodes ({n})",
} } };

function header(overrides: Partial<React.ComponentProps<typeof LiveTabHeader>> = {}) {
  const props = { title: "Monitoring", paused: false, onPausedChange: vi.fn(), isFetching: false, dataUpdatedAt: 1000, hasData: true, onRefresh: vi.fn(), ...overrides };
  return { props, view: render(<NextIntlClientProvider locale="en" messages={messages}><LiveTabHeader {...props} /></NextIntlClientProvider>) };
}

describe("LiveTabHeader", () => {
  it.each([
    { hasData: false, isFetching: true, text: "Loading" },
    { hasData: false, error: new Error("network unavailable"), text: "No data available" },
    { hasData: true, error: new Error("network unavailable"), text: "Showing previous data" },
    { hasData: true, paused: true, text: "Paused" },
    { hasData: true, staleCount: 1, text: "Incomplete cluster totals" },
  ])("shows explicit loading/error/paused/partial state: $text", ({ text, ...props }) => {
    header(props);
    expect(screen.getByText(text)).toBeInTheDocument();
  });

  it("shows failure names and reasons without dropping the capture time", () => {
    header({ failedAgents: [{ agent_id: 7, agent_name: "edge-west", error: "RPC timeout" }] });
    expect(screen.getByText(/edge-west.*RPC timeout/)).toBeInTheDocument();
    expect(screen.getByText(/Captured:/)).toBeInTheDocument();
    expect(screen.queryByText("Auto-refreshing")).not.toBeInTheDocument();
  });

  it("allows manual refresh while paused and resumes through its callback", () => {
    const { props } = header({ paused: true });
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(props.onRefresh).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Resume" }));
    expect(props.onPausedChange).toHaveBeenCalledWith(false);
  });
});

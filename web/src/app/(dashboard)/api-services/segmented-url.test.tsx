import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { copyTextWithFeedback } from "@/lib/utils/clipboard";

import { SegmentedURL, type URLSegment } from "./_components/segmented-url";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn() }));

const publicURL = "https://gateway/v1/api/weather/forecast";
const publicSegments: URLSegment[] = [
  { start: 23, end: 30, kind: "service", label: "Service" },
  { start: 31, end: 39, kind: "route", label: "Route" },
];
const finalURL = "https://weather.example/forecast";
const endpointSegments: URLSegment[] = [
  { start: 8, end: 23, kind: "endpoint", label: "Endpoint" },
];

describe("SegmentedURL", () => {
  it("keeps the public URL as one selectable textContent value", () => {
    render(<SegmentedURL text={publicURL} segments={publicSegments} copyLabel="copy" />);

    const url = screen.getByTestId("segmented-url-text");
    expect(url.textContent).toBe(publicURL);
    expect(url).not.toHaveTextContent("Service");
    expect(url).not.toHaveTextContent("Route");
  });

  it("copies the exact pure URL instead of decorated content", async () => {
    const user = userEvent.setup();
    render(<SegmentedURL text={finalURL} segments={endpointSegments} copyLabel="copy" />);

    const copy = screen.getByRole("button", { name: "copy" });
    expect(copy).toHaveClass("size-8", "max-sm:min-h-11", "max-sm:min-w-11");
    await user.click(copy);

    expect(copyTextWithFeedback).toHaveBeenCalledWith(finalURL, expect.any(Object));
  });

  it.each([
    { start: -1, end: 2, kind: "route", label: "Route" },
    { start: 4, end: 3, kind: "route", label: "Route" },
    { start: 0, end: 99, kind: "route", label: "Route" },
  ] satisfies URLSegment[])("rejects invalid segment boundaries %j", (segment) => {
    expect(() => render(<SegmentedURL text="abc" segments={[segment]} copyLabel="copy" />)).toThrow("invalid URL segment");
  });

  it("sorts valid out-of-order segments without changing the URL text", () => {
    render(<SegmentedURL text="weather/forecast" segments={[
      { start: 7, end: 16, kind: "route", label: "Route" },
      { start: 0, end: 7, kind: "service", label: "Service" },
    ]} copyLabel="copy" />);

    expect(screen.getByTestId("segmented-url-text").textContent).toBe("weather/forecast");
  });

  it("rejects overlapping segments even when each boundary is valid", () => {
    expect(() => render(<SegmentedURL text="weather/forecast" segments={[
      { start: 0, end: 9, kind: "service", label: "Service" },
      { start: 7, end: 16, kind: "route", label: "Route" },
    ]} copyLabel="copy" />)).toThrow("overlapping URL segment");
  });

  it("does not insert whitespace between adjacent semantic segments", () => {
    render(<SegmentedURL text="weather/forecast" segments={[
      { start: 0, end: 7, kind: "service", label: "Service" },
      { start: 7, end: 16, kind: "route", label: "Route" },
    ]} copyLabel="copy" />);

    expect(screen.getByTestId("segmented-url-text").textContent).toBe("weather/forecast");
  });
});

import { describe, expect, it } from "vitest";

import type { SegmentedURLValue } from "../segmented-url";
import { appendForwardSubpathMarker } from "./route-path-mapping";

function slices(value: SegmentedURLValue) {
  return value.segments.map((segment) => value.text.slice(segment.start, segment.end));
}

describe("appendForwardSubpathMarker", () => {
  it("adds only the ellipsis after a trailing slash and shifts a fragment suffix segment", () => {
    const text = "https://edge.test/forecast/#trace";
    const value = appendForwardSubpathMarker({ text, segments: [
      { start: 0, end: 17, kind: "endpoint", label: "Endpoint" },
      { start: 17, end: 26, kind: "route", label: "Route" },
      { start: 27, end: text.length, kind: "endpoint", label: "Fragment" },
    ] });

    expect(value.text).toBe("https://edge.test/forecast/…#trace");
    expect(slices(value)).toEqual(["https://edge.test", "/forecast", "#trace"]);
    expect(value.segments[2]).toMatchObject({ start: 28, end: 34 });
  });

  it("inserts slash and ellipsis before a query while preserving Route offsets", () => {
    const text = "https://edge.test/base/forecast?keep=1";
    const value = appendForwardSubpathMarker({ text, segments: [
      { start: 0, end: 22, kind: "endpoint", label: "Endpoint" },
      { start: 22, end: 31, kind: "route", label: "Route" },
    ] });

    expect(value.text).toBe("https://edge.test/base/forecast/…?keep=1");
    expect(slices(value)).toEqual(["https://edge.test/base", "/forecast"]);
  });

  it("extends a truthful full Endpoint fallback segment across the inserted marker", () => {
    const text = "https://edge.test/unmatched?keep=1";
    const value = appendForwardSubpathMarker({ text, segments: [
      { start: 0, end: text.length, kind: "endpoint", label: "Endpoint" },
    ] });

    expect(value.text).toBe("https://edge.test/unmatched/…?keep=1");
    expect(value.segments).toEqual([{ start: 0, end: value.text.length, kind: "endpoint", label: "Endpoint" }]);
    expect(slices(value)).toEqual([value.text]);
  });
});

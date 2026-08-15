import { describe, expect, it } from "vitest";

import {
  buildCompleteDateRange,
  dateStrToExclusiveEndTs,
  dateStrToTs,
  isFiniteUnixSeconds,
  localDateRangeToUTCRange,
  tsToDateStr,
} from "./date-range";

describe("buildCompleteDateRange", () => {
  it.each([
    ["2026-07-10", "", { startDate: "2026-07-10", endDate: "2026-07-10" }],
    ["", "2026-07-12", { startDate: "2026-07-12", endDate: "2026-07-12" }],
    ["2026-07-12", "2026-07-10", { startDate: "2026-07-10", endDate: "2026-07-12" }],
  ])("completes and sorts %s..%s", (startDate, endDate, expected) => {
    expect(buildCompleteDateRange(startDate, endDate)).toEqual(expected);
  });

  it("clamps an inclusive seven-day range to the end date", () => {
    expect(buildCompleteDateRange("2026-07-01", "2026-07-20", 7)).toEqual({
      startDate: "2026-07-14",
      endDate: "2026-07-20",
    });
    expect(buildCompleteDateRange("2026-07-14", "2026-07-20", 7)).toEqual({
      startDate: "2026-07-14",
      endDate: "2026-07-20",
    });
  });

  it("drops invalid calendar dates and malformed formats", () => {
    expect(buildCompleteDateRange("2026-02-31", "bad-date")).toEqual({
      startDate: "",
      endDate: "",
    });
    expect(buildCompleteDateRange("2026-02-31", "2026-03-01")).toEqual({
      startDate: "2026-03-01",
      endDate: "2026-03-01",
    });
  });

  it.each([
    [1, { startDate: "2026-07-20", endDate: "2026-07-20" }],
    [1.5, { startDate: "2026-07-20", endDate: "2026-07-20" }],
    [7, { startDate: "2026-07-14", endDate: "2026-07-20" }],
    [0, { startDate: "2026-07-20", endDate: "2026-07-20" }],
    [-3, { startDate: "2026-07-20", endDate: "2026-07-20" }],
    [Number.NaN, { startDate: "2026-07-01", endDate: "2026-07-20" }],
    [Number.POSITIVE_INFINITY, { startDate: "2026-07-01", endDate: "2026-07-20" }],
  ])("normalizes maxDays=%s without constructing invalid dates", (maxDays, expected) => {
    expect(buildCompleteDateRange("2026-07-01", "2026-07-20", maxDays)).toEqual(expected);
  });
});

describe("timestamp conversion", () => {
  it.each([
    [1, true],
    [0, false],
    [-1, false],
    [Number.NaN, false],
    [Number.POSITIVE_INFINITY, false],
    ["1", false],
  ])("accepts only finite positive numeric unix seconds %#", (value, expected) => {
    expect(isFiniteUnixSeconds(value)).toBe(expected);
  });

  it.each([Number.NaN, Number.POSITIVE_INFINITY, 0, -1])(
    "returns empty for invalid unix seconds %s without throwing",
    (value) => expect(() => expect(tsToDateStr(value)).toBe("")).not.toThrow(),
  );

  it.each(["2026-02-31", "2026-2-03", "not-a-date"])(
    "returns zero for invalid date string %s",
    (value) => expect(dateStrToTs(value, false)).toBe(0),
  );

  it("uses complete local-day unix bounds", () => {
    const start = dateStrToTs("2026-07-20", false);
    const end = dateStrToTs("2026-07-20", true);

    expect(start).toBeGreaterThan(0);
    expect(end).toBeGreaterThan(start);
    expect(tsToDateStr(start)).toBe("2026-07-20");
    expect(tsToDateStr(end)).toBe("2026-07-20");
  });

  it("converts an inclusive end date to the next local midnight for exclusive APIs", () => {
    const end = dateStrToExclusiveEndTs("2026-07-20");
    const expected = Math.floor(new Date(2026, 6, 21, 0, 0, 0, 0).getTime() / 1000);

    expect(end).toBe(expected);
    expect(tsToDateStr(end - 1)).toBe("2026-07-20");
    expect(tsToDateStr(end)).toBe("2026-07-21");
  });

  it("keeps a fallback calendar day complete through the next local midnight", () => {
    const start = dateStrToTs("2026-11-01", false);
    const end = dateStrToExclusiveEndTs("2026-11-01");
    const expectedEnd = Math.floor(new Date(2026, 10, 2, 0, 0, 0, 0).getTime() / 1000);

    expect(start).toBe(Math.floor(new Date(2026, 10, 1, 0, 0, 0, 0).getTime() / 1000));
    expect(end).toBe(expectedEnd);
    expect(tsToDateStr(end - 1)).toBe("2026-11-01");
    expect(tsToDateStr(end)).toBe("2026-11-02");
  });

  // behavior change: end-exclusive local calendar ranges must preserve a DST fallback day.
  it("uses calendar midnight instead of a fixed 24 hours across DST fallback", () => {
    const originalTimezone = process.env.TZ;
    process.env.TZ = "America/New_York";
    try {
      const start = dateStrToTs("2026-11-01", false);
      const end = dateStrToExclusiveEndTs("2026-11-01");
      expect(end - start).toBe(25 * 60 * 60);
      expect(tsToDateStr(end - 1)).toBe("2026-11-01");
      expect(tsToDateStr(end)).toBe("2026-11-02");
    } finally {
      if (originalTimezone === undefined) delete process.env.TZ;
      else process.env.TZ = originalTimezone;
    }
    expect(process.env.TZ).toBe(originalTimezone);
  });

  it.each(["", "2026-02-31", "2026-7-20"])(
    "returns zero exclusive end for invalid date string %s",
    (value) => expect(dateStrToExclusiveEndTs(value)).toBe(0),
  );
});

it("keeps the UTC envelope around valid local calendar days", () => {
  const from = "2026-07-20";
  const to = "2026-07-21";

  expect(localDateRangeToUTCRange(from, to)).toEqual({
    from: new Date(`${from}T00:00:00`).toISOString().slice(0, 10),
    to: new Date(`${to}T23:59:59.999`).toISOString().slice(0, 10),
  });
});

it("drops invalid local calendar days from the UTC envelope without throwing", () => {
  expect(() => localDateRangeToUTCRange("2026-02-31", "bad-date")).not.toThrow();
  expect(localDateRangeToUTCRange("2026-02-31", "bad-date")).toEqual({
    from: "",
    to: "",
  });
});

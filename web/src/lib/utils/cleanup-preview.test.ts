import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { isCleanupPreviewExpired } from "./cleanup-preview";

describe("isCleanupPreviewExpired", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-23T12:00:00Z"));
  });

  afterEach(() => vi.useRealTimers());

  it("keeps a preview valid before the five minute tolerance", () => {
    const cutoff = Date.now() / 1000 - 30 * 86400 - 299;
    expect(isCleanupPreviewExpired(cutoff, 30, Date.now() - 299_000)).toBe(false);
  });

  it("keeps a preview valid exactly at the tolerance boundary", () => {
    const cutoff = Date.now() / 1000 - 30 * 86400 - 300;
    expect(isCleanupPreviewExpired(cutoff, 30, Date.now() - 300_000)).toBe(false);
  });

  it("expires a preview beyond the tolerance", () => {
    const cutoff = Date.now() / 1000 - 30 * 86400 - 301;
    expect(isCleanupPreviewExpired(cutoff, 30, Date.now() - 301_000)).toBe(true);
  });
});

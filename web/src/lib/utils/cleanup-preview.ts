const CLEANUP_PREVIEW_TOLERANCE_SECONDS = 5 * 60;

export function isCleanupPreviewExpired(
  cutoffUnix: number,
  retainDays: number,
  fetchedAtMs = Date.now(),
  nowMs = Date.now(),
): boolean {
  const ageSeconds = (nowMs - fetchedAtMs) / 1000;
  const expectedCutoff = nowMs / 1000 - retainDays * 86400;
  return (
    ageSeconds > CLEANUP_PREVIEW_TOLERANCE_SECONDS ||
    Math.abs(expectedCutoff - cutoffUnix) > CLEANUP_PREVIEW_TOLERANCE_SECONDS
  );
}

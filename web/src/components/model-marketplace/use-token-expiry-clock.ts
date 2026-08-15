"use client";

import { useEffect, useState } from "react";

import {
  tokenServerTimeAxis,
  useServerTimeOffsetMs,
  type ServerTimeAxis,
} from "@/lib/api/server-time";
import type { Token } from "@/lib/types";

export const MAX_TOKEN_EXPIRY_TIMER_DELAY_MS = 2_147_483_647;

export function nextTokenExpiryDelayMs(tokens: Token[], nowMs: number) {
  const nearestDeadline = tokens.reduce<number | undefined>((nearest, token) => {
    if (token.expired_at <= 0) return nearest;
    const deadline = (token.expired_at + 1) * 1_000;
    if (deadline <= nowMs) return nearest;
    return nearest === undefined ? deadline : Math.min(nearest, deadline);
  }, undefined);
  if (nearestDeadline === undefined) return undefined;
  return Math.min(nearestDeadline - nowMs, MAX_TOKEN_EXPIRY_TIMER_DELAY_MS);
}

export function useTokenExpiryClock(
  tokens: Token[],
  axis: ServerTimeAxis = tokenServerTimeAxis,
) {
  const offsetMs = useServerTimeOffsetMs(axis);
  const [revision, setRevision] = useState(0);
  void revision;
  const serverNowSeconds = offsetMs === undefined ? undefined : axis.nowSeconds();

  useEffect(() => {
    if (offsetMs === undefined) return;
    const delay = nextTokenExpiryDelayMs(tokens, Date.now() + offsetMs);
    if (delay === undefined) return;
    const timer = window.setTimeout(() => setRevision((current) => current + 1), delay);
    return () => window.clearTimeout(timer);
  }, [offsetMs, revision, tokens]);

  useEffect(() => {
    const updateNow = () => setRevision((current) => current + 1);
    document.addEventListener("visibilitychange", updateNow);
    return () => document.removeEventListener("visibilitychange", updateNow);
  }, []);

  return serverNowSeconds;
}

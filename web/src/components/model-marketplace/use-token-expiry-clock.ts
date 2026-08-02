"use client";

import { useEffect, useState } from "react";

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

export function useTokenExpiryClock(tokens: Token[]) {
  const [nowMs, setNowMs] = useState(() => Date.now());

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- newly fetched expiries must be compared with fetch-time, not mount-time
    setNowMs(Date.now());
  }, [tokens]);

  useEffect(() => {
    const delay = nextTokenExpiryDelayMs(tokens, Date.now());
    if (delay === undefined) return;
    const timer = window.setTimeout(() => setNowMs(Date.now()), delay);
    return () => window.clearTimeout(timer);
  }, [nowMs, tokens]);

  useEffect(() => {
    const updateNow = () => setNowMs(Date.now());
    document.addEventListener("visibilitychange", updateNow);
    return () => document.removeEventListener("visibilitychange", updateNow);
  }, []);

  return Math.floor(nowMs / 1_000);
}

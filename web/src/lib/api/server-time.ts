import { useSyncExternalStore } from "react";

import { HTTP_HEADERS } from "@/lib/constants";

export const SERVER_TIME_HEADER = HTTP_HEADERS.SERVER_TIME_MS;

type Listener = () => void;

function currentMonotonicMs() {
  return typeof performance === "undefined" ? Date.now() : performance.now();
}

export class ServerTimeAxis {
  private currentOffsetMs: number | undefined;
  private latestReceivedMonotonicMs = Number.NEGATIVE_INFINITY;
  private latestRequestSequence = Number.NEGATIVE_INFINITY;
  private readonly listeners = new Set<Listener>();

  observe(
    serverTimeMs: number,
    receivedClientNowMs: number,
    receivedMonotonicMs = currentMonotonicMs(),
    requestSequence?: number,
  ) {
    this.update(
      Number.isSafeInteger(serverTimeMs) && serverTimeMs >= 0
        ? serverTimeMs - receivedClientNowMs
        : undefined,
      receivedMonotonicMs,
      requestSequence,
    );
  }

  observeHeader(
    value: string | null,
    receivedClientNowMs: number,
    receivedMonotonicMs = currentMonotonicMs(),
    requestSequence?: number,
  ) {
    const serverTimeMs = value !== null && /^\d+$/.test(value) ? Number(value) : Number.NaN;
    this.observe(serverTimeMs, receivedClientNowMs, receivedMonotonicMs, requestSequence);
  }

  offsetMs = () => this.currentOffsetMs;

  nowMs(clientNowMs = Date.now()) {
    return this.currentOffsetMs === undefined
      ? undefined
      : clientNowMs + this.currentOffsetMs;
  }

  nowSeconds(clientNowMs = Date.now()) {
    const nowMs = this.nowMs(clientNowMs);
    return nowMs === undefined ? undefined : Math.floor(nowMs / 1_000);
  }

  subscribe = (listener: Listener) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  private update(
    offsetMs: number | undefined,
    receivedMonotonicMs: number,
    requestSequence?: number,
  ) {
    if (requestSequence !== undefined) {
      if (requestSequence < this.latestRequestSequence) return;
      this.latestRequestSequence = requestSequence;
    } else if (receivedMonotonicMs < this.latestReceivedMonotonicMs) {
      return;
    }
    this.latestReceivedMonotonicMs = receivedMonotonicMs;
    if (Object.is(this.currentOffsetMs, offsetMs)) return;
    this.currentOffsetMs = offsetMs;
    for (const listener of this.listeners) listener();
  }
}

export const tokenServerTimeAxis = new ServerTimeAxis();

export function useServerTimeOffsetMs(axis = tokenServerTimeAxis) {
  return useSyncExternalStore(axis.subscribe, axis.offsetMs, axis.offsetMs);
}

export function receivedMonotonicMs() {
  return currentMonotonicMs();
}

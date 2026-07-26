"use client";

import { useCallback, useSyncExternalStore } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import type { ChartTopN } from "@/lib/types";

const DEFAULT_TOP_N: ChartTopN = 5;
const TOP_N_CHANGE_EVENT = "chart-top-n-change";

function parseChartTopN(raw: string | null): ChartTopN | null {
  const value = Number(raw);
  return value === 5 || value === 10 || value === 20 ? value : null;
}

function chartTopNStorageKey(userId: number, pathname: string): string | null {
  return Number.isFinite(userId) && userId > 0
    ? `chartTopN:${userId}:${pathname}`
    : null;
}

function readStoredTopN(key: string | null): ChartTopN {
  if (!key || typeof window === "undefined") return DEFAULT_TOP_N;
  try {
    return parseChartTopN(window.localStorage.getItem(key)) ?? DEFAULT_TOP_N;
  } catch {
    return DEFAULT_TOP_N;
  }
}

export function useChartTopN(
  userId: number,
  pathname: string,
): [ChartTopN, (value: ChartTopN) => void] {
  const router = useRouter();
  const searchParams = useSearchParams();
  const query = searchParams.toString();
  const key = chartTopNStorageKey(userId, pathname);

  const subscribe = useCallback((notify: () => void) => {
    if (typeof window === "undefined" || !key) return () => {};
    const onStorage = (event: StorageEvent) => {
      if (event.key === key) notify();
    };
    const onLocalChange = (event: Event) => {
      if ((event as CustomEvent<string>).detail === key) notify();
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(TOP_N_CHANGE_EVENT, onLocalChange);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(TOP_N_CHANGE_EVENT, onLocalChange);
    };
  }, [key]);

  const getSnapshot = useCallback((): ChartTopN => {
    const raw = new URLSearchParams(query).get("top_n");
    if (raw !== null) return parseChartTopN(raw) ?? DEFAULT_TOP_N;
    return readStoredTopN(key);
  }, [key, query]);
  const getServerSnapshot = useCallback((): ChartTopN => DEFAULT_TOP_N, []);
  const value = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);

  const update = useCallback((nextValue: ChartTopN) => {
    const next = new URLSearchParams(query);
    next.set("top_n", String(nextValue));
    if (key && typeof window !== "undefined") {
      try {
        window.localStorage.setItem(key, String(nextValue));
        window.dispatchEvent(new CustomEvent(TOP_N_CHANGE_EVENT, { detail: key }));
      } catch {
        // Storage can be disabled; the shareable URL remains authoritative.
      }
    }
    router.replace(`${pathname}?${next.toString()}`, { scroll: false });
  }, [key, pathname, query, router]);

  return [value, update];
}

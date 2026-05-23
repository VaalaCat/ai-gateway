"use client";

import { useEffect, useMemo, useState } from "react";
import type { VisibilityState } from "@tanstack/react-table";

import { useIsMobile } from "./use-mobile";

interface Config {
  storageKey: string;
  hiddenByDefault?: readonly string[];
  hiddenOnMobile?: readonly string[];
}

export function useResponsiveColumnVisibility(
  config: Config,
): readonly [VisibilityState, (next: VisibilityState) => void] {
  const isMobile = useIsMobile();
  const [override, setOverride] = useState<VisibilityState>(() => {
    if (typeof window === "undefined") return {};
    const saved = localStorage.getItem(`col-vis-${config.storageKey}`);
    if (!saved) return {};
    try { return JSON.parse(saved); } catch { return {}; }
  });

  useEffect(() => {
    localStorage.setItem(`col-vis-${config.storageKey}`, JSON.stringify(override));
  }, [config.storageKey, override]);

  const visibility = useMemo<VisibilityState>(() => {
    const base: VisibilityState = {};
    config.hiddenByDefault?.forEach(k => { base[k] = false; });
    if (isMobile) config.hiddenOnMobile?.forEach(k => { base[k] = false; });
    return { ...base, ...override };
  }, [isMobile, config.hiddenByDefault, config.hiddenOnMobile, override]);

  return [visibility, setOverride] as const;
}

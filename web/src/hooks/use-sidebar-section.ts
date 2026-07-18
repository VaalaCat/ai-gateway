"use client";

import { useCallback, useSyncExternalStore } from "react";

const SIDEBAR_SECTION_EVENT = "sidebar-section-change";

export function useSidebarSection(key: string, defaultOpen: boolean) {
  const storageKey = `sidebar:section:${key}`;
  const subscribe = useCallback((notify: () => void) => {
    const onStorage = (event: StorageEvent) => {
      if (event.key === storageKey) notify();
    };
    const onLocalChange = (event: Event) => {
      if ((event as CustomEvent<string>).detail === storageKey) notify();
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(SIDEBAR_SECTION_EVENT, onLocalChange);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(SIDEBAR_SECTION_EVENT, onLocalChange);
    };
  }, [storageKey]);
  const getSnapshot = useCallback(() => {
    const value = window.localStorage.getItem(storageKey);
    if (value === "open") return true;
    if (value === "closed") return false;
    return defaultOpen;
  }, [defaultOpen, storageKey]);
  const getServerSnapshot = useCallback(() => defaultOpen, [defaultOpen]);
  const open = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);

  const set = (next: boolean) => {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(storageKey, next ? "open" : "closed");
      window.dispatchEvent(new CustomEvent(SIDEBAR_SECTION_EVENT, { detail: storageKey }));
    }
  };

  return [open, set] as const;
}

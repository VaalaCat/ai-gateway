"use client";

import { useEffect, useState } from "react";

export function useSidebarSection(key: string, defaultOpen: boolean) {
  const storageKey = `sidebar:section:${key}`;
  const [open, setOpen] = useState(defaultOpen);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const v = window.localStorage.getItem(storageKey);
    if (v === "open") setOpen(true);
    else if (v === "closed") setOpen(false);
  }, [storageKey]);

  const set = (next: boolean) => {
    setOpen(next);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(storageKey, next ? "open" : "closed");
    }
  };

  return [open, set] as const;
}

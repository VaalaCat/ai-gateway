"use client";

import { useSyncExternalStore } from "react";

function subscribe(listener: () => void) {
  if (typeof document === "undefined") {
    return () => undefined;
  }
  document.addEventListener("visibilitychange", listener);
  return () => document.removeEventListener("visibilitychange", listener);
}

function getSnapshot() {
  return typeof document === "undefined" || document.visibilityState !== "hidden";
}

function getServerSnapshot() {
  return true;
}

export function useDocumentVisibility() {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

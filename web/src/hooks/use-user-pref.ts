"use client";

import { useCallback, useMemo, useSyncExternalStore } from "react";
import { useAuth } from "@/lib/auth";

const KEY_PREFIX = "aigw:pref";
const USER_PREF_EVENT = "aigw-user-pref-change";

function storageKey(userID: number | undefined, name: string): string | null {
  if (typeof userID !== "number" || !Number.isFinite(userID)) return null;
  return `${KEY_PREFIX}:${userID}:${name}`;
}

function readPref<T>(raw: string | null, fallback: T): T {
  if (raw === null) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

function writePref<T>(key: string, value: T): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // quota exceeded / disabled storage — silently drop
  }
}

/**
 * 用户作用域偏好持久化。userId 取自 useAuth().user?.user_id。
 * 首屏（SSR / user 未就绪）始终返回 fallback；user 就绪后切到持久化值。
 * key 形如 aigw:pref:${userId}:${name}，多账号同浏览器互不串扰。
 */
export function useUserPref<T>(name: string, fallback: T): [T, (v: T) => void] {
  const { user } = useAuth();
  const userID = user?.user_id;
  const key = storageKey(userID, name);
  const subscribe = useCallback((notify: () => void) => {
    if (!key) return () => {};
    const onStorage = (event: StorageEvent) => {
      if (event.key === key) notify();
    };
    const onLocalChange = (event: Event) => {
      if ((event as CustomEvent<string>).detail === key) notify();
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(USER_PREF_EVENT, onLocalChange);
    return () => {
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(USER_PREF_EVENT, onLocalChange);
    };
  }, [key]);
  const getSnapshot = useCallback(() => key ? window.localStorage.getItem(key) : null, [key]);
  const getServerSnapshot = useCallback(() => null, []);
  const raw = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
  const value = useMemo(() => readPref(raw, fallback), [fallback, raw]);

  const update = useCallback(
    (next: T) => {
      if (key) {
        writePref(key, next);
        window.dispatchEvent(new CustomEvent(USER_PREF_EVENT, { detail: key }));
      }
    },
    [key],
  );

  return [value, update];
}

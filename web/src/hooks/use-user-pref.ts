"use client";

import { useCallback, useEffect, useState } from "react";
import { useAuth } from "@/lib/auth";

const KEY_PREFIX = "aigw:pref";

function storageKey(userID: number | undefined, name: string): string | null {
  if (typeof userID !== "number" || !Number.isFinite(userID)) return null;
  return `${KEY_PREFIX}:${userID}:${name}`;
}

function readPref<T>(key: string, fallback: T): T {
  if (typeof window === "undefined") return fallback;
  try {
    const raw = window.localStorage.getItem(key);
    if (raw === null) return fallback;
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

  const [value, setValue] = useState<T>(fallback);

  // user 就绪后从 localStorage 读一次（不写：避免无意义覆盖）
  useEffect(() => {
    if (!key) return;
    setValue(readPref(key, fallback));
    // fallback 故意不进依赖：fallback 通常是稳定字面量；若动态变化也不重读
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const update = useCallback(
    (next: T) => {
      setValue(next);
      if (key) writePref(key, next);
    },
    [key],
  );

  return [value, update];
}

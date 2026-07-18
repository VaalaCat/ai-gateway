"use client";

import { useCallback, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { api } from "@/lib/api/client";
import { STORAGE_KEYS, ROLE } from "@/lib/constants";
import type { AuthPayload } from "@/lib/types";
import { decodeJwtPayload } from "@/lib/jwt";

const authListeners = new Set<() => void>();

function emitAuthChange() {
  for (const listener of authListeners) listener();
}

function subscribeAuth(listener: () => void) {
  authListeners.add(listener);
  const onStorage = (event: StorageEvent) => {
    if (event.key === STORAGE_KEYS.TOKEN) listener();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    authListeners.delete(listener);
    window.removeEventListener("storage", onStorage);
  };
}

function getStoredToken() {
  return window.localStorage.getItem(STORAGE_KEYS.TOKEN) ?? "";
}

function getServerToken(): null {
  return null;
}

export function useAuth() {
  const [authCheckedAt] = useState(() => Date.now());
  const token = useSyncExternalStore(subscribeAuth, getStoredToken, getServerToken);
  const user = useMemo(() => {
    if (!token) return null;
    const payload = decodeJwtPayload<AuthPayload>(token);
    return payload && payload.exp * 1000 > authCheckedAt ? payload : null;
  }, [authCheckedAt, token]);
  const loading = token === null;

  useEffect(() => {
    if (token && !user) {
      localStorage.removeItem(STORAGE_KEYS.TOKEN);
      document.cookie = `${STORAGE_KEYS.TOKEN}=; path=/; max-age=0`;
      emitAuthChange();
    }
  }, [token, user]);

  const login = useCallback(async (username: string, password: string) => {
    const res = await api.post<{ token: string }>("/login", {
      username,
      password,
    });
    localStorage.setItem(STORAGE_KEYS.TOKEN, res.token);
    document.cookie = `${STORAGE_KEYS.TOKEN}=${res.token}; path=/; max-age=86400; SameSite=Lax`;
    const payload = decodeJwtPayload<AuthPayload>(res.token);
    emitAuthChange();
    return payload;
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem(STORAGE_KEYS.TOKEN);
    document.cookie = `${STORAGE_KEYS.TOKEN}=; path=/; max-age=0`;
    emitAuthChange();
    window.location.href = "/login";
  }, []);

  return {
    user,
    loading,
    login,
    logout,
    isAdmin: user?.role === ROLE.ADMIN,
    isAuthenticated: !!user,
  };
}

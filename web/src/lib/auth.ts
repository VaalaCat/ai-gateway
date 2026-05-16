"use client";

import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { STORAGE_KEYS, ROLE } from "@/lib/constants";
import type { AuthPayload } from "@/lib/types";
import { decodeJwtPayload } from "@/lib/jwt";


export function useAuth() {
  const [user, setUser] = useState<AuthPayload | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = localStorage.getItem(STORAGE_KEYS.TOKEN);
    if (token) {
      const payload = decodeJwtPayload<AuthPayload>(token);
      if (payload && payload.exp * 1000 > Date.now()) {
        setUser(payload);
      } else {
        localStorage.removeItem(STORAGE_KEYS.TOKEN);
        document.cookie = `${STORAGE_KEYS.TOKEN}=; path=/; max-age=0`;
      }
    }
    setLoading(false);
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const res = await api.post<{ token: string }>("/login", {
      username,
      password,
    });
    localStorage.setItem(STORAGE_KEYS.TOKEN, res.token);
    document.cookie = `${STORAGE_KEYS.TOKEN}=${res.token}; path=/; max-age=86400; SameSite=Lax`;
    const payload = decodeJwtPayload<AuthPayload>(res.token);
    setUser(payload);
    return payload;
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem(STORAGE_KEYS.TOKEN);
    document.cookie = `${STORAGE_KEYS.TOKEN}=; path=/; max-age=0`;
    setUser(null);
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

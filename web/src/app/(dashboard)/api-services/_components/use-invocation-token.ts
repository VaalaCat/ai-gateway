"use client";

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";

import { useUsableTokenForAPIRoute } from "@/lib/api/tokens";
import type { Token } from "@/lib/types";

export type InvocationTokenFailure = "miss" | "error";

export interface InvocationTokenScope {
  viewerUserID: number;
  ownerUserID?: number;
  apiServiceID: number;
  apiRouteID: number;
}

export interface InvocationTokenState {
  tokenID: number;
  token?: Pick<Token, "id" | "name" | "key">;
  isChecking: boolean;
  failure: InvocationTokenFailure | null;
  setTokenID: (tokenID: number) => void;
  rememberToken: () => void;
  clearToken: () => void;
}

interface InvocationTokenOptions {
  value?: number;
  onValueChange?: (tokenID: number) => void;
  rememberScope?: "viewer" | "route";
}

const rememberedFailures = new Map<string, InvocationTokenFailure>();

function parseTokenID(value: string | null | number | undefined) {
  if (typeof value === "number") return Number.isSafeInteger(value) && value > 0 ? value : 0;
  if (!value || !/^\d+$/.test(value)) return 0;
  const id = Number(value);
  return Number.isSafeInteger(id) && id > 0 ? id : 0;
}

function scopeIdentity(scope: InvocationTokenScope) {
  return `${scope.viewerUserID}:${scope.ownerUserID ?? 0}:${scope.apiServiceID}:${scope.apiRouteID}`;
}

function rememberedTokenKey(scope: InvocationTokenScope, rememberScope: "viewer" | "route") {
  return rememberScope === "viewer"
    ? `aigw:api-catalog-token-id:${scope.viewerUserID}`
    : `aigw:api-invocation-token-id:${scope.viewerUserID}:${scope.apiServiceID}:${scope.apiRouteID}`;
}

function readStorage(key: string) {
  try {
    return window.localStorage.getItem(key) ?? "";
  } catch {
    return "";
  }
}

function emitStorageChange(key: string, oldValue: string | null, newValue: string | null) {
  try {
    window.dispatchEvent(new StorageEvent("storage", { key, oldValue, newValue }));
  } catch {
    // Storage already changed; a notification failure must not unlock or break invocation.
  }
}

function subscribeStorage(key: string, notify: () => void) {
  const listener = (event: StorageEvent) => { if (event.key === key) notify(); };
  window.addEventListener("storage", listener);
  return () => window.removeEventListener("storage", listener);
}

function storageSnapshot(key: string) {
  return readStorage(key);
}

function useRememberedTokenID(key: string) {
  const value = useSyncExternalStore(
    (notify) => subscribeStorage(key, notify),
    () => storageSnapshot(key),
    () => "",
  );
  return parseTokenID(value);
}

function writeRememberedToken(key: string, tokenID: number) {
  const value = String(tokenID);
  try {
    window.localStorage.setItem(key, value);
  } catch {
    return;
  }
  rememberedFailures.delete(key);
  emitStorageChange(key, null, value);
}

function removeRememberedToken(key: string, failure?: InvocationTokenFailure) {
  if (failure) rememberedFailures.set(key, failure);
  else rememberedFailures.delete(key);
  const oldValue = readStorage(key);
  try {
    window.localStorage.removeItem(key);
  } catch {
    return;
  }
  emitStorageChange(key, oldValue, null);
}

export function useInvocationToken(
  scope: InvocationTokenScope,
  options: InvocationTokenOptions = {},
): InvocationTokenState {
  const { value, onValueChange, rememberScope = "route" } = options;
  const storageKey = rememberedTokenKey(scope, rememberScope);
  const currentScope = scopeIdentity(scope);
  const rememberedTokenID = useRememberedTokenID(storageKey);
  const hydratedStorageKey = useRef<string | undefined>(undefined);
  const controlled = value !== undefined;
  const [candidate, setCandidate] = useState({ scope: "", tokenID: 0 });
  const candidateTokenID = candidate.scope === currentScope ? candidate.tokenID : undefined;
  const tokenID = controlled
    ? parseTokenID(value)
    : candidateTokenID ?? rememberedTokenID;
  const validation = useUsableTokenForAPIRoute({ ...scope, tokenID });
  const isChecking = tokenID > 0 && validation.isFetching;
  const failure: InvocationTokenFailure | null = tokenID > 0 && validation.isError
    ? "error"
    : tokenID > 0 && validation.isSuccess && validation.data === null
      ? "miss"
      : tokenID === 0
        ? rememberedFailures.get(storageKey) ?? null
        : null;
  const token = !isChecking && !failure && validation.data?.id === tokenID
    ? validation.data
    : undefined;

  useEffect(() => {
    if (failure && rememberedTokenID === tokenID) {
      removeRememberedToken(storageKey, failure);
    }
  }, [failure, rememberedTokenID, storageKey, tokenID]);

  useEffect(() => {
    if (!controlled || hydratedStorageKey.current === storageKey) return;
    hydratedStorageKey.current = storageKey;
    if (parseTokenID(value) === 0 && rememberedTokenID > 0) {
      onValueChange?.(rememberedTokenID);
    }
  }, [controlled, onValueChange, rememberedTokenID, storageKey, value]);

  const setTokenID = useCallback((nextTokenID: number) => {
    const next = parseTokenID(nextTokenID);
    rememberedFailures.delete(storageKey);
    if (controlled) onValueChange?.(next);
    else setCandidate({ scope: currentScope, tokenID: next });
  }, [controlled, currentScope, onValueChange, storageKey]);

  const rememberToken = useCallback(() => {
    if (token?.id === tokenID) writeRememberedToken(storageKey, tokenID);
  }, [storageKey, token, tokenID]);

  const clearToken = useCallback(() => {
    removeRememberedToken(storageKey);
    if (controlled) onValueChange?.(0);
    else setCandidate({ scope: currentScope, tokenID: 0 });
  }, [controlled, currentScope, onValueChange, storageKey]);

  return { tokenID, token, isChecking, failure, setTokenID, rememberToken, clearToken };
}

"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";

import {
  useSearchParamPatch,
  type PatchSearchParams,
} from "@/components/data-table/use-search-param-patch";
import { useToken, useTokens } from "@/lib/api/tokens";
import { ApiError } from "@/lib/api/client";
import { STATUS } from "@/lib/constants";
import type { Token } from "@/lib/types";
import { useTokenExpiryClock } from "./use-token-expiry-clock";

const LAST_TOKEN_KEY_PREFIX = "aigw:model-marketplace:last-token-id";

interface OptimisticTokenSelection {
  viewerId: number;
  tokenId: number | undefined;
  sourceTokenId: number | undefined;
  targetSearch: string;
}

export interface MarketplaceTokenSelection {
  candidateTokenId?: number;
  selectedTokenId?: number;
  validation: {
    status:
      | "idle"
      | "initialPending"
      | "backgroundFetching"
      | "validationError"
      | "validated"
      | "rejected";
    error?: unknown;
    retry: () => unknown;
  };
  ordinaryBootstrap: {
    status: "disabled" | "initialPending" | "error" | "ready";
    totalUsableTokens: number;
    retry: () => unknown;
  };
  tokenUnavailable: boolean;
  handleTokenChange: (tokenId: number | undefined) => void;
  handleTokenUnavailable: (tokenId: number) => void;
}

type TokenValidationStatus = MarketplaceTokenSelection["validation"]["status"];
type OrdinaryBootstrapStatus = MarketplaceTokenSelection["ordinaryBootstrap"]["status"];

function lastTokenStorageKey(userId: number) {
  return `${LAST_TOKEN_KEY_PREFIX}:${userId}`;
}

function parsePositiveTokenId(value: string | null) {
  if (value === null || !/^\d+$/.test(value)) return undefined;
  const tokenId = Number(value);
  return Number.isSafeInteger(tokenId) && tokenId > 0 ? tokenId : undefined;
}

function readRememberedTokenId(userId: number) {
  if (typeof window === "undefined") return undefined;
  try {
    return parsePositiveTokenId(window.localStorage.getItem(lastTokenStorageKey(userId)));
  } catch {
    return undefined;
  }
}

function rememberTokenId(userId: number, tokenId: number) {
  try {
    window.localStorage.setItem(lastTokenStorageKey(userId), String(tokenId));
  } catch {
    // URL state remains usable when storage is unavailable.
  }
}

function forgetRememberedTokenId(userId: number, tokenId: number) {
  try {
    const key = lastTokenStorageKey(userId);
    if (window.localStorage.getItem(key) === String(tokenId)) {
      window.localStorage.removeItem(key);
    }
  } catch {
    // URL state remains usable when storage is unavailable.
  }
}

function tokenIsUsable(token: Token, serverNowSeconds: number | undefined) {
  return token.status === STATUS.ENABLED &&
    (
      token.expired_at <= 0 ||
      serverNowSeconds === undefined ||
      token.expired_at >= serverNowSeconds
    );
}

function rejectedTokenKey(userId: number, tokenId: number) {
  return `${userId}:${tokenId}`;
}

export function useMarketplaceTokenSelection(
  userId: number,
  isAdmin: boolean,
  sharedPatchSearchParams?: PatchSearchParams,
): MarketplaceTokenSelection {
  const searchParams = useSearchParams();
  const defaultPatchSearchParams = useSearchParamPatch();
  const patchSearchParams = sharedPatchSearchParams ?? defaultPatchSearchParams;
  const currentSearch = searchParams.toString();
  const requestedTokenId = parsePositiveTokenId(searchParams.get("token_id"));
  const initializedScopeRef = useRef<string | undefined>(undefined);
  const rejectedTokenKeysRef = useRef(new Set<string>());
  const [rejectedTokenKeys, setRejectedTokenKeys] = useState<Set<string>>(() => new Set());
  const [optimisticSelection, setOptimisticSelection] =
    useState<OptimisticTokenSelection>();
  const [unavailableState, setUnavailableState] = useState<{
    viewerId: number;
    value: boolean;
  }>();

  const bootstrapQuery = useTokens({
    page: 1,
    page_size: 2,
    user_id: userId,
    usable_only: true,
  }, { enabled: !isAdmin });
  if (
    optimisticSelection &&
    (
      currentSearch === optimisticSelection.targetSearch ||
      requestedTokenId !== optimisticSelection.sourceTokenId
    )
  ) {
    setOptimisticSelection(undefined);
  }
  const optimisticIsPending = optimisticSelection?.viewerId === userId &&
    optimisticSelection.targetSearch !== currentSearch &&
    optimisticSelection.sourceTokenId === requestedTokenId;
  const candidateTokenId = optimisticIsPending
    ? optimisticSelection.tokenId
    : requestedTokenId;
  const candidateRejected = candidateTokenId !== undefined &&
    rejectedTokenKeys.has(rejectedTokenKey(userId, candidateTokenId));
  const selectedTokenQuery = useToken(candidateTokenId ?? 0);
  const selectedToken = selectedTokenQuery.data;
  const expiryClockTokens = useMemo(() => selectedToken ? [selectedToken] : [], [selectedToken]);
  const serverNowSeconds = useTokenExpiryClock(expiryClockTokens);
  const selectedTokenMatchesCandidate = candidateTokenId !== undefined &&
    selectedToken?.id === candidateTokenId;
  const selectedTokenBelongsToViewer = isAdmin || selectedToken?.user_id === userId;
  const selectedTokenInvalid = selectedTokenMatchesCandidate &&
    selectedTokenBelongsToViewer &&
    selectedToken !== undefined &&
    !tokenIsUsable(selectedToken, serverNowSeconds);
  const selectedTokenMissing = candidateTokenId !== undefined &&
    selectedTokenQuery.isError &&
    selectedTokenQuery.error instanceof ApiError &&
    selectedTokenQuery.error.status === 404;
  const selectedTokenForeign = candidateTokenId !== undefined &&
    selectedTokenMatchesCandidate &&
    !selectedTokenBelongsToViewer;
  const rejected = candidateRejected || selectedTokenInvalid || selectedTokenMissing || selectedTokenForeign;
  const validated = candidateTokenId !== undefined &&
    !rejected &&
    selectedTokenMatchesCandidate &&
    selectedTokenBelongsToViewer &&
    selectedToken !== undefined &&
    tokenIsUsable(selectedToken, serverNowSeconds);
  const backgroundFetching = validated && selectedTokenQuery.isFetching;
  const validationError = candidateTokenId !== undefined &&
    !rejected &&
    !validated &&
    selectedTokenQuery.isError
    ? selectedTokenQuery.error
    : undefined;
  const selectedTokenId = validated ? candidateTokenId : undefined;
  const validationStatus: TokenValidationStatus = candidateTokenId === undefined
    ? "idle"
    : rejected
      ? "rejected"
      : validationError !== undefined
        ? "validationError"
        : backgroundFetching
          ? "backgroundFetching"
          : validated
            ? "validated"
            : "initialPending";
  const tokenUnavailable = unavailableState?.viewerId === userId && unavailableState.value;
  const ordinaryBootstrapStatus: OrdinaryBootstrapStatus = isAdmin
    ? "disabled"
    : bootstrapQuery.isLoading
      ? "initialPending"
      : bootstrapQuery.isError
        ? "error"
        : "ready";

  const replaceTokenParam = useCallback((tokenId: number | undefined) => {
    const targetSearch = patchSearchParams({ token_id: tokenId, page: undefined });
    setOptimisticSelection({
      viewerId: userId,
      tokenId,
      sourceTokenId: requestedTokenId,
      targetSearch,
    });
  }, [patchSearchParams, requestedTokenId, userId]);

  const refetchTokens = bootstrapQuery.refetch;
  const refetchOrdinaryBootstrap = useCallback(() => {
    if (isAdmin) return undefined;
    return refetchTokens();
  }, [isAdmin, refetchTokens]);
  const handleTokenUnavailable = useCallback((tokenId: number) => {
    const key = rejectedTokenKey(userId, tokenId);
    if (rejectedTokenKeysRef.current.has(key)) return;

    rejectedTokenKeysRef.current.add(key);
    setRejectedTokenKeys((current) => new Set(current).add(key));
    setUnavailableState({ viewerId: userId, value: true });
    forgetRememberedTokenId(userId, tokenId);
    replaceTokenParam(undefined);
    void refetchOrdinaryBootstrap();
  }, [refetchOrdinaryBootstrap, replaceTokenParam, userId]);

  useEffect(() => {
    if (isAdmin) return;
    const initializationScope = `${userId}:${isAdmin}`;
    if (
      ordinaryBootstrapStatus === "initialPending" ||
      bootstrapQuery.isError ||
      initializedScopeRef.current === initializationScope
    ) return;

    if (requestedTokenId) return;

    const total = bootstrapQuery.data?.total ?? 0;
    const initialTokenId = total === 1
      ? bootstrapQuery.data?.data[0]?.id
      : total > 1
        ? readRememberedTokenId(userId)
        : undefined;
    const timer = window.setTimeout(() => {
      if (initializedScopeRef.current === initializationScope) return;
      initializedScopeRef.current = initializationScope;
      if (initialTokenId === undefined) return;

      rememberTokenId(userId, initialTokenId);
      replaceTokenParam(initialTokenId);
    }, 0);
    return () => window.clearTimeout(timer);
  }, [
    bootstrapQuery.data,
    bootstrapQuery.isError,
    isAdmin,
    ordinaryBootstrapStatus,
    replaceTokenParam,
    requestedTokenId,
    userId,
  ]);

  useEffect(() => {
    if (
      candidateTokenId !== undefined &&
      (selectedTokenInvalid || selectedTokenMissing || selectedTokenForeign)
    ) {
      handleTokenUnavailable(candidateTokenId);
    }
  }, [
    candidateTokenId,
    handleTokenUnavailable,
    selectedTokenForeign,
    selectedTokenInvalid,
    selectedTokenMissing,
  ]);

  const handleTokenChange = useCallback((tokenId: number | undefined) => {
    setUnavailableState({ viewerId: userId, value: false });
    if (tokenId !== undefined) {
      const key = rejectedTokenKey(userId, tokenId);
      rejectedTokenKeysRef.current.delete(key);
      setRejectedTokenKeys((current) => {
        if (!current.has(key)) return current;
        const next = new Set(current);
        next.delete(key);
        return next;
      });
    }
    if (tokenId !== undefined && !isAdmin) rememberTokenId(userId, tokenId);
    replaceTokenParam(tokenId);
  }, [isAdmin, replaceTokenParam, userId]);

  return {
    candidateTokenId,
    selectedTokenId,
    validation: {
      status: validationStatus,
      ...(validationError !== undefined ? { error: validationError } : {}),
      retry: selectedTokenQuery.refetch,
    },
    ordinaryBootstrap: {
      status: ordinaryBootstrapStatus,
      totalUsableTokens: isAdmin ? 0 : (bootstrapQuery.data?.total ?? 0),
      retry: refetchOrdinaryBootstrap,
    },
    tokenUnavailable,
    handleTokenChange,
    handleTokenUnavailable,
  };
}

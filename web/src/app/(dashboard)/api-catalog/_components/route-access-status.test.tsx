import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import en from "@/i18n/en.json";
import zh from "@/i18n/zh.json";

import { RouteAccessStatus, type RouteAccessStatusProps } from "./route-access-status";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, values?: Record<string, unknown>) =>
    values?.name ? `${key}:${String(values.name)}` : key,
}));

const verifiedToken = { id: 5, name: "Production Token", key: "sk-production-secret" };

function renderStatus(overrides: Partial<RouteAccessStatusProps> = {}) {
  return render(<RouteAccessStatus
    tokenID={5}
    token={verifiedToken}
    isChecking={false}
    failure={null}
    effectiveAccess="granted"
    {...overrides}
  />);
}

describe("RouteAccessStatus", () => {
  it.each(["granted", "unknown"] as const)(
    "shows a Route-verified Token as callable when effective access is %s",
    (effectiveAccess) => {
      renderStatus({ effectiveAccess });

      expect(screen.getByText("tokenCallable:Production Token")).toBeInTheDocument();
      expect(screen.getByTestId("route-access-status")).toHaveAttribute("data-can-copy-real", "true");
    },
  );

  it("shows an effective-query outage only as auxiliary status without revoking a verified Token", () => {
    renderStatus({ effectiveAccess: "unknown", effectiveUnavailable: true });

    expect(screen.getByText("accessLoadFailed")).toBeInTheDocument();
    expect(screen.getByText("tokenCallable:Production Token")).toBeInTheDocument();
    expect(screen.getByTestId("route-access-status")).toHaveAttribute("data-can-copy-real", "true");
  });

  it.each(["granted", "not_granted", "unknown"] as const)(
    "shows checking and never permits real copying while effective access is %s",
    (effectiveAccess) => {
      renderStatus({ effectiveAccess, isChecking: true });

      expect(screen.getByText("tokenChecking")).toBeInTheDocument();
      expect(screen.getByTestId("route-access-status")).toHaveAttribute("data-can-copy-real", "false");
    },
  );

  it("shows a Token miss with effective access only as an auxiliary hint", () => {
    renderStatus({ token: undefined, failure: "miss", effectiveAccess: "granted" });

    expect(screen.getByText("tokenUnavailable")).toBeInTheDocument();
    expect(screen.getByText("effectiveAccessHint")).toBeInTheDocument();
    expect(screen.getByTestId("route-access-status")).toHaveAttribute("data-can-copy-real", "false");
  });

  it.each(["granted", "unknown"] as const)(
    "shows a retry-or-change message after Token validation error with effective access %s",
    (effectiveAccess) => {
      renderStatus({ token: undefined, failure: "error", effectiveAccess });

      expect(screen.getByText("tokenValidationFailed")).toBeInTheDocument();
      expect(screen.getByTestId("route-access-status")).toHaveAttribute("data-can-copy-real", "false");
    },
  );

  it.each(["granted", "not_granted", "unknown"] as const)(
    "asks for a Token before validation when effective access is %s",
    (effectiveAccess) => {
      renderStatus({ tokenID: 0, token: undefined, effectiveAccess });

      expect(screen.getByText("chooseTokenToInvoke")).toBeInTheDocument();
      expect(screen.getByTestId("route-access-status")).toHaveAttribute("data-can-copy-real", "false");
    },
  );
});

describe("apiCatalog i18n contract", () => {
  const requiredKeys = [
    "title",
    "description",
    "serviceLabel",
    "routeLabel",
    "tokenLabel",
    "searchPlaceholder",
    "loadMore",
    "retry",
    "emptyState",
    "errorState",
    "httpProtocol",
    "websocketProtocol",
    "method",
    "subpath",
    "query",
    "headers",
    "body",
    "copyCommand",
    "copyTemplate",
    "tokenChecking",
    "tokenCallable",
    "tokenUnavailable",
    "tokenValidationFailed",
    "chooseTokenToInvoke",
    "effectiveAccessHint",
    "accessLoadFailed",
    "mobileServicePicker",
    "mobileRoutePicker",
    "mobileTokenPicker",
  ] as const;

  const lookup = (messages: typeof en, key: string) => key.split(".").reduce<unknown>(
    (value, segment) => typeof value === "object" && value !== null
      ? (value as Record<string, unknown>)[segment]
      : undefined,
    messages,
  );

  it.each(Object.entries({ zh, en }))("resolves every Task 6 key to non-empty text in %s", (_locale, messages) => {
    for (const key of requiredKeys) {
      const value = lookup(messages, `apiCatalog.${key}`);
      expect(value, `apiCatalog.${key}`).toEqual(expect.any(String));
      expect(String(value).trim(), `apiCatalog.${key}`).not.toBe("");
    }
  });
});

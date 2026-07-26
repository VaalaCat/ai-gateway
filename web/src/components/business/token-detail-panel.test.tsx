import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { Token } from "@/lib/types";
import { TokenDetailPanel } from "./token-detail-panel";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ isAdmin: false }) }));
vi.mock("@/components/business/token-available-models", () => ({
  TokenAvailableModels: () => null,
}));
vi.mock("@/components/business/token-model-routings", () => ({
  TokenModelRoutings: () => null,
}));
vi.mock("@/components/business/copyable-text", () => ({
  CopyableText: ({ display }: { display: string }) => <span>{display}</span>,
}));

const baseToken: Token = {
  id: 23,
  user_id: 7,
  key: "sk-test",
  name: "production",
  status: 1,
  expired_at: 0,
  models: "",
  trace_enabled: false,
  trace_mode: "full",
  created_at: 1,
  updated_at: 1,
};

describe("TokenDetailPanel trace status", () => {
  it.each([
    { trace_enabled: false, trace_mode: "headers" as const, label: "traceDisabled" },
    { trace_enabled: true, trace_mode: "full" as const, label: "traceModeFull" },
    { trace_enabled: true, trace_mode: "headers" as const, label: "traceModeHeaders" },
  ])("always shows fieldTrace as $label", ({ trace_enabled, trace_mode, label }) => {
    render(
      <TokenDetailPanel token={{ ...baseToken, trace_enabled, trace_mode }} />,
    );

    expect(screen.getByText("fieldTrace")).toBeInTheDocument();
    expect(screen.getByText(label)).toBeInTheDocument();
  });
});

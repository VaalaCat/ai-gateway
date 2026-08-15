import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useLogColumns } from "./log-columns";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

describe("useLogColumns", () => {
  it("keeps the upstream column in the administrator view", () => {
    const { result } = renderHook(() => useLogColumns(vi.fn(), { showInternal: true }));
    expect(result.current.map((column) => column.id)).toContain("upstream");
  });

  it("removes the upstream column from the ordinary-user view", () => {
    const { result } = renderHook(() => useLogColumns(vi.fn(), { showInternal: false }));
    expect(result.current.map((column) => column.id)).not.toContain("upstream");
  });
});

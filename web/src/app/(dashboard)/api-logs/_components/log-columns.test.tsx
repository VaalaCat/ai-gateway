import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { useLogColumns } from "./log-columns";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));

describe("useLogColumns", () => {
  it("offers the administrator the complete request identity and response metrics", () => {
    const { result } = renderHook(() => useLogColumns(vi.fn(), { showInternal: true }));
    expect(result.current.map((column) => column.id)).toEqual(expect.arrayContaining([
      "user_id",
      "upstream",
      "token_name",
      "first_byte_ms",
      "request_bytes",
      "response_bytes",
      "total_cost",
    ]));
  });

  it("removes administrator identity and routing columns from the ordinary-user view", () => {
    const { result } = renderHook(() => useLogColumns(vi.fn(), { showInternal: false }));
    expect(result.current.map((column) => column.id)).not.toContain("upstream");
    expect(result.current.map((column) => column.id)).not.toContain("user_id");
  });
});

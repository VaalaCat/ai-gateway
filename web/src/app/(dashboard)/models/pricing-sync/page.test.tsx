import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import PricingSyncPage from "./page";

const mocks = vi.hoisted(() => ({
  fetchPricing: vi.fn(),
  applyPricing: vi.fn(),
  toastError: vi.fn(),
  translate: (key: string) => key,
}));

vi.mock("next-intl", () => ({ useTranslations: () => mocks.translate }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("sonner", () => ({ toast: { error: mocks.toastError, success: vi.fn() } }));
vi.mock("@/components/business/provider-avatar", () => ({ ProviderAvatar: () => null }));
vi.mock("@/lib/api/models", async (importOriginal) => {
  const original = await importOriginal<typeof import("@/lib/api/models")>();
  return {
    ...original,
    useFetchPricing: () => ({ mutateAsync: mocks.fetchPricing, isPending: false }),
    useApplyPricing: () => ({ mutateAsync: mocks.applyPricing, isPending: false }),
  };
});

function rejectedLater() {
  let reject!: (error: Error) => void;
  const promise = new Promise<never>((_resolve, rejectPromise) => { reject = rejectPromise; });
  return { promise, reject };
}

beforeEach(() => {
  mocks.fetchPricing.mockReset();
  mocks.applyPricing.mockReset();
  mocks.applyPricing.mockResolvedValue({ updated: 1 });
  mocks.toastError.mockReset();
});
afterEach(() => vi.restoreAllMocks());

it("does not report a stale pricing failure after unmount", async () => {
  const pending = rejectedLater();
  mocks.fetchPricing.mockReturnValueOnce(pending.promise);
  const { unmount } = render(<PricingSyncPage />);
  await waitFor(() => expect(mocks.fetchPricing).toHaveBeenCalledOnce());

  unmount();
  await act(async () => { pending.reject(new Error("stale failure")); await pending.promise.catch(() => undefined); });

  expect(mocks.toastError).not.toHaveBeenCalled();
});

it("uses the shared PageHeader without changing the pricing canvas", async () => {
  mocks.fetchPricing.mockResolvedValueOnce({ recommendations: [] });
  render(<PricingSyncPage />);

  expect(screen.getByTestId("page-header")).toHaveTextContent("pricingSyncTitle");
  expect(screen.getByRole("button", { name: "back" })).toBeInTheDocument();
});

it("keeps the applied summary in PageHeader description on semantic muted text", async () => {
  mocks.fetchPricing.mockResolvedValueOnce({
    matches: [{
      model_id: 7,
      model_name: "gpt-4o",
      current: { input_price: 1, output_price: 2, cache_read_price: 0, cache_write_price: 0 },
      has_price: true,
      recommended: { input_price: 2, output_price: 3, cache_read_price: 0, cache_write_price: 0 },
      provenance: "openrouter",
      confidence: "needs_review",
      has_change: true,
      candidates: [],
    }],
    unmatched_models: [],
  });
  render(<PricingSyncPage />);

  fireEvent.click(await screen.findByRole("button", { name: "accept" }));
  await waitFor(() => expect(mocks.applyPricing).toHaveBeenCalledOnce());

  const header = screen.getByTestId("page-header");
  const summary = await within(header).findByText(/appliedSummary/);
  expect(summary).not.toHaveClass("text-green-600");
  expect(summary.parentElement).toHaveClass("text-muted-foreground");
});

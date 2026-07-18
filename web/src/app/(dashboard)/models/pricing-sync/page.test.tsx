import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import PricingSyncPage from "./page";

const mocks = vi.hoisted(() => ({
  fetchPricing: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
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
    useApplyPricing: () => ({ mutateAsync: vi.fn(), isPending: false }),
  };
});

function rejectedLater() {
  let reject!: (error: Error) => void;
  const promise = new Promise<never>((_resolve, rejectPromise) => { reject = rejectPromise; });
  return { promise, reject };
}

beforeEach(() => {
  vi.useFakeTimers();
  mocks.fetchPricing.mockReset();
  mocks.toastError.mockReset();
});
afterEach(() => vi.useRealTimers());

it("does not report a stale pricing failure after unmount", async () => {
  const pending = rejectedLater();
  mocks.fetchPricing.mockReturnValueOnce(pending.promise);
  const { unmount } = render(<PricingSyncPage />);
  await act(async () => { await vi.runAllTimersAsync(); });
  expect(mocks.fetchPricing).toHaveBeenCalledOnce();

  unmount();
  await act(async () => { pending.reject(new Error("stale failure")); await pending.promise.catch(() => undefined); });

  expect(mocks.toastError).not.toHaveBeenCalled();
});

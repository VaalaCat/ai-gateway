import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { UseFormReturn } from "react-hook-form";
import { afterEach, expect, it, vi } from "vitest";

import type { RoutingPreview } from "@/lib/types";
import type { RoutingFormValues } from "./routing-form/types";
import { PreviewPanel } from "./preview-panel";

const mocks = vi.hoisted(() => ({ mutateAsync: vi.fn() }));

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/hooks/use-debounce", () => ({ useDebounce: <T,>(value: T) => value }));
vi.mock("@/lib/api/model-routings", () => ({
  usePreviewModelRouting: () => ({ mutateAsync: mocks.mutateAsync }),
}));
vi.mock("./priority-layers", () => ({
  PriorityCascade: ({ members }: { members: unknown }) => <div data-testid="preview">{JSON.stringify(members)}</div>,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function preview(ref: string): RoutingPreview {
  return { root: { children: [{ ref }] } } as RoutingPreview;
}

function loadingBars() {
  return document.querySelectorAll(".animate-pulse");
}

function formFor(members: RoutingFormValues["members"]): UseFormReturn<RoutingFormValues> {
  const values: RoutingFormValues = {
    name: "route",
    scope: "global",
    user_id: 0,
    members,
    enabled: true,
    remark: "",
  };
  return {
    watch: () => values,
  } as unknown as UseFormReturn<RoutingFormValues>;
}

afterEach(async () => {
  mocks.mutateAsync.mockReset();
});

it("ignores a stale preview response after a newer form request succeeds", async () => {
  const first = deferred<RoutingPreview>();
  const second = deferred<RoutingPreview>();
  mocks.mutateAsync.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  let values = { name: "first", scope: "global", members: [{ ref: "first" }] } as RoutingFormValues;
  const form = { watch: () => values } as unknown as UseFormReturn<RoutingFormValues>;
  const { rerender, unmount } = render(
    <PreviewPanel form={form} />,
  );
  await waitFor(() => expect(mocks.mutateAsync).toHaveBeenCalledTimes(1));

  values = { ...values, name: "second", members: [{ ref: "second" }] } as RoutingFormValues;
  rerender(<PreviewPanel form={form} />);
  await waitFor(() => expect(mocks.mutateAsync).toHaveBeenCalledTimes(2));

  await act(async () => { second.resolve(preview("second")); await second.promise; });
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("second"));

  await act(async () => { first.resolve(preview("first")); await first.promise; });
  expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("second");
  unmount();
});

const invalidMemberCases: [string, RoutingFormValues["members"]][] = [
  ["empty members", []],
  ["missing ref", [{ ref: "", priority: 0, weight: 1 }]],
  ["mixed valid and missing refs", [
    { ref: "valid", priority: 0, weight: 1 },
    { ref: "", priority: 1, weight: 1 },
  ]],
];

it.each(invalidMemberCases)("does not manually preview invalid input: %s", async (_name, members) => {
  render(<PreviewPanel form={formFor(members)} />);

  const refreshButtons = screen.getAllByRole("button", { name: "refresh" });
  expect(refreshButtons[0]).toBeDisabled();
  fireEvent.click(refreshButtons[0]);

  expect(mocks.mutateAsync).not.toHaveBeenCalled();
});

it("does not reveal a result from an older request after invalid input becomes valid again", async () => {
  mocks.mutateAsync.mockResolvedValueOnce(preview("old"));
  let values = { name: "route", scope: "global", members: [{ ref: "old" }] } as RoutingFormValues;
  const form = { watch: () => values } as unknown as UseFormReturn<RoutingFormValues>;
  const { rerender } = render(<PreviewPanel form={form} />);
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("old"));

  values = { ...values, members: [] } as RoutingFormValues;
  rerender(<PreviewPanel form={form} />);
  expect(screen.queryAllByTestId("preview")).toHaveLength(0);

  const pending = deferred<RoutingPreview>();
  mocks.mutateAsync.mockReturnValueOnce(pending.promise);
  values = { ...values, members: [{ ref: "new" }] } as RoutingFormValues;
  rerender(<PreviewPanel form={form} />);

  expect(screen.queryAllByTestId("preview")).toHaveLength(0);
  await act(async () => { pending.resolve(preview("new")); await pending.promise; });
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("new"));
});

it("hides superseded manual loading immediately when the form becomes invalid", async () => {
  const manual = deferred<RoutingPreview>();
  mocks.mutateAsync
    .mockResolvedValueOnce(preview("initial"))
    .mockReturnValueOnce(manual.promise);
  let values = { name: "route", scope: "global", members: [{ ref: "initial" }] } as RoutingFormValues;
  const form = { watch: () => values } as unknown as UseFormReturn<RoutingFormValues>;
  const { rerender } = render(<PreviewPanel form={form} />);
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("initial"));

  fireEvent.click(screen.getAllByRole("button", { name: "refresh" })[0]);
  expect(loadingBars()).toHaveLength(2);

  values = { ...values, members: [] } as RoutingFormValues;
  rerender(<PreviewPanel form={form} />);
  expect(loadingBars()).toHaveLength(0);

  await act(async () => { manual.resolve(preview("stale")); await manual.promise; });
  expect(loadingBars()).toHaveLength(0);
  expect(screen.queryAllByTestId("preview")).toHaveLength(0);
});

it("keeps a superseded manual response from affecting a newer automatic request", async () => {
  const manual = deferred<RoutingPreview>();
  const automatic = deferred<RoutingPreview>();
  mocks.mutateAsync
    .mockResolvedValueOnce(preview("initial"))
    .mockReturnValueOnce(manual.promise)
    .mockReturnValueOnce(automatic.promise);
  let values = { name: "route", scope: "global", members: [{ ref: "initial" }] } as RoutingFormValues;
  const form = { watch: () => values } as unknown as UseFormReturn<RoutingFormValues>;
  const { rerender } = render(<PreviewPanel form={form} />);
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("initial"));

  fireEvent.click(screen.getAllByRole("button", { name: "refresh" })[0]);
  expect(loadingBars()).toHaveLength(2);

  values = { ...values, members: [{ ref: "new" }] } as RoutingFormValues;
  rerender(<PreviewPanel form={form} />);
  await waitFor(() => expect(mocks.mutateAsync).toHaveBeenCalledTimes(3));
  expect(loadingBars()).toHaveLength(0);

  await act(async () => { manual.resolve(preview("stale")); await manual.promise; });
  expect(loadingBars()).toHaveLength(0);
  expect(screen.queryAllByTestId("preview")).toHaveLength(0);

  await act(async () => { automatic.resolve(preview("new")); await automatic.promise; });
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("new"));
});

it("ignores a superseded manual rejection without restoring error or loading state", async () => {
  const manual = deferred<RoutingPreview>();
  mocks.mutateAsync
    .mockResolvedValueOnce(preview("initial"))
    .mockReturnValueOnce(manual.promise);
  let values = { name: "route", scope: "global", members: [{ ref: "initial" }] } as RoutingFormValues;
  const form = { watch: () => values } as unknown as UseFormReturn<RoutingFormValues>;
  const { rerender } = render(<PreviewPanel form={form} />);
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("initial"));

  fireEvent.click(screen.getAllByRole("button", { name: "refresh" })[0]);
  expect(loadingBars()).toHaveLength(2);
  values = { ...values, members: [] } as RoutingFormValues;
  rerender(<PreviewPanel form={form} />);

  await act(async () => {
    manual.reject(new Error("stale failure"));
    await manual.promise.catch(() => undefined);
  });
  expect(loadingBars()).toHaveLength(0);
  expect(screen.queryAllByLabelText("fail")).toHaveLength(0);
});

it("finishes a current manual refresh and shows its result", async () => {
  const manual = deferred<RoutingPreview>();
  mocks.mutateAsync
    .mockResolvedValueOnce(preview("initial"))
    .mockReturnValueOnce(manual.promise);
  const form = formFor([{ ref: "initial", priority: 0, weight: 1 }]);
  render(<PreviewPanel form={form} />);
  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("initial"));

  fireEvent.click(screen.getAllByRole("button", { name: "refresh" })[0]);
  expect(loadingBars()).toHaveLength(2);
  await act(async () => { manual.resolve(preview("manual")); await manual.promise; });

  await waitFor(() => expect(screen.getAllByTestId("preview")[0]).toHaveTextContent("manual"));
  expect(loadingBars()).toHaveLength(0);
});

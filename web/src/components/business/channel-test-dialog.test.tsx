import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, expect, it, vi } from "vitest";

import { ChannelTestDialog } from "./channel-test-dialog";

vi.mock("next-intl", () => ({ useTranslations: () => (key: string) => key }));
vi.mock("@/lib/api/agent-routes", () => ({ useAgentRoutes: () => ({ data: undefined }) }));
vi.mock("./online-agent-select", () => ({ OnlineAgentSelect: () => null }));
vi.mock("./model-name", () => ({ ModelName: ({ name }: { name: string }) => <span>{name}</span> }));

beforeAll(() => {
  Element.prototype.scrollIntoView ??= () => {};
  HTMLElement.prototype.hasPointerCapture ??= () => false;
  HTMLElement.prototype.setPointerCapture ??= () => {};
  HTMLElement.prototype.releasePointerCapture ??= () => {};
});

it.each([true, false])("sends Gemini endpoint_type from the shared admin/BYOK tester (stream support: %s)", async (supportsStream) => {
  const user = userEvent.setup();
  const testFn = vi.fn().mockResolvedValue({ success: true, time_cost: 1 });
  render(<ChannelTestDialog channelLike={{ id: 17, name: "Gemini", type: 24, models: "custom-model", endpoints: JSON.stringify({ generate_content: "/v1beta/models/{model}:{action}" }) }} testFn={testFn} supportsStream={supportsStream} agentSourceType={null} open onOpenChange={vi.fn()} />);
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByRole("option", { name: "apiTypeGemini" }));
  if (supportsStream) await user.click(screen.getByRole("switch"));
  await user.click(screen.getByRole("button", { name: "test" }));
  await waitFor(() => expect(testFn).toHaveBeenCalledWith({
    id: 17, model: "custom-model", endpoint_type: "gemini", ...(supportsStream ? { stream: true } : {}),
  }));
});

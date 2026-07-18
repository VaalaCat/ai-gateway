import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AgentURI } from "./agent-uri";
import { TooltipProvider } from "@/components/ui/tooltip";

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => ({ copyUri: "Copy URI", copied: "Copied", copyFailed: "Copy failed" } as Record<string, string>)[key] ?? key,
}));

vi.mock("@/lib/utils/clipboard", () => ({ copyTextWithFeedback: vi.fn().mockResolvedValue(true) }));

describe("AgentURI", () => {
  it("keeps the layout shrinkable and exposes the full value through tooltip and copy", async () => {
    const user = userEvent.setup();
    const uri = "wss://edge.example.com/a/very/long/relay/path?region=ap-southeast-1";
    const { copyTextWithFeedback } = await import("@/lib/utils/clipboard");
    const { container } = render(<TooltipProvider><AgentURI uri={uri} /></TooltipProvider>);

    expect(container.firstChild).toHaveClass("min-w-0");
    expect(container.querySelector("[aria-label='" + uri + "']")).toHaveClass("font-datatype");
    const prefix = container.querySelector("[data-slot=uri-prefix]");
    const suffix = container.querySelector("[data-slot=uri-suffix]");
    expect(prefix).toHaveClass("min-w-0", "truncate");
    expect(suffix).toHaveClass("shrink-0");
    expect(suffix?.textContent?.length).toBeLessThanOrEqual(24);
    expect(`${prefix?.textContent}${suffix?.textContent}`).toBe(uri);
    const button = screen.getByRole("button", { name: "Copy URI" });
    await user.hover(prefix!);
    expect(await screen.findByRole("tooltip")).toHaveTextContent(uri);
    await user.click(button);
    expect(copyTextWithFeedback).toHaveBeenCalledWith(uri, expect.any(Object));
  });
});

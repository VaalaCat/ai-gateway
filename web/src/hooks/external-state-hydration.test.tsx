import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nProvider } from "@/components/i18n-provider";
import { useIsMobile } from "@/hooks/use-mobile";
import { useSidebarSection } from "@/hooks/use-sidebar-section";

vi.mock("next-intl", () => ({
  NextIntlClientProvider: ({ children, locale }: { children: React.ReactNode; locale: string }) => (
    <div data-testid="locale" data-locale={locale}>{children}</div>
  ),
}));

function MobileProbe() {
  return <span>{useIsMobile() ? "mobile" : "desktop"}</span>;
}

function SidebarProbe() {
  const [open] = useSidebarSection("quality-review", true);
  return <span>{open ? "open" : "closed"}</span>;
}

describe("external state hydration", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    window.localStorage.clear();
    Object.defineProperty(document, "cookie", { configurable: true, writable: true, value: "" });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("reads the current locale cookie without waiting for scheduled effect work", () => {
    document.cookie = "locale=zh";
    render(
      <I18nProvider initialLocale="en" initialMessages={{}}>
        content
      </I18nProvider>,
    );

    expect(screen.getByTestId("locale")).toHaveAttribute("data-locale", "zh");
  });

  it("reads the current media query snapshot without waiting for scheduled effect work", () => {
    Object.defineProperty(window, "innerWidth", { configurable: true, value: 500 });
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({
      matches: true,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }));

    render(<MobileProbe />);

    expect(screen.getByText("mobile")).toBeInTheDocument();
  });

  it("reads a persisted sidebar section without waiting for scheduled effect work", () => {
    window.localStorage.setItem("sidebar:section:quality-review", "closed");

    render(<SidebarProbe />);

    expect(screen.getByText("closed")).toBeInTheDocument();
  });
});

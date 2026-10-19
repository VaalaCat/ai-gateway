import { act, render, waitFor } from "@testing-library/react";
import { renderToString } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";
import { SiteBrandingRuntime, SiteIcon } from "./site-branding";

const { publicConfig, route } = vi.hoisted(() => ({
  publicConfig: { current: {} as Record<string, string> },
  route: { pathname: "/login" },
}));

vi.mock("@/lib/api/system", () => ({
  usePublicConfig: () => ({ data: publicConfig.current }),
}));
vi.mock("next/navigation", () => ({ usePathname: () => route.pathname }));

beforeEach(() => {
  document.title = "AI Gateway";
  document.head.querySelectorAll('link[rel~="icon"]').forEach((link) => link.remove());
  publicConfig.current = { ...SITE_BRANDING_DEFAULTS };
  route.pathname = "/login";
});

afterEach(() => {
  document.head.querySelectorAll('link[rel~="icon"]').forEach((link) => link.remove());
});

describe("site branding runtime", () => {
  it("leaves document metadata untouched during server rendering", () => {
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "Custom title", site_icon_url: "/custom.svg" };
    expect(renderToString(<SiteBrandingRuntime />)).toBe("");
    expect(document.title).toBe("AI Gateway");
    expect(document.head.querySelector('link[rel~="icon"]')).not.toBeInTheDocument();
  });

  it("updates the title and replaces every original favicon, then restores defaults without stale icons", () => {
    const original = document.createElement("link");
    original.rel = "icon";
    original.href = "/favicon.ico";
    original.type = "image/x-icon";
    const shortcut = document.createElement("link");
    shortcut.rel = "shortcut icon";
    shortcut.href = "/old-shortcut.ico";
    document.head.append(original, shortcut);
    const { rerender, unmount } = render(<SiteBrandingRuntime />);

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "Custom <b>title</b>", site_icon_url: "https://cdn.example.com/first.svg" };
    rerender(<SiteBrandingRuntime />);
    expect(document.title).toBe("Custom <b>title</b>");
    let icons = document.head.querySelectorAll('link[rel~="icon"]');
    expect(icons).toHaveLength(1);
    expect(icons[0]).toHaveAttribute("href", "https://cdn.example.com/first.svg");
    expect(icons[0]).not.toHaveAttribute("type");
    expect(original.isConnected).toBe(false);
    expect(shortcut.isConnected).toBe(false);

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "Second brand", site_icon_url: "/second.png" };
    rerender(<SiteBrandingRuntime />);
    icons = document.head.querySelectorAll('link[rel~="icon"]');
    expect(icons).toHaveLength(1);
    expect(icons[0]).toHaveAttribute("href", "/second.png");
    expect(document.title).toBe("Second brand");

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS };
    rerender(<SiteBrandingRuntime />);
    expect(document.title).toBe("AI Gateway");
    expect(Array.from(document.head.querySelectorAll('link[rel~="icon"]'))).toEqual([original, shortcut]);
    expect(original).toHaveAttribute("type", "image/x-icon");
    expect(document.head.querySelector('link[href="/second.png"]')).not.toBeInTheDocument();
    unmount();
  });

  it("supplies the static default favicon when the initial document has no icon", () => {
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_icon_url: "/brand.png" };
    const { rerender, unmount } = render(<SiteBrandingRuntime />);
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS };
    rerender(<SiteBrandingRuntime />);
    expect(document.head.querySelectorAll('link[rel~="icon"]')).toHaveLength(1);
    expect(document.head.querySelector('link[rel="icon"]')).toHaveAttribute("href", "/favicon.ico");
    unmount();
    expect(document.head.querySelector('link[rel~="icon"]')).not.toBeInTheDocument();
  });

  it("reasserts unchanged branding when Next resets the head after a route transition", async () => {
    const staticIcon = document.createElement("link");
    staticIcon.rel = "icon";
    staticIcon.href = "/favicon.ico";
    document.head.appendChild(staticIcon);
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "星河 AI", site_icon_url: "/custom-brand.svg" };
    const { rerender, unmount } = render(<SiteBrandingRuntime />);

    route.pathname = "/system";
    rerender(<SiteBrandingRuntime />);
    expect(document.title).toBe("星河 AI");
    act(() => {
      document.title = "AI Gateway";
      const nextIcon = document.createElement("link");
      nextIcon.rel = "icon";
      nextIcon.href = "/favicon.ico?next-static";
      document.head.appendChild(nextIcon);
    });
    await waitFor(() => {
      expect(document.title).toBe("星河 AI");
      const icons = document.head.querySelectorAll('link[rel~="icon"]');
      expect(icons).toHaveLength(1);
      expect(icons[0]).toHaveAttribute("href", "/custom-brand.svg");
    });

    // A later metadata reconciliation can also remove our link entirely.
    act(() => {
      document.head.querySelector('link[rel~="icon"]')?.remove();
      document.title = "AI Gateway";
    });
    await waitFor(() => {
      expect(document.title).toBe("星河 AI");
      expect(document.head.querySelector('link[rel~="icon"]')).toHaveAttribute("href", "/custom-brand.svg");
    });

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS };
    rerender(<SiteBrandingRuntime />);
    expect(document.title).toBe("AI Gateway");
    expect(document.head.querySelectorAll('link[rel~="icon"]')).toHaveLength(1);
    expect(document.head.querySelector('link[rel~="icon"]')).toBe(staticIcon);
    unmount();
  });

  it("restores the original document on unmount and never renders an unsafe icon URL", () => {
    document.title = "Original title";
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "New title", site_icon_url: "javascript:alert(1)" };
    const { container, unmount } = render(<><SiteBrandingRuntime /><SiteIcon iconUrl={publicConfig.current.site_icon_url} /></>);
    expect(document.title).toBe("New title");
    expect(document.head.querySelector('link[rel="icon"]')).toHaveAttribute("href", "/favicon.ico");
    expect(container.querySelector("img")).not.toBeInTheDocument();
    expect(container.querySelector("svg")).toBeInTheDocument();
    unmount();
    expect(document.title).toBe("Original title");
  });
});

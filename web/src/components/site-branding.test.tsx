import { render } from "@testing-library/react";
import { renderToString } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";
import { SiteBrandingRuntime, SiteIcon } from "./site-branding";

const { publicConfig } = vi.hoisted(() => ({
  publicConfig: { current: undefined as Record<string, string> | undefined },
}));

vi.mock("@/lib/api/system", () => ({
  usePublicConfig: () => ({ data: publicConfig.current }),
}));

beforeEach(() => {
  publicConfig.current = { ...SITE_BRANDING_DEFAULTS };
});

describe("declarative site branding metadata", () => {
  it("renders no default metadata before the public config is known", () => {
    publicConfig.current = undefined;
    expect(renderToString(<SiteBrandingRuntime />)).toBe("");
    const { rerender } = render(<SiteBrandingRuntime />);
    expect(document.head.querySelector('link[rel="icon"]')).not.toBeInTheDocument();
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "Custom title", site_icon_url: "/custom.svg" };
    rerender(<SiteBrandingRuntime />);
    expect(document.title).toBe("Custom title");
    expect(document.head.querySelector('link[rel="icon"]')).toHaveAttribute("href", "/custom.svg");
  });

  it("uses React metadata during server rendering without mutating the document", () => {
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "Custom <b>title</b>", site_icon_url: "/custom.svg" };
    const previousTitle = document.title;
    const markup = renderToString(<SiteBrandingRuntime />);
    expect(markup).toContain("<title>Custom &lt;b&gt;title&lt;/b&gt;</title>");
    expect(markup).toContain('rel="icon" href="/custom.svg"');
    expect(document.title).toBe(previousTitle);
  });

  it("keeps branding through page transitions without removing framework-owned head nodes", () => {
    function App({ page }: { page: string }) {
      return <><link rel="icon" href="/framework-owned.ico" /><SiteBrandingRuntime /><main key={page}>{page}</main></>;
    }
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "星河 AI", site_icon_url: "/custom.svg" };
    const { rerender, unmount } = render(<App page="login" />);
    const frameworkIcon = document.head.querySelector('link[href="/framework-owned.ico"]');
    const brandedIcon = document.head.querySelector('link[href="/custom.svg"]');
    expect(frameworkIcon).toBeInTheDocument();
    expect(brandedIcon).toBeInTheDocument();
    rerender(<App page="system" />);
    expect(document.title).toBe("星河 AI");
    expect(document.head.querySelector('link[href="/framework-owned.ico"]')).toBe(frameworkIcon);
    expect(document.head.querySelector('link[href="/custom.svg"]')).toBe(brandedIcon);

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "Second brand", site_icon_url: "/second.png" };
    rerender(<App page="profile" />);
    expect(document.title).toBe("Second brand");
    expect(document.head.querySelector('link[href="/second.png"]')).toBeInTheDocument();
    expect(document.head.querySelector('link[href="/custom.svg"]')).not.toBeInTheDocument();
    expect(frameworkIcon).toBeInTheDocument();

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS };
    rerender(<App page="system" />);
    expect(document.title).toBe("AI Gateway");
    expect(document.head.querySelector('link[href="/favicon.ico"]')).toBeInTheDocument();
    expect(document.head.querySelector('link[href="/second.png"]')).not.toBeInTheDocument();
    expect(frameworkIcon).toBeInTheDocument();
    expect(() => unmount()).not.toThrow();
    expect(frameworkIcon).not.toBeInTheDocument();
  });

  it("rejects unsafe icons while keeping the static default asset", () => {
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, site_title: "New title", site_icon_url: "javascript:alert(1)" };
    const { container } = render(<><SiteBrandingRuntime /><SiteIcon iconUrl={publicConfig.current.site_icon_url} /></>);
    expect(document.title).toBe("New title");
    expect(document.head.querySelector('link[rel="icon"]')).toHaveAttribute("href", "/favicon.ico");
    expect(container.querySelector("img")).not.toBeInTheDocument();
    expect(container.querySelector("svg")).toBeInTheDocument();
  });
});

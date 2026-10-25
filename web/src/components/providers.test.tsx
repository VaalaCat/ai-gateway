import { Suspense, type ReactNode } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { hydrateRoot } from "react-dom/client";
import { renderToString } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { usePublicConfig, type PublicConfig } from "@/lib/api/system";
import { SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";
import { BrandPlaceholder, SiteIcon } from "./site-branding";
import { Providers } from "./providers";

const { apiGet } = vi.hoisted(() => ({ apiGet: vi.fn() }));
vi.mock("@/lib/api/client", () => ({ api: { get: apiGet } }));
vi.mock("next-themes", () => ({ ThemeProvider: ({ children }: { children: ReactNode }) => children }));
vi.mock("@/components/ui/sonner", () => ({ Toaster: () => null }));

const storageKey = "ai-gateway:public-config:v1";
const savedConfig: PublicConfig = {
  ...SITE_BRANDING_DEFAULTS,
  site_name: "Saved Gateway",
  site_title: "Saved title",
  site_icon_url: "/saved.svg",
  registration_enabled: true,
  invite_enabled: false,
  invite_user_max_codes: 0,
};

function PageBrand({ page }: { page: string }) {
  const { data } = usePublicConfig();
  return <main data-page={page}>{data ? <><SiteIcon iconUrl={data.site_icon_url} />{data.site_name}</> : <BrandPlaceholder />}</main>;
}

beforeEach(() => {
  localStorage.clear();
  apiGet.mockReset();
});

describe("public branding bootstrap", () => {
  it("uses placeholders, not the default brand, while the first request is slow", async () => {
    let finish!: (config: PublicConfig) => void;
    apiGet.mockReturnValue(new Promise<PublicConfig>((resolve) => { finish = resolve; }));
    const { container } = render(<Providers><PageBrand page="login" /></Providers>);
    expect(container.querySelector("[data-brand-placeholder]")).toBeInTheDocument();
    expect(screen.queryByText("AI Gateway")).not.toBeInTheDocument();
    expect(container.querySelector("img, svg")).not.toBeInTheDocument();
    expect(document.head.querySelector('link[rel="icon"]')).not.toBeInTheDocument();
    await act(async () => { finish(savedConfig); });
    expect(await screen.findByText("Saved Gateway")).toBeInTheDocument();
    expect(document.title).toBe("Saved title");
    expect(JSON.parse(localStorage.getItem(storageKey)!)).toEqual(savedConfig);
  });

  it("hydrates deferred static placeholders safely, then restores the cached brand while refreshing", async () => {
    localStorage.setItem(storageKey, JSON.stringify(savedConfig));
    let finish!: (config: PublicConfig) => void;
    apiGet.mockReturnValue(new Promise<PublicConfig>((resolve) => { finish = resolve; }));
    const app = <Providers><Suspense fallback={null}><PageBrand page="dashboard" /></Suspense></Providers>;
    const markup = renderToString(app);
    expect(markup).toContain("data-brand-placeholder");
    expect(markup).not.toContain("Saved Gateway");
    expect(markup).not.toContain("AI Gateway");
    const container = document.createElement("div");
    container.innerHTML = markup;
    document.body.appendChild(container);
    const onRecoverableError = vi.fn();
    let root!: ReturnType<typeof hydrateRoot>;
    try {
      await act(async () => { root = hydrateRoot(container, app, { onRecoverableError }); });
      expect(await screen.findByText("Saved Gateway")).toBeInTheDocument();
      expect(document.title).toBe("Saved title");
      expect(onRecoverableError).not.toHaveBeenCalled();
      expect(apiGet).toHaveBeenCalledWith("/system/public-config");
      const current = { ...savedConfig, site_name: "Current Gateway", site_title: "Current title" };
      await act(async () => { finish(current); });
      expect(await screen.findByText("Current Gateway")).toBeInTheDocument();
      expect(document.title).toBe("Current title");
      expect(JSON.parse(localStorage.getItem(storageKey)!)).toEqual(current);
    } finally {
      await act(async () => { root?.unmount(); });
      container.remove();
    }
  });

  it("retains the shared brand across page changes and refresh reloads without a default flash", async () => {
    apiGet.mockResolvedValue(savedConfig);
    const view = render(<Providers><PageBrand key="dashboard" page="dashboard" /></Providers>);
    expect(await screen.findByText("Saved Gateway")).toBeInTheDocument();
    apiGet.mockReturnValue(new Promise(() => {}));
    view.rerender(<Providers><PageBrand key="system" page="system" /></Providers>);
    expect(screen.getByText("Saved Gateway")).toBeInTheDocument();
    expect(document.title).toBe("Saved title");
    expect(screen.queryByText("AI Gateway")).not.toBeInTheDocument();
    expect(view.container.querySelector("[data-brand-placeholder]")).not.toBeInTheDocument();
    view.unmount();
    render(<Providers><PageBrand page="profile" /></Providers>);
    expect(await screen.findByText("Saved Gateway")).toBeInTheDocument();
    expect(document.title).toBe("Saved title");
    expect(screen.queryByText("AI Gateway")).not.toBeInTheDocument();
  });

  it("persists a server reset to defaults instead of reviving an obsolete custom brand", async () => {
    localStorage.setItem(storageKey, JSON.stringify(savedConfig));
    const defaults = { ...savedConfig, ...SITE_BRANDING_DEFAULTS };
    apiGet.mockResolvedValue(defaults);
    const view = render(<Providers><PageBrand page="system" /></Providers>);
    expect(await screen.findByText("AI Gateway")).toBeInTheDocument();
    await waitFor(() => expect(document.title).toBe("AI Gateway"));
    expect(document.head.querySelector('link[rel="icon"]')).toHaveAttribute("href", "/favicon.ico");
    expect(JSON.parse(localStorage.getItem(storageKey)!)).toEqual(defaults);
    view.unmount();
    apiGet.mockReturnValue(new Promise(() => {}));
    render(<Providers><PageBrand page="login" /></Providers>);
    expect(await screen.findByText("AI Gateway")).toBeInTheDocument();
    expect(screen.queryByText("Saved Gateway")).not.toBeInTheDocument();
  });
});

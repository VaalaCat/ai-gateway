import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";
import { SiteCustomization } from "./site-customization";

const mocks = vi.hoisted(() => ({
  query: {
    data: { settings: {} } as { settings: Record<string, string> } | undefined,
    isError: false,
    isFetching: false,
    refetch: vi.fn(),
  },
  update: { mutateAsync: vi.fn(), isPending: false },
  success: vi.fn(),
}));

vi.mock("@/lib/api/system", () => ({
  useSettings: () => mocks.query,
  useUpdateSettings: () => mocks.update,
}));
vi.mock("sonner", () => ({ toast: { success: mocks.success } }));
vi.mock("next-intl", () => ({
  useTranslations: () => (key: string) => key,
}));

function change(name: string, value: string) {
  fireEvent.change(screen.getByRole("textbox", { name }), { target: { value } });
}

function save() {
  fireEvent.click(screen.getByRole("button", { name: "save" }));
}

describe("SiteCustomization", () => {
  beforeEach(() => {
    mocks.query.data = { settings: { unrelated_setting: "keep me" } };
    mocks.query.isError = false;
    mocks.query.isFetching = false;
    mocks.query.refetch.mockReset();
    mocks.update.isPending = false;
    mocks.update.mutateAsync.mockReset().mockResolvedValue({ settings: {} });
    mocks.success.mockReset();
  });

  it("loads defaults and saves only branding fields, trimming names but preserving welcome line breaks", async () => {
    render(<SiteCustomization />);
    expect(screen.getByRole("textbox", { name: "siteName" })).toHaveValue("AI Gateway");
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
    change("siteName", "  Example Gateway  ");
    change("siteTitle", "  Example tab  ");
    change("siteIconUrl", "/brand.svg");
    change("welcomeTitle", "<b>Welcome</b>");
    change("welcomeMessage", "First line\n<script>literal text</script>\n");
    save();
    await waitFor(() => expect(mocks.success).toHaveBeenCalledWith("saved"));
    expect(mocks.update.mutateAsync).toHaveBeenCalledWith({
      settings: {
        site_name: "Example Gateway",
        site_title: "Example tab",
        site_icon_url: "/brand.svg",
        welcome_title: "<b>Welcome</b>",
        welcome_message: "First line\n<script>literal text</script>\n",
      },
    });
  });

  it.each([
    "javascript:alert(1)", "data:image/svg+xml,<svg/>", "//evil.example/icon.png",
    "https://user:password@example.com/icon.png", "/\\evil.example/icon.png", "https://example.com/\u0001icon.png",
  ])("refuses an unsafe icon URL without rendering it: %s", (url) => {
    render(<SiteCustomization />);
    change("siteIconUrl", url);
    expect(screen.getByRole("textbox", { name: "siteIconUrl" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
    save();
    expect(mocks.update.mutateAsync).not.toHaveBeenCalled();
  });

  it("requires nonblank names and enforces welcome length limits", () => {
    render(<SiteCustomization />);
    change("siteName", "   ");
    change("siteTitle", "x".repeat(21));
    change("welcomeMessage", "x".repeat(101));
    expect(screen.getByRole("textbox", { name: "siteName" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("textbox", { name: "siteTitle" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("textbox", { name: "welcomeMessage" })).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
  });

  it.each([
    ["siteName", 20], ["siteTitle", 20], ["welcomeTitle", 20], ["welcomeMessage", 100],
  ] as const)("accepts the Unicode boundary and prevents saving one character over it: %s", async (name, maximum) => {
    render(<SiteCustomization />);
    const boundary = "界".repeat(maximum - 1) + "🚀";
    change(name, boundary);
    expect(screen.getByRole("textbox", { name })).toHaveAttribute("aria-invalid", "false");
    expect(screen.getByRole("button", { name: "save" })).toBeEnabled();
    change(name, boundary + "界");
    expect(screen.getByRole("textbox", { name })).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: "save" })).toBeDisabled();
    save();
    expect(mocks.update.mutateAsync).not.toHaveBeenCalled();
    change(name, boundary);
    save();
    await waitFor(() => expect(mocks.success).toHaveBeenCalledWith("saved"));
  });

  it("previews a valid URL and reports image failures with the fallback icon", () => {
    render(<SiteCustomization />);
    change("siteIconUrl", "https://example.com/logo.png");
    const image = screen.getByRole("img", { name: "iconPreview" });
    expect(image).toHaveAttribute("src", "https://example.com/logo.png");
    fireEvent.error(image);
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByText("previewFailed")).toBeInTheDocument();
    change("siteIconUrl", "");
    expect(screen.getByText("iconFallback")).toBeInTheDocument();
  });

  it("restores the default draft without writing until the separate save action", async () => {
    mocks.query.data = { settings: {
      site_name: "Custom", site_title: "Custom title", site_icon_url: "/logo.png",
      welcome_title: "Hello", welcome_message: "Welcome back", unrelated_setting: "keep me",
    } };
    render(<SiteCustomization />);
    fireEvent.click(screen.getByRole("button", { name: "restoreDefaults" }));
    expect(mocks.update.mutateAsync).not.toHaveBeenCalled();
    expect(screen.getByRole("textbox", { name: "siteName" })).toHaveValue("AI Gateway");
    expect(screen.getByRole("textbox", { name: "siteIconUrl" })).toHaveValue("");
    expect(screen.getByRole("textbox", { name: "welcomeMessage" })).toHaveValue("");
    save();
    await waitFor(() => expect(mocks.update.mutateAsync).toHaveBeenCalledWith({ settings: SITE_BRANDING_DEFAULTS }));
  });

  it("keeps a failed draft available for retry", async () => {
    mocks.update.mutateAsync.mockRejectedValueOnce(new Error("network failed"));
    render(<SiteCustomization />);
    change("siteName", "My site");
    save();
    expect(await screen.findByText("saveFailed")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "siteName" })).toHaveValue("My site");
    expect(mocks.success).not.toHaveBeenCalled();
    save();
    await waitFor(() => expect(mocks.success).toHaveBeenCalledWith("saved"));
    expect(mocks.update.mutateAsync).toHaveBeenCalledTimes(2);
  });

  it("disables editing and both actions while saving", () => {
    mocks.update.isPending = true;
    render(<SiteCustomization />);
    expect(screen.getByRole("textbox", { name: "siteName" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "saving" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "restoreDefaults" })).toBeDisabled();
  });

  it("does not allow editing defaults after the initial load fails, and offers retry", () => {
    mocks.query.data = undefined;
    mocks.query.isError = true;
    render(<SiteCustomization />);
    expect(screen.getByText("loadFailed")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "retry" }));
    expect(mocks.query.refetch).toHaveBeenCalledOnce();
    expect(mocks.update.mutateAsync).not.toHaveBeenCalled();
  });
});

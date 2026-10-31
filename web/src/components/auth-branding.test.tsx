import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import LoginPage from "@/app/(auth)/login/page";
import RegisterPage from "@/app/(auth)/register/page";
import { SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";

const { publicConfig, publicProviders } = vi.hoisted(() => ({
  publicConfig: { current: {} as Record<string, string | boolean> },
  publicProviders: vi.fn(),
}));

vi.mock("@/lib/api/system", () => ({ usePublicConfig: () => ({ data: publicConfig.current }) }));
vi.mock("@/lib/api/oauth", () => ({ oauthApi: { publicProviders } }));
vi.mock("@/lib/auth", () => ({ useAuth: () => ({ login: vi.fn() }) }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn() }),
  useSearchParams: () => new URLSearchParams(),
}));
vi.mock("next-intl", () => ({ useTranslations: (namespace: string) => (key: string) => `${namespace}.${key}` }));

beforeEach(() => {
  publicConfig.current = { ...SITE_BRANDING_DEFAULTS, registration_enabled: true };
  publicProviders.mockReset().mockResolvedValue([]);
});

describe("auth site branding", () => {
  it("keeps the default login copy and restores it after live customization is cleared", async () => {
    const { container, rerender } = render(<LoginPage />);
    await waitFor(() => expect(publicProviders).toHaveBeenCalledTimes(1));
    expect(screen.getByText("AI Gateway")).toBeInTheDocument();
    expect(screen.getByText("auth.loginSubtitle")).toBeInTheDocument();
    expect(container.querySelector("img")).not.toBeInTheDocument();

    publicConfig.current = { ...publicConfig.current, site_name: "My Gateway", site_icon_url: "/brand.svg", welcome_title: "Welcome aboard", welcome_message: "Line one\nLine two" };
    rerender(<LoginPage />);
    expect(screen.getByText("My Gateway")).toBeInTheDocument();
    expect(screen.getByText("Welcome aboard")).toBeInTheDocument();
    expect(screen.getByText("Line one Line two")).toHaveTextContent("Line one Line two");
    expect(container.querySelector("img")).toHaveAttribute("src", "/brand.svg");
    expect(screen.queryByText("auth.loginSubtitle")).not.toBeInTheDocument();

    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, registration_enabled: true };
    rerender(<LoginPage />);
    expect(screen.getByText("AI Gateway")).toBeInTheDocument();
    expect(screen.getByText("auth.loginSubtitle")).toBeInTheDocument();
    expect(screen.queryByText("Welcome aboard")).not.toBeInTheDocument();
    expect(container.querySelector("img")).not.toBeInTheDocument();
  });

  it.each([
    ["login", LoginPage],
    ["register", RegisterPage],
  ] as const)("renders %s brand and welcome values as literal text, not HTML", async (_name, Page) => {
    publicConfig.current = {
      ...publicConfig.current,
      site_name: "<b>Branded Gateway</b>",
      site_icon_url: "https://cdn.example.com/brand.png",
      welcome_title: "<script>alert('hello')</script>",
      welcome_message: "<img src=x onerror=alert(1)>\nSecond line",
    };
    const { container } = render(<Page />);
    if (_name === "login") await waitFor(() => expect(publicProviders).toHaveBeenCalledTimes(1));
    expect(screen.getByText("<b>Branded Gateway</b>")).toBeInTheDocument();
    expect(screen.getByText("<script>alert('hello')</script>")).toBeInTheDocument();
    const message = screen.getByText("<img src=x onerror=alert(1)> Second line");
    expect(message.textContent).toBe("<img src=x onerror=alert(1)>\nSecond line");
    expect(message).toHaveClass("whitespace-pre-wrap");
    expect(container.querySelector("script, b, img[onerror]")).not.toBeInTheDocument();
    expect(container.querySelectorAll("img")).toHaveLength(1);
    expect(container.querySelector("img")).toHaveAttribute("src", "https://cdn.example.com/brand.png");
    if (_name === "register") expect(screen.getByText("register.title")).toBeInTheDocument();
  });

  it("keeps the registration purpose title when welcome copy is configured and when it is cleared", () => {
    publicConfig.current = { ...publicConfig.current, welcome_title: "Hello", welcome_message: "Create your account here" };
    const { rerender } = render(<RegisterPage />);
    expect(screen.getByText("register.title")).toBeInTheDocument();
    expect(screen.getByText("Hello")).toBeInTheDocument();
    publicConfig.current = { ...SITE_BRANDING_DEFAULTS, registration_enabled: true };
    rerender(<RegisterPage />);
    expect(screen.getByText("register.title")).toBeInTheDocument();
    expect(screen.queryByText("Hello")).not.toBeInTheDocument();
    expect(screen.queryByText("Create your account here")).not.toBeInTheDocument();
  });
});

"use client";

import { useEffect } from "react";
import { usePathname } from "next/navigation";
import { Bot } from "lucide-react";
import { usePublicConfig } from "@/lib/api/system";
import { isSafeSiteIconURL, SITE_BRANDING_DEFAULTS } from "@/lib/site-branding";

export function SiteIcon({ iconUrl, className }: { iconUrl?: string; className?: string }) {
  if (!iconUrl || !isSafeSiteIconURL(iconUrl)) return <Bot className={className} aria-hidden="true" />;

  return (
    // Custom URLs must not go through Next's image optimizer or an allowlist.
    // eslint-disable-next-line @next/next/no-img-element
    <img src={iconUrl} alt="" className={className} />
  );
}

export function SiteBrandingRuntime() {
  const pathname = usePathname();
  const { data: config } = usePublicConfig();
  const title = config?.site_title || SITE_BRANDING_DEFAULTS.site_title;
  const configuredIcon = config?.site_icon_url || "";
  const iconUrl = isSafeSiteIconURL(configuredIcon) ? configuredIcon : "";

  useEffect(() => {
    const previousTitle = document.title;
    const originalLinks = Array.from(document.head.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]'));
    const managedIcon = (iconUrl || originalLinks.length === 0) ? document.createElement("link") : null;
    if (managedIcon) {
      managedIcon.rel = "icon";
      managedIcon.href = iconUrl || "/favicon.ico";
    }
    const activeIcons = managedIcon ? [managedIcon] : originalLinks;

    const applyBranding = () => {
      if (document.title !== title) document.title = title;
      document.head.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]').forEach((link) => {
        if (!activeIcons.includes(link)) link.remove();
      });
      if (managedIcon) {
        if (managedIcon.rel !== "icon") managedIcon.rel = "icon";
        if (managedIcon.getAttribute("href") !== (iconUrl || "/favicon.ico")) managedIcon.href = iconUrl || "/favicon.ico";
      }
      activeIcons.forEach((link) => {
        if (link.parentNode !== document.head) document.head.appendChild(link);
      });
    };

    // Next reconciles static metadata after navigation, sometimes after route
    // effects. Reassert branding on those head changes, writing only on drift
    // so observing our own updates settles without a mutation loop.
    const observer = new MutationObserver(applyBranding);
    applyBranding();
    observer.observe(document.head, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["href", "rel"],
    });

    return () => {
      observer.disconnect();
      document.title = previousTitle;
      managedIcon?.remove();
      originalLinks.forEach((original) => {
        if (original.parentNode !== document.head) document.head.appendChild(original);
      });
    };
  }, [title, iconUrl, pathname]);

  return null;
}

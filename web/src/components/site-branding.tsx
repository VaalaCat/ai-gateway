"use client";

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

export function BrandPlaceholder({ className = "" }: { className?: string }) {
  return <span data-brand-placeholder aria-hidden="true" className={`inline-block h-5 w-28 animate-pulse rounded bg-muted ${className}`} />;
}

export function SiteBrandingRuntime() {
  const { data: config } = usePublicConfig();
  if (!config) return null;

  const iconUrl = isSafeSiteIconURL(config.site_icon_url) ? config.site_icon_url : "";
  return (
    <>
      <title>{config.site_title || SITE_BRANDING_DEFAULTS.site_title}</title>
      <link rel="icon" href={iconUrl || "/favicon.ico"} />
    </>
  );
}

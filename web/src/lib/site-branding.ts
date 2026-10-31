export const SITE_BRANDING_DEFAULTS = {
  site_name: "AI Gateway",
  site_title: "AI Gateway",
  site_icon_url: "",
  welcome_title: "",
  welcome_message: "",
};

export type SiteBranding = typeof SITE_BRANDING_DEFAULTS;

export function isSafeSiteIconURL(value: string): boolean {
  if (!value) return true;
  if (/\s/.test(value)) return false;
  let decoded: string;
  try {
    decoded = decodeURIComponent(value);
  } catch {
    return false;
  }
  if (decoded.includes("\\") || decoded.startsWith("//")) return false;
  for (let index = 0; index < decoded.length; index += 1) {
    const code = decoded.charCodeAt(index);
    if (code < 32 || (code >= 127 && code <= 159)) return false;
  }
  if (value.startsWith("/")) return true;
  if (!/^https?:\/\/[^/?#@]+(?:[/?#]|$)/i.test(value)) return false;
  try {
    const url = new URL(value);
    return (url.protocol === "http:" || url.protocol === "https:") && !!url.hostname && !url.username && !url.password;
  } catch {
    return false;
  }
}

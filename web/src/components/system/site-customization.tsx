"use client";

import { useState } from "react";
import { Bot, Loader2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { useSettings, useUpdateSettings } from "@/lib/api/system";
import { isSafeSiteIconURL, SITE_BRANDING_DEFAULTS, type SiteBranding } from "@/lib/site-branding";

const fields = [
  { key: "site_name", label: "siteName", help: "siteNameHelp", limit: 100, required: true },
  { key: "site_title", label: "siteTitle", help: "siteTitleHelp", limit: 200, required: true },
  { key: "site_icon_url", label: "siteIconUrl", help: "siteIconUrlHelp" },
  { key: "welcome_title", label: "welcomeTitle", help: "welcomeTitleHelp", limit: 200 },
  { key: "welcome_message", label: "welcomeMessage", help: "welcomeMessageHelp", limit: 2000 },
] as const;

export function SiteCustomization() {
  const t = useTranslations("system.siteCustomization");
  const { data, isError, isFetching, refetch } = useSettings();
  const update = useUpdateSettings();
  const [draft, setDraft] = useState<Partial<SiteBranding>>({});
  const [saveError, setSaveError] = useState(false);
  const [failedPreview, setFailedPreview] = useState<string | null>(null);
  const current: SiteBranding = {
    site_name: data?.settings.site_name ?? SITE_BRANDING_DEFAULTS.site_name,
    site_title: data?.settings.site_title ?? SITE_BRANDING_DEFAULTS.site_title,
    site_icon_url: data?.settings.site_icon_url ?? SITE_BRANDING_DEFAULTS.site_icon_url,
    welcome_title: data?.settings.welcome_title ?? SITE_BRANDING_DEFAULTS.welcome_title,
    welcome_message: data?.settings.welcome_message ?? SITE_BRANDING_DEFAULTS.welcome_message,
  };
  const values = { ...current, ...draft };
  const normalized = {
    ...values,
    site_name: values.site_name.trim(),
    site_title: values.site_title.trim(),
  };
  const errors: Partial<Record<keyof SiteBranding, string>> = {};
  for (const field of fields) {
    const value = normalized[field.key];
    if ("required" in field && field.required && !value) errors[field.key] = t("required");
    else if ("limit" in field && Array.from(value).length > field.limit) {
      errors[field.key] = t("tooLong", { max: field.limit });
    }
  }
  if (!isSafeSiteIconURL(normalized.site_icon_url)) errors.site_icon_url = t("invalidIconUrl");
  const hasChanges = fields.some(({ key }) => normalized[key] !== current[key]);
  const invalid = Object.keys(errors).length > 0;
  const iconURL = normalized.site_icon_url;

  async function save(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!data || update.isPending || !hasChanges || invalid) return;
    setSaveError(false);
    try {
      await update.mutateAsync({ settings: normalized });
      setDraft({});
      toast.success(t("saved"));
    } catch {
      setSaveError(true);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("title")}</CardTitle>
        <CardDescription>{t("description")}</CardDescription>
      </CardHeader>
      <CardContent>
        {!data ? (
          isError ? (
            <Alert variant="destructive">
              <AlertDescription>
                <p>{t("loadFailed")}</p>
                <Button type="button" variant="outline" disabled={isFetching} onClick={() => void refetch()}>{t("retry")}</Button>
              </AlertDescription>
            </Alert>
          ) : <p role="status" className="text-sm text-muted-foreground">{t("loading")}</p>
        ) : (
          <form onSubmit={save} className="space-y-5">
            {isError && <Alert><AlertDescription>{t("refreshFailed")}</AlertDescription></Alert>}
            <fieldset disabled={update.isPending} className="grid min-w-0 gap-5 sm:grid-cols-2">
              {fields.map((field) => {
                const id = `site-customization-${field.key}`;
                const error = errors[field.key];
                const props = {
                  id,
                  value: values[field.key],
                  "aria-describedby": `${id}-help${error ? ` ${id}-error` : ""}`,
                  "aria-invalid": Boolean(error),
                  onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => {
                    setDraft((previous) => ({ ...previous, [field.key]: event.target.value }));
                    setSaveError(false);
                  },
                };
                return (
                  <Field key={field.key} data-invalid={Boolean(error) || undefined} className={field.key === "welcome_message" ? "sm:col-span-2" : undefined}>
                    <FieldLabel htmlFor={id}>{t(field.label)}</FieldLabel>
                    {field.key === "welcome_message" ? <Textarea {...props} rows={4} /> : (
                      <Input {...props} autoComplete="off" spellCheck={field.key !== "site_icon_url"} />
                    )}
                    <FieldDescription id={`${id}-help`}>{t(field.help)}</FieldDescription>
                    {error && <FieldError id={`${id}-error`}>{error}</FieldError>}
                  </Field>
                );
              })}
            </fieldset>
            <div className="flex items-center gap-3 rounded-lg border p-3">
              {iconURL && !errors.site_icon_url && failedPreview !== iconURL ? (
                // Administrators may choose any validated external image URL; Next Image requires a fixed host allowlist.
                // eslint-disable-next-line @next/next/no-img-element
                <img src={iconURL} alt={t("iconPreview")} className="size-10 rounded object-contain" onError={() => setFailedPreview(iconURL)} />
              ) : <Bot aria-hidden="true" className="size-10 text-primary" />}
              <p className="text-sm text-muted-foreground">{failedPreview === iconURL && iconURL ? t("previewFailed") : t("iconFallback")}</p>
            </div>
            {saveError && <Alert variant="destructive"><AlertDescription>{t("saveFailed")}</AlertDescription></Alert>}
            <div className="flex flex-wrap justify-end gap-2">
              <Button type="button" variant="outline" disabled={update.isPending} onClick={() => { setDraft({ ...SITE_BRANDING_DEFAULTS }); setSaveError(false); }}>
                {t("restoreDefaults")}
              </Button>
              <Button type="submit" disabled={!hasChanges || invalid || update.isPending}>
                {update.isPending && <Loader2 className="animate-spin" />}
                {update.isPending ? t("saving") : t("save")}
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  );
}

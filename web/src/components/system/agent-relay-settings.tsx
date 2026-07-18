"use client";

import { useState } from "react";
import { CircleAlert, RefreshCw } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldTitle,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  useSettings,
  useUpdateAgentRelaySettings,
  type AgentRelaySettingsPatch,
} from "@/lib/api/system";
import { parseOptionalRelayURI } from "@/lib/utils/relay-uri";

const DEFAULT_URI_KEY = "agent.relay_default_uri";
const FALLBACK_ENABLED_KEY = "agent.relay_fallback_enabled";
const PROBE_SUCCESS_TTL_KEY = "agent.connectivity_probe_success_ttl_seconds";
const PROBE_RETRY_MIN_KEY = "agent.connectivity_probe_failure_retry_min_seconds";
const PROBE_RETRY_MAX_KEY = "agent.connectivity_probe_failure_retry_max_seconds";

function integerInRange(value: string, minimum: number, maximum: number) {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed >= minimum && parsed <= maximum;
}

export function AgentRelaySettings() {
  const t = useTranslations("system.agentRelay");
  const { data, isError, isFetching, refetch } = useSettings();
  const update = useUpdateAgentRelaySettings();
  const currentURI = data?.settings[DEFAULT_URI_KEY] ?? "";
  const currentEnabled = data?.settings[FALLBACK_ENABLED_KEY] === "1";
  const currentSuccessTTL = data?.settings[PROBE_SUCCESS_TTL_KEY] ?? "300";
  const currentRetryMin = data?.settings[PROBE_RETRY_MIN_KEY] ?? "30";
  const currentRetryMax = data?.settings[PROBE_RETRY_MAX_KEY] ?? "300";
  const hasBaseline = data !== undefined;
  const [uriDraft, setURIDraft] = useState<string | null>(null);
  const [enabledDraft, setEnabledDraft] = useState<boolean | null>(null);
  const [successTTLDraft, setSuccessTTLDraft] = useState<string | null>(null);
  const [retryMinDraft, setRetryMinDraft] = useState<string | null>(null);
  const [retryMaxDraft, setRetryMaxDraft] = useState<string | null>(null);
  const [saveError, setSaveError] = useState(false);

  const uri = uriDraft ?? currentURI;
  const enabled = enabledDraft ?? currentEnabled;
  const successTTL = successTTLDraft ?? currentSuccessTTL;
  const retryMin = retryMinDraft ?? currentRetryMin;
  const retryMax = retryMaxDraft ?? currentRetryMax;
  const parsedURI = parseOptionalRelayURI(uri);
  const uriError = "error" in parsedURI ? parsedURI.error : undefined;
  const normalizedURI = "normalized" in parsedURI ? parsedURI.normalized : uri;
  const uriChanged = normalizedURI !== currentURI;
  const enabledChanged = enabled !== currentEnabled;
  const successTTLChanged = successTTL !== currentSuccessTTL;
  const retryMinChanged = retryMin !== currentRetryMin;
  const retryMaxChanged = retryMax !== currentRetryMax;
  const successTTLError = !integerInRange(successTTL, 30, 3600);
  const retryMinError = !integerInRange(retryMin, 5, 300);
  const retryMaxError = !integerInRange(retryMax, 5, 3600) || Number(retryMax) < Number(retryMin);
  const timingError = successTTLError || retryMinError || retryMaxError;
  const hasChanges = uriChanged || enabledChanged || successTTLChanged || retryMinChanged || retryMaxChanged;

  const retry = () => {
    void refetch();
  };

  const save = async () => {
    if (!hasBaseline || update.isPending || uriError || timingError || !hasChanges) return;
    const settings: AgentRelaySettingsPatch = {};
    if (uriChanged) settings[DEFAULT_URI_KEY] = normalizedURI;
    if (enabledChanged) settings[FALLBACK_ENABLED_KEY] = enabled ? "1" : "0";
    if (successTTLChanged) settings[PROBE_SUCCESS_TTL_KEY] = successTTL;
    if (retryMinChanged) settings[PROBE_RETRY_MIN_KEY] = retryMin;
    if (retryMaxChanged) settings[PROBE_RETRY_MAX_KEY] = retryMax;

    setSaveError(false);
    try {
      await update.mutateAsync({ settings });
      setURIDraft(null);
      setEnabledDraft(null);
      setSuccessTTLDraft(null);
      setRetryMinDraft(null);
      setRetryMaxDraft(null);
      toast.success(t("saved"));
    } catch {
      setSaveError(true);
    }
  };

  return (
    <section aria-labelledby="agent-relay-settings-title" className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h3 id="agent-relay-settings-title" className="text-sm font-medium">
          {t("title")}
        </h3>
        <p className="text-sm text-muted-foreground">{t("description")}</p>
      </div>

      {!hasBaseline ? (
        isError ? (
          <Alert variant="destructive">
            <CircleAlert />
            <AlertTitle>{t("loadFailed")}</AlertTitle>
            <AlertDescription>
              <p>{t("loadFailedDescription")}</p>
              <Button type="button" variant="outline" size="sm" disabled={isFetching} onClick={retry}>
                <RefreshCw className={isFetching ? "animate-spin" : undefined} />
                {t("retry")}
              </Button>
            </AlertDescription>
          </Alert>
        ) : (
          <div role="status" aria-label={t("loading")} className="space-y-3 py-1">
            <Skeleton className="h-4 w-32" />
            <Skeleton className="h-9 w-full" />
            <Skeleton className="h-8 w-3/5" />
          </div>
        )
      ) : (
        <>
          {isError ? (
            <Alert className="py-2">
              <CircleAlert />
              <AlertTitle>{t("refreshFailed")}</AlertTitle>
              <AlertDescription>
                <p>{t("refreshFailedDescription")}</p>
                <Button type="button" variant="outline" size="sm" disabled={isFetching} onClick={retry}>
                  <RefreshCw className={isFetching ? "animate-spin" : undefined} />
                  {t("retry")}
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}

          <FieldGroup className="gap-5">
            <Field data-invalid={Boolean(uriError) || undefined} data-disabled={update.isPending || undefined}>
              <FieldLabel htmlFor="agent-relay-default-uri">{t("defaultUri")}</FieldLabel>
              <Input
                id="agent-relay-default-uri"
                value={uri}
                disabled={update.isPending}
                aria-invalid={Boolean(uriError)}
                autoCapitalize="none"
                autoComplete="off"
                spellCheck={false}
                placeholder={t("defaultUriPlaceholder")}
                onChange={(event) => {
                  setURIDraft(event.target.value);
                  setSaveError(false);
                }}
              />
              <FieldDescription>{t("defaultUriDescription")}</FieldDescription>
              {uriError ? (
                <FieldError>{uriError === "too_long" ? t("uriTooLong") : t("invalidUri")}</FieldError>
              ) : null}
            </Field>

            <Field orientation="horizontal" data-disabled={update.isPending || undefined}>
              <FieldContent>
                <FieldTitle>{t("fallbackEnabled")}</FieldTitle>
                <FieldDescription>{t("fallbackEnabledDescription")}</FieldDescription>
              </FieldContent>
              <Switch
                id="agent-relay-fallback-enabled"
                checked={enabled}
                disabled={update.isPending}
                aria-label={t("fallbackEnabled")}
                onCheckedChange={(checked) => {
                  setEnabledDraft(checked);
                  setSaveError(false);
                }}
              />
            </Field>

            <FieldGroup className="grid min-w-0 gap-4 sm:grid-cols-3">
              <Field data-invalid={successTTLError || undefined} data-disabled={update.isPending || undefined}>
                <FieldLabel htmlFor="agent-probe-success-ttl">{t("probeSuccessTtl")}</FieldLabel>
                <Input
                  id="agent-probe-success-ttl"
                  type="number"
                  min={30}
                  max={3600}
                  value={successTTL}
                  disabled={update.isPending}
                  aria-invalid={successTTLError}
                  onChange={(event) => setSuccessTTLDraft(event.target.value)}
                />
                <FieldDescription>{t("probeSuccessTtlDescription")}</FieldDescription>
                {successTTLError ? <FieldError>{t("rangeSeconds", { min: 30, max: 3600 })}</FieldError> : null}
              </Field>
              <Field data-invalid={retryMinError || undefined} data-disabled={update.isPending || undefined}>
                <FieldLabel htmlFor="agent-probe-retry-min">{t("probeRetryMin")}</FieldLabel>
                <Input
                  id="agent-probe-retry-min"
                  type="number"
                  min={5}
                  max={300}
                  value={retryMin}
                  disabled={update.isPending}
                  aria-invalid={retryMinError}
                  onChange={(event) => setRetryMinDraft(event.target.value)}
                />
                <FieldDescription>{t("probeRetryMinDescription")}</FieldDescription>
                {retryMinError ? <FieldError>{t("rangeSeconds", { min: 5, max: 300 })}</FieldError> : null}
              </Field>
              <Field data-invalid={retryMaxError || undefined} data-disabled={update.isPending || undefined}>
                <FieldLabel htmlFor="agent-probe-retry-max">{t("probeRetryMax")}</FieldLabel>
                <Input
                  id="agent-probe-retry-max"
                  type="number"
                  min={5}
                  max={3600}
                  value={retryMax}
                  disabled={update.isPending}
                  aria-invalid={retryMaxError}
                  onChange={(event) => setRetryMaxDraft(event.target.value)}
                />
                <FieldDescription>{t("probeRetryMaxDescription")}</FieldDescription>
                {retryMaxError ? <FieldError>{t("probeRetryMaxError")}</FieldError> : null}
              </Field>
            </FieldGroup>
          </FieldGroup>

          {normalizedURI === "" && !uriError ? (
            <div className="flex min-w-0 flex-col items-start gap-1.5 sm:flex-row sm:items-center">
              <Badge variant="secondary">{t("derivedFromMaster")}</Badge>
              <p className="text-sm text-muted-foreground">{t("derivedFromMasterDescription")}</p>
            </div>
          ) : null}

          {saveError ? (
            <Alert variant="destructive">
              <AlertTitle>{t("saveFailed")}</AlertTitle>
              <AlertDescription>{t("saveFailedDescription")}</AlertDescription>
            </Alert>
          ) : null}
        </>
      )}

      <div className="flex justify-end">
        <Button type="button" disabled={!hasBaseline || !hasChanges || Boolean(uriError) || timingError || update.isPending} onClick={save}>
          {update.isPending ? t("saving") : t("save")}
        </Button>
      </div>
    </section>
  );
}
